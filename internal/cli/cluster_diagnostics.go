package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/noderings/cli/internal/config"
	"github.com/noderings/cli/internal/diagnostics"
	"github.com/noderings/cli/internal/logger"
)

var (
	clusterStatusCmd = &cobra.Command{
		Use:   "status",
		Short: "Show local registration phase and cluster reachability",
		RunE:  runClusterStatus,
	}
	clusterInfoCmd = &cobra.Command{
		Use:   "info",
		Short: "Show agent metadata, versions, and CIDRs",
		RunE:  runClusterInfo,
	}
	clusterHealthCmd = &cobra.Command{
		Use:   "health",
		Short: "Run component health checks",
		RunE:  runClusterHealth,
	}
	clusterLogsCmd = &cobra.Command{
		Use:   "logs",
		Short: "Show recent cluster-related logs",
		RunE:  runClusterLogs,
	}
	clusterDebugCmd = &cobra.Command{
		Use:   "debug",
		Short: "Write a debug report bundle under ~/.nr/debug-<timestamp>/",
		RunE:  runClusterDebug,
	}
)

func init() {
	clusterCmd.AddCommand(clusterStatusCmd)
	clusterCmd.AddCommand(clusterInfoCmd)
	clusterCmd.AddCommand(clusterHealthCmd)
	clusterCmd.AddCommand(clusterLogsCmd)
	clusterCmd.AddCommand(clusterDebugCmd)

	for _, cmd := range []*cobra.Command{clusterStatusCmd, clusterInfoCmd, clusterHealthCmd, clusterLogsCmd, clusterDebugCmd} {
		cmd.Flags().String("name", "", "Agent/cluster name")
		cmd.Flags().String("agent-id", "", "Agent ID (UUID)")
		cmd.Flags().String("output", config.OutputFormatText, "Output format: text|json")
	}

	clusterLogsCmd.Flags().Int("lines", 200, "Number of log lines to collect (max 2000)")
}

func newDiagnosticsCollector(cmd *cobra.Command) (*diagnostics.Collector, *config.Config, error) {
	configDir, _ := cmd.Flags().GetString("config-dir")
	if configDir == "" {
		configDir = config.GetConfigDir()
	}
	key, err := resolveClusterStateKey(cmd)
	if err != nil {
		return nil, nil, err
	}
	cfgLoader := config.NewLoader()
	cfg, err := cfgLoader.Load()
	if err != nil {
		return nil, nil, err
	}
	return diagnostics.NewCollector(configDir, key), cfg, nil
}

func runClusterStatus(cmd *cobra.Command, args []string) error {
	ctx := context.Background()
	collector, cfg, err := newDiagnosticsCollector(cmd)
	if err != nil {
		return err
	}
	log, _ := logger.NewLogger(cfg.Logging.Level, cfg.Logging.File)
	warnInsecureTLS(cmd, cfg, log)

	status, err := collector.CollectStatus(ctx)
	if err != nil {
		return err
	}

	outputRaw, _ := cmd.Flags().GetString("output")
	output, err := parseOutputFormat(outputRaw)
	if err != nil {
		return err
	}
	if output == config.OutputFormatJSON {
		return writeJSON(status)
	}

	fmt.Printf("Phase:        %s\n", status.Phase)
	fmt.Printf("Agent ID:     %s\n", status.AgentID)
	fmt.Printf("Agent Name:   %s\n", status.AgentName)
	fmt.Printf("API:          %v\n", status.APIReachable)
	fmt.Printf("k3s:          %v\n", status.K3sInstalled)
	fmt.Printf("Calico:       %v\n", status.CalicoOK)
	fmt.Printf("Liqo:         %v\n", status.LiqoOK)
	if status.PeeringSummary != "" {
		fmt.Printf("Peering:      %s\n", status.PeeringSummary)
	}
	if status.LastError != "" {
		fmt.Printf("Last Error:   %s\n", status.LastError)
	}
	return nil
}

func runClusterInfo(cmd *cobra.Command, args []string) error {
	collector, cfg, err := newDiagnosticsCollector(cmd)
	if err != nil {
		return err
	}
	info, err := collector.CollectInfo(cfg.K3s.ClusterCIDR, cfg.K3s.ServiceCIDR)
	if err != nil {
		return err
	}

	outputRaw, _ := cmd.Flags().GetString("output")
	output, err := parseOutputFormat(outputRaw)
	if err != nil {
		return err
	}
	if output == config.OutputFormatJSON {
		return writeJSON(info)
	}

	fmt.Printf("Agent ID:     %s\n", info.AgentID)
	fmt.Printf("Agent Name:   %s\n", info.AgentName)
	fmt.Printf("Phase:        %s\n", info.Phase)
	fmt.Printf("Cluster CIDR: %s\n", info.ClusterCIDR)
	fmt.Printf("Service CIDR: %s\n", info.ServiceCIDR)
	fmt.Printf("Kubeconfig:   %s\n", info.Kubeconfig)
	fmt.Printf("State:        %s\n", info.StatePath)
	if len(info.Versions) > 0 {
		fmt.Println("Versions:")
		for k, v := range info.Versions {
			fmt.Printf("  %s: %s\n", k, v)
		}
	}
	return nil
}

func runClusterHealth(cmd *cobra.Command, args []string) error {
	ctx := context.Background()
	collector, cfg, err := newDiagnosticsCollector(cmd)
	if err != nil {
		return err
	}
	checks, err := collector.CollectHealth(ctx)
	if err != nil {
		return err
	}

	allPass, failed := diagnostics.SummarizeHealth(checks)

	outputRaw, _ := cmd.Flags().GetString("output")
	output, err := parseOutputFormat(outputRaw)
	if err != nil {
		return err
	}
	if output == config.OutputFormatJSON {
		if err := writeJSON(checks); err != nil {
			return err
		}
		if !allPass {
			return fmt.Errorf("health check failed (%d component(s))", failed)
		}
		return nil
	}

	for _, c := range checks {
		mark := markPass()
		switch c.Status {
		case "fail":
			mark = markFail()
		case "warn":
			mark = markWarn()
		}
		fmt.Printf("%s %s: %s\n", mark, c.Component, c.Message)
	}
	if !allPass {
		return fmt.Errorf("health check failed (%d component(s))", failed)
	}
	_ = cfg
	fmt.Printf("%s All health checks passed\n", markPass())
	return nil
}

func runClusterLogs(cmd *cobra.Command, args []string) error {
	ctx := context.Background()
	collector, _, err := newDiagnosticsCollector(cmd)
	if err != nil {
		return err
	}
	lines, _ := cmd.Flags().GetInt("lines")
	logs, err := collector.CollectLogs(ctx, lines)
	if err != nil {
		return err
	}
	fmt.Print(logs)
	return nil
}

func runClusterDebug(cmd *cobra.Command, args []string) error {
	ctx := context.Background()
	collector, cfg, err := newDiagnosticsCollector(cmd)
	if err != nil {
		return err
	}
	dir, err := collector.WriteDebugBundle(ctx, cfg.K3s.ClusterCIDR, cfg.K3s.ServiceCIDR)
	if err != nil {
		return err
	}
	fmt.Printf("%s Debug report written to %s\n", markPass(), dir)
	return nil
}
