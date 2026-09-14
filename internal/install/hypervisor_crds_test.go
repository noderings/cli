package install

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/noderings/cli/internal/config"
)

func writeChart(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, helmChartFile), []byte("name: test\nversion: 0.1.2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func clearHypervisorChartEnvs(t *testing.T) {
	t.Helper()
	for _, key := range localOperatorChartEnvs() {
		t.Setenv(key, "")
	}
	t.Setenv(config.EnvHypervisorOperatorCRDsChartVersion, "")
}

func crdChartNamed(t *testing.T, release string) hypervisorCRDChart {
	t.Helper()
	for _, c := range hypervisorCRDCharts() {
		if c.release == release {
			return c
		}
	}
	t.Fatalf("missing CRD chart %s", release)
	return hypervisorCRDChart{}
}

func TestResolveCRDChartPrefersSiblingOfLocalOperatorChart(t *testing.T) {
	charts := t.TempDir()
	writeChart(t, filepath.Join(charts, "virtfusion-operator"))
	writeChart(t, filepath.Join(charts, helmReleaseVirtFusionCRDs))
	writeChart(t, filepath.Join(charts, helmReleaseProxmoxCRDs))

	clearHypervisorChartEnvs(t)
	t.Setenv(config.EnvVirtFusionOperatorChart, filepath.Join(charts, "virtfusion-operator"))

	px := crdChartNamed(t, helmReleaseProxmoxCRDs)
	got, ver := resolveCRDChart(px)
	want := filepath.Join(charts, helmReleaseProxmoxCRDs)
	if got != want || ver != "" {
		t.Fatalf("proxmox CRDs: got %q version=%q want %q empty version", got, ver, want)
	}

	vf := crdChartNamed(t, helmReleaseVirtFusionCRDs)
	got, ver = resolveCRDChart(vf)
	want = filepath.Join(charts, helmReleaseVirtFusionCRDs)
	if got != want || ver != "" {
		t.Fatalf("virtfusion CRDs: got %q version=%q want %q empty version", got, ver, want)
	}
}

func TestResolveCRDChartExplicitEnvOCI(t *testing.T) {
	clearHypervisorChartEnvs(t)
	oci := helmOCIScheme + "example.invalid/" + helmReleaseProxmoxCRDs
	t.Setenv(config.EnvProxmoxOperatorCRDsChart, oci)

	px := crdChartNamed(t, helmReleaseProxmoxCRDs)
	got, ver := resolveCRDChart(px)
	if got != oci {
		t.Fatalf("chart=%q", got)
	}
	if ver != config.DefaultProxmoxOperatorChartVersion {
		t.Fatalf("version=%q", ver)
	}
}

func TestResolveCRDChartFallsBackToDefaultOCI(t *testing.T) {
	clearHypervisorChartEnvs(t)

	px := crdChartNamed(t, helmReleaseProxmoxCRDs)
	got, ver := resolveCRDChart(px)
	if !strings.HasPrefix(got, helmOCIScheme) {
		t.Fatalf("expected OCI fallback, got %q", got)
	}
	if got != config.DefaultProxmoxOperatorCRDsChartOCI {
		t.Fatalf("oci=%q", got)
	}
	if ver != config.DefaultProxmoxOperatorChartVersion {
		t.Fatalf("version=%q", ver)
	}

	vf := crdChartNamed(t, helmReleaseVirtFusionCRDs)
	got, ver = resolveCRDChart(vf)
	if got != config.DefaultVirtFusionOperatorCRDsChartOCI {
		t.Fatalf("virtfusion oci=%q", got)
	}
	if ver != config.DefaultVirtFusionOperatorChartVersion {
		t.Fatalf("virtfusion version=%q", ver)
	}

	svm := crdChartNamed(t, helmReleaseSolusVMCRDs)
	got, ver = resolveCRDChart(svm)
	if got != config.DefaultSolusVMOperatorCRDsChartOCI {
		t.Fatalf("solusvm oci=%q", got)
	}
	if ver != config.DefaultSolusVMOperatorChartVersion {
		t.Fatalf("solusvm version=%q", ver)
	}
}

