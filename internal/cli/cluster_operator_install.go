package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/noderings/cli/internal/api"
	generated "github.com/noderings/cli/internal/api/generated"
	"github.com/noderings/cli/internal/config"
	"github.com/noderings/cli/internal/install"
	"github.com/noderings/cli/internal/logger"
	"github.com/noderings/cli/internal/state"
)

func runOperatorInstallPhase(
	ctx context.Context,
	apiClient *api.Client,
	log *logger.Logger,
	stateManager *state.Manager,
	agentID string,
	opts clusterRegisterOpts,
) error {
	if opts.skipOperatorInstall {
		log.Infof("Skipping %s install (--skip-operator-install)", operatorHelmName(opts.hypervisorDriver))
		return nil
	}

	if config.IsSolusVMHypervisor(opts.hypervisorDriver) {
		return runSolusVMOperatorInstallPhase(ctx, apiClient, log, stateManager, agentID, opts)
	}

	if config.IsVirtFusionHypervisor(opts.hypervisorDriver) {
		return runVirtFusionOperatorInstallPhase(ctx, apiClient, log, stateManager, agentID, opts)
	}

	log.Info("Installing proxmox-operator and vnc-gateway...")
	log.Infof("Agent ID: %s (from registration)", agentID)
	stateManager.SetPhase(state.PhaseOperatorInstall)
	if err := stateManager.Save(); err != nil {
		log.Warnf("Failed to save state: %v", err)
	}

	kubeconfig := install.EnsureReadableKubeconfig(ctx, "", log)
	opCfg := install.BaseConfigFromEnv(kubeconfig)
	// Register flag wins over VNC_GATEWAY_NAMESPACE so offload and Helm install agree.
	if ns := strings.TrimSpace(opts.vncGatewayNamespace); ns != "" {
		opCfg.VNCGatewayNamespace = ns
	}

	chart, version := install.ResolveOperatorChart(opts.operatorChartPath)
	if opts.operatorChartVersion != "" {
		version = opts.operatorChartVersion
	}
	opCfg.ChartPath = chart
	opCfg.ChartVersion = version
	opCfg.AgentID = strings.TrimSpace(agentID)
	if opCfg.ChartPath == "" {
		err := fmt.Errorf("proxmox-operator chart required: set --operator-chart or PROXMOX_OPERATOR_CHART")
		stateManager.SetError(state.PhaseOperatorInstall, err.Error(), true)
		_ = stateManager.Save()
		return err
	}
	log.Infof("Operator chart: %s%s", opCfg.ChartPath, chartVersionSuffix(opCfg.ChartVersion))

	done, err := completeOperatorInstallFromReuse(ctx, log, stateManager, kubeconfig, config.HypervisorDriverProxmox, opCfg.ChartPath, opCfg.ChartVersion, opts)
	if err != nil {
		return err
	}
	if done {
		return nil
	}

	instancesFile := opts.proxmoxInstancesFile
	if instancesFile == "" {
		instancesFile = strings.TrimSpace(os.Getenv("PROXMOX_INSTANCES_FILE"))
	}

	nonInteractive := opts.yes
	if err := collectOperatorInstallInputs(opCfg, instancesFile, nonInteractive, log); err != nil {
		stateManager.SetError(state.PhaseOperatorInstall, err.Error(), true)
		_ = stateManager.Save()
		return fmt.Errorf("operator install config: %w (or use --skip-operator-install)%s", err, operatorCredentialRecoveryHint(stateManager.GetState().AgentName))
	}
	log.Infof("Proxmox instances: %d", len(opCfg.Instances))

	// Always issue when unset: local Multipass and production both authenticate via the
	// metrics auth gateway (Bearer). MIMIR_TLS_ENABLED=false only disables TLS to Mimir;
	// it does not mean authMode none. Set MIMIR_BEARER_TOKEN explicitly to reuse a token.
	if strings.TrimSpace(opCfg.MimirBearerToken) == "" {
		token, issueErr := issueMetricsWriteCredential(ctx, apiClient, agentID, log)
		if issueErr != nil {
			stateManager.SetError(state.PhaseOperatorInstall, issueErr.Error(), true)
			_ = stateManager.Save()
			return fmt.Errorf("issue metrics write credential: %w", issueErr)
		}
		opCfg.MimirBearerToken = token
		log.Info("Issued per-provider Mimir write token via API")
	}

	installer := install.NewProxmoxOperatorInstaller(opCfg, log)
	if err := installer.Install(ctx); err != nil {
		stateManager.SetError(state.PhaseOperatorInstall, err.Error(), true)
		_ = stateManager.Save()
		return fmt.Errorf("install proxmox-operator: %w", err)
	}

	stateManager.AddCheckpoint(state.PhaseOperatorInstall, state.CheckpointStatusSuccess, "")
	log.Info("✓ Operator install complete")
	return nil
}

