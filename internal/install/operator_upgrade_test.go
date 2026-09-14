package install

import (
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/noderings/cli/internal/config"
)

func TestParseHelmInstanceSecrets(t *testing.T) {
	t.Parallel()
	yaml := []byte(`
proxmox:
  instances:
    - id: pve-1
      url: https://pve.example:8006
      existingSecret: operator-proxmox-operator-proxmox-credentials-pve-1
    - id: pve-1
      existingSecret: operator-proxmox-operator-proxmox-credentials-pve-1
virtfusion:
  instances:
    - id: vf-1
      existingSecret: operator-virtfusion-operator-virtfusion-credentials-vf-1
solusvm:
  instances:
    - id: svm-1
      existingSecret: operator-solusvm-operator-solusvm-credentials-svm-1
`)
	got, err := ParseHelmInstanceSecrets(yaml, config.HypervisorDriverProxmox)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"operator-proxmox-operator-proxmox-credentials-pve-1"}
	if !slices.Equal(got, want) {
		t.Fatalf("proxmox secrets=%v want %v", got, want)
	}
	got, err = ParseHelmInstanceSecrets(yaml, config.HypervisorDriverVirtFusion)
	if err != nil || len(got) != 1 || !strings.Contains(got[0], "virtfusion-credentials-vf-1") {
		t.Fatalf("virtfusion secrets=%v err=%v", got, err)
	}
	got, err = ParseHelmInstanceSecrets(yaml, config.HypervisorDriverSolusVM)
	if err != nil || len(got) != 1 || !strings.Contains(got[0], "solusvm-credentials-svm-1") {
		t.Fatalf("solusvm secrets=%v err=%v", got, err)
	}
	got, err = ParseHelmInstanceSecrets([]byte(helmNullValues), config.HypervisorDriverProxmox)
	if err != nil || got != nil {
		t.Fatalf("null values: got=%v err=%v", got, err)
	}
	got, err = ParseHelmInstanceSecrets(nil, config.HypervisorDriverProxmox)
	if err != nil || got != nil {
		t.Fatalf("empty values: got=%v err=%v", got, err)
	}
	_, err = ParseHelmInstanceSecrets([]byte("instances: ["), config.HypervisorDriverProxmox)
	if err == nil {
		t.Fatal("expected parse error for truncated yaml")
	}
}

func TestInPlaceUpgradeArgsReusesValuesAndSetsVPrefixedImage(t *testing.T) {
	t.Parallel()
	chartVer := config.DefaultProxmoxOperatorChartVersion
	imageTag := config.HarborOperatorImageTag(chartVer)
	args := InPlaceUpgradeArgs(OperatorUpgrade{
		Driver:                    config.HypervisorDriverProxmox,
		Chart:                     config.DefaultProxmoxOperatorChartOCI,
		ChartVersion:              chartVer,
		ImageTag:                  imageTag,
		ImageRegistry:             config.DefaultHarborRegistry,
		Release:                   config.DefaultProxmoxOperatorHelmRelease,
		Namespace:                 config.DefaultProxmoxOperatorHelmNamespace,
		VNCGatewayNamespace:       config.DefaultVNCGatewayNamespace,
		VNCGatewayImageTag:        config.DefaultVNCGatewayImageTagProxmox,
		VNCGatewayImageRepository: config.DefaultVNCGatewayImageRepository,
	})
	if slices.Contains(args, helmFlagInstall) {
		t.Fatalf("in-place upgrade must not pass %s: %s", helmFlagInstall, strings.Join(args, " "))
	}
	if slices.Contains(args, helmFlagReuseValues) {
		t.Fatalf("must use %s, not %s: %s", helmFlagResetThenReuseValues, helmFlagReuseValues, strings.Join(args, " "))
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{
		helmFlagResetThenReuseValues,
		helmFlagVersion + " " + chartVer,
		helmFlagSetString + " " + helmImageTag + "=" + imageTag,
		helmDisableCRDSubchart,
		helmFlagSetString + " " + helmVNCGatewayImageRepo + "=" + config.DefaultVNCGatewayImageRepository,
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in %s", want, joined)
		}
	}
	if strings.Contains(joined, helmImageTag+"="+chartVer) {
		t.Fatalf("bare chart tag must not be used as %s: %s", helmImageTag, joined)
	}
	if strings.Contains(joined, "TOKEN") || strings.Contains(joined, "existingSecret") {
		t.Fatalf("upgrade argv must not pass secrets: %s", joined)
	}
	if strings.Contains(joined, helmAlloyMimirServiceEndpoint) || strings.Contains(joined, helmAlloyMimirTLSEnabled) {
		t.Fatalf("in-place upgrade must not rewrite mimir values: %s", joined)
	}
}

