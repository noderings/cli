package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/noderings/cli/internal/api"
	generated "github.com/noderings/cli/internal/api/generated"
	"github.com/noderings/cli/internal/config"
	"github.com/noderings/cli/internal/install"
	"github.com/noderings/cli/internal/k8s"
	"github.com/noderings/cli/internal/logger"
	"github.com/noderings/cli/internal/state"
)

var (
	uuidPattern             = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	dns1123NamespacePattern = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

	clusterCmd = &cobra.Command{
		Use:   "cluster",
		Short: "Cluster management commands",
		Long:  "High-level cluster management with installation orchestration",
	}

	clusterRegisterCmd = &cobra.Command{
		Use:   "register",
		Short: "Register VM and install k3s, Calico, and Liqo",
		Long: `Register a VM with NodeRings and set up k3s, Calico, and Liqo.
This command orchestrates the full installation process.`,
		RunE: runClusterRegister,
	}
)

func init() {
	clusterCmd.AddCommand(clusterRegisterCmd)

	// cluster register flags
	clusterRegisterCmd.Flags().String("name", "", "Agent/cluster name (required unless --agent-id, must be unique in organization)")
	clusterRegisterCmd.Flags().String("agent-id", "", "Existing agent UUID (from UI install command; skips CreateAgent)")
	clusterRegisterCmd.Flags().String("agent-ip", "", "Agent public IP address (required, must be valid public IP)")
	clusterRegisterCmd.Flags().String("gateway-region", "", "Gateway region (required: AMS01)")
	clusterRegisterCmd.Flags().String("description", "", "Optional description for the agent")
	clusterRegisterCmd.Flags().Bool("resume", false, "Resume a failed installation from last checkpoint")
	clusterRegisterCmd.Flags().Bool("force", false, "Force reinstallation even if already registered (implies cleanup; auto-reuses existing agent name)")
	clusterRegisterCmd.Flags().BoolP("yes", "y", false, "Non-interactive: reuse an existing agent with the same name without prompting")
	clusterRegisterCmd.Flags().Bool("skip-prechecks", false, "Skip pre-installation checks (not recommended)")
	clusterRegisterCmd.Flags().String("operator-namespace", "", "Optional provider-local namespace to offload (operator namespace is normally offloaded from the remote cluster after inbound peering)")
	clusterRegisterCmd.Flags().String("vnc-gateway-namespace", "", "VNC gateway namespace to offload from provider (default: vnc-gateway)")
	clusterRegisterCmd.Flags().StringSlice("offload-namespaces", []string{}, "Additional namespaces to offload to peered clusters (space-separated)")
	clusterRegisterCmd.Flags().Bool("disable-offloading", false, "Skip provider-side namespace offloading (VNC)")
	clusterRegisterCmd.Flags().String("namespace-mapping-strategy", config.DefaultNamespaceMappingStrategy, "Namespace mapping strategy for offloading")
	clusterRegisterCmd.Flags().String("remote-cluster-id", "", "Remote Liqo cluster ID used in offloading selector (auto-detected if empty)")
	clusterRegisterCmd.Flags().String("noderings-cluster-name", "noderings", "Deprecated: fallback selector value only when explicitly set")
	clusterRegisterCmd.Flags().Bool("dry-run", false, "Show what would be executed without running")
	clusterRegisterCmd.Flags().Bool("offline", false, "Skip fetching platform versions from the API (use local config pins)")
	clusterRegisterCmd.Flags().Bool("skip-operator-install", false, "Skip hypervisor operator / vnc-gateway Helm install")
	clusterRegisterCmd.Flags().Bool("reinstall-operator", false, "Re-run hypervisor operator Helm install even if that phase already succeeded (use with --resume)")
	clusterRegisterCmd.Flags().String("operator-chart", "", "Local path or OCI ref for the hypervisor operator chart (default: Harbor OCI or sibling checkout)")
	clusterRegisterCmd.Flags().String("operator-chart-version", "", "Helm chart version when using OCI (default: "+config.DefaultProxmoxOperatorChartVersion+")")
	clusterRegisterCmd.Flags().String("hypervisor-driver", config.HypervisorDriverProxmox, "Hypervisor driver: proxmox (default), virtfusion, or solusvm")
	clusterRegisterCmd.Flags().String("proxmox-instances-file", "", "YAML file with proxmox.instances list (or PROXMOX_INSTANCES_FILE)")
	clusterRegisterCmd.Flags().String("virtfusion-instances-file", "", "YAML file with virtfusion.instances list (or VIRTFUSION_INSTANCES_FILE)")
	clusterRegisterCmd.Flags().String("solusvm-instances-file", "", "YAML file with solusvm.instances list (or SOLUSVM_INSTANCES_FILE)")
	clusterRegisterCmd.Flags().String("output", config.OutputFormatText, "Output format for post-register verify: text|json")
}

