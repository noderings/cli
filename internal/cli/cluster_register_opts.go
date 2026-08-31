package cli

import (
	"context"
	"fmt"
	"strings"

	generated "github.com/noderings/cli/internal/api/generated"
	"github.com/noderings/cli/internal/config"
	"github.com/noderings/cli/internal/install"
	"github.com/noderings/cli/internal/logger"
	"github.com/noderings/cli/internal/state"
)

type clusterRegisterOpts struct {
	dev                       bool
	disableOffloading         bool
	skipOperatorInstall       bool
	reinstallOperator         bool
	operatorChartPath         string
	operatorChartVersion      string
	hypervisorDriver          string
	proxmoxInstancesFile      string
	virtfusionInstancesFile   string
	solusvmInstancesFile      string
	yes                       bool
	operatorNamespace         string
	vncGatewayNamespace       string
	offloadNamespaces         []string
	namespaceMappingStrategy  string
	remoteClusterID           string
	noderingsClusterName      string
	legacyClusterNameProvided bool
	agentID                   string
	agentIP                   string
	output                    string
}

func applyDevRegisterProfile(cfg *config.Config, dev bool) error {
	if !dev {
		return nil
	}
	if cfg == nil {
		return fmt.Errorf("--dev requires configuration")
	}
	if strings.TrimSpace(cfg.Mothership.Host) == "" {
		return fmt.Errorf("--dev requires mothership.host in config (set NR_MOTHERSHIP_HOST or ~/.nr/config.yaml)")
	}
	cfg.Mothership.TLSInsecure = true
	return nil
}

func buildInstallLiqoConfig(cfg *config.Config, agentID, agentIP, kubeconfigPath string, dev bool) *install.LiqoConfig {
	liqoAPIServerURL := cfg.Liqo.APIServerURL
	if liqoAPIServerURL == "" && agentIP != "" {
		liqoAPIServerURL = fmt.Sprintf("https://%s:%d", agentIP, config.DefaultAgentK8sAPIPort)
	}

	gwServiceType := cfg.Liqo.GWServiceType
	gwServerServiceLocation := cfg.Liqo.GWServerServiceLocation
	disableAPIServerSanityCheck := false

	gwClientAddress := strings.TrimSpace(cfg.Liqo.GWClientAddress)
	gwClientPort := cfg.Liqo.GWClientPort

	if dev {
		// Local control-plane clusters: NodePort only. Do not pass --gw-client-address/port;
		// liqoctl discovers the endpoint from the gateway server Service.
		gwServiceType = config.DevGWServiceType
		gwClientAddress = ""
		gwClientPort = ""
		if liqoAPIServerURL != "" {
			disableAPIServerSanityCheck = true
		}
	}

	if gwServerServiceLocation == "" {
		gwServerServiceLocation = config.DefaultGWServerServiceLocation
	}

	podOffloadingStrategy := cfg.Liqo.PodOffloadingStrategy
	if podOffloadingStrategy == "" {
		podOffloadingStrategy = config.DefaultPodOffloadingStrategy
	}

	return &install.LiqoConfig{
		Version:                     cfg.Liqo.Version,
		Timeout:                     cfg.Liqo.Timeout,
		GWServiceType:               gwServiceType,
		GWServerServiceLocation:     gwServerServiceLocation,
		PodOffloadingStrategy:       podOffloadingStrategy,
		PodCIDR:                     cfg.Liqo.PodCIDR,
		ServiceCIDR:                 cfg.Liqo.ServiceCIDR,
		ClusterID:                   agentID,
		KubeconfigPath:              kubeconfigPath,
		ProxyURL:                    cfg.Liqo.ProxyURL,
		APIServerURL:                liqoAPIServerURL,
		GWServerServiceNodePort:     cfg.Liqo.GWServerServiceNodePort,
		GWClientAddress:             gwClientAddress,
		GWClientPort:                gwClientPort,
		DisableAPIServerSanityCheck: disableAPIServerSanityCheck,
		ChartOCI:                    cfg.Liqo.ChartOCI,
		ChartVersion:                cfg.Liqo.ChartVersion,
	}
}

