package install

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/noderings/cli/internal/config"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"

	"github.com/noderings/cli/internal/k8s"
)

// defaultCalicoRolloutTimeout bounds the whole Calico install when RolloutTimeout is unparseable.
const defaultCalicoRolloutTimeout = 10 * time.Minute

// CalicoInstaller handles Calico installation using client-go
type CalicoInstaller struct {
	k8sClient *k8s.Client
	config    *CalicoConfig
	logger    Logger
}

// CalicoConfig holds Calico installation configuration
type CalicoConfig struct {
	Version            string // e.g., "v3.30.2"
	RolloutTimeout     string // e.g., "10m"
	CRDsURL            string // URL to Calico CRDs manifest (operator-crds.yaml)
	OperatorURL        string // URL to Calico operator manifest (tigera-operator.yaml)
	CustomResourcesURL string // URL to Calico custom resources manifest (custom-resources.yaml)
	PodCIDR            string // Calico IPv4 pool CIDR. Must match k3s cluster CIDR.
}

// NewCalicoInstaller creates a new Calico installer
func NewCalicoInstaller(k8sClient *k8s.Client, config *CalicoConfig, logger Logger) *CalicoInstaller {
	// Set default URLs if not provided
	if config.CRDsURL == "" {
		config.CRDsURL = fmt.Sprintf("https://raw.githubusercontent.com/projectcalico/calico/%s/manifests/operator-crds.yaml", config.Version)
	}
	if config.OperatorURL == "" {
		config.OperatorURL = fmt.Sprintf("https://raw.githubusercontent.com/projectcalico/calico/%s/manifests/tigera-operator.yaml", config.Version)
	}
	if config.CustomResourcesURL == "" {
		config.CustomResourcesURL = fmt.Sprintf("https://raw.githubusercontent.com/projectcalico/calico/%s/manifests/custom-resources.yaml", config.Version)
	}

	return &CalicoInstaller{
		k8sClient: k8sClient,
		config:    config,
		logger:    logger,
	}
}

// Install installs Calico using Kubernetes client-go.
// Calico remains the provider-cluster CNI (k3s flannel is disabled). Liqo peering
// does not require matching CNIs across clusters, but the local cluster still needs
// a CNI for in-cluster pod networking.
//
// When Calico is already present, Install still re-applies networking patches and
// verifies ClusterIP DNS so resume/--force paths heal a broken CNI dataplane.
func (c *CalicoInstaller) Install(ctx context.Context) error {
	timeout, err := time.ParseDuration(c.config.RolloutTimeout)
	if err != nil {
		timeout = defaultCalicoRolloutTimeout
	}

	// RolloutTimeout bounds the whole install. Each stage below gets the remaining budget,
	// so a broken cluster cannot serialize several full-length waits.
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if c.isInstalled(ctx) {
		c.logger.Info("Calico appears to be already installed; ensuring networking patches and DNS")
	} else {
		c.logger.Info("Applying Calico CRDs...")
		if err := c.k8sClient.ApplyManifest(ctx, c.config.CRDsURL); err != nil {
			return fmt.Errorf("apply Calico CRDs: %w", err)
		}

		c.logger.Info("Applying Calico operator...")
		if err := c.k8sClient.ApplyManifest(ctx, c.config.OperatorURL); err != nil {
			return fmt.Errorf("apply Calico operator manifest: %w", err)
		}

		c.logger.Info("Waiting for Calico operator to be ready...")
		if err := c.k8sClient.WaitForRollout(ctx, config.DefaultTigeraOperatorNamespace, config.DefaultTigeraOperatorDeployment, remainingBudget(ctx, timeout)); err != nil {
			return fmt.Errorf("wait for Calico operator rollout: %w", err)
		}

		c.logger.Info("Applying Calico custom resources...")
		if err := c.applyCustomResources(ctx); err != nil {
			return fmt.Errorf("apply Calico custom resources: %w", err)
		}
	}

	// Always patch Installation + Felix. Upstream Calico v3.30 VXLAN NOTRACK rules can
	// render as "all UDP" on iptables-nft hosts (missing --dport 4789), which disables
	// conntrack and breaks kube-proxy ClusterIP DNAT for DNS. IPIPCrossSubnet avoids
	// that VXLAN NOTRACK path entirely while still providing an overlay for multi-node.
	//
	// FelixConfiguration is served by the Calico aggregated API (calico-apiserver). Patching
	// it immediately after applying Installation races the APIService becoming Available —
	// wait for that, then retry Get/Patch for the full rollout timeout.
	c.logger.Info("Patching Calico Installation for k3s networking...")
	if err := c.patchInstallation(ctx, remainingBudget(ctx, timeout)); err != nil {
		return fmt.Errorf("patch Calico installation: %w", err)
	}

	c.logger.Info("Waiting for Calico aggregated API before FelixConfiguration patch...")
	if err := c.waitForCalicoAPIService(ctx, remainingBudget(ctx, timeout)); err != nil {
		return fmt.Errorf("wait for Calico API: %w", err)
	}

	if err := c.patchFelixConfiguration(ctx, remainingBudget(ctx, timeout)); err != nil {
		return fmt.Errorf("patch FelixConfiguration: %w", err)
	}

	c.logger.Info("Waiting for Calico pods to be ready...")
	if err := c.waitForCalicoPods(ctx, remainingBudget(ctx, timeout)); err != nil {
		return fmt.Errorf("wait for Calico pods: %w", err)
	}

	c.logger.Info("Verifying ClusterIP DNS...")
	if err := c.verifyClusterDNS(ctx, remainingBudget(ctx, timeout)); err != nil {
		return fmt.Errorf("cluster DNS check failed: %w", err)
	}

	c.logger.Info("✓ Calico installed successfully")
	return nil
}

