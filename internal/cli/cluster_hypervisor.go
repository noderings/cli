package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"github.com/noderings/cli/internal/config"
	"github.com/noderings/cli/internal/logger"
	"github.com/noderings/cli/internal/state"
)

func init() {
	cmd := &cobra.Command{
		Use:   "install-hypervisor",
		Short: "Install one hypervisor operator",
		Long: `Install proxmox, virtfusion, or solusvm on the current cluster.

Run it on a new agent, or after game servers are already installed.
An agent has one hypervisor. A second choice is rejected.
Pass --agent-id to store that driver on an agent registered with --pterodactyl only.`,
		RunE: runInstallHypervisor,
	}
	cmd.Flags().String("hypervisor-driver", "", "proxmox, virtfusion, or solusvm (required)")
	cmd.Flags().String("agent-id", "", "Agent UUID. Stores the driver when this agent had no hypervisor yet.")
	cmd.Flags().BoolP("yes", "y", false, "Non-interactive: read hypervisor credentials from the environment or an instances file")
	cmd.Flags().String("operator-chart", "", "Local path or OCI ref for the hypervisor operator chart")
	cmd.Flags().String("operator-chart-version", "", "Helm chart version when using OCI")
	cmd.Flags().String("proxmox-instances-file", "", "YAML file with proxmox.instances list (or PROXMOX_INSTANCES_FILE)")
	cmd.Flags().String("virtfusion-instances-file", "", "YAML file with virtfusion.instances list (or VIRTFUSION_INSTANCES_FILE)")
	cmd.Flags().String("solusvm-instances-file", "", "YAML file with solusvm.instances list (or SOLUSVM_INSTANCES_FILE)")
	cmd.Flags().String("vnc-gateway-namespace", "", "VNC gateway namespace (default: vnc-gateway)")
	clusterCmd.AddCommand(cmd)
}

func runInstallHypervisor(cmd *cobra.Command, _ []string) error {
	if !cmd.Flags().Changed("hypervisor-driver") {
		return UsageErrorf("--hypervisor-driver is required (proxmox, virtfusion, or solusvm)")
	}
	raw, _ := cmd.Flags().GetString("hypervisor-driver")
	driver, err := parseHypervisorDriver(raw)
	if err != nil {
		return err
	}
	if strings.TrimSpace(raw) == "" {
		return UsageErrorf("--hypervisor-driver is required (proxmox, virtfusion, or solusvm)")
	}

	present, err := installedHypervisorNamespaces()
	if err != nil {
		return err
	}
	if err := conflictingHypervisorNamespace(present, driver); err != nil {
		return err
	}

	agentID, _ := cmd.Flags().GetString("agent-id")
	agentID = strings.TrimSpace(agentID)
	yes, _ := cmd.Flags().GetBool("yes")
	chart, _ := cmd.Flags().GetString("operator-chart")
	chartVersion, _ := cmd.Flags().GetString("operator-chart-version")
	pveFile, _ := cmd.Flags().GetString("proxmox-instances-file")
	vfFile, _ := cmd.Flags().GetString("virtfusion-instances-file")
	svmFile, _ := cmd.Flags().GetString("solusvm-instances-file")
	vncNS, _ := cmd.Flags().GetString("vnc-gateway-namespace")

	opts := clusterRegisterOpts{
		yes:                     yes,
		hypervisorDriver:        driver,
		operatorChartPath:       chart,
		operatorChartVersion:    chartVersion,
		proxmoxInstancesFile:    pveFile,
		virtfusionInstancesFile: vfFile,
		solusvmInstancesFile:    svmFile,
		vncGatewayNamespace:     vncNS,
	}
	if err := validateRegisterHypervisorOpts(opts); err != nil {
		return err
	}

	ctx := context.Background()
	log, err := logger.NewLogger("info", "")
	if err != nil {
		return err
	}
	if agentID != "" || strings.TrimSpace(os.Getenv("MIMIR_BEARER_TOKEN")) == "" {
		apiClient, apiErr := getAuthenticatedAPIClient(cmd)
		if apiErr != nil {
			return fmt.Errorf("log in, or set MIMIR_BEARER_TOKEN, before installing the hypervisor operator: %w", apiErr)
		}
		if agentID != "" {
			orgDriver := fetchOrganizationHypervisorDriver(ctx, apiClient)
			bound, bindErr := resolveHypervisorDriver(driver, true, orgDriver, false)
			if bindErr != nil {
				return bindErr
			}
			if err := persistAgentHypervisorDriver(ctx, apiClient, agentID, bound); err != nil {
				return err
			}
			log.Infof("Stored hypervisor driver %s on agent %s", bound, agentID)
		}
		stateManager := state.NewManager(config.GetConfigDir(), agentID)
		return runOperatorInstallPhase(ctx, apiClient, log, stateManager, agentID, opts)
	}

	stateManager := state.NewManager(config.GetConfigDir(), "install-hypervisor")
	return runOperatorInstallPhase(ctx, nil, log, stateManager, "", opts)
}

func hypervisorNamespace(driver string) string {
	switch driver {
	case config.HypervisorDriverVirtFusion:
		return config.DefaultVirtFusionOperatorHelmNamespace
	case config.HypervisorDriverSolusVM:
		return config.DefaultSolusVMOperatorHelmNamespace
	default:
		return config.DefaultProxmoxOperatorHelmNamespace
	}
}

func conflictingHypervisorNamespace(present []string, requested string) error {
	want := hypervisorNamespace(requested)
	for _, ns := range present {
		ns = strings.TrimSpace(ns)
		if ns == "" || ns == want {
			continue
		}
		return UsageErrorf("this cluster already has the %s hypervisor. An agent has one of proxmox, virtfusion, or solusvm", ns)
	}
	return nil
}

func installedHypervisorNamespaces() ([]string, error) {
	known := []string{
		config.DefaultProxmoxOperatorHelmNamespace,
		config.DefaultVirtFusionOperatorHelmNamespace,
		config.DefaultSolusVMOperatorHelmNamespace,
	}
	var found []string
	for _, ns := range known {
		check := exec.Command("kubectl", "get", "ns", ns)
		if err := check.Run(); err == nil {
			found = append(found, ns)
		}
	}
	return found, nil
}
