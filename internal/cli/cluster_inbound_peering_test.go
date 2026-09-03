package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"k8s.io/client-go/tools/clientcmd"

	"github.com/noderings/cli/internal/config"
)

// peeringKubeconfig builds a peering-user kubeconfig pointing at the given API server.
func peeringKubeconfig(server string) string {
	return `apiVersion: v1
kind: Config
clusters:
- cluster:
    certificate-authority-data: ZmFrZQ==
    server: ` + server + `
  name: provider
contexts:
- context:
    cluster: provider
    namespace: liqo-tenant-abc
    user: provider-user
  name: default-context
current-context: default-context
users:
- name: provider-user
  user:
    client-certificate-data: ZmFrZQ==
    client-key-data: ZmFrZQ==
`
}

func resolvesTo(proxyURL string) func(context.Context) (string, error) {
	return func(context.Context) (string, error) { return proxyURL, nil }
}

func mustNotResolve(t *testing.T) func(context.Context) (string, error) {
	t.Helper()
	return func(context.Context) (string, error) {
		t.Error("proxy URL resolver should not have been called")
		return "", nil
	}
}

func TestApplyInboundAPIServerProxySetsProxyURL(t *testing.T) {
	in := peeringKubeconfig("https://192.168.1.161:6443")

	out, err := applyInboundAPIServerProxy(t.Context(), testLogger(t), config.InboundAPIProxyAuto, in,
		resolvesTo("http://10.81.0.1:8118"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cfg, err := clientcmd.Load([]byte(out))
	if err != nil {
		t.Fatalf("result is not a valid kubeconfig: %v", err)
	}
	cluster := cfg.Clusters["provider"]
	if cluster == nil {
		t.Fatal("cluster entry was dropped")
	}
	if cluster.ProxyURL != "http://10.81.0.1:8118" {
		t.Errorf("proxy-url = %q, want %q", cluster.ProxyURL, "http://10.81.0.1:8118")
	}
	// The Liqo proxy pins the CONNECT target, so the server must survive untouched for the
	// API server certificate to still validate.
	if cluster.Server != "https://192.168.1.161:6443" {
		t.Errorf("server = %q, want it preserved", cluster.Server)
	}
	if cfg.Contexts["default-context"].Namespace != "liqo-tenant-abc" {
		t.Error("context namespace was lost")
	}
}

func TestApplyInboundAPIServerProxySkips(t *testing.T) {
	tests := []struct {
		name   string
		mode   string
		server string
	}{
		{
			name:   "never leaves a private address alone",
			mode:   config.InboundAPIProxyNever,
			server: "https://192.168.1.161:6443",
		},
		{
			name:   "auto leaves a public address alone",
			mode:   config.InboundAPIProxyAuto,
			server: "https://95.179.200.10:6443",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := peeringKubeconfig(tt.server)
			out, err := applyInboundAPIServerProxy(t.Context(), testLogger(t), tt.mode, in, mustNotResolve(t))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if out != in {
				t.Error("kubeconfig was modified")
			}
		})
	}
}

func TestApplyInboundAPIServerProxyAlwaysOnPublicAddress(t *testing.T) {
	in := peeringKubeconfig("https://95.179.200.10:6443")

	out, err := applyInboundAPIServerProxy(t.Context(), testLogger(t), config.InboundAPIProxyAlways, in,
		resolvesTo("http://10.81.0.1:8118"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cfg, _ := clientcmd.Load([]byte(out))
	if cfg.Clusters["provider"].ProxyURL == "" {
		t.Error("always mode should proxy even a public address")
	}
}

func TestApplyInboundAPIServerProxyErrors(t *testing.T) {
	t.Run("unparsable kubeconfig", func(t *testing.T) {
		_, err := applyInboundAPIServerProxy(t.Context(), testLogger(t), config.InboundAPIProxyAlways,
			"\tnot: [valid", resolvesTo("http://10.81.0.1:8118"))
		if err == nil {
			t.Fatal("expected an error")
		}
	})

	t.Run("resolver failure is wrapped", func(t *testing.T) {
		sentinel := errors.New("no configuration yet")
		_, err := applyInboundAPIServerProxy(t.Context(), testLogger(t), config.InboundAPIProxyAlways,
			peeringKubeconfig("https://192.168.1.161:6443"),
			func(context.Context) (string, error) { return "", sentinel })
		if !errors.Is(err, sentinel) {
			t.Fatalf("error does not wrap the resolver failure: %v", err)
		}
	})
}

func TestCurrentCluster(t *testing.T) {
	t.Run("resolves through the current context", func(t *testing.T) {
		cfg, err := clientcmd.Load([]byte(peeringKubeconfig("https://192.168.1.161:6443")))
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		cluster, err := currentCluster(cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cluster.Server != "https://192.168.1.161:6443" {
			t.Errorf("got %q", cluster.Server)
		}
	})

	t.Run("missing context", func(t *testing.T) {
		cfg, err := clientcmd.Load([]byte("apiVersion: v1\nkind: Config\ncurrent-context: nope\n"))
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if _, err := currentCluster(cfg); err == nil || !strings.Contains(err.Error(), "no context named") {
			t.Errorf("got %v", err)
		}
	})

	t.Run("context references an unknown cluster", func(t *testing.T) {
		cfg, err := clientcmd.Load([]byte(`apiVersion: v1
kind: Config
contexts:
- context:
    cluster: ghost
    user: u
  name: c
current-context: c
`))
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if _, err := currentCluster(cfg); err == nil || !strings.Contains(err.Error(), "unknown cluster") {
			t.Errorf("got %v", err)
		}
	})
}

func TestParseInboundAPIProxyMode(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{in: "", want: config.DefaultInboundAPIProxy},
		{in: "auto", want: config.InboundAPIProxyAuto},
		{in: "  AUTO  ", want: config.InboundAPIProxyAuto},
		{in: "always", want: config.InboundAPIProxyAlways},
		{in: "never", want: config.InboundAPIProxyNever},
		{in: "sometimes", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := parseInboundAPIProxyMode(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got %q", got)
				}
				if ExitCode(err) != ExitMisuse {
					t.Errorf("invalid config should exit with %d, got %d", ExitMisuse, ExitCode(err))
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