// applyCustomResources downloads upstream custom-resources.yaml and rewrites the
// default ipPool (192.168.0.0/16 / VXLAN) to k3s PodCIDR + IPIP before apply.
// Calico will not change an existing IPPool CIDR, so applying the upstream
// defaults first is what made liqoctl reject the cluster (backend#589).
func (c *CalicoInstaller) applyCustomResources(ctx context.Context) error {
	//nolint:gosec // G107: URL is the version-pinned Calico custom-resources manifest
	resp, err := http.Get(c.config.CustomResourcesURL)
	if err != nil {
		return fmt.Errorf("download Calico custom resources: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download Calico custom resources: unexpected status %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read Calico custom resources: %w", err)
	}
	if c.config.PodCIDR != "" {
		c.logger.Infof("Rewriting Calico ipPool cidr to %s before apply", c.config.PodCIDR)
		raw = rewriteUpstreamCalicoCIDR(raw, c.config.PodCIDR)
	}
	return c.k8sClient.ApplyManifestBytes(ctx, raw)
}

func rewriteUpstreamCalicoCIDR(raw []byte, podCIDR string) []byte {
	raw = bytes.ReplaceAll(raw, []byte("192.168.0.0/16"), []byte(podCIDR))
	return bytes.ReplaceAll(raw, []byte("VXLANCrossSubnet"), []byte("IPIPCrossSubnet"))
}

// remainingBudget returns the time left on ctx's deadline, capped at max.
func remainingBudget(ctx context.Context, max time.Duration) time.Duration {
	deadline, ok := ctx.Deadline()
	if !ok {
		return max
	}
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return 0
	}
	if remaining < max {
		return remaining
	}
	return max
}

// isInstalled checks if Calico is already installed
func (c *CalicoInstaller) isInstalled(ctx context.Context) bool {
	clientset := c.k8sClient.GetClientset()

	deployment, err := clientset.AppsV1().Deployments(config.DefaultTigeraOperatorNamespace).Get(ctx, config.DefaultTigeraOperatorDeployment, metav1.GetOptions{})
	if err != nil {
		return false
	}
	if deployment.Status.ReadyReplicas > 0 {
		return true
	}

	pods, err := clientset.CoreV1().Pods(config.DefaultCalicoSystemNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: "k8s-app=calico-node",
	})
	if err == nil && len(pods.Items) > 0 {
		return true
	}

	return false
}

