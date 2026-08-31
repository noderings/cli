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

// Helm value paths for the proxmox-operator chart (must match chart templates).
const (
	helmAlloyMimirServiceEndpoint = "alloy.mimir.serviceEndpoint"
	helmAlloyMimirTLSEnabled      = "alloy.mimir.tls.enabled"
	//nolint:gosec // G101: Helm value path, not a credential
	helmAlloyMimirSecretName  = "alloy.mimir.secretName"
	helmAlloyAgentID          = "alloy.agentId"
	helmAlloyEnvFrom          = "alloy.alloy.envFrom"
	helmVNCGatewayNamespace   = "vncGateway.namespace"
	helmVNCAllowRemoteClients = "vncGateway.networkPolicy.allowRemoteClients=true"
	// CRDs are installed into kube-system by EnsureHypervisorCRDs so Liqo can
	// watch both API groups. The operator chart subchart must not try to adopt them.
	helmDisableCRDSubchart = "crds.enabled=false"
)

// ProxmoxOperatorConfig holds inputs for provider-side operator + vnc-gateway install.
type ProxmoxOperatorConfig struct {
	ChartPath            string
	ChartVersion         string
	HelmNamespace        string
	HelmRelease          string
	VNCGatewayNamespace  string
	Instances            []ProxmoxInstance
	MimirBearerToken     string
	MimirServiceEndpoint string
	// MimirTLSEnabled selects https vs http for alloy.mimir.serviceEndpoint.
	// Production (metrics.noderings.com) uses true; local control-plane Mimir uses false.
	MimirTLSEnabled bool
	// AgentID is stamped onto Alloy remote_write external_labels as agent_id.
	AgentID            string
	VNCGatewayImageTag string
	VNCTLSServerName   string
	VNCCAFile          string
	KubeconfigPath     string
}

// ProxmoxOperatorInstaller installs proxmox-operator via helm subprocess.
type ProxmoxOperatorInstaller struct {
	config *ProxmoxOperatorConfig
	logger Logger
}

// NewProxmoxOperatorInstaller creates an installer.
func NewProxmoxOperatorInstaller(cfg *ProxmoxOperatorConfig, logger Logger) *ProxmoxOperatorInstaller {
	return &ProxmoxOperatorInstaller{config: cfg, logger: logger}
}

