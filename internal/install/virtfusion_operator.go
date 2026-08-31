package install

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"

	"github.com/noderings/cli/internal/config"
)

// VirtFusionOperatorConfig holds inputs for provider-side VirtFusion operator + vnc-gateway install.
type VirtFusionOperatorConfig struct {
	ChartPath            string
	ChartVersion         string
	HelmNamespace        string
	HelmRelease          string
	VNCGatewayNamespace  string
	Instances            []VirtFusionInstance
	MimirBearerToken     string
	MimirServiceEndpoint string
	// MimirTLSEnabled selects https vs http for alloy.mimir.serviceEndpoint.
	MimirTLSEnabled bool
	// AgentID is stamped onto Alloy remote_write external_labels as agent_id.
	AgentID            string
	VNCGatewayImageTag string
	KubeconfigPath     string
}

// VirtFusionOperatorInstaller installs virtfusion-operator via helm subprocess.
type VirtFusionOperatorInstaller struct {
	config *VirtFusionOperatorConfig
	logger Logger
}

// NewVirtFusionOperatorInstaller creates a VirtFusion operator installer.
func NewVirtFusionOperatorInstaller(cfg *VirtFusionOperatorConfig, logger Logger) *VirtFusionOperatorInstaller {
	return &VirtFusionOperatorInstaller{config: cfg, logger: logger}
}

// ResolveVirtFusionOperatorChart returns local path, OCI ref, or empty.
// Precedence: explicit path/OCI → VIRTFUSION_OPERATOR_CHART → sibling checkout → Harbor OCI default.
func ResolveVirtFusionOperatorChart(explicit string) (chart string, version string) {
	explicit = strings.TrimSpace(explicit)
	version = getenvDefault("VIRTFUSION_OPERATOR_CHART_VERSION", config.DefaultVirtFusionOperatorChartVersion)
	if explicit != "" {
		if strings.HasPrefix(explicit, "oci://") {
			return explicit, version
		}
		if st, err := os.Stat(filepath.Join(explicit, "Chart.yaml")); err == nil && !st.IsDir() {
			return explicit, ""
		}
		return explicit, version
	}
	if env := strings.TrimSpace(os.Getenv("VIRTFUSION_OPERATOR_CHART")); env != "" {
		if strings.HasPrefix(env, "oci://") {
			return env, version
		}
		if st, err := os.Stat(filepath.Join(env, "Chart.yaml")); err == nil && !st.IsDir() {
			return env, ""
		}
		return env, version
	}
	for _, c := range []string{
		filepath.Join("..", "operator", "charts", "virtfusion-operator"),
		filepath.Join("..", "..", "operator", "charts", "virtfusion-operator"),
	} {
		if st, err := os.Stat(filepath.Join(c, "Chart.yaml")); err == nil && !st.IsDir() {
			return c, ""
		}
	}
	return config.DefaultVirtFusionOperatorChartOCI, version
}

// VirtFusionBaseConfigFromEnv fills non-secret defaults from environment.
// HelmNamespace is virtfusion-system unless HELM_NAMESPACE is explicitly set.
func VirtFusionBaseConfigFromEnv(kubeconfigPath string) *VirtFusionOperatorConfig {
	endpoint := getenvDefault(config.EnvMimirServiceEndpoint, config.DefaultMimirServiceEndpoint)
	helmNS := config.DefaultVirtFusionOperatorHelmNamespace
	if v, set := os.LookupEnv(config.EnvHelmNamespace); set {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			helmNS = trimmed
		}
	}
	return &VirtFusionOperatorConfig{
		HelmNamespace:        helmNS,
		HelmRelease:          getenvDefault(config.EnvHelmRelease, config.DefaultProxmoxOperatorHelmRelease),
		VNCGatewayNamespace:  getenvDefault(config.EnvVNCGatewayNamespace, config.DefaultVNCGatewayNamespace),
		MimirBearerToken:     strings.TrimSpace(os.Getenv(config.EnvMimirBearerToken)),
		MimirServiceEndpoint: endpoint,
		MimirTLSEnabled:      mimirTLSEnabled(),
		VNCGatewayImageTag:   config.ResolveVNCGatewayImageTag(config.HypervisorDriverVirtFusion),
		KubeconfigPath:       kubeconfigPath,
	}
}

