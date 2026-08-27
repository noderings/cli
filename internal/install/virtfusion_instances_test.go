package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noderings/cli/internal/config"
)

func TestVirtFusionCredentialsSecretName(t *testing.T) {
	got := VirtFusionCredentialsSecretName("operator", "vf-1")
	want := "operator-virtfusion-operator-virtfusion-credentials-vf-1"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestLoadVirtFusionInstancesFileWrapped(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "instances.yaml")
	content := `
instances:
  - id: vf-1
    url: https://cp1.example.com
    token: token1
  - id: vf-2
    url: https://cp2.example.com
    token: token2
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadVirtFusionInstancesFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d", len(got))
	}
	if got[1].ID != "vf-2" || got[1].Token != "token2" {
		t.Fatalf("unexpected second instance: %+v", got[1])
	}
}

func TestLoadVirtFusionInstancesFileList(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "list.yaml")
	content := `
- url: https://cp.example.com
  token: tok
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadVirtFusionInstancesFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "vf-1" {
		t.Fatalf("expected default id vf-1, got %+v", got)
	}
}

func TestVirtFusionInstanceValidate(t *testing.T) {
	err := (VirtFusionInstance{ID: "x"}).Validate()
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestVirtFusionInstanceValidateRequiresHTTPS(t *testing.T) {
	inst := VirtFusionInstance{ID: "x", URL: "http://cp.example.com", Token: "t"}
	err := inst.Validate()
	if err == nil || !strings.Contains(err.Error(), "https") {
		t.Fatalf("expected https requirement, got %v", err)
	}

	inst.URL = "https://cp.example.com"
	if err := inst.Validate(); err != nil {
		t.Fatalf("https url should be valid: %v", err)
	}
}

func TestLoadVirtFusionInstancesFileRejectsWorldReadable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "instances.yaml")
	content := "instances:\n  - id: vf-1\n    url: https://cp.example.com\n    token: t\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadVirtFusionInstancesFile(path); err == nil || !strings.Contains(err.Error(), "chmod 600") {
		t.Fatalf("expected permission error, got %v", err)
	}

	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadVirtFusionInstancesFile(path); err != nil {
		t.Fatalf("0600 file should load: %v", err)
	}
}

func TestVirtFusionInstanceFromEnv(t *testing.T) {
	t.Setenv("VIRTFUSION_URL", "")
	t.Setenv("VIRTFUSION_TOKEN", "")
	t.Setenv("VIRTFUSION_INSTANCE_ID", "")
	inst, err := VirtFusionInstanceFromEnv()
	if err != nil || inst != nil {
		t.Fatalf("expected nil,nil got %v %v", inst, err)
	}

	t.Setenv("VIRTFUSION_URL", "https://cp.example.com")
	t.Setenv("VIRTFUSION_TOKEN", "tok")
	inst, err = VirtFusionInstanceFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if inst == nil || inst.ID != "vf-1" {
		t.Fatalf("got %+v", inst)
	}
}

func TestResolveVirtFusionOperatorChartOCIDefault(t *testing.T) {
	t.Setenv("VIRTFUSION_OPERATOR_CHART", "")
	t.Setenv("VIRTFUSION_OPERATOR_CHART_VERSION", "")
	chart, ver := ResolveVirtFusionOperatorChart("oci://harbor.noderings.com/noderings/virtfusion-operator")
	if chart != "oci://harbor.noderings.com/noderings/virtfusion-operator" {
		t.Fatalf("chart=%q", chart)
	}
	if ver == "" {
		t.Fatal("expected version")
	}
}

func TestVirtFusionBaseConfigFromEnvHelmNamespace(t *testing.T) {
	t.Setenv(config.EnvHelmNamespace, "")
	cfg := VirtFusionBaseConfigFromEnv("")
	if cfg.HelmNamespace != config.DefaultVirtFusionOperatorHelmNamespace {
		t.Fatalf("default helm ns=%s want %s", cfg.HelmNamespace, config.DefaultVirtFusionOperatorHelmNamespace)
	}

	t.Setenv(config.EnvHelmNamespace, "custom-vf")
	cfg = VirtFusionBaseConfigFromEnv("")
	if cfg.HelmNamespace != "custom-vf" {
		t.Fatalf("explicit HELM_NAMESPACE=%s", cfg.HelmNamespace)
	}
}

func TestVirtFusionBaseConfigFromEnvVNCGatewayImageTag(t *testing.T) {
	t.Setenv(config.EnvVNCGatewayImageTag, "")
	t.Setenv(config.EnvVirtFusionVNCGatewayImageTag, "")
	cfg := VirtFusionBaseConfigFromEnv("")
	if cfg.VNCGatewayImageTag != config.DefaultVNCGatewayImageTagRFB {
		t.Fatalf("default tag=%s want %s", cfg.VNCGatewayImageTag, config.DefaultVNCGatewayImageTagRFB)
	}

	t.Setenv(config.EnvVirtFusionVNCGatewayImageTag, "custom-rfb")
	cfg = VirtFusionBaseConfigFromEnv("")
	if cfg.VNCGatewayImageTag != "custom-rfb" {
		t.Fatalf("driver tag=%s", cfg.VNCGatewayImageTag)
	}
}
