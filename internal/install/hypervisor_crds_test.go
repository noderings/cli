package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noderings/cli/internal/config"
)

func writeChart(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Chart.yaml"), []byte("name: test\nversion: 0.1.2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestResolveCRDChartPrefersSiblingOfLocalOperatorChart(t *testing.T) {
	charts := t.TempDir()
	writeChart(t, filepath.Join(charts, "virtfusion-operator"))
	writeChart(t, filepath.Join(charts, "virtfusion-operator-crds"))
	writeChart(t, filepath.Join(charts, "proxmox-operator-crds"))

	t.Setenv("VIRTFUSION_OPERATOR_CHART", filepath.Join(charts, "virtfusion-operator"))
	t.Setenv("VIRTFUSION_OPERATOR_CRDS_CHART", "")
	t.Setenv("PROXMOX_OPERATOR_CRDS_CHART", "")
	t.Setenv("PROXMOX_OPERATOR_CHART", "")

	px := hypervisorCRDCharts()[0]
	got, ver := resolveCRDChart(px)
	want := filepath.Join(charts, "proxmox-operator-crds")
	if got != want || ver != "" {
		t.Fatalf("proxmox CRDs: got %q version=%q want %q empty version", got, ver, want)
	}

	vf := hypervisorCRDCharts()[1]
	got, ver = resolveCRDChart(vf)
	want = filepath.Join(charts, "virtfusion-operator-crds")
	if got != want || ver != "" {
		t.Fatalf("virtfusion CRDs: got %q version=%q want %q empty version", got, ver, want)
	}
}

func TestResolveCRDChartExplicitEnvOCI(t *testing.T) {
	t.Setenv("PROXMOX_OPERATOR_CRDS_CHART", "oci://example.invalid/proxmox-operator-crds")
	t.Setenv("VIRTFUSION_OPERATOR_CHART", "")
	t.Setenv("VIRTFUSION_OPERATOR_CRDS_CHART", "")
	t.Setenv("PROXMOX_OPERATOR_CHART", "")

	px := hypervisorCRDCharts()[0]
	got, ver := resolveCRDChart(px)
	if got != "oci://example.invalid/proxmox-operator-crds" {
		t.Fatalf("chart=%q", got)
	}
	if ver != config.DefaultProxmoxOperatorChartVersion {
		t.Fatalf("version=%q", ver)
	}
}

func TestResolveCRDChartFallsBackToDefaultOCI(t *testing.T) {
	t.Setenv("PROXMOX_OPERATOR_CRDS_CHART", "")
	t.Setenv("VIRTFUSION_OPERATOR_CRDS_CHART", "")
	t.Setenv("SOLUSVM_OPERATOR_CRDS_CHART", "")
	t.Setenv("PROXMOX_OPERATOR_CHART", "")
	t.Setenv("VIRTFUSION_OPERATOR_CHART", "")
	t.Setenv("SOLUSVM_OPERATOR_CHART", "")
	t.Setenv("HYPERVISOR_OPERATOR_CRDS_CHART_VERSION", "")

	px := hypervisorCRDCharts()[0]
	got, ver := resolveCRDChart(px)
	if !strings.HasPrefix(got, "oci://") {
		t.Fatalf("expected OCI fallback, got %q", got)
	}
	if got != config.DefaultProxmoxOperatorCRDsChartOCI {
		t.Fatalf("oci=%q", got)
	}
	if ver != config.DefaultProxmoxOperatorChartVersion {
		t.Fatalf("version=%q", ver)
	}
}