// Install runs secret creation then helm upgrade --install (no secrets on the Helm argv).
func (p *VirtFusionOperatorInstaller) Install(ctx context.Context) error {
	if p.config == nil {
		return fmt.Errorf("virtfusion operator config is required")
	}
	if len(p.config.Instances) == 0 {
		return fmt.Errorf("at least one VirtFusion instance is required")
	}
	for _, inst := range p.config.Instances {
		if err := inst.Validate(); err != nil {
			return err
		}
	}
	requireMimirToken := p.config.MimirTLSEnabled || strings.TrimSpace(p.config.MimirBearerToken) != ""
	if p.config.MimirTLSEnabled && strings.TrimSpace(p.config.MimirBearerToken) == "" {
		return fmt.Errorf("%s is required for TLS remote_write (production)", config.EnvMimirBearerToken)
	}

	chart := strings.TrimSpace(p.config.ChartPath)
	if chart == "" {
		return fmt.Errorf("chart path or OCI ref is required")
	}
	isOCI := strings.HasPrefix(chart, "oci://")
	if !isOCI {
		if _, err := os.Stat(filepath.Join(chart, "Chart.yaml")); err != nil {
			return fmt.Errorf("chart not found at %s: %w", chart, err)
		}
	}

	p.logger.Info("Ensuring namespaces for virtfusion-operator and vnc-gateway...")
	if err := EnsureHypervisorCRDs(ctx, p.config.KubeconfigPath, p.logger); err != nil {
		return fmt.Errorf("ensure hypervisor CRDs: %w", err)
	}
	if err := p.applyStdout(ctx, "kubectl", "create", "namespace", p.config.HelmNamespace, "--dry-run=client", "-o", "yaml"); err != nil {
		return fmt.Errorf("ensure helm namespace: %w", err)
	}
	if err := p.applyStdout(ctx, "kubectl", "create", "namespace", p.config.VNCGatewayNamespace, "--dry-run=client", "-o", "yaml"); err != nil {
		return fmt.Errorf("ensure vnc-gateway namespace: %w", err)
	}

	p.logger.Info("Creating VirtFusion and Mimir credentials Secrets (not passed via Helm --set)...")
	for _, inst := range p.config.Instances {
		secretName := VirtFusionCredentialsSecretName(p.config.HelmRelease, inst.ID)
		// RFB vnc-gateway does not need the VF token; keep the Secret in the Helm ns only.
		if err := p.createVirtFusionSecret(ctx, p.config.HelmNamespace, secretName, inst); err != nil {
			return err
		}
	}
	if requireMimirToken {
		if err := p.createMimirSecret(ctx, p.config.HelmNamespace); err != nil {
			return err
		}
	} else {
		p.logger.Infof("Skipping %s Secret (local/no-TLS authMode none)", config.DefaultMimirCredentialsSecret)
	}

	if !isOCI {
		chartsDir := filepath.Join(chart, "charts")
		if entries, err := os.ReadDir(chartsDir); err != nil || len(entries) == 0 {
			p.logger.Info("Updating Helm chart dependencies...")
			if err := p.run(ctx, "helm", "dependency", "update", chart); err != nil {
				return fmt.Errorf("helm dependency update: %w", err)
			}
		}
	}

	repo := strings.TrimSpace(os.Getenv("VIRTFUSION_VNC_GATEWAY_IMAGE_REPOSITORY"))
	if err := EnsureRFBVNCGatewayImage(ctx, repo, p.config.VNCGatewayImageTag, p.logger); err != nil {
		return err
	}

	args := []string{
		"upgrade", "--install", p.config.HelmRelease, chart,
		"--namespace", p.config.HelmNamespace,
		"--create-namespace",
		// Endpoint is always --set from env (MIMIR_SERVICE_ENDPOINT) or metrics.noderings.com.
		"--set-string", fmt.Sprintf("%s=%s", helmAlloyMimirServiceEndpoint, p.config.MimirServiceEndpoint),
		"--set", fmt.Sprintf("%s=%t", helmAlloyMimirTLSEnabled, p.config.MimirTLSEnabled),
		"--set-string", fmt.Sprintf("%s=%s", helmVNCGatewayNamespace, p.config.VNCGatewayNamespace),
		"--set", helmVNCAllowRemoteClients,
		"--set", helmDisableCRDSubchart,
		"--set-string", fmt.Sprintf("image.registry=%s", getenvDefault("OPERATOR_IMAGE_REGISTRY", config.DefaultHarborRegistry)),
	}
	if requireMimirToken {
		args = append(args,
			"--set", fmt.Sprintf("%s=%s", helmAlloyMimirSecretName, config.DefaultMimirCredentialsSecret),
			"--set-json", fmt.Sprintf(`%s=[{"secretRef":{"name":"%s"}}]`, helmAlloyEnvFrom, config.DefaultMimirCredentialsSecret),
		)
	} else {
		args = append(args, "--set", fmt.Sprintf("%s=", helmAlloyMimirSecretName))
	}
	if aid := strings.TrimSpace(p.config.AgentID); aid != "" {
		if _, err := uuid.Parse(aid); err != nil {
			return fmt.Errorf("invalid agent ID for %s: %w", helmAlloyAgentID, err)
		}
		args = append(args, "--set-string", fmt.Sprintf("%s=%s", helmAlloyAgentID, aid))
	}
	if isOCI && p.config.ChartVersion != "" {
		args = append(args, "--version", p.config.ChartVersion)
	}
	for i, inst := range p.config.Instances {
		secretName := VirtFusionCredentialsSecretName(p.config.HelmRelease, inst.ID)
		prefix := fmt.Sprintf("virtfusion.instances[%d]", i)
		args = append(args,
			"--set-string", fmt.Sprintf("%s.id=%s", prefix, inst.ID),
			"--set-string", fmt.Sprintf("%s.url=%s", prefix, inst.URL),
			"--set-string", fmt.Sprintf("%s.existingSecret=%s", prefix, secretName),
		)
	}
	if p.config.VNCGatewayImageTag != "" {
		args = append(args, "--set-string", fmt.Sprintf("vncGateway.image.tag=%s", p.config.VNCGatewayImageTag))
	}
	args = append(args, "--set", "virtfusion.verifySSL=false")
	// Optional overrides; chart defaults publish to harbor.noderings.com/noderings.
	if v := strings.TrimSpace(os.Getenv("VIRTFUSION_OPERATOR_IMAGE_REPOSITORY")); v != "" {
		args = append(args, "--set-string", fmt.Sprintf("image.repository=%s", v))
	}
	if v := strings.TrimSpace(os.Getenv("VIRTFUSION_OPERATOR_IMAGE_TAG")); v != "" {
		args = append(args, "--set-string", fmt.Sprintf("image.tag=%s", v))
	}
	if v := strings.TrimSpace(os.Getenv("VIRTFUSION_EXPORTER_IMAGE_REPOSITORY")); v != "" {
		args = append(args, "--set-string", fmt.Sprintf("prometheusExporter.image.repository=%s", v))
	}
	if v := strings.TrimSpace(os.Getenv("VIRTFUSION_EXPORTER_IMAGE_TAG")); v != "" {
		args = append(args, "--set-string", fmt.Sprintf("prometheusExporter.image.tag=%s", v))
	}
	if v := strings.TrimSpace(os.Getenv("VIRTFUSION_VNC_GATEWAY_IMAGE_REPOSITORY")); v != "" {
		args = append(args, "--set-string", fmt.Sprintf("vncGateway.image.repository=%s", v))
	}
	if p.config.KubeconfigPath != "" {
		args = append(args, "--kubeconfig", p.config.KubeconfigPath)
	}

	p.logger.Infof("Installing/upgrading virtfusion-operator via Helm (%s)...", chart)
	if err := p.run(ctx, "helm", args...); err != nil {
		return fmt.Errorf("helm upgrade --install: %w", err)
	}

	workloads := []string{
		"deployment/" + config.HelmVirtFusionOperatorDeployName(p.config.HelmRelease),
		"daemonset/" + config.HelmAlloyDaemonSetName(p.config.HelmRelease),
		"deployment/" + config.HelmVirtFusionExporterDeployName(p.config.HelmRelease),
	}
	for _, workload := range workloads {
		if err := p.kubectl(ctx, "rollout", "restart", workload, "-n", p.config.HelmNamespace); err != nil {
			p.logger.Warnf("Could not restart %s: %v", workload, err)
		}
		if err := p.kubectl(ctx, "rollout", "status", workload, "-n", p.config.HelmNamespace,
			"--timeout="+config.DefaultHelmRolloutTimeout); err != nil {
			return fmt.Errorf("%s did not become ready: %w", workload, err)
		}
	}

	vncDeploy := "deployment/" + config.HelmVirtFusionVNCGatewayDeployName(p.config.HelmRelease)
	if err := p.kubectl(ctx, "rollout", "status", vncDeploy, "-n", p.config.VNCGatewayNamespace,
		"--timeout="+config.DefaultHelmRolloutTimeout); err != nil {
		return fmt.Errorf("vnc-gateway did not become ready: %w", err)
	}

	p.logger.Info("✓ virtfusion-operator / vnc-gateway install complete")
	return nil
}

