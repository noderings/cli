package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/noderings/cli/internal/api"
	generated "github.com/noderings/cli/internal/api/generated"
	"github.com/noderings/cli/internal/auth"
	"github.com/noderings/cli/internal/config"
)

var (
	agentCmd = &cobra.Command{
		Use:   "agent",
		Short: "Agent management commands",
		Long:  "Manage agents directly via API (lower-level operations)",
	}

	agentCreateCmd = &cobra.Command{
		Use:   "create",
		Short: "Create a new agent",
		Long:  "Create a new agent in the backend API",
		RunE:  runAgentCreate,
	}

	agentListCmd = &cobra.Command{
		Use:   "list",
		Short: "List all agents",
		Long:  "List all agents in your organization",
		RunE:  runAgentList,
	}

	agentGetCmd = &cobra.Command{
		Use:   "get",
		Short: "Get agent information",
		Long:  "Get detailed information about a specific agent",
		RunE:  runAgentGet,
	}

	agentUpdateCmd = &cobra.Command{
		Use:   "update",
		Short: "Update agent information",
		Long:  "Update agent information (IP, region, description)",
		RunE:  runAgentUpdate,
	}

	agentDeleteCmd = &cobra.Command{
		Use:   "delete",
		Short: "Delete an agent",
		Long:  "Delete an agent from the backend (does NOT uninstall k3s)",
		RunE:  runAgentDelete,
	}
)

func init() {
	agentCmd.AddCommand(agentCreateCmd)
	agentCmd.AddCommand(agentListCmd)
	agentCmd.AddCommand(agentGetCmd)
	agentCmd.AddCommand(agentUpdateCmd)
	agentCmd.AddCommand(agentDeleteCmd)

	// agent create flags
	agentCreateCmd.Flags().String("name", "", "Agent name (required, must be unique in organization)")
	agentCreateCmd.Flags().String("agent-ip", "", "Agent public IP address (required, must be valid public IP)")
	agentCreateCmd.Flags().String("gateway-region", "", "Gateway region (required)")
	agentCreateCmd.Flags().String("description", "", "Optional description for the agent")
	agentCreateCmd.Flags().String("hypervisor-driver", "", "Optional. Uses the organization hypervisor driver when omitted. Must match the organization if set.")
	agentCreateCmd.Flags().String("output", config.OutputFormatText, "Output format: text|json")

	// agent list flags
	agentListCmd.Flags().String("output", config.OutputFormatText, "Output format: text|json")
	agentListCmd.Flags().String("format", config.OutputFormatText, "Alias for --output")

	// agent get flags
	agentGetCmd.Flags().String("agent-id", "", "Agent ID (UUID)")
	agentGetCmd.Flags().String("name", "", "Agent name (alternative to ID)")
	agentGetCmd.Flags().String("output", config.OutputFormatText, "Output format: text|json")

	// agent update flags
	agentUpdateCmd.Flags().String("agent-id", "", "Agent ID (UUID)")
	agentUpdateCmd.Flags().String("name", "", "Agent name (alternative to ID for finding agent)")
	agentUpdateCmd.Flags().String("set-name", "", "Set new agent name")
	agentUpdateCmd.Flags().String("description", "", "Update description")

	// agent delete flags
	agentDeleteCmd.Flags().String("agent-id", "", "Agent ID (UUID)")
	agentDeleteCmd.Flags().String("name", "", "Agent name (alternative to ID)")
	agentDeleteCmd.Flags().Bool("force", false, "Skip confirmation prompt")
}

// defaultAPITimeout bounds an ordinary API call.
const defaultAPITimeout = 20 * time.Second

// apiClientOption customizes the client built by getAuthenticatedAPIClient.
type apiClientOption func(*api.Config, *apiClientExtras)

type apiClientExtras struct {
	skipProviderOrg bool
}

// withAPITimeout raises the HTTP client timeout for endpoints that block on slow server-side
// work. http.Client.Timeout is a hard wall-clock cap that preempts the request context, so
// widening only the context is not enough: the client aborts mid-flight while the server keeps
// going, leaving the caller unable to tell a timeout from a failure.
func withAPITimeout(timeout time.Duration) apiClientOption {
	return func(cfg *api.Config, _ *apiClientExtras) { cfg.Timeout = timeout }
}

// withoutProviderOrganization skips listing orgs to set X-Organization-ID.
// Use for auth status: ListOrganizations must work before a provider org exists.
func withoutProviderOrganization() apiClientOption {
	return func(_ *api.Config, extra *apiClientExtras) {
		extra.skipProviderOrg = true
	}
}