func getenvDefault(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

// envBool reports whether key is set to a truthy value ("1", "true", "yes", "on").
func envBool(key string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// ResolveOperatorChart returns local path, OCI ref, or empty.
// Precedence: explicit path/OCI → sibling checkout → Harbor OCI default.
func ResolveOperatorChart(explicit string) (chart string, version string) {
	explicit = strings.TrimSpace(explicit)
	version = getenvDefault("PROXMOX_OPERATOR_CHART_VERSION", config.DefaultProxmoxOperatorChartVersion)
	if explicit != "" {
		if strings.HasPrefix(explicit, "oci://") {
			return explicit, version
		}
		if st, err := os.Stat(filepath.Join(explicit, "Chart.yaml")); err == nil && !st.IsDir() {
			return explicit, ""
		}
		return explicit, version
	}
	if env := strings.TrimSpace(os.Getenv("PROXMOX_OPERATOR_CHART")); env != "" {
		if strings.HasPrefix(env, "oci://") {
			return env, version
		}
		if st, err := os.Stat(filepath.Join(env, "Chart.yaml")); err == nil && !st.IsDir() {
			return env, ""
		}
		return env, version
	}
	for _, c := range []string{
		filepath.Join("..", "operator", "charts", "proxmox-operator"),
		filepath.Join("..", "..", "operator", "charts", "proxmox-operator"),
	} {
		if st, err := os.Stat(filepath.Join(c, "Chart.yaml")); err == nil && !st.IsDir() {
			return c, ""
		}
	}
	return config.DefaultProxmoxOperatorChartOCI, version
}

// BaseConfigFromEnv fills non-secret defaults from environment (same knobs as install-proxmox-operator-provider.sh).
func BaseConfigFromEnv(kubeconfigPath string) *ProxmoxOperatorConfig {
	endpoint := getenvDefault(config.EnvMimirServiceEndpoint, config.DefaultMimirServiceEndpoint)
	return &ProxmoxOperatorConfig{
		HelmNamespace:        getenvDefault(config.EnvHelmNamespace, config.DefaultProxmoxOperatorHelmNamespace),
		HelmRelease:          getenvDefault(config.EnvHelmRelease, config.DefaultProxmoxOperatorHelmRelease),
		VNCGatewayNamespace:  getenvDefault(config.EnvVNCGatewayNamespace, config.DefaultVNCGatewayNamespace),
		MimirBearerToken:     strings.TrimSpace(os.Getenv(config.EnvMimirBearerToken)),
		MimirServiceEndpoint: endpoint,
		MimirTLSEnabled:      mimirTLSEnabled(),
		VNCGatewayImageTag:   config.ResolveVNCGatewayImageTag(config.HypervisorDriverProxmox),
		VNCTLSServerName:     strings.TrimSpace(os.Getenv("VNC_PROXMOX_TLS_SERVER_NAME")),
		VNCCAFile:            strings.TrimSpace(os.Getenv("VNC_PROXMOX_CA_FILE")),
		KubeconfigPath:       kubeconfigPath,
	}
}

// mimirTLSEnabled defaults to TLS because the bearer token must not cross the internet in cleartext.
// Plaintext HTTP (local control-plane Mimir) requires an explicit MIMIR_TLS_ENABLED=0 opt-out.
func mimirTLSEnabled() bool {
	if _, set := os.LookupEnv(config.EnvMimirTLSEnabled); set {
		return envBool(config.EnvMimirTLSEnabled)
	}
	return true
}

// Install runs secret creation then helm upgrade --install (no secrets on the Helm argv).
func (p *ProxmoxOperatorInstaller) Install(ctx context.Context) error {
	if p.config == nil {
		return fmt.Errorf("proxmox operator config is required")
	}
	if len(p.config.Instances) == 0 {
		return fmt.Errorf("at least one Proxmox instance is required")
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

	p.logger.Info("Ensuring namespaces for proxmox-operator and vnc-gateway...")
	if err := EnsureHypervisorCRDs(ctx, p.config.KubeconfigPath, p.logger); err != nil {
		return fmt.Errorf("ensure hypervisor CRDs: %w", err)
	}
	if err := p.applyStdout(ctx, "kubectl", "create", "namespace", p.config.HelmNamespace, "--dry-run=client", "-o", "yaml"); err != nil {
		return fmt.Errorf("ensure helm namespace: %w", err)
	}
	if err := p.applyStdout(ctx, "kubectl", "create", "namespace", p.config.VNCGatewayNamespace, "--dry-run=client", "-o", "yaml"); err != nil {
		return fmt.Errorf("ensure vnc-gateway namespace: %w", err)
	}

	p.logger.Info("Creating Proxmox and Mimir credentials Secrets (not passed via Helm --set)...")
	for _, inst := range p.config.Instances {
		secretName := CredentialsSecretName(p.config.HelmRelease, inst.ID)
		for _, ns := range []string{p.config.HelmNamespace, p.config.VNCGatewayNamespace} {
			if err := p.createProxmoxSecret(ctx, ns, secretName, inst); err != nil {
				return err
			}
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

	// User-supplied scalars use --set-string so values containing "," or "." are not
	// reinterpreted by Helm as list/key separators.
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
		secretName := CredentialsSecretName(p.config.HelmRelease, inst.ID)
		prefix := fmt.Sprintf("proxmox.instances[%d]", i)
		args = append(args,
			"--set-string", fmt.Sprintf("%s.id=%s", prefix, inst.ID),
			"--set-string", fmt.Sprintf("%s.url=%s", prefix, inst.URL),
			"--set-string", fmt.Sprintf("%s.existingSecret=%s", prefix, secretName),
		)
	}
	if p.config.VNCGatewayImageTag != "" {
		if strings.EqualFold(p.config.VNCGatewayImageTag, config.DefaultVNCGatewayImageTagRFB) {
			p.logger.Warn("Proxmox vnc-gateway tag is rfb; stock Harbor main is required for Proxmox ticket/readiness")
		}
		args = append(args, "--set-string", fmt.Sprintf("vncGateway.image.tag=%s", p.config.VNCGatewayImageTag))
	}
	if p.config.VNCTLSServerName != "" {
		args = append(args, "--set-string", fmt.Sprintf("vncGateway.proxmoxTLSServerName=%s", p.config.VNCTLSServerName))
	}
	if p.config.VNCCAFile != "" {
		args = append(args, "--set-file", fmt.Sprintf("vncGateway.proxmoxCAPem=%s", p.config.VNCCAFile))
	}
	args = append(args, "--set", "proxmox.verifySSL=false")
	args = append(args, "--set", "vncGateway.proxmoxInsecureSkipVerify=true")
	if p.config.KubeconfigPath != "" {
		args = append(args, "--kubeconfig", p.config.KubeconfigPath)
	}

	p.logger.Infof("Installing/upgrading proxmox-operator via Helm (%s)...", chart)
	if err := p.run(ctx, "helm", args...); err != nil {
		return fmt.Errorf("helm upgrade --install: %w", err)
	}

	workloads := []string{
		"deployment/" + config.HelmProxmoxOperatorDeployName(p.config.HelmRelease),
		"daemonset/" + config.HelmAlloyDaemonSetName(p.config.HelmRelease),
	}
	for _, workload := range workloads {
		// Restart is best-effort: the workload may not exist yet on a first install.
		if err := p.kubectl(ctx, "rollout", "restart", workload, "-n", p.config.HelmNamespace); err != nil {
			p.logger.Warnf("Could not restart %s: %v", workload, err)
		}
		if err := p.kubectl(ctx, "rollout", "status", workload, "-n", p.config.HelmNamespace,
			"--timeout="+config.DefaultHelmRolloutTimeout); err != nil {
			return fmt.Errorf("%s did not become ready: %w", workload, err)
		}
	}

	vncDeploy := "deployment/" + config.HelmVNCGatewayDeployName(p.config.HelmRelease)
	if err := p.kubectl(ctx, "rollout", "status", vncDeploy, "-n", p.config.VNCGatewayNamespace,
		"--timeout="+config.DefaultHelmRolloutTimeout); err != nil {
		return fmt.Errorf("vnc-gateway did not become ready: %w", err)
	}

	p.logger.Info("✓ proxmox-operator / vnc-gateway install complete")
	return nil
}

func (p *ProxmoxOperatorInstaller) createProxmoxSecret(ctx context.Context, ns, name string, inst ProxmoxInstance) error {
	return p.applySecret(ctx, ns, name, map[string]string{
		"PROXMOX_URL":          inst.URL,
		"PROXMOX_USERNAME":     inst.Username,
		"PROXMOX_TOKEN_ID":     inst.TokenID,
		"PROXMOX_TOKEN_SECRET": inst.TokenSecret,
	})
}

func (p *ProxmoxOperatorInstaller) createMimirSecret(ctx context.Context, ns string) error {
	return p.applySecret(ctx, ns, config.DefaultMimirCredentialsSecret, map[string]string{
		config.EnvMimirBearerToken: p.config.MimirBearerToken,
	})
}

// applySecret pipes a rendered Secret to `kubectl apply -f -`. Secret values must never be
// passed as process arguments (kubectl --from-literal), because argv is world-readable via ps.
func (p *ProxmoxOperatorInstaller) applySecret(ctx context.Context, ns, name string, data map[string]string) error {
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

func (p *ProxmoxOperatorInstaller) kubectl(ctx context.Context, args ...string) error {
	if p.config.KubeconfigPath != "" {
		args = append([]string{"--kubeconfig", p.config.KubeconfigPath}, args...)
	}
	return p.run(ctx, "kubectl", args...)
}

func (p *ProxmoxOperatorInstaller) applyStdout(ctx context.Context, name string, args ...string) error {
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

func (p *ProxmoxOperatorInstaller) applyManifest(ctx context.Context, manifest []byte) error {
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

func (p *ProxmoxOperatorInstaller) run(ctx context.Context, name string, args ...string) error {
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

func sanitizeArgs(args []string) string {
	out := make([]string, len(args))
	for i, a := range args {
		if strings.Contains(a, "TOKEN_SECRET=") || strings.Contains(a, "PASSWORD=") || strings.Contains(a, "password=") {
			if idx := strings.Index(a, "="); idx > 0 {
				out[i] = a[:idx+1] + "***"
				continue
			}
		}
		out[i] = a
	}
	return strings.Join(out, " ")
}
