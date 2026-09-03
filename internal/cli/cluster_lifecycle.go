package cli

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/noderings/cli/internal/api"
	"github.com/noderings/cli/internal/config"
	"github.com/noderings/cli/internal/install"
	"github.com/noderings/cli/internal/logger"
	"github.com/noderings/cli/internal/state"
)

const (
	// deleteAgentRetries bounds DeleteAgent auth-refresh retries during deregister.
	deleteAgentRetries = 3
	// deleteAgentTimeout bounds the DeleteAgent call: the platform unpeers and unoffloads
	// synchronously, so this must not inherit an unbounded context. It also has to exceed the
	// server's own Liqo unpeer/unoffload budget, or the client aborts a delete that is still
	// progressing and the platform sees its context canceled mid-teardown.
	deleteAgentTimeout = 5 * time.Minute
)

var (
	clusterDeregisterCmd = &cobra.Command{
		Use:   "deregister",
		Short: "Deregister cluster and uninstall local components",
		RunE:  runClusterDeregister,
	}
	clusterRollbackCmd = &cobra.Command{
		Use:   "rollback",
		Short: "Roll back installation state to the last successful checkpoint",
		RunE:  runClusterRollback,
	}
	clusterRecoverCmd = &cobra.Command{
		Use:   "recover",
		Short: "Reconcile local state with the installed cluster",
		RunE:  runClusterRecover,
	}
	clusterResetCmd = &cobra.Command{
		Use:   "reset",
		Short: "Reset installation checkpoints (keeps agent identity)",
		RunE:  runClusterReset,
	}
)

func init() {
	clusterCmd.AddCommand(clusterDeregisterCmd)
	clusterCmd.AddCommand(clusterRollbackCmd)
	clusterCmd.AddCommand(clusterRecoverCmd)
	clusterCmd.AddCommand(clusterResetCmd)

	for _, cmd := range []*cobra.Command{clusterDeregisterCmd, clusterRollbackCmd, clusterRecoverCmd, clusterResetCmd} {
		cmd.Flags().String("name", "", "Agent/cluster name (required)")
		cmd.Flags().String("agent-id", "", "Agent ID (UUID)")
	}

	clusterDeregisterCmd.Flags().Bool("force", false, "Continue on partial cleanup failures")
	clusterDeregisterCmd.Flags().BoolP("yes", "y", false, "Skip the destructive-action confirmation prompt")
	clusterDeregisterCmd.Flags().Bool("skip-api", false, "Skip DeleteAgent API call")
}

// deleteAgentResponseErr maps a DeleteAgent response to an error, treating an
// already-absent agent as success so a retried deregister can reach local teardown.
func deleteAgentResponseErr(resp *http.Response) error {
	if resp.StatusCode < 400 {
		return nil
	}
	err := api.ParseError(resp)
	if isAgentNotFound(err) {
		return nil
	}
	return err
}

