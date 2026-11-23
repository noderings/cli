package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noderings/cli/internal/config"
)

func TestSolusVMCredentialsSecretName(t *testing.T) {
	got := SolusVMCredentialsSecretName("operator", "svm-1")
	want := "operator-solusvm-operator-solusvm-credentials-svm-1"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestLoadSolusVMInstancesFileWrapped(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "instances.yaml")
	content := `
instances:
  - id: svm-1
    url: https://mn1.example.com
    token: token1
  - id: svm-2
    url: https://mn2.example.com
    token: token2
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadSolusVMInstancesFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d", len(got))
	}
	if got[1].ID != "svm-2" || got[1].Token != "token2" {
		t.Fatalf("unexpected second instance: %+v", got[1])
	}
}

func TestLoadSolusVMInstancesFileList(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "list.yaml")
	content := `
- url: https://mn.example.com
  token: tok
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadSolusVMInstancesFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "svm-1" {
		t.Fatalf("expected default id svm-1, got %+v", got)
	}
}

func TestSolusVMInstanceValidate(t *testing.T) {
	err := (SolusVMInstance{ID: "x"}).Validate()
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestSolusVMInstanceValidateRequiresHTTPS(t *testing.T) {
	inst := SolusVMInstance{ID: "x", URL: "http://mn.example.com", Token: "t"}
	err := inst.Validate()
	if err == nil || !strings.Contains(err.Error(), "https") {
		t.Fatalf("expected https requirement, got %v", err)
	}

	inst.URL = "https://mn.example.com"
	if err := inst.Validate(); err != nil {
		t.Fatalf("https url should be valid: %v", err)
	}
}

func TestSolusVMInstanceValidateRejectsSolusVM1(t *testing.T) {
	inst := SolusVMInstance{ID: "x", URL: "https://mn.example.com:5656", Token: "t"}
	err := inst.Validate()
	if err == nil || !strings.Contains(err.Error(), "5656") {
		t.Fatalf("expected SolusVM 1 :5656 rejection, got %v", err)
	}

	inst.URL = "https://mn.example.com/api/admin/command.php"
	err = inst.Validate()
	if err == nil || !strings.Contains(err.Error(), "/api/admin") {
		t.Fatalf("expected SolusVM 1 /api/admin rejection, got %v", err)
	}
}

func TestLoadSolusVMInstancesFileRejectsWorldReadable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "instances.yaml")
	content := "instances:\n  - id: svm-1\n    url: https://mn.example.com\n    token: t\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSolusVMInstancesFile(path); err == nil || !strings.Contains(err.Error(), "chmod 600") {
		t.Fatalf("expected permission error, got %v", err)
	}

	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSolusVMInstancesFile(path); err != nil {
		t.Fatalf("0600 file should load: %v", err)
	}
}

func TestSolusVMInstanceFromEnv(t *testing.T) {
	t.Setenv("SOLUSVM_URL", "")
	t.Setenv("SOLUSVM_TOKEN", "")
	t.Setenv("SOLUSVM_INSTANCE_ID", "")
	inst, err := SolusVMInstanceFromEnv()
	if err != nil || inst != nil {
		t.Fatalf("expected nil,nil got %v %v", inst, err)
	}

	t.Setenv("SOLUSVM_URL", "https://mn.example.com")
	t.Setenv("SOLUSVM_TOKEN", "tok")
	inst, err = SolusVMInstanceFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if inst == nil || inst.ID != "svm-1" {
		t.Fatalf("got %+v", inst)
	}
}

func TestResolveSolusVMOperatorChartOCIDefault(t *testing.T) {
	t.Setenv("SOLUSVM_OPERATOR_CHART", "")
	t.Setenv("SOLUSVM_OPERATOR_CHART_VERSION", "")
	chart, ver := ResolveSolusVMOperatorChart("oci://harbor.noderings.com/nrings/solusvm-operator")
	if chart != "oci://harbor.noderings.com/nrings/solusvm-operator" {
		t.Fatalf("chart=%q", chart)
	}
	if ver == "" {
		t.Fatal("expected version")
	}
}

func TestSolusVMBaseConfigFromEnvHelmNamespace(t *testing.T) {
	t.Setenv(config.EnvHelmNamespace, "")
	cfg := SolusVMBaseConfigFromEnv("")
	if cfg.HelmNamespace != config.DefaultSolusVMOperatorHelmNamespace {
		t.Fatalf("default helm ns=%s want %s", cfg.HelmNamespace, config.DefaultSolusVMOperatorHelmNamespace)
	}

	t.Setenv(config.EnvHelmNamespace, "custom-svm")
	cfg = SolusVMBaseConfigFromEnv("")
	if cfg.HelmNamespace != "custom-svm" {
		t.Fatalf("explicit HELM_NAMESPACE=%s", cfg.HelmNamespace)
	}
}

func TestSolusVMBaseConfigFromEnvVNCGatewayImageTag(t *testing.T) {
	t.Setenv(config.EnvVNCGatewayImageTag, "")
	t.Setenv(config.EnvSolusVMVNCGatewayImageTag, "")
	cfg := SolusVMBaseConfigFromEnv("")
	if cfg.VNCGatewayImageTag != config.DefaultVNCGatewayImageTagRFB {
		t.Fatalf("default tag=%s want %s", cfg.VNCGatewayImageTag, config.DefaultVNCGatewayImageTagRFB)
	}

	t.Setenv(config.EnvSolusVMVNCGatewayImageTag, "custom-rfb")
	cfg = SolusVMBaseConfigFromEnv("")
	if cfg.VNCGatewayImageTag != "custom-rfb" {
		t.Fatalf("driver tag=%s", cfg.VNCGatewayImageTag)
	}
}