func buildPeerLiqoConfig(cfg *config.Config, kubeconfigPath string, agentIP string, dev bool) *install.LiqoConfig {
	peerCfg := buildInstallLiqoConfig(cfg, "", agentIP, kubeconfigPath, dev)
	peerCfg.ClusterID = ""
	peerCfg.APIServerURL = ""
	peerCfg.DisableAPIServerSanityCheck = false
	return peerCfg
}

func resolveAgentIPForLiqo(apiIP string, opts clusterRegisterOpts) string {
	if strings.TrimSpace(apiIP) != "" {
		return strings.TrimSpace(apiIP)
	}
	return strings.TrimSpace(opts.agentIP)
}

func resolveRemoteClusterID(
	log *logger.Logger,
	opts clusterRegisterOpts,
	detectedID string,
	detectErr error,
) (string, error) {
	if opts.remoteClusterID != "" {
		return opts.remoteClusterID, nil
	}
	if detectErr == nil && detectedID != "" {
		return detectedID, nil
	}
	if opts.legacyClusterNameProvided {
		return opts.noderingsClusterName, nil
	}
	if detectErr != nil {
		return "", detectErr
	}
	return "", fmt.Errorf("remote Liqo cluster ID is empty; set --remote-cluster-id")
}

func collectOffloadNamespaces(opts clusterRegisterOpts) []string {
	seen := map[string]struct{}{}
	var out []string
	add := func(ns string) {
		ns = strings.TrimSpace(ns)
		if ns == "" {
			return
		}
		if _, ok := seen[ns]; ok {
			return
		}
		seen[ns] = struct{}{}
		out = append(out, ns)
	}

	// VNC first (production default). Operator is only included when explicitly set
	// via --operator-namespace (normally offloaded from the remote cluster after inbound peering).
	add(opts.vncGatewayNamespace)
	add(opts.operatorNamespace)
	for _, ns := range opts.offloadNamespaces {
		add(ns)
	}
	return out
}

func parseHypervisorDriver(raw string) (string, error) {
	d := strings.ToLower(strings.TrimSpace(raw))
	d = strings.TrimPrefix(d, "platform_driver_")
	switch d {
	case "", config.HypervisorDriverProxmox:
		return config.HypervisorDriverProxmox, nil
	case config.HypervisorDriverVirtFusion:
		return config.HypervisorDriverVirtFusion, nil
	case config.HypervisorDriverSolusVM:
		return config.HypervisorDriverSolusVM, nil
	default:
		return "", UsageErrorf("unsupported --hypervisor-driver %q (want proxmox, virtfusion, or solusvm)", raw)
	}
}

// resolveHypervisorDriver uses an explicit flag, then the org driver.
// There is no proxmox default: an empty org with no flag is an error.
func resolveHypervisorDriver(flagRaw string, flagChanged bool, orgDriver string) (string, error) {
	org := strings.TrimSpace(orgDriver)
	if flagChanged {
		parsed, err := parseHypervisorDriver(flagRaw)
		if err != nil {
			return "", err
		}
		if org != "" {
			existing, err := parseHypervisorDriver(org)
			if err != nil {
				return "", err
			}
			if parsed != existing {
				return "", UsageErrorf("organization hypervisor driver is %s; --hypervisor-driver %s does not match", existing, parsed)
			}
		}
		return parsed, nil
	}
	if org != "" {
		return parseHypervisorDriver(org)
	}
	return "", UsageErrorf("this organization has no hypervisor driver")
}

func hypervisorDriverToAPI(driver string) *generated.V1PlatformDriver {
	var d generated.V1PlatformDriver
	switch strings.ToLower(strings.TrimSpace(driver)) {
	case config.HypervisorDriverVirtFusion:
		d = generated.PLATFORMDRIVERVIRTFUSION
	case config.HypervisorDriverSolusVM:
		d = generated.PLATFORMDRIVERSOLUSVM
	case config.HypervisorDriverProxmox:
		d = generated.PLATFORMDRIVERPROXMOX
	default:
		return nil
	}
	return &d
}