func runClusterRegister(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	// Get authenticated API client
	apiClient, err := getAuthenticatedAPIClient(cmd)
	if err != nil {
		return err
	}

	// Get config directory
	configDir, _ := cmd.Flags().GetString("config-dir")
	if configDir == "" {
		configDir = config.GetConfigDir()
	}

	// Load configuration
	cfgLoader := config.NewLoader()
	cfg, err := cfgLoader.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// Get flags
	name, _ := cmd.Flags().GetString("name")
	agentIDFlag, _ := cmd.Flags().GetString("agent-id")
	agentIP, _ := cmd.Flags().GetString("agent-ip")
	gatewayRegion, _ := cmd.Flags().GetString("gateway-region")
	description, _ := cmd.Flags().GetString("description")
	resume, _ := cmd.Flags().GetBool("resume")
	force, _ := cmd.Flags().GetBool("force")
	yes, _ := cmd.Flags().GetBool("yes")
	skipPrechecks, _ := cmd.Flags().GetBool("skip-prechecks")
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	offline, _ := cmd.Flags().GetBool("offline")
	operatorNamespace, _ := cmd.Flags().GetString("operator-namespace")
	vncGatewayNamespace, _ := cmd.Flags().GetString("vnc-gateway-namespace")
	offloadNamespaces, _ := cmd.Flags().GetStringSlice("offload-namespaces")
	disableOffloading, _ := cmd.Flags().GetBool("disable-offloading")
	namespaceMappingStrategy, _ := cmd.Flags().GetString("namespace-mapping-strategy")
	remoteClusterID, _ := cmd.Flags().GetString("remote-cluster-id")
	noderingsClusterName, _ := cmd.Flags().GetString("noderings-cluster-name")
	legacyClusterNameProvided := cmd.Flags().Changed("noderings-cluster-name")
	skipOperatorInstall, _ := cmd.Flags().GetBool("skip-operator-install")
	reinstallOperator, _ := cmd.Flags().GetBool("reinstall-operator")
	operatorChartPath, _ := cmd.Flags().GetString("operator-chart")
	operatorChartVersion, _ := cmd.Flags().GetString("operator-chart-version")
	hypervisorDriverRaw, _ := cmd.Flags().GetString("hypervisor-driver")
	proxmoxInstancesFile, _ := cmd.Flags().GetString("proxmox-instances-file")
	virtfusionInstancesFile, _ := cmd.Flags().GetString("virtfusion-instances-file")
	solusvmInstancesFile, _ := cmd.Flags().GetString("solusvm-instances-file")
	devMode := IsDevMode(cmd)

	output, err := resolveOutputFlag(cmd)
	if err != nil {
		return err
	}

	hypervisorDriver, err := parseHypervisorDriver(hypervisorDriverRaw)
	if err != nil {
		return err
	}

	if operatorChartPath == "" && hypervisorDriver != config.HypervisorDriverVirtFusion && hypervisorDriver != config.HypervisorDriverSolusVM {
		operatorChartPath = strings.TrimSpace(os.Getenv("PROXMOX_OPERATOR_CHART"))
	}

	registerOpts := clusterRegisterOpts{
		dev:                       devMode,
		disableOffloading:         disableOffloading,
		skipOperatorInstall:       skipOperatorInstall,
		reinstallOperator:         reinstallOperator,
		operatorChartPath:         operatorChartPath,
		operatorChartVersion:      operatorChartVersion,
		hypervisorDriver:          hypervisorDriver,
		proxmoxInstancesFile:      proxmoxInstancesFile,
		virtfusionInstancesFile:   virtfusionInstancesFile,
		solusvmInstancesFile:      solusvmInstancesFile,
		yes:                       yes,
		operatorNamespace:         operatorNamespace,
		vncGatewayNamespace:       vncGatewayNamespace,
		offloadNamespaces:         offloadNamespaces,
		namespaceMappingStrategy:  namespaceMappingStrategy,
		remoteClusterID:           remoteClusterID,
		noderingsClusterName:      noderingsClusterName,
		legacyClusterNameProvided: legacyClusterNameProvided,
		agentID:                   strings.TrimSpace(agentIDFlag),
		agentIP:                   strings.TrimSpace(agentIP),
		output:                    output,
	}

	if err := validateRegisterHypervisorOpts(registerOpts); err != nil {
		return err
	}

	if err := applyDevRegisterProfile(cfg, devMode); err != nil {
		return err
	}

	if !disableOffloading {
		// Production model: offload only vnc-gateway from the provider.
		// Operator namespace is offloaded from the remote cluster after inbound peering.
		if !cmd.Flags().Changed("vnc-gateway-namespace") && vncGatewayNamespace == "" {
			registerOpts.vncGatewayNamespace = config.DefaultVNCGatewayNamespace
		}
	}

	// Validate required fields (unless resuming)
	if !resume {
		if registerOpts.agentID == "" && name == "" {
			return RequiredFlagf("name", "or pass --agent-id from the install command")
		}
		if agentIP == "" {
			return RequiredFlag("agent-ip")
		}
		if gatewayRegion == "" {
			return RequiredFlag("gateway-region")
		}
	}

	if !offline {
		if err := rejectUnverifiedProvider(ctx, apiClient); err != nil {
			return err
		}
	}

	// Create logger
	log, err := logger.NewLogger(cfg.Logging.Level, cfg.Logging.File)
	if err != nil {
		return fmt.Errorf("create logger: %w", err)
	}
	warnInsecureTLS(cmd, cfg, log)
	if devMode {
		log.Info("Development mode enabled for local control-plane clusters")
	}

	// Initialize state manager (will be updated with agent ID after creation)
	stateKey := name
	if stateKey == "" {
		stateKey = registerOpts.agentID
	}
	stateManager := state.NewManager(configDir, stateKey)
	if err := stateManager.Load(); err != nil {
		return fmt.Errorf("load state: %w", err)
	}

	// Handle force flag: clean up existing local cluster before reinstall
	if force {
		if !yes {
			ok, confirmErr := confirmYesNo(
				"Force reinstall uninstalls local Liqo, Calico, and k3s before re-registering. Continue?",
				"re-run with --force --yes to skip confirmation",
			)
			if confirmErr != nil {
				return confirmErr
			}
			if !ok {
				fmt.Fprintln(os.Stderr, "Canceled.")
				return nil
			}
		}
		log.Warn("Force flag set: cleaning up existing local cluster components before reinstall")
		cleaner, cleanErr := install.NewClusterCleaner(log)
		if cleanErr != nil {
			return fmt.Errorf("initialize cleaner: %w", cleanErr)
		}
		peeringKubeconfig := ""
		if stateManager.GetState().AgentID != "" {
			peeringKubeconfig = filepath.Join(configDir, fmt.Sprintf("peering-%s-kubeconfig.yaml", stateManager.GetState().AgentID))
		}
		_ = cleaner.ForceCleanupAll(ctx, peeringKubeconfig)
		stateManager.ResetCheckpoints()
		_ = stateManager.Save()
	}

	// Handle resume
	if resume {
		return handleResume(ctx, stateManager, apiClient, log, configDir, skipPrechecks, dryRun, offline, cfg, registerOpts)
	}

	// Handle dry-run
	if dryRun {
		log.Info("DRY-RUN MODE: No changes will be made")
		log.Infof("Would create agent: name=%s, ip=%s, region=%s", name, agentIP, gatewayRegion)
		log.Info("Would install: k3s, Calico, Liqo")
		if namespaces := collectOffloadNamespaces(registerOpts); len(namespaces) > 0 {
			log.Infof("Would offload namespaces: %v", namespaces)
		}
		if registerOpts.skipOperatorInstall {
			log.Infof("Would skip %s install", operatorHelmName(registerOpts.hypervisorDriver))
		} else {
			log.Infof("Would install %s / vnc-gateway", operatorHelmName(registerOpts.hypervisorDriver))
		}
		log.Info("Would verify provider health (subsections scoped to register flags)")
		return nil
	}

	// Step 0: Fetch platform versions and bootstrap CLI tools (required unless --offline)
	pins, err := preparePlatformVersions(ctx, apiClient, stateManager, cfg, log, offline)
	if err != nil {
		return err
	}

	// Step 1: Validate prerequisites and system requirements (unless skipped)
	if !skipPrechecks {
		log.Info("Validating prerequisites...")
		validator := install.NewToolValidator(pins, cfg)
		results, err := validator.ValidatePrerequisites(ctx)
		if err != nil {
			return fmt.Errorf("prerequisite validation: %w", err)
		}
		if err := validator.PrintResults(results); err != nil {
			return err
		}

		// Run system checks
		log.Info("Running pre-flight system checks...")
		systemValidator := install.NewSystemValidator(log)
		systemResults, err := systemValidator.ValidateSystem(ctx)
		if err != nil {
			return fmt.Errorf("system validation: %w", err)
		}

		// Print system check results
		hasFailures := false
		for _, result := range systemResults {
			switch result.Status {
			case "pass":
				log.Infof("✓ %s: %s", result.Check, result.Message)
			case "warn":
				log.Warnf("⚠ %s: %s", result.Check, result.Message)
			case "fail":
				log.Errorf("✗ %s: %s", result.Check, result.Message)
				hasFailures = true
			}
		}

		if hasFailures {
			return fmt.Errorf("pre-flight checks failed. Please fix the issues above and try again")
		}
	}

	// Step 2: Create agent or reuse existing (--agent-id or same --name)
	var agentID string
	var reused bool
	if registerOpts.agentID != "" {
		log.Infof("Using existing agent ID %s from --agent-id", registerOpts.agentID)
		// Reinstalling over a provisioned agent is destructive, so require --force/--yes
		// just like the --name reuse path does.
		resolvedName, resolveErr := resolveExistingAgent(ctx, apiClient, registerOpts.agentID, name, agentIP, force || yes)
		if resolveErr != nil {
			return fmt.Errorf("resolve agent %s: %w", registerOpts.agentID, resolveErr)
		}
		agentID = registerOpts.agentID
		reused = true
		if name == "" {
			name = resolvedName
		}
	} else {
		log.Info("Resolving agent in backend...")
		var createErr error
		agentID, reused, createErr = createOrResolveAgent(ctx, apiClient, name, agentIP, gatewayRegion, description, force, yes, log)
		if createErr != nil {
			return fmt.Errorf("resolve agent: %w", createErr)
		}
	}
	if reused {
		log.Infof("Reusing agent '%s' (ID: %s)", name, agentID)
	}

	// Update state with agent info
	stateManager.SetAgentInfo(agentID, name)
	stateManager.SetRegisterScope(state.RegisterScope{
		DisableOffloading:   registerOpts.disableOffloading,
		SkipOperatorInstall: registerOpts.skipOperatorInstall,
		HypervisorDriver:    registerOpts.hypervisorDriver,
		OperatorNamespace:   registerOpts.operatorNamespace,
		VNCGatewayNamespace: registerOpts.vncGatewayNamespace,
		OffloadNamespaces:   registerOpts.offloadNamespaces,
	})
	if err := stateManager.Save(); err != nil {
		return fmt.Errorf("save state: %w", err)
	}

	log.Infof("✓ Agent ready: ID=%s, Name=%s", agentID, name)

	// Step 3: Install k3s
	log.Info("Installing k3s...")
	stateManager.SetPhase(state.PhaseK3s)
	if err := stateManager.Save(); err != nil {
		log.Warnf("Failed to save state: %v", err)
	}

	// k3s configuration (from config file or defaults / server pins)
	k3sConfig := &install.K3sConfig{
		InstallScriptURL: cfg.K3s.InstallScriptURL,
		KubeconfigMode:   cfg.K3s.KubeconfigMode,
		ClusterCIDR:      cfg.K3s.ClusterCIDR,
		ServiceCIDR:      cfg.K3s.ServiceCIDR,
		FlannelBackend:   cfg.K3s.FlannelBackend,
		InstallDisables:  cfg.K3s.InstallDisables,
		InstallChannel:   cfg.K3s.InstallChannel,
		Version:          cfg.K3s.Version,
		NodeIP:           agentIP,
		NodeExternalIP:   agentIP,
	}

	k3sInstaller, err := install.NewK3sInstaller(k3sConfig, log)
	if err != nil {
		stateManager.SetError(state.PhaseK3s, err.Error(), true)
		_ = stateManager.Save()
		return fmt.Errorf("initialize k3s installer: %w", err)
	}
	if err := k3sInstaller.Install(ctx); err != nil {
		stateManager.SetError(state.PhaseK3s, err.Error(), true)
		_ = stateManager.Save()
		return fmt.Errorf("install k3s: %w", err)
	}

	stateManager.AddCheckpoint(state.PhaseK3s, state.CheckpointStatusSuccess, "")
	if err := stateManager.Save(); err != nil {
		log.Warnf("Failed to save state: %v", err)
	}

	// Step 4: Install Calico
	log.Info("Installing Calico...")
	stateManager.SetPhase(state.PhaseCalico)
	if err := stateManager.Save(); err != nil {
		log.Warnf("Failed to save state: %v", err)
	}

	k8sClient := k3sInstaller.GetK8sClient()
	if k8sClient == nil {
		// Create k8s client if not available from k3s installer
		k8sClient, err = newReadableK8sClient(ctx, log)
		if err != nil {
			return fmt.Errorf("create k8s client: %w", err)
		}
	}

	// Calico configuration (from config file or defaults)
	calicoConfig := &install.CalicoConfig{
		Version:            cfg.Calico.Version,
		RolloutTimeout:     cfg.Calico.RolloutTimeout,
		CustomResourcesURL: fmt.Sprintf("https://raw.githubusercontent.com/projectcalico/calico/%s/manifests/custom-resources.yaml", cfg.Calico.Version),
		PodCIDR:            cfg.K3s.ClusterCIDR,
	}

	calicoInstaller := install.NewCalicoInstaller(k8sClient, calicoConfig, log)
	if err := calicoInstaller.Install(ctx); err != nil {
		stateManager.SetError(state.PhaseCalico, err.Error(), true)
		_ = stateManager.Save()
		return fmt.Errorf("install Calico: %w", err)
	}

	// Wait for nodes to be Ready after Calico installation
	log.Info("Waiting for nodes to become Ready (after CNI)...")
	rolloutTimeout, err := time.ParseDuration(cfg.Calico.RolloutTimeout)
	if err != nil {
		rolloutTimeout = 10 * time.Minute // Default fallback
	}
	if err := k8sClient.WaitForNodes(ctx, 1, rolloutTimeout); err != nil {
		log.Warnf("Failed to wait for nodes: %v", err)
	}

	stateManager.AddCheckpoint(state.PhaseCalico, state.CheckpointStatusSuccess, "")
	if err := stateManager.Save(); err != nil {
		log.Warnf("Failed to save state: %v", err)
	}

	// Step 5: Install Liqo
	log.Info("Installing Liqo...")
	stateManager.SetPhase(state.PhaseLiqo)
	if err := stateManager.Save(); err != nil {
		log.Warnf("Failed to save state: %v", err)
	}

	// Get a user-readable kubeconfig (sudo-copies k3s.yaml into ~/.nr when mode 600)
	localKubeconfigPath := install.EnsureReadableKubeconfig(ctx, k3sInstaller.GetKubeconfigPath(), log)

	prereqValidator := install.NewToolValidator(pins, cfg)
	liqoConfig := buildInstallLiqoConfig(cfg, agentID, agentIP, localKubeconfigPath, registerOpts.dev)

	liqoManager := install.NewLiqoManager(prereqValidator, k8sClient, liqoConfig, log)
	if err := liqoManager.Install(ctx); err != nil {
		stateManager.SetError(state.PhaseLiqo, err.Error(), true)
		_ = stateManager.Save()
		return fmt.Errorf("install Liqo: %w", err)
	}

	stateManager.AddCheckpoint(state.PhaseLiqo, state.CheckpointStatusSuccess, "")

	// Step 6: Generate remote peering kubeconfig and peer clusters
	log.Info("Generating remote peering kubeconfig...")
	stateManager.SetPhase(state.PhasePeering)
	if err := stateManager.Save(); err != nil {
		log.Warnf("Failed to save state: %v", err)
	}

	remoteKubeconfigPath, gwPort, err := generateRemotePeeringKubeconfig(ctx, apiClient, agentID, configDir, cfg, log)
	if err != nil {
		stateManager.SetError(state.PhasePeering, err.Error(), true)
		_ = stateManager.Save()
		return fmt.Errorf("generate remote peering kubeconfig: %w", err)
	}

	log.Info("Configuring Liqo peering...")
	peerLiqoConfig := buildPeerLiqoConfig(cfg, localKubeconfigPath, agentIP, registerOpts.dev)
	liqoManager = install.NewLiqoManager(prereqValidator, k8sClient, peerLiqoConfig, log)
	if err := liqoManager.Peer(ctx, remoteKubeconfigPath, gwPort); err != nil {
		stateManager.SetError(state.PhasePeering, err.Error(), true)
		_ = stateManager.Save()
		return fmt.Errorf("peer clusters: %w", err)
	}

	stateManager.AddCheckpoint(state.PhasePeering, state.CheckpointStatusSuccess, "")

	// Step 7: Offload VNC namespace (provider → remote)
	if err := runOffloadingPhase(ctx, log, stateManager, liqoManager, agentID, registerOpts); err != nil {
		return err
	}

	// Step 8: Inbound peering (remote → provider) and operator namespace offload on the control plane
	if err := runInboundPeeringPhase(ctx, apiClient, log, stateManager, liqoManager, agentID, configDir, registerOpts); err != nil {
		return err
	}

	// Step 9: Install hypervisor operator + vnc-gateway
	if err := runOperatorInstallPhase(ctx, apiClient, log, stateManager, agentID, registerOpts); err != nil {
		return err
	}

	// Step 10: Verify provider cluster health (subsections honor register flags)
	if err := runPostRegisterVerify(ctx, apiClient, log, agentID, registerOpts); err != nil {
		return err
	}

	stateManager.SetPhase(state.PhaseComplete)
	if err := stateManager.Save(); err != nil {
		log.Warnf("Failed to save state: %v", err)
	}

	log.Info("✓ Cluster registration complete!")
	log.Infof("Agent ID: %s", agentID)
	log.Infof("Agent Name: %s", name)

	return nil
}

