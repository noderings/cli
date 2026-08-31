package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noderings/cli/internal/config"
)

func TestCredentialsSecretName(t *testing.T) {
	got := CredentialsSecretName("operator", "proxmox-1")
	want := "operator-proxmox-operator-proxmox-credentials-proxmox-1"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestLoadProxmoxInstancesFileWrapped(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "instances.yaml")
	content := `
instances:
  - id: proxmox-1
    url: https://pve1:8006
    username: user@pve
    tokenId: tok
    tokenSecret: secret1
  - id: proxmox-2
    url: https://pve2:8006
    username: user@pve
    tokenId: tok2
    tokenSecret: secret2
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadProxmoxInstancesFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d", len(got))
	}
	if got[1].ID != "proxmox-2" || got[1].TokenSecret != "secret2" {
		t.Fatalf("unexpected second instance: %+v", got[1])
	}
}

func TestLoadProxmoxInstancesFileList(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "list.yaml")
	content := `
- url: https://pve1:8006
  username: user@pve
  tokenId: tok
  tokenSecret: secret1
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadProxmoxInstancesFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "proxmox-1" {
		t.Fatalf("expected default id proxmox-1, got %+v", got)
	}
}

func TestProxmoxInstanceValidate(t *testing.T) {
	err := (ProxmoxInstance{ID: "x"}).Validate()
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestProxmoxInstanceValidateRequiresHTTPS(t *testing.T) {
	inst := ProxmoxInstance{ID: "x", URL: "http://pve:8006", Username: "u@pve", TokenID: "t", TokenSecret: "s"}
	err := inst.Validate()
	if err == nil || !strings.Contains(err.Error(), "https") {
		t.Fatalf("expected https requirement, got %v", err)
	}

	inst.URL = "https://pve:8006"
	if err := inst.Validate(); err != nil {
		t.Fatalf("https url should be valid: %v", err)
	}
}

func TestProxmoxInstanceValidateRejectsSwappedTokenFields(t *testing.T) {
	// The trap: token ID typed as username, UUID secret typed as tokenId.
	err := (ProxmoxInstance{
		ID:          "proxmox-1",
		URL:         "https://192.168.1.100:8006",
		Username:    "kopf-operator-token",
		TokenID:     "4e441214-2036-42c9-a924-963e9a0f1981",
		TokenSecret: "unused",
	}).Validate()
	if err == nil || !strings.Contains(err.Error(), "user@realm") {
		t.Fatalf("expected username realm error, got %v", err)
	}

	err = (ProxmoxInstance{
		ID:          "proxmox-1",
		URL:         "https://192.168.1.100:8006",
		Username:    "kopfoperator@pve",
		TokenID:     "4e441214-2036-42c9-a924-963e9a0f1981",
		TokenSecret: "unused",
	}).Validate()
	if err == nil || !strings.Contains(err.Error(), "UUID") {
		t.Fatalf("expected tokenId UUID error, got %v", err)
	}

	err = (ProxmoxInstance{
		ID:          "proxmox-1",
		URL:         "https://192.168.1.100:8006",
		Username:    "kopfoperator@pve",
		TokenID:     "kopf-operator-token",
		TokenSecret: "4e441214-2036-42c9-a924-963e9a0f1981",
	}).Validate()
	if err != nil {
		t.Fatalf("valid instance: %v", err)
	}
}

func TestLoadProxmoxInstancesFileRejectsWorldReadable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "instances.yaml")
	content := "instances:\n  - id: p1\n    url: https://pve:8006\n    username: u@pve\n    tokenId: t\n    tokenSecret: s\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadProxmoxInstancesFile(path); err == nil || !strings.Contains(err.Error(), "chmod 600") {
		t.Fatalf("expected permission error, got %v", err)
	}

	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadProxmoxInstancesFile(path); err != nil {
		t.Fatalf("0600 file should load: %v", err)
	}
}

func TestProxmoxInstanceFromEnv(t *testing.T) {
	t.Setenv("PROXMOX_URL", "")
	t.Setenv("PROXMOX_USERNAME", "")
	t.Setenv("PROXMOX_TOKEN_ID", "")
	t.Setenv("PROXMOX_TOKEN_SECRET", "")
	inst, err := ProxmoxInstanceFromEnv()
	if err != nil || inst != nil {
		t.Fatalf("expected nil,nil got %v %v", inst, err)
	}

	t.Setenv("PROXMOX_URL", "https://pve:8006")
	t.Setenv("PROXMOX_USERNAME", "u@pve")
	t.Setenv("PROXMOX_TOKEN_ID", "tid")
	t.Setenv("PROXMOX_TOKEN_SECRET", "sec")
	inst, err = ProxmoxInstanceFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if inst == nil || inst.ID != "proxmox-1" {
		t.Fatalf("got %+v", inst)
	}
}

func TestResolveOperatorChartOCIDefault(t *testing.T) {
	t.Setenv("PROXMOX_OPERATOR_CHART", "")
	t.Setenv("PROXMOX_OPERATOR_CHART_VERSION", "")
	// Avoid picking up a local sibling chart from the caller's cwd by using an explicit empty
	// and verifying OCI when no local Chart.yaml is found relative to cwd — may still find
	// sibling if tests run from monorepo. Prefer explicit OCI.
	chart, ver := ResolveOperatorChart("oci://harbor.noderings.com/noderings/proxmox-operator")
	if chart != "oci://harbor.noderings.com/noderings/proxmox-operator" {
		t.Fatalf("chart=%q", chart)
	}
	if ver == "" {
		t.Fatal("expected version")
	}
}

func TestBaseConfigFromEnvVNCGatewayImageTag(t *testing.T) {
	cfg := BaseConfigFromEnv("")
	if cfg.VNCGatewayImageTag != config.DefaultVNCGatewayImageTagProxmox {
		t.Fatalf("proxmox tag=%s want %s (generic rfb must not apply)", cfg.VNCGatewayImageTag, config.DefaultVNCGatewayImageTagProxmox)
	}

	t.Setenv(config.EnvProxmoxVNCGatewayImageTag, "pve-custom")
	cfg = BaseConfigFromEnv("")
	if cfg.VNCGatewayImageTag != "pve-custom" {
		t.Fatalf("driver tag=%s", cfg.VNCGatewayImageTag)
	}
}