func validateRegisterHypervisorOpts(opts clusterRegisterOpts) error {
	pveFile := strings.TrimSpace(opts.proxmoxInstancesFile)
	vfFile := strings.TrimSpace(opts.virtfusionInstancesFile)
	svmFile := strings.TrimSpace(opts.solusvmInstancesFile)
	switch opts.hypervisorDriver {
	case config.HypervisorDriverSolusVM:
		if pveFile != "" || vfFile != "" {
			return UsageErrorf("--proxmox-instances-file and --virtfusion-instances-file are not valid with --hypervisor-driver solusvm (use --solusvm-instances-file)")
		}
	case config.HypervisorDriverVirtFusion:
		if pveFile != "" || svmFile != "" {
			return UsageErrorf("--proxmox-instances-file and --solusvm-instances-file are not valid with --hypervisor-driver virtfusion (use --virtfusion-instances-file)")
		}
	case config.HypervisorDriverProxmox, "":
		if vfFile != "" || svmFile != "" {
			return UsageErrorf("--virtfusion-instances-file and --solusvm-instances-file are not valid with --hypervisor-driver proxmox")
		}
	}
	return nil
}

func operatorHelmName(driver string) string {
	if config.IsSolusVMHypervisor(driver) {
		return "solusvm-operator"
	}
	if config.IsVirtFusionHypervisor(driver) {
		return "virtfusion-operator"
	}
	return "proxmox-operator"
}

func runOffloadingPhase(
	ctx context.Context,
	log *logger.Logger,
	stateManager *state.Manager,
	liqoManager *install.LiqoManager,
	agentID string,
	opts clusterRegisterOpts,
) error {
	if opts.disableOffloading {
		log.Info("Namespace offloading disabled by flag; skipping offloading step")
		return nil
	}

	offloadNamespaces := collectOffloadNamespaces(opts)
	if len(offloadNamespaces) == 0 {
		return nil
	}

	log.Info("Offloading namespaces...")
	stateManager.SetPhase(state.PhaseOffloading)
	if err := stateManager.Save(); err != nil {
		log.Warnf("Failed to save state: %v", err)
	}

	detectedRemoteClusterID, detectErr := liqoManager.GetPeeredClusterID(ctx)
	effectiveRemoteClusterID, err := resolveRemoteClusterID(log, opts, detectedRemoteClusterID, detectErr)
	if err != nil {
		stateManager.SetError(state.PhaseOffloading, err.Error(), true)
		_ = stateManager.Save()
		return fmt.Errorf("resolve remote Liqo cluster ID for selector: %w (you can set --remote-cluster-id explicitly)", err)
	}
	log.Infof("Using remote Liqo cluster ID for offloading selector: %s", effectiveRemoteClusterID)

	for _, ns := range offloadNamespaces {
		remoteNS, remoteNSErr := buildRemoteNamespaceName(agentID, ns)
		if remoteNSErr != nil {
			stateManager.SetError(state.PhaseOffloading, remoteNSErr.Error(), true)
			_ = stateManager.Save()
			return fmt.Errorf("build remote namespace name for %s: %w", ns, remoteNSErr)
		}
		selector := fmt.Sprintf("liqo.io/remote-cluster-id=%s", effectiveRemoteClusterID)
		if err := liqoManager.OffloadNamespace(ctx, ns, remoteNS, opts.namespaceMappingStrategy, selector); err != nil {
			stateManager.SetError(state.PhaseOffloading, err.Error(), true)
			_ = stateManager.Save()
			return fmt.Errorf("offload namespace %s: %w", ns, err)
		}
	}

	stateManager.AddCheckpoint(state.PhaseOffloading, state.CheckpointStatusSuccess, "")
	return nil
}
