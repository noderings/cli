package install

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/noderings/cli/internal/config"
)

var (
	liqoIPGVR = schema.GroupVersionResource{
		Group:    "ipam.liqo.io",
		Version:  "v1alpha1",
		Resource: "ips",
	}
	liqoConfigurationGVR = schema.GroupVersionResource{
		Group:    "networking.liqo.io",
		Version:  "v1beta1",
		Resource: "configurations",
	}
)

// sharedAddressSpace is RFC 6598 carrier-grade NAT space: shaped like a routable address but
// never reachable from the internet.
var sharedAddressSpace = netip.MustParsePrefix("100.64.0.0/10")

// GetAPIServerProxyIP returns this cluster's api-server-proxy address as remapped into the
// local ExternalCIDR. A remote peer reaches it by mapping this address once more, through its
// own Configuration for this cluster.
func (l *LiqoManager) GetAPIServerProxyIP(ctx context.Context) (string, error) {
	restConfig := l.k8sClient.GetConfig()
	if restConfig == nil {
		return "", fmt.Errorf("no local kubeconfig")
	}
	dyn, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		return "", fmt.Errorf("create dynamic client: %w", err)
	}

	obj, err := dyn.Resource(liqoIPGVR).Namespace(config.DefaultLiqoNamespace).
		Get(ctx, config.LiqoAPIServerProxyIPName, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("get IP %s/%s: %w", config.DefaultLiqoNamespace, config.LiqoAPIServerProxyIPName, err)
	}

	ip, found, err := unstructured.NestedString(obj.Object, "status", "ip")
	if err != nil {
		return "", fmt.Errorf("read status.ip of IP %q: %w", config.LiqoAPIServerProxyIPName, err)
	}
	if !found || ip == "" {
		return "", fmt.Errorf("IP %q has no status.ip yet; the Liqo networking module must be enabled and established",
			config.LiqoAPIServerProxyIPName)
	}
	return ip, nil
}

// ResolveInboundAPIServerProxyURL returns the HTTP CONNECT proxy URL a remote cluster must use
// to reach this cluster's Kubernetes API server through the Liqo tunnel.
//
// The address is mapped twice: once by this cluster into its own ExternalCIDR, and once by the
// remote cluster, whose Configuration records how it remapped that CIDR. remoteKubeconfigPath
// must point at the remote cluster, and localClusterID identifies this cluster there.
func (l *LiqoManager) ResolveInboundAPIServerProxyURL(
	ctx context.Context,
	remoteKubeconfigPath, localClusterID string,
) (string, error) {
	proxyIP, err := l.GetAPIServerProxyIP(ctx)
	if err != nil {
		return "", err
	}

	remapped, err := remapAddressForRemote(ctx, remoteKubeconfigPath, localClusterID, proxyIP)
	if err != nil {
		return "", err
	}

	l.logger.Infof("API server proxy %s is reachable from the remote cluster as %s", proxyIP, remapped)
	return fmt.Sprintf("http://%s:%d", remapped, config.LiqoAPIServerProxyPort), nil
}

