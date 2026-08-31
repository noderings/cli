package cli

import (
	"testing"

	"k8s.io/client-go/tools/clientcmd"
)

func TestOverrideKubeconfigServerInsecure(t *testing.T) {
	t.Parallel()

	const input = `apiVersion: v1
clusters:
- cluster:
    certificate-authority-data: Y2E=
    server: https://127.0.0.1:6443
  name: remote
contexts:
- context:
    cluster: remote
    user: remote
  name: remote
current-context: remote
kind: Config
users:
- name: remote
  user:
    token: test
`

	got, changed, err := overrideKubeconfigServer(input, "https://192.168.2.1:16443", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changed {
		t.Fatal("expected kubeconfig to change")
	}

	cfg, err := clientcmd.Load([]byte(got))
	if err != nil {
		t.Fatalf("load rewritten kubeconfig: %v", err)
	}
	cluster := cfg.Clusters["remote"]
	if cluster == nil {
		t.Fatal("missing remote cluster")
	}
	if cluster.Server != "https://192.168.2.1:16443" {
		t.Fatalf("server = %q, want rewritten URL", cluster.Server)
	}
	if !cluster.InsecureSkipTLSVerify {
		t.Fatal("expected insecure TLS verify skip for --dev control plane")
	}
	if len(cluster.CertificateAuthorityData) != 0 {
		t.Fatal("expected CA data cleared when skipping TLS verify")
	}
}
