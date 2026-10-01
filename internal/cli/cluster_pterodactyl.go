package cli

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/noderings/cli/internal/config"
	nrinstall "github.com/noderings/cli/internal/install"
	"github.com/noderings/cli/internal/logger"
)

// errPterodactylCredentialsRequired is returned when panel settings are still
// missing and the command cannot prompt (--yes, or stdin is not a terminal).
var errPterodactylCredentialsRequired = errors.New("pterodactyl credentials required: set PTERODACTYL_PANEL_URL, PTERODACTYL_APPLICATION_KEY or PTERODACTYL_APPLICATION_KEY_FILE, PTERODACTYL_CLIENT_KEY or PTERODACTYL_CLIENT_KEY_FILE, and PTERODACTYL_PANEL_USER_ID, pass the matching flags, or run interactively (without --yes)")

func init() {
	cmd := &cobra.Command{
		Use:   "install-pterodactyl",
		Short: "Install the Pterodactyl operator",
		Long: `Install the game server operator on the current cluster.

Use it on its own, before a hypervisor, or after nr cluster register --hypervisor-driver.
It does not set --hypervisor-driver. The panel URL, application key, client key, and panel user id are prompted unless you set them with flags or PTERODACTYL_* environment variables. Keys stay in a Secret on the agent. They are not sent to NodeRings.`,
		RunE: runInstallPterodactyl,
	}
	cmd.Flags().String("panel-url", "", "Panel origin with no /admin (prompted, or PTERODACTYL_PANEL_URL)")
	cmd.Flags().String("application-key-file", "", "File containing the ptla_ application key (prompted, or PTERODACTYL_APPLICATION_KEY / PTERODACTYL_APPLICATION_KEY_FILE)")
	cmd.Flags().String("client-key-file", "", "File containing the ptlc_ client key (prompted, or PTERODACTYL_CLIENT_KEY / PTERODACTYL_CLIENT_KEY_FILE)")
	cmd.Flags().String("panel-user-id", "", "Panel user id that owns NodeRings servers (prompted, or PTERODACTYL_PANEL_USER_ID)")
	cmd.Flags().String("operator-chart", "", "Local chart path or OCI reference")
	cmd.Flags().String("agent-id", "", "Agent UUID stamped on Pterodactyl metrics")
	clusterCmd.AddCommand(cmd)
}

type pterodactylInstall struct {
	PanelURL        string
	ApplicationKey  string
	ClientKey       string
	PanelUserID     string
	Chart           string
	Namespace       string
	SecretName      string
	AgentID         string
	MimirEndpoint   string
	MimirTLS        bool
	MimirSecretName string
}