// patchInstallation patches the Calico Installation resource for k3s + Liqo.
func (c *CalicoInstaller) patchInstallation(ctx context.Context, timeout time.Duration) error {
	client, err := c.dynamicClient()
	if err != nil {
		return fmt.Errorf("create dynamic client: %w", err)
	}

	gvr := schema.GroupVersionResource{
		Group:    "operator.tigera.io",
		Version:  "v1",
		Resource: "installations",
	}
	dr := client.Resource(gvr)

	installationName, err := c.resolveInstallationName(ctx, dr, timeout)
	if err != nil {
		return err
	}

	calicoNetwork := map[string]interface{}{
		"nodeAddressAutodetectionV4": map[string]interface{}{
			"skipInterface": "liqo.*",
		},
		// Prefer IPIP over VXLAN on provider k3s: Calico's VXLAN UDP NOTRACK rule has
		// been observed to render without --dport on iptables-nft (Ubuntu), which
		// NOTRACKs all UDP and breaks kube-dns ClusterIP DNAT.
		"bgp": "Enabled",
	}
	if c.config.PodCIDR != "" {
		calicoNetwork["ipPools"] = []map[string]interface{}{
			{
				"name":          "default-ipv4-ippool",
				"blockSize":     26,
				"cidr":          c.config.PodCIDR,
				"encapsulation": "IPIPCrossSubnet",
				"natOutgoing":   "Enabled",
				"nodeSelector":  "all()",
			},
		}
	}

	patch := map[string]interface{}{
		"spec": map[string]interface{}{
			"calicoNetwork": calicoNetwork,
		},
	}
	patchBytes, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("marshal patch: %w", err)
	}

	if err := c.waitAndPatchDynamic(ctx, gvr, installationName, patchBytes, timeout, "Installation"); err != nil {
		return err
	}

	c.logger.Info("✓ Patched Calico Installation for k3s networking (IPIPCrossSubnet)")
	return nil
}

func (c *CalicoInstaller) resolveInstallationName(ctx context.Context, dr dynamic.NamespaceableResourceInterface, timeout time.Duration) (string, error) {
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	name := "default"
	var lastErr error
	err := wait.PollUntilContextTimeout(waitCtx, calicoAPIPollInterval, timeout, true, func(ctx context.Context) (bool, error) {
		if _, err := dr.Get(ctx, name, metav1.GetOptions{}); err == nil {
			lastErr = nil
			return true, nil
		} else {
			lastErr = err
		}

		list, listErr := dr.List(ctx, metav1.ListOptions{})
		if listErr != nil {
			if isRetryableCalicoAPIError(listErr) {
				return false, nil
			}
			return false, listErr
		}
		if len(list.Items) > 0 {
			if n := list.Items[0].GetName(); n != "" {
				name = n
				c.logger.Infof("Found Installation resource named %q, using it for patching", name)
				lastErr = nil
				return true, nil
			}
		}
		if lastErr != nil && !isRetryableCalicoAPIError(lastErr) {
			return false, lastErr
		}
		return false, nil
	})
	if err != nil {
		if lastErr != nil {
			return "", fmt.Errorf("installation resource not found within %s: %w", timeout, lastErr)
		}
		return "", fmt.Errorf("installation resource not found within %s: %w", timeout, err)
	}
	return name, nil
}

// patchFelixConfiguration pins Felix settings that keep kube-proxy UDP services healthy.
func (c *CalicoInstaller) patchFelixConfiguration(ctx context.Context, timeout time.Duration) error {
	gvr := schema.GroupVersionResource{
		Group:    "projectcalico.org",
		Version:  "v3",
		Resource: "felixconfigurations",
	}

	patch := map[string]interface{}{
		"spec": map[string]interface{}{
			// Explicit VXLAN port so any residual VXLAN NOTRACK rule cannot match all UDP.
			"vxlanPort": 4789,
			"vxlanVNI":  4096,
		},
	}
	patchBytes, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("marshal felix patch: %w", err)
	}

	if err := c.waitAndPatchDynamic(ctx, gvr, "default", patchBytes, timeout, "FelixConfiguration"); err != nil {
		return err
	}
	c.logger.Info("✓ Patched FelixConfiguration (vxlanPort=4789)")
	return nil
}