// getAuthenticatedAPIClient creates an authenticated API client
// It supports multiple authentication methods (both are fully supported):
// 1. Service Account tokens: Environment variables (NR_API_TOKEN, NODERINGS_API_TOKEN) - highest priority
// 2. Service Account tokens: Config file (auth.token)
// 3. OAuth tokens: OAuth token storage (via 'nr auth login')
//
// The priority order determines which token is used when multiple are available.
func getAuthenticatedAPIClient(cmd *cobra.Command, opts ...apiClientOption) (*api.Client, error) {
	// Load configuration
	cfgLoader := config.NewLoader()
	cfg, err := cfgLoader.Load()
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	// Get API URL using global helper (flag -> dev -> config)
	apiURL := GetAPIURL(cmd, cfg.API.BaseURL)

	// Get TLS insecure setting: --dev flag overrides config
	_, _, tlsInsecureFromDev := GetDevSettings(cmd)
	tlsInsecure := tlsInsecureFromDev
	if !tlsInsecure {
		tlsInsecure = cfg.API.TLSInsecure
	}

	// Get config directory
	configDir, _ := cmd.Flags().GetString("config-dir")
	if configDir == "" {
		configDir = config.GetConfigDir()
	}

	// Load token from various sources (env vars, config file, or OAuth storage)
	tokenInfo, err := auth.LoadTokenInfo(cfg, configDir)
	if err != nil {
		return nil, fmt.Errorf("not authenticated: %w - set NR_API_TOKEN env var, add token to config file, or run 'nr auth login'", err)
	}

	// For OAuth tokens, check expiration
	if tokenInfo.IsOAuthToken && tokenInfo.OAuthToken != nil {
		if tokenInfo.OAuthToken.IsExpired() {
			return nil, fmt.Errorf("token expired - run 'nr auth refresh' or 'nr auth login'")
		}
	}

	// Create API client
	apiCfg := &api.Config{
		BaseURL:     apiURL,
		Timeout:     defaultAPITimeout,
		TLSInsecure: tlsInsecure,
		CACertPath:  cfg.API.CACertPath,
	}
	extra := &apiClientExtras{}
	for _, opt := range opts {
		opt(apiCfg, extra)
	}
	apiClient, err := api.NewClient(apiCfg)
	if err != nil {
		return nil, fmt.Errorf("create API client: %w", err)
	}

	if tlsInsecure {
		warnInsecureTLSOnce(nil)
	}

	// Set authentication token
	apiClient.SetToken(tokenInfo.Token)

	// Set up automatic token refresh on 401 (only for OAuth tokens)
	if tokenInfo.IsOAuthToken {
		apiClient.SetTokenRefreshFunc(func(ctx context.Context) (string, error) {
			// Reload token info to get latest OAuth token
			currentTokenInfo, err := auth.LoadTokenInfo(cfg, configDir)
			if err != nil {
				return "", fmt.Errorf("load token for refresh: %w", err)
			}

			if !currentTokenInfo.IsOAuthToken || currentTokenInfo.OAuthToken == nil {
				return "", fmt.Errorf("no OAuth token available for refresh")
			}

			currentToken := currentTokenInfo.OAuthToken
			if currentToken.RefreshToken == "" {
				return "", fmt.Errorf("no refresh token available - please run 'nr auth login'")
			}

			// Create OAuth client for token refresh
			oauthClient := auth.NewOAuthClient(&auth.OAuthConfig{
				ClientID:    "nr-cli",
				TokenURL:    apiURL + "/v1/oauth2/token",
				APIBaseURL:  apiURL,
				TLSInsecure: tlsInsecure,
			})

			// Refresh the token
			newToken, err := oauthClient.RefreshToken(ctx, currentToken.RefreshToken)
			if err != nil {
				return "", fmt.Errorf("refresh token: %w", err)
			}

			// Save the refreshed token
			storage, err := auth.NewStorage(configDir)
			if err != nil {
				return "", fmt.Errorf("create storage: %w", err)
			}

			if err := storage.SaveToken(newToken); err != nil {
				return "", fmt.Errorf("save refreshed token: %w", err)
			}

			return newToken.AccessToken, nil
		})
	}
	// Service account tokens (from env vars or config) don't support refresh
	// They are long-lived JWT tokens that must be regenerated if expired

	// OAuth binds to the home (often client) org. List organizations and send
	// X-Organization-ID for the unique provider tenant. Service account JWTs are
	// already org-scoped; ListOrganizations is user-only.
	if !extra.skipProviderOrg && tokenInfo.IsOAuthToken {
		if err := ensureProviderOrganization(context.Background(), apiClient); err != nil {
			return nil, err
		}
	}

	return apiClient, nil
}