func (p *VirtFusionOperatorInstaller) createVirtFusionSecret(ctx context.Context, ns, name string, inst VirtFusionInstance) error {
	return p.applySecret(ctx, ns, name, map[string]string{
		"VIRTFUSION_URL":            inst.URL,
		"VIRTFUSION_TOKEN":          inst.Token,
		"VIRTFUSION_USER_API_TOKEN": inst.UserAPIToken,
		"VIRTFUSION_USER_ID":        fmt.Sprintf("%d", inst.UserID),
		"VIRTFUSION_USER_NAME":      inst.UserName,
	})
}

func (p *VirtFusionOperatorInstaller) createMimirSecret(ctx context.Context, ns string) error {
	return p.applySecret(ctx, ns, config.DefaultMimirCredentialsSecret, map[string]string{
		config.EnvMimirBearerToken: p.config.MimirBearerToken,
	})
}

// applySecret pipes a rendered Secret to `kubectl apply -f -`. Secret values must never be
// passed as process arguments (kubectl --from-literal), because argv is world-readable via ps.
func (p *VirtFusionOperatorInstaller) applySecret(ctx context.Context, ns, name string, data map[string]string) error {
	secret := corev1.Secret{
		TypeMeta:   metav1.TypeMeta{Kind: "Secret", APIVersion: "v1"},
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Type:       corev1.SecretTypeOpaque,
		StringData: data,
	}
	manifest, err := yaml.Marshal(&secret)
	if err != nil {
		return fmt.Errorf("render secret %s/%s: %w", ns, name, err)
	}
	return p.applyManifest(ctx, manifest)
}