func TestIsHelmCRDOwnershipConflict(t *testing.T) {
	t.Parallel()
	err := fmt.Errorf("helm proxmox-operator-crds: exit status 1")
	out := `Unable to continue with install: CustomResourceDefinition "firewallrulesets.vm.proxmox.com" in namespace "" exists and cannot be imported into the current release: invalid ownership metadata; annotation validation error: key "meta.helm.sh/release-name" must equal "proxmox-operator-crds"`
	if !isHelmCRDOwnershipConflict(err, []byte(out)) {
		t.Fatal("expected ownership conflict")
	}
	if isHelmCRDOwnershipConflict(nil, []byte(out)) {
		t.Fatal("nil error is not an ownership conflict")
	}
	if isHelmCRDOwnershipConflict(fmt.Errorf("connection refused"), []byte("dial tcp")) {
		t.Fatal("network errors are not ownership conflicts")
	}
}

func TestIsHypervisorCRDName(t *testing.T) {
	t.Parallel()
	proxmoxVM := "proxmoxvms." + config.ProxmoxCRDAPIGroup
	if !isHypervisorCRDName(proxmoxVM) {
		t.Fatal("proxmox")
	}
	if !isHypervisorCRDName("virtfusionvms." + config.VirtFusionCRDAPIGroup) {
		t.Fatal("virtfusion")
	}
	if !isHypervisorCRDName("solusvmvms." + config.SolusVMCRDAPIGroup) {
		t.Fatal("solusvm")
	}
	if isHypervisorCRDName("connections.networking.liqo.io") {
		t.Fatal("liqo must not be treated as a hypervisor CRD")
	}
	if !isHypervisorCRDName("customresourcedefinition.apiextensions.k8s.io/" + proxmoxVM) {
		t.Fatal("kubectl -o name prefix must be stripped")
	}
}

func TestParseHypervisorCRDNames(t *testing.T) {
	t.Parallel()
	proxmoxVM := "proxmoxvms." + config.ProxmoxCRDAPIGroup
	virtfusionVM := "virtfusionvms." + config.VirtFusionCRDAPIGroup
	got := parseHypervisorCRDNames(proxmoxVM + "\nconnections.networking.liqo.io\n" + virtfusionVM + "\n")
	want := []string{proxmoxVM, virtfusionVM}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestCRDKeepAnnotationArgs(t *testing.T) {
	t.Parallel()
	proxmoxVM := "proxmoxvms." + config.ProxmoxCRDAPIGroup
	kubeconfig := "/tmp/kube"
	args := crdKeepAnnotationArgs([]string{proxmoxVM}, kubeconfig)
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, kubectlCmdAnnotate+" "+kubectlResourceCRD+" "+proxmoxVM+" "+helmResourcePolicyAnnotation()) {
		t.Fatalf("names must precede annotation: %s", joined)
	}
	if !strings.Contains(joined, helmFlagKubeconfig+" "+kubeconfig) {
		t.Fatalf("missing kubeconfig: %s", joined)
	}
	if slices.Contains(args, helmFlagTakeOwnership) {
		t.Fatalf("unexpected take-ownership: %s", joined)
	}
}

func TestHelmCRDInstallArgsNoTakeOwnership(t *testing.T) {
	clearHypervisorChartEnvs(t)
	args := helmCRDInstallArgs(crdChartNamed(t, helmReleaseProxmoxCRDs), "/tmp/kubeconfig")
	joined := strings.Join(args, " ")
	if strings.Contains(joined, helmFlagTakeOwnership) {
		t.Fatalf("helm 3.16 agents cannot pass %s: %s", helmFlagTakeOwnership, joined)
	}
	if !strings.Contains(joined, helmCmdUpgrade+" "+helmFlagInstall+" "+helmReleaseProxmoxCRDs) {
		t.Fatalf("args=%s", joined)
	}
}

func TestResolveCRDChartSharedVersionOverride(t *testing.T) {
	clearHypervisorChartEnvs(t)
	override := "9.9.9"
	t.Setenv(config.EnvHypervisorOperatorCRDsChartVersion, override)

	for _, c := range hypervisorCRDCharts() {
		_, ver := resolveCRDChart(c)
		if ver != override {
			t.Fatalf("%s version=%q want %s", c.release, ver, override)
		}
	}
}