func runVirtFusionOperatorInstallPhase(
	ctx context.Context,
	apiClient *api.Client,
	log *logger.Logger,
	stateManager *state.Manager,
	agentID string,
	opts clusterRegisterOpts,
) error {
	log.Info("Installing virtfusion-operator and vnc-gateway...")
	log.Infof("Agent ID: %s (from registration)", agentID)
	stateManager.SetPhase(state.PhaseOperatorInstall)
	if err := stateManager.Save(); err != nil {
		log.Warnf("Failed to save state: %v", err)
	}

	kubeconfig := install.EnsureReadableKubeconfig(ctx, "", log)
	opCfg := install.VirtFusionBaseConfigFromEnv(kubeconfig)
	if ns := strings.TrimSpace(opts.vncGatewayNamespace); ns != "" {
		opCfg.VNCGatewayNamespace = ns
	}

	chart, version := install.ResolveVirtFusionOperatorChart(opts.operatorChartPath)
	if opts.operatorChartVersion != "" {
		version = opts.operatorChartVersion
	}
	opCfg.ChartPath = chart
	opCfg.ChartVersion = version
	opCfg.AgentID = strings.TrimSpace(agentID)
	if opCfg.ChartPath == "" {
		err := fmt.Errorf("virtfusion-operator chart required: set --operator-chart or VIRTFUSION_OPERATOR_CHART")
		stateManager.SetError(state.PhaseOperatorInstall, err.Error(), true)
		_ = stateManager.Save()
		return err
	}
	log.Infof("Operator chart: %s%s", opCfg.ChartPath, chartVersionSuffix(opCfg.ChartVersion))

	done, err := completeOperatorInstallFromReuse(ctx, log, stateManager, kubeconfig, config.HypervisorDriverVirtFusion, opCfg.ChartPath, opCfg.ChartVersion, opts)
	if err != nil {
		return err
	}
	if done {
		return nil
	}

	instancesFile := opts.virtfusionInstancesFile
	if instancesFile == "" {
		instancesFile = strings.TrimSpace(os.Getenv("VIRTFUSION_INSTANCES_FILE"))
	}

	nonInteractive := opts.yes
	if err := collectVirtFusionOperatorInstallInputs(opCfg, instancesFile, nonInteractive, log); err != nil {
		stateManager.SetError(state.PhaseOperatorInstall, err.Error(), true)
		_ = stateManager.Save()
		return fmt.Errorf("operator install config: %w (or use --skip-operator-install)%s", err, operatorCredentialRecoveryHint(stateManager.GetState().AgentName))
	}
	log.Infof("VirtFusion instances: %d", len(opCfg.Instances))

	if strings.TrimSpace(opCfg.MimirBearerToken) == "" {
		token, issueErr := issueMetricsWriteCredential(ctx, apiClient, agentID, log)
		if issueErr != nil {
			stateManager.SetError(state.PhaseOperatorInstall, issueErr.Error(), true)
			_ = stateManager.Save()
			return fmt.Errorf("issue metrics write credential: %w", issueErr)
		}
		opCfg.MimirBearerToken = token
		log.Info("Issued per-provider Mimir write token via API")
	}

	installer := install.NewVirtFusionOperatorInstaller(opCfg, log)
	if err := installer.Install(ctx); err != nil {
		stateManager.SetError(state.PhaseOperatorInstall, err.Error(), true)
		_ = stateManager.Save()
		return fmt.Errorf("install virtfusion-operator: %w", err)
	}

	stateManager.AddCheckpoint(state.PhaseOperatorInstall, state.CheckpointStatusSuccess, "")
	log.Info("✓ Operator install complete")
	return nil
}