func loadPterodactylInstall(cmd *cobra.Command, chartFlag string, nonInteractive bool) (pterodactylInstall, error) {
	panelURL, _ := cmd.Flags().GetString("panel-url")
	userID, _ := cmd.Flags().GetString("panel-user-id")
	chart, _ := cmd.Flags().GetString(chartFlag)
	panelURL = strings.TrimSpace(panelURL)
	if panelURL == "" {
		panelURL = strings.TrimSpace(os.Getenv("PTERODACTYL_PANEL_URL"))
	}
	userID = strings.TrimSpace(userID)
	if userID == "" {
		userID = strings.TrimSpace(os.Getenv("PTERODACTYL_PANEL_USER_ID"))
	}
	appKey, err := pterodactylKey(cmd, "application-key-file", "PTERODACTYL_APPLICATION_KEY_FILE", "PTERODACTYL_APPLICATION_KEY")
	if err != nil {
		return pterodactylInstall{}, err
	}
	clientKey, err := pterodactylKey(cmd, "client-key-file", "PTERODACTYL_CLIENT_KEY_FILE", "PTERODACTYL_CLIENT_KEY")
	if err != nil {
		return pterodactylInstall{}, err
	}
	if panelURL != "" {
		if err := validatePanelURL(panelURL); err != nil {
			return pterodactylInstall{}, err
		}
	}
	if userID != "" {
		if _, err := parsePanelUserID(userID); err != nil {
			return pterodactylInstall{}, err
		}
	}
	if panelURL == "" || appKey == "" || clientKey == "" || userID == "" {
		if nonInteractive || !isStdinTerminal() {
			return pterodactylInstall{}, errPterodactylCredentialsRequired
		}
		beginCredentialPrompt("Configure Pterodactyl panel access for the operator")
		if panelURL == "" {
			panelURL, err = promptString("Pterodactyl panel URL (e.g. https://panel.example.com, no /admin)", "")
			if err != nil {
				return pterodactylInstall{}, err
			}
		}
		if appKey == "" {
			appKey, err = promptVisibleToken("Pterodactyl application API key (ptla_)")
			if err != nil {
				return pterodactylInstall{}, err
			}
		}
		if clientKey == "" {
			clientKey, err = promptVisibleToken("Pterodactyl client API key (ptlc_)")
			if err != nil {
				return pterodactylInstall{}, err
			}
		}
		if userID == "" {
			userID, err = promptString("Pterodactyl panel user ID (numeric, from the Users list)", "")
			if err != nil {
				return pterodactylInstall{}, err
			}
		}
	}
	if err := validatePanelURL(panelURL); err != nil {
		return pterodactylInstall{}, err
	}
	panelUserID, err := parsePanelUserID(userID)
	if err != nil {
		return pterodactylInstall{}, err
	}
	if strings.TrimSpace(appKey) == "" || strings.TrimSpace(clientKey) == "" {
		return pterodactylInstall{}, errPterodactylCredentialsRequired
	}
	chart = strings.TrimSpace(chart)
	if chart == "" {
		chart = strings.TrimSpace(os.Getenv("PTERODACTYL_OPERATOR_CHART"))
	}
	if chart == "" {
		chart = config.DefaultPterodactylOperatorChartOCI
	}
	return pterodactylInstall{
		PanelURL:       strings.TrimRight(panelURL, "/"),
		ApplicationKey: strings.TrimSpace(appKey),
		ClientKey:      strings.TrimSpace(clientKey),
		PanelUserID:    strconv.Itoa(panelUserID),
		Chart:          chart,
		Namespace:      config.DefaultPterodactylOperatorHelmNamespace,
		SecretName:     "operator-pterodactyl-operator-panel-credentials",
	}, nil
}

// preflightPterodactylInstall rejects a bad URL, user id, or key file before
// register spends time on the cluster. Missing values are filled later, the
// same way Proxmox credentials are.
func preflightPterodactylInstall(cmd *cobra.Command) error {
	_, err := loadPterodactylInstall(cmd, "pterodactyl-chart", true)
	if errors.Is(err, errPterodactylCredentialsRequired) {
		return nil
	}
	return err
}

func pterodactylKey(cmd *cobra.Command, flagName, fileEnv, valueEnv string) (string, error) {
	path, _ := cmd.Flags().GetString(flagName)
	path = strings.TrimSpace(path)
	if path == "" {
		path = strings.TrimSpace(os.Getenv(fileEnv))
	}
	if path != "" {
		return readKeyFile(path)
	}
	return strings.TrimSpace(os.Getenv(valueEnv)), nil
}

func validatePanelURL(panelURL string) error {
	panelURL = strings.TrimRight(strings.TrimSpace(panelURL), "/")
	parsed, err := url.Parse(panelURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil || strings.ContainsAny(panelURL, " ,\t\r\n") {
		return UsageErrorf("--panel-url must be an http(s) origin with no /admin and no path")
	}
	return nil
}

func parsePanelUserID(userID string) (int, error) {
	panelUserID, err := strconv.Atoi(strings.TrimSpace(userID))
	if err != nil || panelUserID <= 0 {
		return 0, UsageErrorf("--panel-user-id must be a positive integer")
	}
	return panelUserID, nil
}

func readKeyFile(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", UsageErrorf("application and client key files are required")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read key file: %w", err)
	}
	key := strings.TrimSpace(string(raw))
	if key == "" {
		return "", UsageErrorf("key file is empty")
	}
	return key, nil
}

func runInstallPterodactyl(cmd *cobra.Command, _ []string) error {
	install, err := loadPterodactylInstall(cmd, "operator-chart", false)
	if err != nil {
		return err
	}
	agentID, _ := cmd.Flags().GetString("agent-id")
	install.AgentID = strings.TrimSpace(agentID)
	return applyPterodactylRelease(cmd, install)
}

func ensurePterodactylRelease(cmd *cobra.Command, opts clusterRegisterOpts, agentID string) error {
	if !opts.installPterodactyl {
		return nil
	}
	install, err := loadPterodactylInstall(cmd, "pterodactyl-chart", opts.yes)
	if err != nil {
		return err
	}
	install.AgentID = strings.TrimSpace(agentID)
	if install.AgentID == "" {
		install.AgentID = strings.TrimSpace(opts.agentID)
	}
	return applyPterodactylRelease(cmd, install)
}