// generateRemotePeeringKubeconfig calls the Agent API flow to generate peering config and extracts kubeconfig.
func generateRemotePeeringKubeconfig(ctx context.Context, apiClient *api.Client, agentID, configDir string, cfg *config.Config, log *logger.Logger) (string, string, error) {
	genClient := apiClient.GetGeneratedClient()

	// Call API to generate remote peering kubeconfig with auto-refresh and retry.
	// This uses the generated AgentService flow in the client.
	resp, err := apiClient.DoWithAutoRefresh(ctx, 3, func() (*http.Response, error) {
		return genClient.AgentServiceGeneratePeeringConfig(ctx, agentID, generated.AgentServiceGeneratePeeringConfigJSONRequestBody{})
	})
	if err != nil {
		return "", "", fmt.Errorf("call generate peering config API: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		return "", "", api.ParseError(resp)
	}

	// Parse response using generated type - GeneratePeeringConfig returns V1GeneratePeeringConfigResponse per protobuf
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", fmt.Errorf("read response: %w", err)
	}

	var peeringResponse generated.V1GeneratePeeringConfigResponse
	if err := json.Unmarshal(body, &peeringResponse); err != nil {
		return "", "", fmt.Errorf("parse response: %w", err)
	}

	// Extract config from response (protobuf defines only 'config' field)
	if peeringResponse.Config == nil || *peeringResponse.Config == "" {
		return "", "", fmt.Errorf("config not found in peering config response")
	}
	kubeconfig := *peeringResponse.Config
	if cfg != nil && cfg.Mothership.Host != "" {
		serverURL, insecureSkip := resolvePeeringAPIServerURL(kubeconfig, cfg)
		rewritten, changed, rewriteErr := overrideKubeconfigServer(kubeconfig, serverURL, insecureSkip)
		if rewriteErr != nil {
			return "", "", fmt.Errorf("rewrite remote kubeconfig server: %w", rewriteErr)
		}
		if changed && log != nil {
			if insecureSkip {
				log.Infof("Rewriting remote peering kubeconfig server to %s (insecure TLS)", serverURL)
			} else {
				log.Infof("Rewriting remote peering kubeconfig server to %s (preserving CA for Liqo API checks)", serverURL)
			}
		}
		kubeconfig = rewritten
	}

	// Write kubeconfig to temporary file
	kubeconfigPath := filepath.Join(configDir, fmt.Sprintf("peering-%s-kubeconfig.yaml", agentID))
	if err := os.WriteFile(kubeconfigPath, []byte(kubeconfig), 0600); err != nil {
		return "", "", fmt.Errorf("write kubeconfig: %w", err)
	}

	gwPort := ""
	if _, port, err := getAgentPeeringInfo(ctx, apiClient, agentID); err != nil {
		if log != nil {
			log.Warnf("Could not resolve gateway service port from agent peering info: %v", err)
		}
	} else {
		gwPort = port
	}

	return kubeconfigPath, gwPort, nil
}