// findAgentIDByName finds an agent ID by name by listing all agents
func findAgentIDByName(ctx context.Context, client *api.Client, name string) (string, error) {
	agent, err := findAgentByName(ctx, client, name)
	if err != nil {
		return "", err
	}
	if agent.Id == nil || *agent.Id == "" {
		return "", fmt.Errorf("agent with name '%s' has no id", name)
	}
	return *agent.Id, nil
}

// getAgentID gets agent ID from flags (either directly or by name lookup)
func getAgentID(ctx context.Context, cmd *cobra.Command, client *api.Client) (string, error) {
	agentID, _ := cmd.Flags().GetString("agent-id")
	name, _ := cmd.Flags().GetString("name")

	if agentID != "" {
		return agentID, nil
	}

	if name != "" {
		return findAgentIDByName(ctx, client, name)
	}

	return "", RequiredOneOfFlags("agent-id", "name")
}

func runAgentCreate(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	output, err := resolveOutputFlag(cmd)
	if err != nil {
		return err
	}

	// Get authenticated API client
	apiClient, err := getAuthenticatedAPIClient(cmd)
	if err != nil {
		return err
	}

	// Get flags
	name, _ := cmd.Flags().GetString("name")
	agentIP, _ := cmd.Flags().GetString("agent-ip")
	gatewayRegion, _ := cmd.Flags().GetString("gateway-region")
	description, _ := cmd.Flags().GetString("description")
	hypervisorDriverRaw, _ := cmd.Flags().GetString("hypervisor-driver")

	// Validate required fields
	if name == "" {
		return RequiredFlag("name")
	}
	if agentIP == "" {
		return RequiredFlag("agent-ip")
	}
	if gatewayRegion == "" {
		return RequiredFlag("gateway-region")
	}

	if err := rejectUnverifiedProvider(ctx, apiClient); err != nil {
		return err
	}

	region, err := parseAgentGatewayRegion(gatewayRegion)
	if err != nil {
		return err
	}

	orgDriver := fetchOrganizationHypervisorDriver(ctx, apiClient)
	hypervisorDriver, err := resolveHypervisorDriver(
		hypervisorDriverRaw,
		cmd.Flags().Changed("hypervisor-driver"),
		orgDriver,
	)
	if err != nil {
		return err
	}

	// Create request body
	reqBody := generated.AgentServiceCreateAgentJSONRequestBody{
		Name:             &name,
		AgentPublicIp:    &agentIP,
		GatewayRegion:    &region,
		HypervisorDriver: hypervisorDriverToAPI(hypervisorDriver),
	}
	if description != "" {
		reqBody.Description = &description
	}

	// Call API with auto-refresh and retry
	genClient := apiClient.GetGeneratedClient()
	resp, err := apiClient.DoWithAutoRefresh(ctx, 3, func() (*http.Response, error) {
		return genClient.AgentServiceCreateAgent(ctx, reqBody)
	})
	if err != nil {
		return fmt.Errorf("create agent: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Handle error response
	if resp.StatusCode >= 400 {
		apiErr := api.ParseError(resp)
		if apiErr, ok := apiErr.(*api.APIError); ok && apiErr.IsAlreadyExists() {
			return fmt.Errorf("agent name '%s' already exists in your organization", name)
		}
		return apiErr
	}

	// Parse success response - CreateAgent returns GetAgentResponse with the Agent
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	var createResponse generated.V1GetAgentResponse
	if err := json.Unmarshal(body, &createResponse); err != nil {
		return fmt.Errorf("parse response: %w", err)
	}
	if createResponse.Agent == nil {
		return fmt.Errorf("agent not found in create response")
	}
	agent := createResponse.Agent

	// Output based on format
	if output == config.OutputFormatJSON {
		return writeJSON(createResponse)
	}
	id := ""
	agentName := ""
	if agent.Id != nil {
		id = *agent.Id
	}
	if agent.Name != nil {
		agentName = *agent.Name
	}
	fmt.Printf("%s Agent created successfully!\n", markPass())
	fmt.Printf("Agent ID:   %s\n", id)
	fmt.Printf("Agent Name: %s\n", agentName)

	return nil
}

func runAgentList(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	output, err := resolveOutputFlag(cmd)
	if err != nil {
		return err
	}

	// Get authenticated API client
	apiClient, err := getAuthenticatedAPIClient(cmd)
	if err != nil {
		return err
	}

	// Call API with auto-refresh and retry
	genClient := apiClient.GetGeneratedClient()
	// page_size is required (protobuf validation)
	// Use -1 to get all results (as per validation: gte = -1)
	pageSize := int32(-1)
	params := &generated.AgentServiceListAgentsParams{
		PageSize: &pageSize,
	}
	resp, err := apiClient.DoWithAutoRefresh(ctx, 3, func() (*http.Response, error) {
		return genClient.AgentServiceListAgents(ctx, params)
	})
	if err != nil {
		return fmt.Errorf("list agents: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		return api.ParseError(resp)
	}

	// Parse response using generated type
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	var listResponse generated.V1ListAgentsResponse
	if err := json.Unmarshal(body, &listResponse); err != nil {
		return fmt.Errorf("parse response: %w", err)
	}

	// Handle nil pointers
	agents := []generated.V1GetAgentResponse{}
	if listResponse.Agents != nil {
		agents = *listResponse.Agents
	}

	// Output based on format
	if output == config.OutputFormatJSON {
		return writeJSON(listResponse)
	}
	if len(agents) == 0 {
		fmt.Println("No agents found.")
		return nil
	}
	fmt.Printf("Found %d agent(s):\n\n", len(agents))
	for _, agentResp := range agents {
		if agentResp.Agent == nil {
			continue
		}
		agent := agentResp.Agent
		id := ""
		name := ""
		if agent.Id != nil {
			id = *agent.Id
		}
		if agent.Name != nil {
			name = *agent.Name
		}
		fmt.Printf("ID:   %s\n", id)
		fmt.Printf("Name: %s\n", name)
		if agent.AgentPublicIp != nil && *agent.AgentPublicIp != "" {
			fmt.Printf("IP:   %s\n", *agent.AgentPublicIp)
		}
		if agent.GatewayRegion != nil {
			fmt.Printf("Region: %s\n", *agent.GatewayRegion)
		}
		fmt.Println()
	}

	return nil
}

func runAgentGet(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	output, err := resolveOutputFlag(cmd)
	if err != nil {
		return err
	}

	// Get authenticated API client
	apiClient, err := getAuthenticatedAPIClient(cmd)
	if err != nil {
		return err
	}

	// Get agent ID
	agentID, err := getAgentID(ctx, cmd, apiClient)
	if err != nil {
		return err
	}

	// Call API with auto-refresh and retry
	genClient := apiClient.GetGeneratedClient()
	resp, err := apiClient.DoWithAutoRefresh(ctx, 3, func() (*http.Response, error) {
		return genClient.AgentServiceGetAgent(ctx, agentID)
	})
	if err != nil {
		return fmt.Errorf("get agent: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		return api.ParseError(resp)
	}

	// Parse response using generated type
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	var getResponse generated.V1GetAgentResponse
	if err := json.Unmarshal(body, &getResponse); err != nil {
		return fmt.Errorf("parse response: %w", err)
	}

	if getResponse.Agent == nil {
		return fmt.Errorf("agent not found in response")
	}
	agent := getResponse.Agent

	// Output based on format
	if output == config.OutputFormatJSON {
		return writeJSON(getResponse)
	}
	fmt.Printf("Agent Information:\n")
	id := ""
	name := ""
	if agent.Id != nil {
		id = *agent.Id
	}
	if agent.Name != nil {
		name = *agent.Name
	}
	fmt.Printf("  ID:          %s\n", id)
	fmt.Printf("  Name:        %s\n", name)
	if agent.AgentPublicIp != nil && *agent.AgentPublicIp != "" {
		fmt.Printf("  IP:          %s\n", *agent.AgentPublicIp)
	}
	if agent.GatewayRegion != nil {
		fmt.Printf("  Region:      %s\n", *agent.GatewayRegion)
	}
	if agent.Description != nil && *agent.Description != "" {
		fmt.Printf("  Description: %s\n", *agent.Description)
	}
	if agent.Status != nil {
		fmt.Printf("  Status:      %s\n", *agent.Status)
	}

	return nil
}

func runAgentUpdate(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	// Get authenticated API client
	apiClient, err := getAuthenticatedAPIClient(cmd)
	if err != nil {
		return err
	}

	// Get agent ID
	agentID, err := getAgentID(ctx, cmd, apiClient)
	if err != nil {
		return err
	}

	// Get update flags
	setName, _ := cmd.Flags().GetString("set-name")
	description, _ := cmd.Flags().GetString("description")

	// Build update body (only include fields that are set)
	updateBody := generated.AgentServiceUpdateAgentJSONRequestBody{}
	hasUpdates := false

	if setName != "" {
		updateBody.Name = &setName
		hasUpdates = true
	}

	if description != "" {
		updateBody.Description = &description
		hasUpdates = true
	}

	if !hasUpdates {
		return RequiredOneOfFlags("set-name", "description")
	}

	// Call API with auto-refresh and retry
	genClient := apiClient.GetGeneratedClient()
	resp, err := apiClient.DoWithAutoRefresh(ctx, 3, func() (*http.Response, error) {
		return genClient.AgentServiceUpdateAgent(ctx, agentID, updateBody)
	})
	if err != nil {
		return fmt.Errorf("update agent: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		return api.ParseError(resp)
	}

	// Parse response using generated type
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	var updateResponse generated.V1UpdateAgentResponse
	if err := json.Unmarshal(body, &updateResponse); err != nil {
		return fmt.Errorf("parse response: %w", err)
	}

	if updateResponse.Agent == nil {
		return fmt.Errorf("agent not found in response")
	}
	agent := updateResponse.Agent

	id := ""
	name := ""
	if agent.Id != nil {
		id = *agent.Id
	}
	if agent.Name != nil {
		name = *agent.Name
	}

	fmt.Printf("✓ Agent updated successfully!\n")
	fmt.Printf("Agent ID: %s\n", id)
	fmt.Printf("Agent Name: %s\n", name)

	return nil
}

func runAgentDelete(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	// DeleteAgent unpeers and unoffloads on the platform synchronously, so it needs the same
	// extended budget the deregister path uses; the default timeout aborts a delete mid-flight.
	apiClient, err := getAuthenticatedAPIClient(cmd, withAPITimeout(deleteAgentTimeout))
	if err != nil {
		return err
	}

	// Get agent ID
	agentID, err := getAgentID(ctx, cmd, apiClient)
	if err != nil {
		return err
	}

	// Get agent name for confirmation (optional, but helpful)
	agentName := ""
	name, _ := cmd.Flags().GetString("name")
	if name != "" {
		agentName = name
	} else {
		// Try to get name from API
		genClient := apiClient.GetGeneratedClient()
		resp, err := apiClient.DoWithAutoRefresh(ctx, 3, func() (*http.Response, error) {
			return genClient.AgentServiceGetAgent(ctx, agentID)
		})
		if err == nil && resp.StatusCode == http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			var getResponse generated.V1GetAgentResponse
			if err := json.Unmarshal(body, &getResponse); err == nil {
				if getResponse.Agent != nil && getResponse.Agent.Name != nil {
					agentName = *getResponse.Agent.Name
				}
			}
		}
	}

	// Confirm deletion (unless --force)
	force, _ := cmd.Flags().GetBool("force")
	if !force {
		confirmMsg := fmt.Sprintf("Are you sure you want to delete agent '%s' (ID: %s)?", agentName, agentID)
		if agentName == "" {
			confirmMsg = fmt.Sprintf("Are you sure you want to delete agent (ID: %s)?", agentID)
		}
		ok, err := confirmYesNo(confirmMsg, "re-run with --force to skip confirmation")
		if err != nil {
			return err
		}
		if !ok {
			fmt.Fprintln(os.Stderr, "Canceled.")
			return nil
		}
	}

	// Call API with auto-refresh and retry
	deleteCtx, cancel := context.WithTimeout(ctx, deleteAgentTimeout)
	defer cancel()

	genClient := apiClient.GetGeneratedClient()
	resp, err := apiClient.DoWithAutoRefresh(deleteCtx, deleteAgentRetries, func() (*http.Response, error) {
		return genClient.AgentServiceDeleteAgent(deleteCtx, agentID)
	})
	if err != nil {
		return fmt.Errorf("delete agent: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		return api.ParseError(resp)
	}

	if agentName != "" {
		fmt.Printf("%s Agent '%s' (ID: %s) deleted successfully!\n", markPass(), agentName, agentID)
	} else {
		fmt.Printf("%s Agent (ID: %s) deleted successfully!\n", markPass(), agentID)
	}
	fmt.Println("Note: This only removes the agent from the API. k3s is NOT uninstalled.")

	return nil
}
