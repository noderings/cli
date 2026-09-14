package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/noderings/cli/internal/config"
	"github.com/noderings/cli/internal/install"
	"github.com/noderings/cli/internal/logger"
)

const (
	flagHypervisorDriver     = "hypervisor-driver"
	flagOperatorChart        = "operator-chart"
	flagOperatorChartVersion = "operator-chart-version"
	flagDryRun               = "dry-run"
)

var clusterOperatorCmd = &cobra.Command{
	Use:   "operator",
	Short: "Manage the hypervisor operator Helm release on this agent",
}

var clusterOperatorUpgradeCmd = &cobra.Command{
	Use:   "upgrade",
	Short: "Upgrade operator chart and image without re-entering hypervisor credentials",
	Long:  operatorUpgradeLong(),
	Args:  cobra.NoArgs,
	RunE:  runClusterOperatorUpgrade,
}

func operatorUpgradeLong() string {
	chart := config.DefaultProxmoxOperatorChartVersion
	return fmt.Sprintf(`Upgrade hypervisor CRD charts and the operator Helm release on this agent.

Existing Kubernetes Secrets and Helm instance values are reused. Hypervisor
tokens are not requested. The operator image tag is the Harbor v-prefix of the
CLI-pinned chart (%s, not %s).

Does not require NR_API_TOKEN or --org-id. Run on the agent VM.

To rotate hypervisor credentials instead, use:
  nr cluster register --resume --reinstall-operator --name <name> --org-id <org>
with PROXMOX_* / VIRTFUSION_* / SOLUSVM_* or an instances file.`,
		config.HarborOperatorImageTag(chart), chart)
}

func init() {
	clusterCmd.AddCommand(clusterOperatorCmd)
	clusterOperatorCmd.AddCommand(clusterOperatorUpgradeCmd)
	clusterOperatorUpgradeCmd.Flags().String(flagHypervisorDriver, "", "proxmox, virtfusion, or solusvm (detected from the live Helm release if omitted)")
	clusterOperatorUpgradeCmd.Flags().String(flagOperatorChart, "", "Local path or OCI ref for the hypervisor operator chart")
	clusterOperatorUpgradeCmd.Flags().String(flagOperatorChartVersion, "", "Helm chart version when using OCI")
	clusterOperatorUpgradeCmd.Flags().Bool(flagDryRun, false, "Print the Helm upgrade and exit")
}

func runClusterOperatorUpgrade(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	cfgLoader := config.NewLoader()
	cfg, err := cfgLoader.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	log, err := logger.NewLogger(cfg.Logging.Level, cfg.Logging.File)
	if err != nil {
		return err
	}

	driverFlag, err := cmd.Flags().GetString(flagHypervisorDriver)
	if err != nil {
		return err
	}
	chartPath, err := cmd.Flags().GetString(flagOperatorChart)
	if err != nil {
		return err
	}
	chartVersion, err := cmd.Flags().GetString(flagOperatorChartVersion)
	if err != nil {
		return err
	}
	dryRun, err := cmd.Flags().GetBool(flagDryRun)
	if err != nil {
		return err
	}

	var driver string
	if strings.TrimSpace(driverFlag) != "" {
		driver, err = parseHypervisorDriver(driverFlag)
		if err != nil {
			return err
		}
	}

	kubeconfig := install.EnsureReadableKubeconfig(ctx, "", log)
	existing, err := install.DetectReusableOperator(ctx, kubeconfig, driver)
	if errors.Is(err, install.ErrNoReusableOperator) {
		return fmt.Errorf("no reusable operator Helm release found (chart values must reference existing Secrets). First install or credential rotation: nr cluster register --resume --reinstall-operator")
	}
	if err != nil {
		return fmt.Errorf("detect operator release: %w", err)
	}
	if driver == "" {
		driver = existing.Driver
	}
	if driver != existing.Driver {
		return UsageErrorf("live operator is %s; --%s %s does not match", existing.Driver, flagHypervisorDriver, driver)
	}

	if chartPath == "" {
		chartPath = strings.TrimSpace(os.Getenv(operatorChartEnv(driver)))
	}

	u, err := install.NewOperatorUpgrade(driver, kubeconfig, chartPath, chartVersion)
	if err != nil {
		return err
	}
	log.Infof("Reusing Secrets %s in %s (release %s)", strings.Join(existing.SecretNames, ", "), existing.Namespace, existing.Release)
	if dryRun {
		_, err := fmt.Fprintf(cmd.OutOrStdout(), "helm %s\n", strings.Join(install.InPlaceUpgradeArgs(u), " "))
		return err
	}
	return install.UpgradeOperatorRelease(ctx, u, log)
}

func operatorChartEnv(driver string) string {
	switch {
	case config.IsVirtFusionHypervisor(driver):
		return config.EnvVirtFusionOperatorChart
	case config.IsSolusVMHypervisor(driver):
		return config.EnvSolusVMOperatorChart
	default:
		return config.EnvProxmoxOperatorChart
	}
}