func applyPterodactylRelease(cmd *cobra.Command, install pterodactylInstall) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	// Same as the Proxmox installer: helm lives in ~/.nr/bin, and the kubeconfig
	// is the readable k3s copy (lab or production agent) or KUBECONFIG / ~/.kube/config.
	if _, err := nrinstall.EnsureCLIBinDir(); err != nil {
		return fmt.Errorf("helm bin dir: %w", err)
	}
	kubeconfig := nrinstall.EnsureReadableKubeconfig(ctx, "", nil)
	if strings.TrimSpace(kubeconfig) == "" {
		return fmt.Errorf("could not find a readable kubeconfig (tried KUBECONFIG, ~/.nr/k3s.kubeconfig, ~/.kube/config, and /etc/rancher/k3s/k3s.yaml)")
	}
	if err := applyPanelSecret(ctx, install, kubeconfig); err != nil {
		return err
	}
	if err := configurePterodactylMetrics(ctx, cmd, &install, kubeconfig); err != nil {
		return err
	}
	// CRDs are installed on their own before peering so the virtual-kubelet can
	// watch them. The operator subchart must not try to adopt or delete them.
	if err := nrinstall.EnsureHypervisorCRDs(ctx, kubeconfig, nil); err != nil {
		return err
	}
	if err := nrinstall.AnnotateOperatorCRDsKeep(ctx, kubeconfig, nil); err != nil {
		return err
	}
	helmArgs := pterodactylHelmArgs(install, kubeconfig)
	helm := exec.CommandContext(ctx, "helm", helmArgs...)
	helm.Stdout = cmd.OutOrStdout()
	helm.Stderr = cmd.ErrOrStderr()
	if err := helm.Run(); err != nil {
		return fmt.Errorf("install pterodactyl operator: %w", err)
	}
	fmt.Fprintln(cmd.OutOrStdout(), "panel keys are stored only in the agent Secret")
	return nil
}

func pterodactylHelmArgs(install pterodactylInstall, kubeconfig string) []string {
	args := []string{
		"upgrade", "--install", "operator", install.Chart,
		"--namespace", install.Namespace,
		"--create-namespace",
		"--set", "crds.enabled=false",
		"--set-string", "panel.url=" + install.PanelURL,
		"--set-string", "panel.existingSecret=" + install.SecretName,
	}
	if install.MimirEndpoint != "" {
		args = append(args,
			"--set-string", "alloy.mimir.serviceEndpoint="+install.MimirEndpoint,
			"--set", fmt.Sprintf("alloy.mimir.tls.enabled=%t", install.MimirTLS),
		)
		if install.MimirSecretName != "" {
			args = append(args,
				"--set", "alloy.mimir.secretName="+install.MimirSecretName,
				"--set-json", fmt.Sprintf(`alloy.alloy.envFrom=[{"secretRef":{"name":"%s"}}]`, install.MimirSecretName),
			)
		} else {
			args = append(args, "--set", "alloy.mimir.secretName=")
		}
		if install.AgentID != "" {
			args = append(args, "--set-string", "alloy.agentId="+install.AgentID)
		}
	}
	args = append(args, "--wait", "--timeout", "180s", "--kubeconfig", kubeconfig)
	return args
}

func configurePterodactylMetrics(ctx context.Context, cmd *cobra.Command, install *pterodactylInstall, kubeconfig string) error {
	if install.AgentID != "" {
		if _, err := uuid.Parse(install.AgentID); err != nil {
			return fmt.Errorf("invalid agent ID for alloy.agentId: %w", err)
		}
	}
	endpoint := strings.TrimSpace(os.Getenv(config.EnvMimirServiceEndpoint))
	if endpoint == "" {
		endpoint = config.DefaultMimirServiceEndpoint
	}
	install.MimirEndpoint = endpoint
	install.MimirTLS = pterodactylMimirTLS()
	token := strings.TrimSpace(os.Getenv(config.EnvMimirBearerToken))
	if token == "" {
		token = existingMimirToken(ctx, kubeconfig)
	}
	if token == "" && install.AgentID != "" {
		apiClient, err := getAuthenticatedAPIClient(cmd)
		if err != nil {
			if install.MimirTLS {
				return fmt.Errorf("log in, or set %s, before installing Pterodactyl metrics: %w", config.EnvMimirBearerToken, err)
			}
		} else {
			log, logErr := logger.NewLogger("info", "")
			if logErr != nil {
				return fmt.Errorf("logger: %w", logErr)
			}
			issued, issueErr := issueMetricsWriteCredential(ctx, apiClient, install.AgentID, log)
			if issueErr != nil {
				return fmt.Errorf("issue metrics write credential: %w", issueErr)
			}
			token = issued
		}
	}
	if install.MimirTLS && token == "" {
		return fmt.Errorf("%s is required for TLS remote_write (production)", config.EnvMimirBearerToken)
	}
	if token == "" {
		return nil
	}
	install.MimirSecretName = config.DefaultMimirCredentialsSecret
	return applyMimirSecret(ctx, install.Namespace, kubeconfig, token)
}