func TestNewOperatorUpgradePinsVImageTag(t *testing.T) {
	t.Setenv(config.EnvProxmoxOperatorImageTag, "")
	u, err := NewOperatorUpgrade(config.HypervisorDriverProxmox, "", config.DefaultProxmoxOperatorChartOCI, config.DefaultProxmoxOperatorChartVersion)
	if err != nil {
		t.Fatal(err)
	}
	wantTag := config.HarborOperatorImageTag(config.DefaultProxmoxOperatorChartVersion)
	if u.ImageTag != wantTag {
		t.Fatalf("image tag=%q", u.ImageTag)
	}
	if u.ChartVersion != config.DefaultProxmoxOperatorChartVersion {
		t.Fatalf("chart version=%q", u.ChartVersion)
	}
	wantExporter := kubeDeployment(config.HelmExporterDeployName(u.Release))
	if !slices.Contains(u.Workloads, wantExporter) {
		t.Fatalf("proxmox upgrade must wait for exporter: %v", u.Workloads)
	}
	if u.VNCGatewayImageRepository != config.DefaultVNCGatewayImageRepository {
		t.Fatalf("vnc repo=%q", u.VNCGatewayImageRepository)
	}
}

func TestNewOperatorUpgradeRejectsUnknownDriver(t *testing.T) {
	t.Parallel()
	_, err := NewOperatorUpgrade("vmware", "", config.DefaultProxmoxOperatorChartOCI, config.DefaultProxmoxOperatorChartVersion)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestOperatorUpgradeValidateRejectsBareChartTag(t *testing.T) {
	t.Parallel()
	u := OperatorUpgrade{
		Chart:        config.DefaultProxmoxOperatorChartOCI,
		ChartVersion: config.DefaultProxmoxOperatorChartVersion,
		ImageTag:     config.DefaultProxmoxOperatorChartVersion,
		Release:      config.DefaultProxmoxOperatorHelmRelease,
		Namespace:    config.DefaultProxmoxOperatorHelmNamespace,
	}
	if err := u.Validate(); err == nil {
		t.Fatal("expected refusal of image.tag equal to chart version")
	}
}

func TestIsHelmReleaseNotFound(t *testing.T) {
	t.Parallel()
	err := &exec.ExitError{}
	if !isHelmReleaseNotFound(err, []byte("Error: "+helmReleaseNotFoundToken)) {
		t.Fatal("expected helm missing-release")
	}
	if isHelmReleaseNotFound(err, []byte(`namespaces "`+config.DefaultProxmoxOperatorHelmNamespace+`" not found`)) {
		t.Fatal("namespace errors must not be treated as a missing release")
	}
	if isHelmReleaseNotFound(nil, []byte(helmReleaseNotFoundToken)) {
		t.Fatal("nil error is not a helm not-found")
	}
}

func TestResolveVNCGatewayImageRepositoryEnvWins(t *testing.T) {
	t.Setenv(config.EnvProxmoxVNCGatewayImageRepository, "example.invalid/vnc")
	got := resolveVNCGatewayImageRepository(config.HypervisorDriverProxmox)
	if got != "example.invalid/vnc" {
		t.Fatalf("got %q", got)
	}
}

func TestResolveVNCGatewayImageRepositoryDefault(t *testing.T) {
	t.Setenv(config.EnvProxmoxVNCGatewayImageRepository, "")
	got := resolveVNCGatewayImageRepository(config.HypervisorDriverProxmox)
	if got != config.DefaultVNCGatewayImageRepository {
		t.Fatalf("got %q", got)
	}
}

func TestIsKubeNotFound(t *testing.T) {
	t.Parallel()
	err := fmt.Errorf("kubectl failed: exit status 1\nError from server %s: deployments.apps %q not found", kubeNotFoundToken, config.HelmVirtFusionExporterDeployName(config.DefaultProxmoxOperatorHelmRelease))
	if !isKubeNotFound(err) {
		t.Fatal("expected kube NotFound")
	}
	if isKubeNotFound(fmt.Errorf("%s did not become ready: timed out waiting", kubeDaemonSet(config.HelmAlloyDaemonSetName(config.DefaultProxmoxOperatorHelmRelease)))) {
		t.Fatal("timeout must not be treated as missing")
	}
	if isKubeNotFound(nil) {
		t.Fatal("nil is not NotFound")
	}
}