func runSolusVMOperatorInstallPhase(
	ctx context.Context,
	apiClient *api.Client,
	log *logger.Logger,
	stateManager *state.Manager,
	agentID string,
	opts clusterRegisterOpts,
) error {
	log.Info("Installing solusvm-operator and vnc-gateway...")
	log.Infof("Agent ID: %s (from registration)", agentID)
	stateManager.SetPhase(state.PhaseOperatorInstall)
	if err := stateManager.Save(); err != nil {
		log.Warnf("Failed to save state: %v", err)
	}

	kubeconfig := install.EnsureReadableKubeconfig(ctx, "", log)
	opCfg := install.SolusVMBaseConfigFromEnv(kubeconfig)
	if ns := strings.TrimSpace(opts.vncGatewayNamespace); ns != "" {
		opCfg.VNCGatewayNamespace = ns
	}

	chart, version := install.ResolveSolusVMOperatorChart(opts.operatorChartPath)
	if opts.operatorChartVersion != "" {
		version = opts.operatorChartVersion
	}
	opCfg.ChartPath = chart
	opCfg.ChartVersion = version
	opCfg.AgentID = strings.TrimSpace(agentID)
	if opCfg.ChartPath == "" {
		err := fmt.Errorf("solusvm-operator chart required: set --operator-chart or SOLUSVM_OPERATOR_CHART")
		stateManager.SetError(state.PhaseOperatorInstall, err.Error(), true)
		_ = stateManager.Save()
		return err
	}
	log.Infof("Operator chart: %s%s", opCfg.ChartPath, chartVersionSuffix(opCfg.ChartVersion))

	done, err := completeOperatorInstallFromReuse(ctx, log, stateManager, kubeconfig, config.HypervisorDriverSolusVM, opCfg.ChartPath, opCfg.ChartVersion, opts)
	if err != nil {
		return err
	}
	if done {
		return nil
	}

	instancesFile := opts.solusvmInstancesFile
	if instancesFile == "" {
		instancesFile = strings.TrimSpace(os.Getenv("SOLUSVM_INSTANCES_FILE"))
	}

	nonInteractive := opts.yes
	if err := collectSolusVMOperatorInstallInputs(opCfg, instancesFile, nonInteractive, log); err != nil {
		stateManager.SetError(state.PhaseOperatorInstall, err.Error(), true)
		_ = stateManager.Save()
		return fmt.Errorf("operator install config: %w (or use --skip-operator-install)%s", err, operatorCredentialRecoveryHint(stateManager.GetState().AgentName))
	}
	log.Infof("SolusVM instances: %d", len(opCfg.Instances))

	if strings.TrimSpace(opCfg.MimirBearerToken) == "" {
		token, issueErr := issueMetricsWriteCredential(ctx, apiClient, agentID, log)
		if issueErr != nil {
			stateManager.SetError(state.PhaseOperatorInstall, issueErr.Error(), true)
			_ = stateManager.Save()
			return fmt.Errorf("issue metrics write credential: %w", issueErr)
		}
		opCfg.MimirBearerToken = token
		log.Info("Issued per-provider Mimir write token via API")
	}

	installer := install.NewSolusVMOperatorInstaller(opCfg, log)
	if err := installer.Install(ctx); err != nil {
		stateManager.SetError(state.PhaseOperatorInstall, err.Error(), true)
		_ = stateManager.Save()
		return fmt.Errorf("install solusvm-operator: %w", err)
	}

	stateManager.AddCheckpoint(state.PhaseOperatorInstall, state.CheckpointStatusSuccess, "")
	log.Info("✓ Operator install complete")
	return nil
}

func chartVersionSuffix(version string) string {
	if version == "" {
		return ""
	}
	return " @" + version
}

func completeOperatorInstallFromReuse(
	ctx context.Context,
	log *logger.Logger,
	stateManager *state.Manager,
	kubeconfig, driver, chart, version string,
	opts clusterRegisterOpts,
) (bool, error) {
	if !opts.reinstallOperator {
		return false, nil
	}
	reused, err := tryReuseOperatorSecrets(ctx, log, kubeconfig, driver, chart, version, opts)
	if err != nil {
		stateManager.SetError(state.PhaseOperatorInstall, err.Error(), true)
		_ = stateManager.Save()
		return false, err
	}
	if !reused {
		return false, nil
	}
	stateManager.AddCheckpoint(state.PhaseOperatorInstall, state.CheckpointStatusSuccess, "")
	log.Info("✓ Operator install complete")
	return true, nil
}