// deleteAgentViaAPI removes the agent from the platform, which also unpeers and unoffloads
// from the platform side before local teardown begins.
func deleteAgentViaAPI(ctx context.Context, cmd *cobra.Command, agentID string, log *logger.Logger) error {
	apiClient, err := getAuthenticatedAPIClient(cmd, withAPITimeout(deleteAgentTimeout))
	if err != nil {
		return fmt.Errorf("create API client: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, deleteAgentTimeout)
	defer cancel()

	genClient := apiClient.GetGeneratedClient()
	resp, err := apiClient.DoWithAutoRefresh(ctx, deleteAgentRetries, func() (*http.Response, error) {
		return genClient.AgentServiceDeleteAgent(ctx, agentID)
	})
	if err != nil {
		// DoWithAutoRefresh already maps a 404 onto an error, so the already-deleted case
		// arrives here rather than in the response below. A retried deregister must still
		// reach local teardown.
		if isAgentNotFound(err) {
			log.Infof("Agent %s is already absent from the API", agentID)
			return nil
		}
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if err := deleteAgentResponseErr(resp); err != nil {
		return err
	}

	log.Infof("✓ Deleted agent %s from API (platform peering removed)", agentID)
	return nil
}

func resolveClusterStateKey(cmd *cobra.Command) (string, error) {
	name, _ := cmd.Flags().GetString("name")
	agentID, _ := cmd.Flags().GetString("agent-id")
	if agentID != "" {
		return agentID, nil
	}
	if name != "" {
		return name, nil
	}
	return "", RequiredOneOfFlags("name", "agent-id")
}

func runClusterDeregister(cmd *cobra.Command, args []string) error {
	ctx := context.Background()
	force, _ := cmd.Flags().GetBool("force")
	skipAPI, _ := cmd.Flags().GetBool("skip-api")

	configDir, _ := cmd.Flags().GetString("config-dir")
	if configDir == "" {
		configDir = config.GetConfigDir()
	}

	key, err := resolveClusterStateKey(cmd)
	if err != nil {
		return err
	}

	cfgLoader := config.NewLoader()
	cfg, err := cfgLoader.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	log, err := logger.NewLogger(cfg.Logging.Level, cfg.Logging.File)
	if err != nil {
		return fmt.Errorf("create logger: %w", err)
	}
	warnInsecureTLS(cmd, cfg, log)

	stateManager := state.NewManager(configDir, key)
	if err := stateManager.Load(); err != nil {
		return fmt.Errorf("load state: %w", err)
	}
	st := stateManager.GetState()
	agentID := st.AgentID
	if agentID == "" {
		agentID, _ = cmd.Flags().GetString("agent-id")
	}
	agentName := st.AgentName
	if agentName == "" {
		agentName, _ = cmd.Flags().GetString("name")
	}

	if !skipAPI && agentID == "" {
		return fmt.Errorf(
			"agent ID is missing from local state; pass --agent-id, or use --skip-api only if the agent is already deleted",
		)
	}

	yes, _ := cmd.Flags().GetBool("yes")
	if !yes {
		question := fmt.Sprintf("Deregister %q? This uninstalls local Liqo, Calico, and k3s", key)
		if !skipAPI {
			question += " and deletes the agent from the API"
		}
		ok, confirmErr := confirmYesNo(question, "re-run with --yes to skip confirmation")
		if confirmErr != nil {
			return confirmErr
		}
		if !ok {
			fmt.Fprintln(os.Stderr, "Canceled.")
			return nil
		}
	}

	if backupPath, err := stateManager.Backup(); err != nil {
		return fmt.Errorf("backup state: %w", err)
	} else if backupPath != "" {
		log.Infof("State backed up to %s", backupPath)
	}

	cleaner, err := install.NewClusterCleaner(log)
	if err != nil {
		return err
	}

	peeringKubeconfig := ""
	if agentID != "" {
		peeringKubeconfig = filepath.Join(configDir, fmt.Sprintf("peering-%s-kubeconfig.yaml", agentID))
		if _, err := os.Stat(peeringKubeconfig); err != nil {
			peeringKubeconfig = ""
		}
	}

	handleErr := func(step string, err error) error {
		if err == nil {
			return nil
		}
		if force {
			log.Warnf("%s failed (continuing due to --force): %v", step, err)
			return nil
		}
		return fmt.Errorf("%s: %w", step, err)
	}

	// Provider-local offloads (vnc-gateway) are not removed by DeleteAgent. Do this first so
	// a failed or aborted API delete cannot leave NamespaceOffloading behind for the next
	// register (Liqo rejects changing RemoteNamespaceName in place).
	if err := handleErr("unoffload namespaces", cleaner.UnoffloadNamespaces(ctx)); err != nil {
		return err
	}

	// The platform must drop its inbound peering first: while it holds the peering, its
	// controllers recreate the local ForeignCluster and `liqoctl uninstall` pre-checks fail.
	// Not subject to --force: local teardown cannot succeed while that peering stands.
	platformOwnsRemoteUnpeer := false
	if !skipAPI && agentID != "" {
		if err := deleteAgentViaAPI(ctx, cmd, agentID, log); err != nil {
			return fmt.Errorf("delete agent: %w\n"+
				"Resolve the API error first. Use --skip-api only when the agent is already deleted", err)
		}
		platformOwnsRemoteUnpeer = true
	}

	// DeleteAgent hands the remote half of the teardown to the platform, which unpeers and
	// then revokes the peering user. Driving the remote side from here too would race that
	// task and fail with 403 as soon as it revokes the credentials, so give up the remote
	// kubeconfig and wait for the handover. Under --skip-api nobody else is unpeering, so the
	// local run keeps the remote kubeconfig and does it itself.
	if platformOwnsRemoteUnpeer {
		peeringKubeconfig = ""
		if err := handleErr("wait for platform unpeer", cleaner.WaitForPlatformUnpeer(ctx)); err != nil {
			return err
		}
	}

	if err := handleErr("unpeer Liqo", cleaner.UnpeerLiqo(ctx, peeringKubeconfig)); err != nil {
		return err
	}
	if err := handleErr("unoffload namespaces", cleaner.UnoffloadNamespaces(ctx)); err != nil {
		return err
	}
	if err := handleErr("cleanup Liqo tenant leftovers", cleaner.CleanupLiqoTenantLeftovers(ctx)); err != nil {
		return err
	}
	if err := handleErr("uninstall Liqo", cleaner.UninstallLiqo(ctx, "")); err != nil {
		return err
	}
	if err := handleErr("uninstall Calico", cleaner.UninstallCalico(ctx)); err != nil {
		return err
	}
	if err := handleErr("uninstall k3s", cleaner.UninstallK3s(ctx)); err != nil {
		return err
	}

	if peeringKubeconfig != "" {
		_ = os.Remove(peeringKubeconfig)
	}
	if err := handleErr("clear local state", stateManager.ClearLocalState()); err != nil {
		return err
	}

	log.Infof("✓ Cluster deregistered (name=%s id=%s)", agentName, agentID)
	return nil
}

func runClusterRollback(cmd *cobra.Command, args []string) error {
	configDir, _ := cmd.Flags().GetString("config-dir")
	if configDir == "" {
		configDir = config.GetConfigDir()
	}
	key, err := resolveClusterStateKey(cmd)
	if err != nil {
		return err
	}

	cfgLoader := config.NewLoader()
	cfg, err := cfgLoader.Load()
	if err != nil {
		return err
	}
	log, err := logger.NewLogger(cfg.Logging.Level, cfg.Logging.File)
	if err != nil {
		return err
	}

	stateManager := state.NewManager(configDir, key)
	if err := stateManager.Load(); err != nil {
		return err
	}
	if _, err := stateManager.Backup(); err != nil {
		return fmt.Errorf("backup state: %w", err)
	}

	cp, err := stateManager.RollbackToLastCheckpoint()
	if err != nil {
		return err
	}
	if err := stateManager.Save(); err != nil {
		return err
	}

	log.Infof("✓ Rolled back to checkpoint phase %s (use 'nr cluster register --resume' to continue)", cp.Phase)
	return nil
}

func runClusterRecover(cmd *cobra.Command, args []string) error {
	ctx := context.Background()
	configDir, _ := cmd.Flags().GetString("config-dir")
	if configDir == "" {
		configDir = config.GetConfigDir()
	}
	key, err := resolveClusterStateKey(cmd)
	if err != nil {
		return err
	}

	cfgLoader := config.NewLoader()
	cfg, err := cfgLoader.Load()
	if err != nil {
		return err
	}
	log, err := logger.NewLogger(cfg.Logging.Level, cfg.Logging.File)
	if err != nil {
		return err
	}

	stateManager := state.NewManager(configDir, key)
	if err := stateManager.Load(); err != nil {
		return err
	}

	k3sOK, calicoOK, liqoOK, apiOK := install.DetectInstalledComponents(ctx)
	peeringOK := apiOK && liqoOK // best-effort: Liqo present implies peering may exist
	complete := k3sOK && calicoOK && liqoOK && peeringOK && stateManager.GetState().Phase == state.PhaseComplete

	newPhase := state.ReconcilePhase(k3sOK, calicoOK, liqoOK, peeringOK, complete)
	oldPhase := stateManager.GetState().Phase
	stateManager.SetPhase(newPhase)
	stateManager.ClearError()
	if err := stateManager.Save(); err != nil {
		return err
	}

	log.Infof("✓ Recovered state: %s → %s (k3s=%v calico=%v liqo=%v api=%v)", oldPhase, newPhase, k3sOK, calicoOK, liqoOK, apiOK)
	return nil
}

func runClusterReset(cmd *cobra.Command, args []string) error {
	configDir, _ := cmd.Flags().GetString("config-dir")
	if configDir == "" {
		configDir = config.GetConfigDir()
	}
	key, err := resolveClusterStateKey(cmd)
	if err != nil {
		return err
	}

	cfgLoader := config.NewLoader()
	cfg, err := cfgLoader.Load()
	if err != nil {
		return err
	}
	log, err := logger.NewLogger(cfg.Logging.Level, cfg.Logging.File)
	if err != nil {
		return err
	}

	stateManager := state.NewManager(configDir, key)
	if err := stateManager.Load(); err != nil {
		return err
	}
	if _, err := stateManager.Backup(); err != nil {
		log.Warnf("Backup failed: %v", err)
	}
	stateManager.ResetCheckpoints()
	if err := stateManager.Save(); err != nil {
		return err
	}
	log.Info("✓ Checkpoints reset")
	return nil
}

func warnInsecureTLS(cmd *cobra.Command, cfg *config.Config, log *logger.Logger) {
	dev := IsDevMode(cmd)
	tlsInsecure := cfg != nil && cfg.API.TLSInsecure
	if !dev {
		_, _, tlsInsecureFromDev := GetDevSettings(cmd)
		tlsInsecure = tlsInsecure || tlsInsecureFromDev
	} else {
		tlsInsecure = true
	}
	if dev {
		log.Warn("SECURITY: --dev mode enabled (TLS verification may be disabled; local control plane only)")
	}
	if tlsInsecure {
		warnInsecureTLSOnce(log)
	}
}
