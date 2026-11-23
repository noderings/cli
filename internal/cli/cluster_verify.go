package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/noderings/cli/internal/api"
	generated "github.com/noderings/cli/internal/api/generated"
	"github.com/noderings/cli/internal/config"
	"github.com/noderings/cli/internal/install"
	"github.com/noderings/cli/internal/logger"
	"github.com/noderings/cli/internal/state"
	"github.com/noderings/cli/internal/verify"
)

var clusterVerifyCmd = &cobra.Command{
	Use:   "verify",
	Short: "Verify provider cluster health (subsections aware of register flags)",
	Long: `Verify that the local provider cluster is healthy after registration.

Subsections: kubernetes, calico, liqo, peering, offloading, agent, operator.

Checks that are not applicable for the register flags used (for example
--skip-operator-install or --disable-offloading) are skipped rather than failed.

Examples:
  nr cluster verify --name my-provider
  nr cluster verify --agent-id <uuid> --skip-operator
  nr cluster verify --section kubernetes,liqo,operator
`,
	RunE: runClusterVerify,
}

func init() {
	clusterCmd.AddCommand(clusterVerifyCmd)

	clusterVerifyCmd.Flags().String("name", "", "Agent/cluster name")
	clusterVerifyCmd.Flags().String("agent-id", "", "Agent ID (UUID)")
	clusterVerifyCmd.Flags().String("output", config.OutputFormatText, "Output format: text|json")
	clusterVerifyCmd.Flags().StringSlice("section", nil, "Limit to subsections (kubernetes,calico,liqo,peering,offloading,agent,operator)")
	clusterVerifyCmd.Flags().Bool("skip-operator", false, "Do not require hypervisor operator Helm workloads (same as register --skip-operator-install)")
	clusterVerifyCmd.Flags().Bool("skip-operator-install", false, "Alias for --skip-operator")
	clusterVerifyCmd.Flags().Bool("disable-offloading", false, "Do not require provider NamespaceOffloading (same as register --disable-offloading)")
	clusterVerifyCmd.Flags().String("vnc-gateway-namespace", "", "VNC gateway namespace expected to be offloaded (default: vnc-gateway)")
	clusterVerifyCmd.Flags().String("operator-namespace", "", "Optional extra provider namespace expected to be offloaded")
	clusterVerifyCmd.Flags().StringSlice("offload-namespaces", nil, "Additional namespaces expected to be offloaded")
	clusterVerifyCmd.Flags().Bool("no-agent-api", false, "Skip platform API agent/provisioned checks")
}

type agentAPIStatusFetcher struct {
	api *api.Client
}