func overrideKubeconfigServer(kubeconfig, serverURL string, insecureSkipTLSVerify bool) (string, bool, error) {
	cfg, err := clientcmd.Load([]byte(kubeconfig))
	if err != nil {
		return "", false, err
	}

	changed := false
	for _, c := range cfg.Clusters {
		if c == nil {
			continue
		}
		if c.Server != serverURL {
			c.Server = serverURL
			changed = true
		}
		if insecureSkipTLSVerify && !c.InsecureSkipTLSVerify {
			c.InsecureSkipTLSVerify = true
			c.CertificateAuthorityData = nil
			changed = true
		}
	}

	rewritten, err := clientcmd.Write(*cfg)
	if err != nil {
		return "", false, err
	}
	return string(rewritten), changed, nil
}

func getAgentPeeringInfo(ctx context.Context, apiClient *api.Client, agentID string) (string, string, error) {
	genClient := apiClient.GetGeneratedClient()
	resp, err := apiClient.DoWithAutoRefresh(ctx, 3, func() (*http.Response, error) {
		return genClient.AgentServiceGetAgent(ctx, agentID)
	})
	if err != nil {
		return "", "", fmt.Errorf("get agent: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		return "", "", api.ParseError(resp)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", fmt.Errorf("read response: %w", err)
	}

	var getResponse generated.V1GetAgentResponse
	if err := json.Unmarshal(body, &getResponse); err != nil {
		return "", "", fmt.Errorf("parse response: %w", err)
	}

	if getResponse.Agent == nil {
		return "", "", fmt.Errorf("agent not found in response")
	}

	if getResponse.Agent.AgentPublicIp == nil || *getResponse.Agent.AgentPublicIp == "" {
		return "", "", fmt.Errorf("agent public IP not found")
	}

	gwPort := ""
	if getResponse.Agent.GwServerServicePort != nil && *getResponse.Agent.GwServerServicePort > 0 {
		gwPort = fmt.Sprintf("%d", *getResponse.Agent.GwServerServicePort)
	}

	return *getResponse.Agent.AgentPublicIp, gwPort, nil
}

func agentIsProvisioned(agent *generated.V1Agent) bool {
	return agent != nil && agent.Provisioned != nil && *agent.Provisioned
}

// buildRemoteNamespaceName returns the remote-cluster namespace for a provider-local offload.
// Production convention (test-k3d): remote = "{fullAgentID}-{localNamespace}"
// e.g. local "vnc-gateway" → "{agentUUID}-vnc-gateway".
func buildRemoteNamespaceName(agentID, localNamespace string) (string, error) {
	agentID = strings.TrimSpace(agentID)
	localNamespace = strings.TrimSpace(localNamespace)
	if agentID == "" {
		return "", fmt.Errorf("agent ID is required")
	}
	if localNamespace == "" {
		return "", fmt.Errorf("local namespace is required")
	}

	remoteNS := fmt.Sprintf("%s-%s", strings.ToLower(agentID), localNamespace)
	if len(remoteNS) > config.MaxK8sNamespaceLen {
		return "", fmt.Errorf("remote namespace name %q exceeds maximum length of %d characters", remoteNS, config.MaxK8sNamespaceLen)
	}
	if !dns1123NamespacePattern.MatchString(remoteNS) {
		return "", fmt.Errorf("remote namespace name %q is not DNS-1123 compliant", remoteNS)
	}

	return remoteNS, nil
}

// createAgent creates a new agent via the API. Prefer createOrResolveAgent for register.
func createAgent(ctx context.Context, apiClient *api.Client, name, agentIP, gatewayRegion, description string) (string, bool, error) {
	region, err := parseAgentGatewayRegion(gatewayRegion)
	if err != nil {
		return "", false, err
	}

	reqBody := generated.AgentServiceCreateAgentJSONRequestBody{
		Name:          &name,
		AgentPublicIp: &agentIP,
		GatewayRegion: &region,
	}
	if description != "" {
		reqBody.Description = &description
	}

	genClient := apiClient.GetGeneratedClient()
	resp, err := apiClient.DoWithAutoRefresh(ctx, 3, func() (*http.Response, error) {
		return genClient.AgentServiceCreateAgent(ctx, reqBody)
	})
	if err != nil {
		return "", false, fmt.Errorf("create agent: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		return "", false, api.ParseError(resp)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", false, fmt.Errorf("read response: %w", err)
	}

	var createResponse generated.V1GetAgentResponse
	if err := json.Unmarshal(body, &createResponse); err != nil {
		return "", false, fmt.Errorf("parse response: %w", err)
	}

	if createResponse.Agent == nil || createResponse.Agent.Id == nil || *createResponse.Agent.Id == "" {
		return "", false, fmt.Errorf("create agent response missing agent id")
	}

	return *createResponse.Agent.Id, false, nil
}

// preparePlatformVersions fetches server pins (unless offline), applies them to
// config/state, and ensures helm/liqoctl are installed at the pinned versions.
func preparePlatformVersions(
	ctx context.Context,
	apiClient *api.Client,
	stateManager *state.Manager,
	cfg *config.Config,
	log *logger.Logger,
	offline bool,
) (*api.PlatformPins, error) {
	if offline {
		log.Warn("Offline mode: using local config component versions (not server pins)")
		if _, err := install.EnsureCLIBinDir(); err != nil {
			log.Warnf("Failed to ensure CLI bin dir: %v", err)
		}
		stateManager.SetVersions(map[string]string{
			api.ComponentK3s:    cfg.K3s.Version,
			api.ComponentCalico: cfg.Calico.Version,
			api.ComponentLiqo:   cfg.Liqo.Version,
		})
		if err := stateManager.Save(); err != nil {
			log.Warnf("Failed to save state: %v", err)
		}
		return nil, nil
	}

	log.Info("Fetching platform versions from API...")
	pins, err := apiClient.GetPlatformVersionsWithRetry(ctx, 3)
	if err != nil {
		return nil, fmt.Errorf("platform versions unavailable (required; use --offline to skip): %w", err)
	}
	install.ApplyPlatformPinsToConfig(cfg, pins, log)
	stateManager.SetVersions(install.VersionsMap(pins))
	if err := stateManager.Save(); err != nil {
		log.Warnf("Failed to save state: %v", err)
	}

	log.Info("Bootstrapping helm and liqoctl...")
	bootstrapper, err := install.NewToolBootstrapper(cfg.Downloads.VerifyChecksums, log)
	if err != nil {
		return nil, fmt.Errorf("initialize tool bootstrapper: %w", err)
	}
	if err := bootstrapper.BootstrapTools(ctx, pins); err != nil {
		return nil, fmt.Errorf("bootstrap tools: %w", err)
	}
	return pins, nil
}

// handleResume resumes a failed installation
func handleResume(ctx context.Context, stateManager *state.Manager, apiClient *api.Client, log *logger.Logger, configDir string, skipPrechecks, dryRun, offline bool, cfg *config.Config, registerOpts clusterRegisterOpts) error {
	installState := stateManager.GetState()

	if installState.AgentID == "" {
		return fmt.Errorf("cannot resume: no agent ID found in state. Please run without --resume to start fresh")
	}

	if err := applyDevRegisterProfile(cfg, registerOpts.dev); err != nil {
		return err
	}
	if registerOpts.dev {
		log.Info("Development mode enabled for local control-plane clusters")
	}

	log.Infof("Resuming installation for agent %s (%s)", installState.AgentID, installState.AgentName)
	log.Infof("Last phase: %s", installState.Phase)

	stateManager.SetRegisterScope(state.RegisterScope{
		DisableOffloading:   registerOpts.disableOffloading,
		SkipOperatorInstall: registerOpts.skipOperatorInstall,
		HypervisorDriver:    registerOpts.hypervisorDriver,
		OperatorNamespace:   registerOpts.operatorNamespace,
		VNCGatewayNamespace: registerOpts.vncGatewayNamespace,
		OffloadNamespaces:   registerOpts.offloadNamespaces,
	})
	if err := stateManager.Save(); err != nil {
		log.Warnf("Failed to save state: %v", err)
	}

	// A run with --skip-operator-install reaches PhaseComplete without an
	// operator_install checkpoint. Resume that step instead of reporting nothing to do.
	// --reinstall-operator clears a prior success so Helm can be re-run (e.g. chart/image fix).
	if registerOpts.reinstallOperator {
		if registerOpts.skipOperatorInstall {
			return UsageErrorf("--reinstall-operator and --skip-operator-install are mutually exclusive")
		}
		log.Info("Clearing operator_install checkpoint (--reinstall-operator)")
		stateManager.ClearCheckpoints(state.PhaseOperatorInstall)
		if err := stateManager.Save(); err != nil {
			log.Warnf("Failed to save state: %v", err)
		}
	}

	operatorInstallPending := !registerOpts.skipOperatorInstall &&
		!stateManager.HasSuccessfulCheckpoint(state.PhaseOperatorInstall)

	if installState.Phase == state.PhaseComplete && !operatorInstallPending {
		log.Info("Installation already complete; verifying provider health...")
		if err := runPostRegisterVerify(ctx, apiClient, log, installState.AgentID, registerOpts); err != nil {
			return err
		}
		log.Info("Tip: re-run operator install with --resume --reinstall-operator")
		return nil
	}

	// Must run before any phase: it bootstraps helm/liqoctl and puts them on PATH.
	if _, err := preparePlatformVersions(ctx, apiClient, stateManager, cfg, log, offline); err != nil {
		return err
	}

	if installState.Phase == state.PhaseComplete {
		log.Infof("Registration complete but %s was never installed; resuming operator install...", operatorHelmName(registerOpts.hypervisorDriver))
		return resumeFromOperatorInstall(ctx, stateManager, apiClient, log, registerOpts)
	}

	// Determine where to resume: prefer last successful checkpoint (next phase).
	// If the process died mid-phase with no success checkpoint, retry the current phase.
	lastCheckpoint := stateManager.GetLastCheckpoint()
	if lastCheckpoint != nil {
		log.Infof("Resuming from checkpoint: %s", lastCheckpoint.Phase)
		switch lastCheckpoint.Phase {
		case state.PhaseK3s:
			log.Info("k3s already installed, continuing with Calico...")
			return resumeFromCalico(ctx, stateManager, apiClient, log, configDir, skipPrechecks, dryRun, cfg, registerOpts)
		case state.PhaseCalico:
			log.Info("Calico already installed, continuing with Liqo...")
			return resumeFromLiqo(ctx, stateManager, apiClient, log, configDir, skipPrechecks, dryRun, cfg, registerOpts)
		case state.PhaseLiqo:
			// Do not trust a stale liqo checkpoint: a rolled-back / empty install can
			// still leave phase=liqo success. resumeFromLiqo re-checks and installs if needed.
			log.Info("Ensuring Liqo is installed before peering...")
			return resumeFromLiqo(ctx, stateManager, apiClient, log, configDir, skipPrechecks, dryRun, cfg, registerOpts)
		case state.PhasePeering:
			log.Info("Peering already configured, continuing with offloading...")
			return resumeFromOffloading(ctx, stateManager, apiClient, log, configDir, cfg, registerOpts)
		case state.PhaseOffloading:
			log.Info("Offloading already completed, continuing with inbound peering...")
			return resumeFromInboundPeering(ctx, stateManager, apiClient, log, configDir, cfg, registerOpts)
		case state.PhaseInboundPeering:
			log.Info("Inbound peering already completed, continuing with operator install...")
			return resumeFromOperatorInstall(ctx, stateManager, apiClient, log, registerOpts)
		case state.PhaseOperatorInstall:
			log.Info("Operator install already completed; verifying before marking complete...")
			if err := runPostRegisterVerify(ctx, apiClient, log, installState.AgentID, registerOpts); err != nil {
				return err
			}
			stateManager.SetPhase(state.PhaseComplete)
			if err := stateManager.Save(); err != nil {
				log.Warnf("Failed to save state: %v", err)
			}
			log.Info("✓ Cluster registration complete!")
			return nil
		default:
			return fmt.Errorf("cannot resume from phase: %s", lastCheckpoint.Phase)
		}
	}

	log.Warnf("No successful checkpoint; retrying incomplete phase %q", installState.Phase)
	switch installState.Phase {
	case state.PhaseK3s, state.PhaseCurrent, "":
		return resumeFromK3s(ctx, stateManager, apiClient, log, configDir, skipPrechecks, dryRun, cfg, registerOpts)
	case state.PhaseCalico:
		return resumeFromCalico(ctx, stateManager, apiClient, log, configDir, skipPrechecks, dryRun, cfg, registerOpts)
	case state.PhaseLiqo:
		return resumeFromLiqo(ctx, stateManager, apiClient, log, configDir, skipPrechecks, dryRun, cfg, registerOpts)
	case state.PhasePeering:
		return resumeFromPeering(ctx, stateManager, apiClient, log, configDir, skipPrechecks, dryRun, cfg, registerOpts)
	case state.PhaseOffloading:
		return resumeFromOffloading(ctx, stateManager, apiClient, log, configDir, cfg, registerOpts)
	case state.PhaseInboundPeering:
		return resumeFromInboundPeering(ctx, stateManager, apiClient, log, configDir, cfg, registerOpts)
	case state.PhaseOperatorInstall:
		return resumeFromOperatorInstall(ctx, stateManager, apiClient, log, registerOpts)
	default:
		return fmt.Errorf("no checkpoint found to resume from (phase=%s)", installState.Phase)
	}
}

// resumeFromK3s retries k3s install (skips if already present) then continues with Calico.
func resumeFromK3s(ctx context.Context, stateManager *state.Manager, apiClient *api.Client, log *logger.Logger, configDir string, skipPrechecks, dryRun bool, cfg *config.Config, registerOpts clusterRegisterOpts) error {
	agentIP, _, infoErr := getAgentPeeringInfo(ctx, apiClient, stateManager.GetAgentID())
	if infoErr != nil {
		return fmt.Errorf("get agent for k3s resume: %w", infoErr)
	}

	log.Info("Installing k3s...")
	stateManager.SetPhase(state.PhaseK3s)
	if err := stateManager.Save(); err != nil {
		log.Warnf("Failed to save state: %v", err)
	}

	k3sConfig := &install.K3sConfig{
		InstallScriptURL: cfg.K3s.InstallScriptURL,
		KubeconfigMode:   cfg.K3s.KubeconfigMode,
		ClusterCIDR:      cfg.K3s.ClusterCIDR,
		ServiceCIDR:      cfg.K3s.ServiceCIDR,
		FlannelBackend:   cfg.K3s.FlannelBackend,
		InstallDisables:  cfg.K3s.InstallDisables,
		InstallChannel:   cfg.K3s.InstallChannel,
		Version:          cfg.K3s.Version,
		NodeIP:           agentIP,
		NodeExternalIP:   agentIP,
	}
	k3sInstaller, err := install.NewK3sInstaller(k3sConfig, log)
	if err != nil {
		stateManager.SetError(state.PhaseK3s, err.Error(), true)
		_ = stateManager.Save()
		return fmt.Errorf("initialize k3s installer: %w", err)
	}
	if err := k3sInstaller.Install(ctx); err != nil {
		stateManager.SetError(state.PhaseK3s, err.Error(), true)
		_ = stateManager.Save()
		return fmt.Errorf("install k3s: %w", err)
	}
	stateManager.AddCheckpoint(state.PhaseK3s, state.CheckpointStatusSuccess, "")
	if err := stateManager.Save(); err != nil {
		log.Warnf("Failed to save state: %v", err)
	}
	return resumeFromCalico(ctx, stateManager, apiClient, log, configDir, skipPrechecks, dryRun, cfg, registerOpts)
}

// resumeFromCalico resumes installation from Calico phase
func resumeFromCalico(ctx context.Context, stateManager *state.Manager, apiClient *api.Client, log *logger.Logger, configDir string, skipPrechecks, dryRun bool, cfg *config.Config, registerOpts clusterRegisterOpts) error {

	// Create k8s client
	k8sClient, err := newReadableK8sClient(ctx, log)
	if err != nil {
		return fmt.Errorf("create k8s client: %w", err)
	}

	// Install Calico
	log.Info("Installing Calico...")
	stateManager.SetPhase(state.PhaseCalico)
	if err := stateManager.Save(); err != nil {
		log.Warnf("Failed to save state: %v", err)
	}

	calicoConfig := &install.CalicoConfig{
		Version:            cfg.Calico.Version,
		RolloutTimeout:     cfg.Calico.RolloutTimeout,
		CustomResourcesURL: fmt.Sprintf("https://raw.githubusercontent.com/projectcalico/calico/%s/manifests/custom-resources.yaml", cfg.Calico.Version),
		PodCIDR:            cfg.K3s.ClusterCIDR,
	}

	calicoInstaller := install.NewCalicoInstaller(k8sClient, calicoConfig, log)
	if err := calicoInstaller.Install(ctx); err != nil {
		stateManager.SetError(state.PhaseCalico, err.Error(), true)
		_ = stateManager.Save()
		return fmt.Errorf("install Calico: %w", err)
	}

	// Wait for nodes
	log.Info("Waiting for nodes to become Ready (after CNI)...")
	rolloutTimeout, err := time.ParseDuration(cfg.Calico.RolloutTimeout)
	if err != nil {
		rolloutTimeout = 10 * time.Minute // Default fallback
	}
	if err := k8sClient.WaitForNodes(ctx, 1, rolloutTimeout); err != nil {
		log.Warnf("Failed to wait for nodes: %v", err)
	}

	stateManager.AddCheckpoint(state.PhaseCalico, state.CheckpointStatusSuccess, "")
	if err := stateManager.Save(); err != nil {
		log.Warnf("Failed to save state: %v", err)
	}

	// Continue with Liqo
	return resumeFromLiqo(ctx, stateManager, apiClient, log, configDir, skipPrechecks, dryRun, cfg, registerOpts)
}

// resumeFromLiqo resumes installation from Liqo phase
func resumeFromLiqo(ctx context.Context, stateManager *state.Manager, apiClient *api.Client, log *logger.Logger, configDir string, skipPrechecks, dryRun bool, cfg *config.Config, registerOpts clusterRegisterOpts) error {

	// Create k8s client
	k8sClient, err := newReadableK8sClient(ctx, log)
	if err != nil {
		return fmt.Errorf("create k8s client: %w", err)
	}

	// Re-apply Calico networking + DNS check. Resume often jumps here from a Calico
	// checkpoint after an earlier Liqo failure; heal the VXLAN/NOTRACK DNS break first.
	log.Info("Ensuring Calico networking before Liqo...")
	calicoInstaller := install.NewCalicoInstaller(k8sClient, &install.CalicoConfig{
		Version:            cfg.Calico.Version,
		RolloutTimeout:     cfg.Calico.RolloutTimeout,
		CustomResourcesURL: fmt.Sprintf("https://raw.githubusercontent.com/projectcalico/calico/%s/manifests/custom-resources.yaml", cfg.Calico.Version),
		PodCIDR:            cfg.K3s.ClusterCIDR,
	}, log)
	if err := calicoInstaller.Install(ctx); err != nil {
		stateManager.SetError(state.PhaseCalico, err.Error(), true)
		_ = stateManager.Save()
		return fmt.Errorf("ensure Calico before Liqo: %w", err)
	}

	// Install Liqo
	log.Info("Installing Liqo...")
	stateManager.SetPhase(state.PhaseLiqo)
	if err := stateManager.Save(); err != nil {
		log.Warnf("Failed to save state: %v", err)
	}

	prereqValidator := install.NewToolValidator(nil, cfg)
	agentPublicIP, _, infoErr := getAgentPeeringInfo(ctx, apiClient, stateManager.GetAgentID())
	if infoErr != nil {
		log.Warnf("Failed to get agent peering info: %v. Liqo API server URL may need liqo.api_server_url in config.", infoErr)
	}
	agentPublicIP = resolveAgentIPForLiqo(agentPublicIP, registerOpts)
	localKubeconfigPath := install.EnsureReadableKubeconfig(ctx, "", log)
	liqoConfig := buildInstallLiqoConfig(cfg, stateManager.GetAgentID(), agentPublicIP, localKubeconfigPath, registerOpts.dev)

	liqoManager := install.NewLiqoManager(prereqValidator, k8sClient, liqoConfig, log)
	if err := liqoManager.Install(ctx); err != nil {
		stateManager.SetError(state.PhaseLiqo, err.Error(), true)
		_ = stateManager.Save()
		return fmt.Errorf("install Liqo: %w", err)
	}

	stateManager.AddCheckpoint(state.PhaseLiqo, state.CheckpointStatusSuccess, "")
	if err := stateManager.Save(); err != nil {
		log.Warnf("Failed to save state: %v", err)
	}

	// Continue with peering
	return resumeFromPeering(ctx, stateManager, apiClient, log, configDir, skipPrechecks, dryRun, cfg, registerOpts)
}

// resumeFromPeering resumes installation from peering phase
func resumeFromPeering(ctx context.Context, stateManager *state.Manager, apiClient *api.Client, log *logger.Logger, configDir string, skipPrechecks, dryRun bool, cfg *config.Config, registerOpts clusterRegisterOpts) error {
	// If Liqo was never actually installed (stale checkpoint), install first.
	k8sClient, err := newReadableK8sClient(ctx, log)
	if err != nil {
		return fmt.Errorf("create k8s client: %w", err)
	}
	prereqValidator := install.NewToolValidator(nil, cfg)
	agentPublicIP, _, infoErr := getAgentPeeringInfo(ctx, apiClient, stateManager.GetAgentID())
	if infoErr != nil {
		log.Warnf("Failed to get agent peering info: %v", infoErr)
	}
	agentPublicIP = resolveAgentIPForLiqo(agentPublicIP, registerOpts)
	localKubeconfigPath := install.EnsureReadableKubeconfig(ctx, "", log)
	installCfg := buildInstallLiqoConfig(cfg, stateManager.GetAgentID(), agentPublicIP, localKubeconfigPath, registerOpts.dev)
	liqoInstaller := install.NewLiqoManager(prereqValidator, k8sClient, installCfg, log)
	if !liqoInstaller.IsInstalled(ctx) {
		log.Warn("Liqo checkpoint present but install not healthy; installing Liqo before peering")
		return resumeFromLiqo(ctx, stateManager, apiClient, log, configDir, skipPrechecks, dryRun, cfg, registerOpts)
	}

	stateManager.SetPhase(state.PhasePeering)
	if err := stateManager.Save(); err != nil {
		log.Warnf("Failed to save state: %v", err)
	}

	peerLiqoConfig := buildPeerLiqoConfig(cfg, localKubeconfigPath, agentPublicIP, registerOpts.dev)
	liqoManager := install.NewLiqoManager(prereqValidator, k8sClient, peerLiqoConfig, log)

	agentID := stateManager.GetAgentID()
	existingKubeconfig := filepath.Join(configDir, fmt.Sprintf("peering-%s-kubeconfig.yaml", agentID))
	if raw, readErr := os.ReadFile(existingKubeconfig); readErr == nil && cfg != nil && cfg.Mothership.Host != "" {
		// Stale peering kubeconfigs may still point at an unreachable k8s_api_host;
		// rewrite before best-effort unpeer so cleanup does not hang for ~30s.
		serverURL, insecureSkip := resolvePeeringAPIServerURL(string(raw), cfg)
		if rewritten, changed, rewriteErr := overrideKubeconfigServer(string(raw), serverURL, insecureSkip); rewriteErr == nil && changed {
			_ = os.WriteFile(existingKubeconfig, []byte(rewritten), 0o600)
		}
	}
	if _, statErr := os.Stat(existingKubeconfig); statErr == nil {
		liqoManager.ResetPartialPeering(ctx, existingKubeconfig)
	}

	log.Info("Generating remote peering kubeconfig...")
	remoteKubeconfigPath, gwPort, err := generateRemotePeeringKubeconfig(ctx, apiClient, agentID, configDir, cfg, log)
	if err != nil {
		stateManager.SetError(state.PhasePeering, err.Error(), true)
		_ = stateManager.Save()
		return fmt.Errorf("generate remote peering kubeconfig: %w", err)
	}

	if err := liqoManager.PeerAfterReset(ctx, remoteKubeconfigPath, gwPort); err != nil {
		stateManager.SetError(state.PhasePeering, err.Error(), true)
		_ = stateManager.Save()
		return fmt.Errorf("peer clusters: %w", err)
	}

	stateManager.AddCheckpoint(state.PhasePeering, state.CheckpointStatusSuccess, "")
	if err := runOffloadingPhase(ctx, log, stateManager, liqoManager, stateManager.GetAgentID(), registerOpts); err != nil {
		return err
	}
	return resumeFromInboundPeering(ctx, stateManager, apiClient, log, configDir, cfg, registerOpts)
}

func resumeFromOffloading(ctx context.Context, stateManager *state.Manager, apiClient *api.Client, log *logger.Logger, configDir string, cfg *config.Config, registerOpts clusterRegisterOpts) error {
	k8sClient, err := newReadableK8sClient(ctx, log)
	if err != nil {
		return fmt.Errorf("create k8s client: %w", err)
	}

	prereqValidator := install.NewToolValidator(nil, cfg)
	localKubeconfigPath := install.EnsureReadableKubeconfig(ctx, "", log)
	agentPublicIP, _, infoErr := getAgentPeeringInfo(ctx, apiClient, stateManager.GetAgentID())
	if infoErr != nil {
		log.Warnf("Failed to get agent peering info: %v", infoErr)
	}
	agentPublicIP = resolveAgentIPForLiqo(agentPublicIP, registerOpts)
	liqoManager := install.NewLiqoManager(prereqValidator, k8sClient, buildPeerLiqoConfig(cfg, localKubeconfigPath, agentPublicIP, registerOpts.dev), log)
	if err := runOffloadingPhase(ctx, log, stateManager, liqoManager, stateManager.GetAgentID(), registerOpts); err != nil {
		return err
	}
	return resumeFromInboundPeering(ctx, stateManager, apiClient, log, configDir, cfg, registerOpts)
}

func resumeFromInboundPeering(ctx context.Context, stateManager *state.Manager, apiClient *api.Client, log *logger.Logger, configDir string, cfg *config.Config, registerOpts clusterRegisterOpts) error {
	k8sClient, err := newReadableK8sClient(ctx, log)
	if err != nil {
		return fmt.Errorf("create k8s client: %w", err)
	}
	prereqValidator := install.NewToolValidator(nil, cfg)
	localKubeconfigPath := install.EnsureReadableKubeconfig(ctx, "", log)
	agentPublicIP, _, infoErr := getAgentPeeringInfo(ctx, apiClient, stateManager.GetAgentID())
	if infoErr != nil {
		log.Warnf("Failed to get agent peering info: %v", infoErr)
	}
	agentPublicIP = resolveAgentIPForLiqo(agentPublicIP, registerOpts)
	liqoManager := install.NewLiqoManager(prereqValidator, k8sClient, buildPeerLiqoConfig(cfg, localKubeconfigPath, agentPublicIP, registerOpts.dev), log)
	if err := runInboundPeeringPhase(ctx, apiClient, log, stateManager, liqoManager, stateManager.GetAgentID(), configDir, registerOpts); err != nil {
		return err
	}
	return resumeFromOperatorInstall(ctx, stateManager, apiClient, log, registerOpts)
}

func resumeFromOperatorInstall(ctx context.Context, stateManager *state.Manager, apiClient *api.Client, log *logger.Logger, registerOpts clusterRegisterOpts) error {
	if err := runOperatorInstallPhase(ctx, apiClient, log, stateManager, stateManager.GetAgentID(), registerOpts); err != nil {
		return err
	}
	if err := runPostRegisterVerify(ctx, apiClient, log, stateManager.GetAgentID(), registerOpts); err != nil {
		return err
	}
	stateManager.SetPhase(state.PhaseComplete)
	if err := stateManager.Save(); err != nil {
		log.Warnf("Failed to save state: %v", err)
	}
	log.Info("✓ Cluster registration complete!")
	return nil
}

// newReadableK8sClient builds a client-go client using a kubeconfig the
// current user can read (sudo-copies k3s.yaml when mode 600).
func newReadableK8sClient(ctx context.Context, log *logger.Logger) (*k8s.Client, error) {
	path := install.EnsureReadableKubeconfig(ctx, "", log)
	if path == "" {
		return nil, fmt.Errorf("could not find a readable kubeconfig (tried ~/.kube/config and /etc/rancher/k3s/k3s.yaml)")
	}
	return k8s.NewClient(path)
}