func pterodactylMimirTLS() bool {
	value, set := os.LookupEnv(config.EnvMimirTLSEnabled)
	if !set {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func existingMimirToken(ctx context.Context, kubeconfig string) string {
	namespaces := []string{
		config.DefaultProxmoxOperatorHelmNamespace,
		config.DefaultVirtFusionOperatorHelmNamespace,
		config.DefaultSolusVMOperatorHelmNamespace,
		config.DefaultPterodactylOperatorHelmNamespace,
	}
	for _, ns := range namespaces {
		cmd := exec.CommandContext(ctx, "kubectl", withKubeconfig(kubeconfig, "get", "secret", config.DefaultMimirCredentialsSecret, "-n", ns, "-o", "jsonpath={.data.MIMIR_BEARER_TOKEN}")...)
		out, err := cmd.Output()
		if err != nil || len(bytesTrim(out)) == 0 {
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(out)))
		if err != nil || len(raw) == 0 {
			continue
		}
		return string(raw)
	}
	return ""
}

func bytesTrim(in []byte) string {
	return strings.TrimSpace(string(in))
}

func applyMimirSecret(ctx context.Context, namespace, kubeconfig, token string) error {
	ns := exec.CommandContext(ctx, "kubectl", withKubeconfig(kubeconfig, "create", "namespace", namespace)...)
	if out, err := ns.CombinedOutput(); err != nil && !strings.Contains(string(out), "AlreadyExists") {
		return fmt.Errorf("create namespace: %w", err)
	}
	manifest := map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata": map[string]string{
			"name":      config.DefaultMimirCredentialsSecret,
			"namespace": namespace,
		},
		"type": "Opaque",
		"data": map[string]string{
			config.EnvMimirBearerToken: base64.StdEncoding.EncodeToString([]byte(token)),
		},
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		return fmt.Errorf("encode mimir secret: %w", err)
	}
	apply := exec.CommandContext(ctx, "kubectl", withKubeconfig(kubeconfig, "apply", "-f", "-")...)
	apply.Stdin = strings.NewReader(string(raw))
	if out, err := apply.CombinedOutput(); err != nil {
		return fmt.Errorf("write mimir secret: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

func withKubeconfig(kubeconfig string, args ...string) []string {
	if strings.TrimSpace(kubeconfig) == "" {
		return args
	}
	return append([]string{"--kubeconfig", kubeconfig}, args...)
}

func applyPanelSecret(ctx context.Context, install pterodactylInstall, kubeconfig string) error {
	ns := exec.CommandContext(ctx, "kubectl", withKubeconfig(kubeconfig, "create", "namespace", install.Namespace)...)
	if out, err := ns.CombinedOutput(); err != nil && !strings.Contains(string(out), "AlreadyExists") {
		return fmt.Errorf("create namespace: %w", err)
	}
	manifest := map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata": map[string]string{
			"name":      install.SecretName,
			"namespace": install.Namespace,
		},
		"type": "Opaque",
		"data": map[string]string{
			"PANEL_URL":       base64.StdEncoding.EncodeToString([]byte(install.PanelURL)),
			"APPLICATION_KEY": base64.StdEncoding.EncodeToString([]byte(install.ApplicationKey)),
			"CLIENT_KEY":      base64.StdEncoding.EncodeToString([]byte(install.ClientKey)),
			"PANEL_USER_ID":   base64.StdEncoding.EncodeToString([]byte(install.PanelUserID)),
		},
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		return fmt.Errorf("encode panel secret: %w", err)
	}
	apply := exec.CommandContext(ctx, "kubectl", withKubeconfig(kubeconfig, "apply", "-f", "-")...)
	apply.Stdin = strings.NewReader(string(raw))
	if _, err := apply.CombinedOutput(); err != nil {
		return fmt.Errorf("write panel secret: %w", err)
	}
	return nil
}