func (p *VirtFusionOperatorInstaller) kubectl(ctx context.Context, args ...string) error {
	if p.config.KubeconfigPath != "" {
		args = append([]string{"--kubeconfig", p.config.KubeconfigPath}, args...)
	}
	return p.run(ctx, "kubectl", args...)
}

func (p *VirtFusionOperatorInstaller) applyStdout(ctx context.Context, name string, args ...string) error {
	if name == "kubectl" && p.config.KubeconfigPath != "" {
		args = append([]string{"--kubeconfig", p.config.KubeconfigPath}, args...)
	}
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("%s %s: %w", name, sanitizeArgs(args), err)
	}
	return p.applyManifest(ctx, out)
}

func (p *VirtFusionOperatorInstaller) applyManifest(ctx context.Context, manifest []byte) error {
	applyArgs := []string{"apply", "-f", "-"}
	if p.config.KubeconfigPath != "" {
		applyArgs = append([]string{"--kubeconfig", p.config.KubeconfigPath}, applyArgs...)
	}
	apply := exec.CommandContext(ctx, "kubectl", applyArgs...)
	apply.Stdin = bytes.NewReader(manifest)
	var stderr strings.Builder
	apply.Stderr = &stderr
	if err := apply.Run(); err != nil {
		return fmt.Errorf("kubectl apply: %w (%s)", err, stderr.String())
	}
	return nil
}

func (p *VirtFusionOperatorInstaller) run(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := stderr.String()
		if msg == "" {
			msg = stdout.String()
		}
		return fmt.Errorf("%s failed: %w\n%s", name, err, msg)
	}
	return nil
}
