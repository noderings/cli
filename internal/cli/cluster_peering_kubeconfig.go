package cli

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"strings"
	"time"

	"k8s.io/client-go/tools/clientcmd"

	"github.com/noderings/cli/internal/config"
)

// resolvePeeringAPIServerURL picks a control-plane Kubernetes API URL reachable from the
// provider VM with valid TLS when possible. Liqo's ResourceSlice acceptance requires
// the foreign-cluster API server checker to verify the remote apiserver using the
// peering kubeconfig CA — rewriting to an IP absent from the server cert SANs breaks
// peering even when insecure-skip works for liqoctl itself.
func resolvePeeringAPIServerURL(kubeconfig string, cfg *config.Config) (serverURL string, insecureSkipTLSVerify bool) {
	if cfg == nil || strings.TrimSpace(cfg.Mothership.Host) == "" {
		return "", false
	}

	port := cfg.Mothership.K8sAPIPort
	if port == 0 {
		port = config.DefaultMothershipK8sAPIPort
	}

	caData := peeringKubeconfigCAData(kubeconfig)
	candidates := peeringAPIServerCandidates(kubeconfig, cfg)

	if len(caData) > 0 {
		if host, ok := firstTLSValidHost(caData, candidates, port); ok {
			return fmt.Sprintf("https://%s:%d", host, port), false
		}
	}

	// Insecure fallback must stay on a host the provider can dial (mothership.host).
	// Never prefer k8s_api_host here — bootstrap often writes a cluster-internal SAN
	// that is unreachable from the provider VM.
	host := strings.TrimSpace(cfg.Mothership.Host)
	return fmt.Sprintf("https://%s:%d", host, port), cfg.Mothership.TLSInsecure
}

func peeringKubeconfigCAData(kubeconfig string) []byte {
	cfg, err := clientcmd.Load([]byte(kubeconfig))
	if err != nil {
		return nil
	}
	for _, cluster := range cfg.Clusters {
		if cluster == nil {
			continue
		}
		if len(cluster.CertificateAuthorityData) > 0 {
			return cluster.CertificateAuthorityData
		}
	}
	return nil
}

func peeringAPIServerCandidates(kubeconfig string, cfg *config.Config) []string {
	seen := map[string]struct{}{}
	var out []string

	add := func(host string) {
		host = strings.TrimSpace(host)
		if host == "" || host == "0.0.0.0" || host == "127.0.0.1" || host == "::1" {
			return
		}
		if _, ok := seen[host]; ok {
			return
		}
		seen[host] = struct{}{}
		out = append(out, host)
	}

	// Prefer reachable host addresses first. k8s_api_host is often a cert SAN that is
	// cluster-internal (unroutable from the provider); keep it last for TLS name checks.
	if cfg != nil {
		add(cfg.Mothership.Host)
	}

	if cfgObj, err := clientcmd.Load([]byte(kubeconfig)); err == nil {
		for _, cluster := range cfgObj.Clusters {
			if cluster == nil {
				continue
			}
			if host := hostFromServerURL(cluster.Server); host != "" {
				add(host)
			}
		}
	}

	if cfg != nil {
		add(cfg.Mothership.K8sAPIHost)
	}

	return out
}

func hostFromServerURL(server string) string {
	server = strings.TrimSpace(server)
	if server == "" {
		return ""
	}
	if !strings.Contains(server, "://") {
		server = "https://" + server
	}
	host, _, err := net.SplitHostPort(strings.TrimPrefix(strings.TrimPrefix(server, "https://"), "http://"))
	if err != nil {
		// No port in URL.
		return strings.TrimPrefix(strings.TrimPrefix(server, "https://"), "http://")
	}
	return host
}

func firstTLSValidHost(caData []byte, candidates []string, port int) (string, bool) {
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caData) {
		return "", false
	}

	for _, host := range candidates {
		if host == "" {
			continue
		}
		addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))
		conn, err := tls.DialWithDialer(
			&net.Dialer{Timeout: 3 * time.Second},
			"tcp",
			addr,
			&tls.Config{
				RootCAs:    pool,
				ServerName: host,
				MinVersion: tls.VersionTLS12,
			},
		)
		if err != nil {
			continue
		}
		_ = conn.Close()
		return host, true
	}
	return "", false
}