// verifyClusterDNS ensures pods can resolve via the kube-dns ClusterIP (UDP/53).
// This catches the Calico VXLAN NOTRACK / kube-proxy DNAT failure before Liqo install.
func (c *CalicoInstaller) verifyClusterDNS(ctx context.Context, timeout time.Duration) error {
	clientset := c.k8sClient.GetClientset()
	const (
		ns      = "default"
		podName = "nr-dns-check"
	)
	// Air-gapped / private-registry hosts cannot pull from Docker Hub.
	image := getenvDefault(config.EnvDNSCheckImage, config.DefaultDNSCheckImage)

	_ = clientset.CoreV1().Pods(ns).Delete(ctx, podName, metav1.DeleteOptions{})

	var grace int64
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: ns,
			Labels:    map[string]string{"app.kubernetes.io/name": "nr-dns-check"},
		},
		Spec: corev1.PodSpec{
			RestartPolicy:                 corev1.RestartPolicyNever,
			TerminationGracePeriodSeconds: &grace,
			Containers: []corev1.Container{
				{
					Name:  "check",
					Image: image,
					Command: []string{
						"sh", "-c",
						// Force UDP to ClusterIP nameserver from /etc/resolv.conf.
						"nslookup kubernetes.default.svc.cluster.local >/tmp/out 2>&1; ec=$?; cat /tmp/out; exit $ec",
					},
				},
			},
		},
	}

	if _, err := clientset.CoreV1().Pods(ns).Create(ctx, pod, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("create dns check pod: %w", err)
	}
	defer func() {
		_ = clientset.CoreV1().Pods(ns).Delete(context.Background(), podName, metav1.DeleteOptions{})
	}()

	deadline := time.Now().Add(minDuration(timeout, 3*time.Minute))
	var phase corev1.PodPhase
	err := wait.PollUntilContextCancel(ctx, 2*time.Second, true, func(ctx context.Context) (bool, error) {
		if time.Now().After(deadline) {
			return false, fmt.Errorf("timeout waiting for dns check pod")
		}
		p, getErr := clientset.CoreV1().Pods(ns).Get(ctx, podName, metav1.GetOptions{})
		if getErr != nil {
			if apierrors.IsNotFound(getErr) {
				return false, nil
			}
			return false, getErr
		}
		phase = p.Status.Phase
		return phase == corev1.PodSucceeded || phase == corev1.PodFailed, nil
	})
	if err != nil {
		return err
	}

	req := clientset.CoreV1().Pods(ns).GetLogs(podName, &corev1.PodLogOptions{})
	logBytes, logErr := req.Do(ctx).Raw()
	logOut := strings.TrimSpace(string(logBytes))
	if phase != corev1.PodSucceeded {
		if logErr != nil {
			return fmt.Errorf("nslookup via kube-dns ClusterIP failed (pod phase=%s); Calico UDP/NOTRACK may be breaking kube-proxy DNAT", phase)
		}
		return fmt.Errorf("nslookup via kube-dns ClusterIP failed (pod phase=%s): %s", phase, logOut)
	}
	if logErr == nil && logOut != "" {
		c.logger.Debugf("DNS check output: %s", logOut)
	}
	c.logger.Info("✓ ClusterIP DNS is working")
	return nil
}

func minDuration(a, b time.Duration) time.Duration {
	if a > 0 && a < b {
		return a
	}
	return b
}

// waitForCalicoPods waits for Calico pods to be ready
func (c *CalicoInstaller) waitForCalicoPods(ctx context.Context, timeout time.Duration) error {
	clientset := c.k8sClient.GetClientset()
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			daemonset, err := clientset.AppsV1().DaemonSets(config.DefaultCalicoSystemNamespace).Get(ctx, config.DefaultCalicoNodeDaemonSet, metav1.GetOptions{})
			if err != nil {
				if time.Now().After(deadline) {
					return fmt.Errorf("timeout waiting for Calico daemonset: %w", err)
				}
				continue
			}

			nodes, listErr := clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
			physicalReady := 0
			if listErr == nil {
				physicalReady = countReadyPhysicalNodes(nodes.Items)
			}

			ready := int(daemonset.Status.NumberReady)
			desired := int(daemonset.Status.DesiredNumberScheduled)
			if calicoReadyIgnoringVirtualNodes(ready, physicalReady) {
				if desired > ready {
					c.logger.Infof("Calico ready on %d physical node(s); ignoring %d DaemonSet slot(s) on Liqo virtual node(s)",
						physicalReady, desired-ready)
				}
				return nil
			}

			if time.Now().After(deadline) {
				return fmt.Errorf("timeout waiting for Calico pods (ready: %d/%d, physical nodes: %d)",
					ready, desired, physicalReady)
			}
		}
	}
}