func tryReuseOperatorSecrets(
	ctx context.Context,
	log *logger.Logger,
	kubeconfig, driver, chart, version string,
	opts clusterRegisterOpts,
) (bool, error) {
	provided, err := hypervisorCredsProvided(driver, opts)
	if err != nil {
		return false, err
	}
	if provided {
		log.Info("Hypervisor credentials provided; rewriting operator Secrets")
		return false, nil
	}
	existing, err := install.DetectReusableOperator(ctx, kubeconfig, driver)
	if errors.Is(err, install.ErrNoReusableOperator) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("detect operator release: %w", err)
	}
	log.Infof("Reusing %d existing hypervisor Secret(s); skipping credential prompt", len(existing.SecretNames))
	u, err := install.NewOperatorUpgrade(driver, kubeconfig, chart, version)
	if err != nil {
		return false, err
	}
	if err := u.BindLiveRelease(existing.Release, existing.Namespace); err != nil {
		return false, err
	}
	if err := install.UpgradeOperatorRelease(ctx, u, log); err != nil {
		return false, err
	}
	return true, nil
}

func hypervisorCredsProvided(driver string, opts clusterRegisterOpts) (bool, error) {
	switch {
	case config.IsSolusVMHypervisor(driver):
		if strings.TrimSpace(opts.solusvmInstancesFile) != "" || strings.TrimSpace(os.Getenv(config.EnvSolusVMInstancesFile)) != "" {
			return true, nil
		}
		inst, err := install.SolusVMInstanceFromEnv()
		if err != nil {
			return false, err
		}
		return inst != nil, nil
	case config.IsVirtFusionHypervisor(driver):
		if strings.TrimSpace(opts.virtfusionInstancesFile) != "" || strings.TrimSpace(os.Getenv(config.EnvVirtFusionInstancesFile)) != "" {
			return true, nil
		}
		inst, err := install.VirtFusionInstanceFromEnv()
		if err != nil {
			return false, err
		}
		return inst != nil, nil
	default:
		if strings.TrimSpace(opts.proxmoxInstancesFile) != "" || strings.TrimSpace(os.Getenv(config.EnvProxmoxInstancesFile)) != "" {
			return true, nil
		}
		inst, err := install.ProxmoxInstanceFromEnv()
		if err != nil {
			return false, err
		}
		return inst != nil, nil
	}
}

// resolveExistingAgent loads an agent by ID and returns its name. Optionally checks name/IP consistency.
// When allowProvisioned is false, already-provisioned agents are rejected (UI install-command path).
func resolveExistingAgent(ctx context.Context, apiClient *api.Client, agentID, expectedName, expectedIP string, allowProvisioned bool) (string, error) {
	if !uuidPattern.MatchString(agentID) {
		return "", fmt.Errorf("invalid agent id")
	}
	genClient := apiClient.GetGeneratedClient()
	resp, err := apiClient.DoWithAutoRefresh(ctx, 3, func() (*http.Response, error) {
		return genClient.AgentServiceGetAgent(ctx, agentID)
	})
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		return "", api.ParseError(resp)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	var parsed generated.V1GetAgentResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("parse agent: %w", err)
	}
	if parsed.Agent == nil || parsed.Agent.Id == nil {
		return "", fmt.Errorf("agent not found")
	}
	name := ""
	if parsed.Agent.Name != nil {
		name = *parsed.Agent.Name
	}
	if expectedName != "" && name != "" && expectedName != name {
		return "", fmt.Errorf("agent name mismatch: --name=%s but agent is named %s", expectedName, name)
	}
	if expectedIP != "" && parsed.Agent.AgentPublicIp != nil && *parsed.Agent.AgentPublicIp != "" {
		if *parsed.Agent.AgentPublicIp != expectedIP {
			return "", fmt.Errorf("agent IP mismatch: --agent-ip=%s but agent has %s", expectedIP, *parsed.Agent.AgentPublicIp)
		}
	}
	if !allowProvisioned && agentIsProvisioned(parsed.Agent) {
		return "", fmt.Errorf("agent %s is already provisioned; use --force/--yes with --name to re-register, or deregister first", agentID)
	}
	return name, nil
}