// remapAddressForRemote maps an address of the local ExternalCIDR into the CIDR the remote
// cluster assigned to it, reading the Configuration that the remote cluster holds for us.
func remapAddressForRemote(ctx context.Context, remoteKubeconfigPath, localClusterID, address string) (string, error) {
	clientConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		&clientcmd.ClientConfigLoadingRules{ExplicitPath: remoteKubeconfigPath},
		&clientcmd.ConfigOverrides{},
	)
	namespace, _, err := clientConfig.Namespace()
	if err != nil {
		return "", fmt.Errorf("read namespace from %s: %w", remoteKubeconfigPath, err)
	}
	restConfig, err := clientConfig.ClientConfig()
	if err != nil {
		return "", fmt.Errorf("load remote kubeconfig %s: %w", remoteKubeconfigPath, err)
	}
	dyn, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		return "", fmt.Errorf("create remote dynamic client: %w", err)
	}

	list, err := dyn.Resource(liqoConfigurationGVR).Namespace(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s", config.LiqoLabelRemoteClusterID, localClusterID),
	})
	if err != nil {
		return "", fmt.Errorf("list configurations in namespace %s on the remote cluster: %w", namespace, err)
	}
	if len(list.Items) == 0 {
		return "", fmt.Errorf("no configuration for cluster %q in namespace %s on the remote cluster; "+
			"the outbound peering must be established first", localClusterID, namespace)
	}

	cfg := list.Items[0]
	specCIDRs, _, err := unstructured.NestedStringSlice(cfg.Object, "spec", "remote", "cidr", "external")
	if err != nil {
		return "", fmt.Errorf("read spec.remote.cidr.external of configuration %q: %w", cfg.GetName(), err)
	}
	statusCIDRs, _, err := unstructured.NestedStringSlice(cfg.Object, "status", "remote", "cidr", "external")
	if err != nil {
		return "", fmt.Errorf("read status.remote.cidr.external of configuration %q: %w", cfg.GetName(), err)
	}
	if len(specCIDRs) == 0 || len(statusCIDRs) == 0 {
		return "", fmt.Errorf("configuration %q is not remapped yet", cfg.GetName())
	}

	return remapWithinCIDRs(address, specCIDRs, statusCIDRs)
}

// remapWithinCIDRs finds the spec CIDR containing address and rebases it onto the status CIDR
// at the same index, preserving the host bits. It mirrors Liqo's external CIDR remapping.
func remapWithinCIDRs(address string, spec, status []string) (string, error) {
	ip := net.ParseIP(address)
	if ip == nil {
		return "", fmt.Errorf("invalid IP %q", address)
	}

	for i, specCIDR := range spec {
		_, specNet, err := net.ParseCIDR(specCIDR)
		if err != nil {
			return "", fmt.Errorf("parse spec CIDR %q: %w", specCIDR, err)
		}
		if !specNet.Contains(ip) {
			continue
		}
		if i >= len(status) {
			return "", fmt.Errorf("spec CIDR %q has no matching status CIDR", specCIDR)
		}
		if specCIDR == status[i] {
			return address, nil
		}
		_, statusNet, err := net.ParseCIDR(status[i])
		if err != nil {
			return "", fmt.Errorf("parse status CIDR %q: %w", status[i], err)
		}
		return remapMask(ip, *statusNet)
	}

	return "", fmt.Errorf("address %q is outside the remote external CIDRs %v", address, spec)
}

// remapMask keeps the host bits of addr and replaces the network bits with those of network.
//
// For example 10.72.0.1 remapped onto 10.81.0.0/16 yields 10.81.0.1.
func remapMask(addr net.IP, network net.IPNet) (string, error) {
	base, mask := network.IP, network.Mask
	ip := addr.To4()
	if len(mask) == net.IPv6len {
		ip = addr.To16()
	}
	if len(ip) != len(mask) || len(base) != len(mask) {
		return "", fmt.Errorf("cannot remap %s onto %s: address family mismatch", addr, network.String())
	}

	out := make(net.IP, len(ip))
	for i := range ip {
		out[i] = base[i] | (ip[i] &^ mask[i])
	}
	return out.String(), nil
}

// APIServerNeedsProxy reports whether a Kubernetes API server URL is unreachable from outside
// the cluster's own network, and so has to be proxied through the Liqo tunnel. DNS names are
// assumed routable: only literal non-public addresses are proxied.
func APIServerNeedsProxy(apiServerURL string) bool {
	addr, err := netip.ParseAddr(apiServerHost(apiServerURL))
	if err != nil {
		return false
	}
	addr = addr.Unmap()
	return !addr.IsGlobalUnicast() || addr.IsPrivate() || sharedAddressSpace.Contains(addr)
}

// apiServerHost extracts the host of an API server URL, tolerating a missing scheme.
func apiServerHost(apiServerURL string) string {
	raw := strings.TrimSpace(apiServerURL)
	if raw == "" {
		return ""
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Hostname()
}