func (f agentAPIStatusFetcher) FetchAgentStatus(ctx context.Context, agentID string) (*verify.AgentStatus, error) {
	genClient := f.api.GetGeneratedClient()
	resp, err := f.api.DoWithAutoRefresh(ctx, 3, func() (*http.Response, error) {
		return genClient.AgentServiceGetAgent(ctx, agentID)
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		return nil, api.ParseError(resp)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var parsed generated.V1GetAgentResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("parse get agent response: %w", err)
	}
	if parsed.Agent == nil {
		return nil, fmt.Errorf("agent missing in response")
	}
	st := &verify.AgentStatus{}
	if parsed.Agent.Name != nil {
		st.Name = *parsed.Agent.Name
	}
	if parsed.Agent.Provisioned != nil {
		st.Provisioned = *parsed.Agent.Provisioned
	}
	if parsed.Agent.InboundPeeringState != nil {
		st.InboundPeeringState = string(*parsed.Agent.InboundPeeringState)
	}
	if parsed.Agent.InboundPeeringError != nil {
		st.InboundPeeringError = *parsed.Agent.InboundPeeringError
	}
	if parsed.Agent.ServiceStatus != nil {
		st.ServiceStatus = string(*parsed.Agent.ServiceStatus)
	}
	return st, nil
}

// runPostRegisterVerify verifies provider health after a successful register path.
func runPostRegisterVerify(
	ctx context.Context,
	apiClient *api.Client,
	log *logger.Logger,
	agentID string,
	registerOpts clusterRegisterOpts,
) error {
	scope := verify.RegisterScope{
		AgentID:             agentID,
		DisableOffloading:   registerOpts.disableOffloading,
		SkipOperatorInstall: registerOpts.skipOperatorInstall,
		HypervisorDriver:    registerOpts.hypervisorDriver,
		OperatorNamespace:   registerOpts.operatorNamespace,
		VNCGatewayNamespace: registerOpts.vncGatewayNamespace,
		OffloadNamespaces:   registerOpts.offloadNamespaces,
	}
	opts := verify.OptionsFromRegister(scope)

	log.Info("Verifying provider cluster health...")
	report, err := executeVerify(ctx, apiClient, opts)
	if err != nil {
		return err
	}
	if registerOpts.output == config.OutputFormatJSON {
		if err := writeJSON(report); err != nil {
			return err
		}
	} else if err := verify.FormatText(os.Stdout, report); err != nil {
		return err
	}
	log.Info(verify.SummaryLine(report))
	if !report.Passed() {
		return fmt.Errorf("provider verification failed (%d check(s)); fix the issues above or re-run: nr cluster verify", report.FailedCount())
	}
	return nil
}

func executeVerify(ctx context.Context, apiClient *api.Client, opts verify.Options) (*verify.Report, error) {
	if err := verify.ValidateOnlySections(opts.OnlySections); err != nil {
		return nil, NewUsageError(err)
	}

	// Prefer pinned tools from a prior register (~/.nr/bin) when present.
	if _, err := install.EnsureCLIBinDir(); err != nil {
		// Non-fatal: LookPath below still resolves system PATH.
		_ = err
	}

	kubePath := install.EnsureReadableKubeconfig(ctx, opts.KubeconfigPath, nil)
	opts.KubeconfigPath = kubePath

	k8sClient, err := newReadableK8sClient(ctx, nil)
	if err != nil {
		// Still run what we can; kubernetes section will fail clearly.
		k8sClient = nil
	}

	var fetcher verify.AgentStatusFetcher
	if opts.ExpectAgentAPI && apiClient != nil {
		fetcher = agentAPIStatusFetcher{api: apiClient}
	}

	report, err := verify.New(opts, k8sClient, fetcher).Run(ctx)
	if err != nil {
		return nil, err
	}
	if len(opts.OnlySections) > 0 && len(report.Sections) == 0 {
		return nil, fmt.Errorf("no verify sections ran for --section=%v", opts.OnlySections)
	}
	return report, nil
}

func runClusterVerify(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	configDir, _ := cmd.Flags().GetString("config-dir")
	if configDir == "" {
		configDir = config.GetConfigDir()
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

	name, _ := cmd.Flags().GetString("name")
	agentIDFlag, _ := cmd.Flags().GetString("agent-id")
	outputRaw, _ := cmd.Flags().GetString("output")
	output, err := parseOutputFormat(outputRaw)
	if err != nil {
		return err
	}
	sections, _ := cmd.Flags().GetStringSlice("section")
	skipOperator, _ := cmd.Flags().GetBool("skip-operator")
	if cmd.Flags().Changed("skip-operator-install") {
		skipOperatorInstall, _ := cmd.Flags().GetBool("skip-operator-install")
		skipOperator = skipOperatorInstall
	}
	disableOffloading, _ := cmd.Flags().GetBool("disable-offloading")
	vncNS, _ := cmd.Flags().GetString("vnc-gateway-namespace")
	operatorNS, _ := cmd.Flags().GetString("operator-namespace")
	extraOffload, _ := cmd.Flags().GetStringSlice("offload-namespaces")
	noAgentAPI, _ := cmd.Flags().GetBool("no-agent-api")

	agentID := strings.TrimSpace(agentIDFlag)
	stateKey := strings.TrimSpace(name)
	if stateKey == "" {
		stateKey = agentID
	}
	if stateKey == "" {
		return RequiredOneOfFlags("name", "agent-id")
	}

	sm := state.NewManager(configDir, stateKey)
	_ = sm.Load()
	st := sm.GetState()
	if agentID == "" {
		agentID = st.AgentID
	}
	if agentID == "" {
		return fmt.Errorf("agent ID unknown; pass --agent-id or register first")
	}

	persisted := sm.GetRegisterScope()

	// Prefer explicit flags; otherwise infer scope from persisted register flags / checkpoints.
	if !cmd.Flags().Changed("skip-operator") && !cmd.Flags().Changed("skip-operator-install") {
		if persisted != nil {
			skipOperator = persisted.SkipOperatorInstall
		} else {
			skipOperator = !sm.HasSuccessfulCheckpoint(state.PhaseOperatorInstall)
		}
	}
	if !cmd.Flags().Changed("disable-offloading") {
		if persisted != nil {
			disableOffloading = persisted.DisableOffloading
		} else if st.Phase == state.PhaseComplete && !sm.HasSuccessfulCheckpoint(state.PhaseOffloading) {
			disableOffloading = true
		}
	}
	if !cmd.Flags().Changed("vnc-gateway-namespace") {
		if vncNS == "" && persisted != nil && persisted.VNCGatewayNamespace != "" {
			vncNS = persisted.VNCGatewayNamespace
		}
		if vncNS == "" && !disableOffloading {
			vncNS = config.DefaultVNCGatewayNamespace
		}
	}
	if !cmd.Flags().Changed("operator-namespace") && operatorNS == "" && persisted != nil {
		operatorNS = persisted.OperatorNamespace
	}
	if !cmd.Flags().Changed("offload-namespaces") && len(extraOffload) == 0 && persisted != nil {
		extraOffload = persisted.OffloadNamespaces
	}

	hypervisorDriver := ""
	if persisted != nil {
		hypervisorDriver = persisted.HypervisorDriver
	}

	scope := verify.RegisterScope{
		AgentID:             agentID,
		DisableOffloading:   disableOffloading,
		SkipOperatorInstall: skipOperator,
		HypervisorDriver:    hypervisorDriver,
		OperatorNamespace:   operatorNS,
		VNCGatewayNamespace: vncNS,
		OffloadNamespaces:   extraOffload,
	}
	opts := verify.OptionsFromRegister(scope)
	opts.OnlySections = sections
	if noAgentAPI {
		opts.ExpectAgentAPI = false
	}

	var apiClient *api.Client
	if opts.ExpectAgentAPI {
		apiClient, err = getAuthenticatedAPIClient(cmd)
		if err != nil {
			return err
		}
	}

	report, err := executeVerify(ctx, apiClient, opts)
	if err != nil {
		return err
	}

	if output == config.OutputFormatJSON {
		if err := writeJSON(report); err != nil {
			return err
		}
	} else if err := verify.FormatText(os.Stdout, report); err != nil {
		return err
	}

	if !report.Passed() {
		return fmt.Errorf("provider verification failed (%d check(s))", report.FailedCount())
	}
	return nil
}
