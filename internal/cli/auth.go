package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/noderings/cli/internal/api"
	generated "github.com/noderings/cli/internal/api/generated"
	"github.com/noderings/cli/internal/auth"
	"github.com/noderings/cli/internal/config"
)

// authVerifyTimeout bounds the single probe request made by `nr auth status`.
const authVerifyTimeout = 15 * time.Second

var (
	authCmd = &cobra.Command{
		Use:   "auth",
		Short: "Authentication commands",
		Long:  "Manage authentication with NodeRings API",
	}

	authLoginCmd = &cobra.Command{
		Use:   "login",
		Short: "Authenticate with NodeRings API",
		Long: `Authenticate with NodeRings API using browser-based OAuth2 flow.
This command will open your browser for authentication.

On headless hosts (no browser), use a service account instead:
  nr auth set-token --token '<token>'
  # or: export NR_API_TOKEN=...
Create the token in the UI under Access Control → Service Accounts.`,
		RunE: runAuthLogin,
	}

	authStatusCmd = &cobra.Command{
		Use:   "status",
		Short: "Check authentication status",
		Long: `Check if you are currently authenticated and show token information.

By default this makes one authenticated request so the result reflects whether the
server accepts the token. A token can parse correctly on disk yet be revoked, deleted,
or issued against a different environment. Use --offline to inspect the local token only.`,
		RunE: runAuthStatus,
	}

	authRefreshCmd = &cobra.Command{
		Use:   "refresh",
		Short: "Refresh authentication token",
		Long:  "Refresh the current access token using the refresh token",
		RunE:  runAuthRefresh,
	}

	authLogoutCmd = &cobra.Command{
		Use:   "logout",
		Short: "Log out and remove stored tokens",
		Long:  "Remove stored authentication tokens and log out",
		RunE:  runAuthLogout,
	}

	authSetTokenCmd = &cobra.Command{
		Use:   "set-token",
		Short: "Set service account token in config file",
		Long: `Set a service account token in the config file (~/.nr/config.yaml).
This is useful for CI/CD pipelines or when you want to persist a token.

Alternatively, you can:
  - Set NR_API_TOKEN environment variable
  - Add 'auth.token' directly to ~/.nr/config.yaml`,
		RunE: runAuthSetToken,
	}
)

func init() {
	authCmd.AddCommand(authLoginCmd)
	authCmd.AddCommand(authStatusCmd)
	authCmd.AddCommand(authRefreshCmd)
	authCmd.AddCommand(authLogoutCmd)
	authCmd.AddCommand(authSetTokenCmd)

	// Add flags
	authLoginCmd.Flags().String("browser", "", "Specify browser to use (auto-detect by default)")
	authLoginCmd.Flags().Bool("no-browser", false, "Don't open browser, show URL for manual access")
	authLoginCmd.Flags().Bool("force", false, "Force re-authentication even if valid token exists")

	authSetTokenCmd.Flags().String("token", "", "Service account token to set (required)")
	authSetTokenCmd.Flags().Bool("from-env", false, "Read token from NR_API_TOKEN environment variable")

	authStatusCmd.Flags().String("output", config.OutputFormatText, "Output format: text|json")
	authStatusCmd.Flags().Bool("offline", false, "Inspect the local token only; do not verify it with the server")
}

func runAuthLogin(cmd *cobra.Command, args []string) error {
	// Get dev settings (--dev flag enables all dev features)
	debug, verbose, tlsInsecureFromDev := GetDevSettings(cmd)

	// Load configuration
	cfgLoader := config.NewLoader()
	cfg, err := cfgLoader.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// Get API URL using global helper (flag -> dev -> config)
	apiURL := GetAPIURL(cmd, cfg.API.BaseURL)

	// Get TLS insecure setting: --dev flag overrides config
	tlsInsecure := tlsInsecureFromDev
	if !tlsInsecure {
		tlsInsecure = cfg.API.TLSInsecure
	}

	// Get frontend URL: --dev flag uses hard-coded dev URL, otherwise use config
	frontendURL := cfg.Frontend.BaseURL
	if devFrontendURL := GetDevFrontendURL(cmd); devFrontendURL != "" {
		frontendURL = devFrontendURL
	}

	// Get config directory
	configDir, _ := cmd.Flags().GetString("config-dir")
	if configDir == "" {
		configDir = config.GetConfigDir()
	}

	// Check if already authenticated (unless --force)
	force, _ := cmd.Flags().GetBool("force")
	if !force {
		storage, err := auth.NewStorage(configDir)
		if err == nil {
			token, err := storage.LoadToken()
			if err == nil && !token.IsExpired() {
				fmt.Println("Already authenticated. Use --force to re-authenticate.")
				return nil
			}
		}
	}

	// Create OAuth client with fixed callback port
	oauthClient := auth.NewOAuthClient(&auth.OAuthConfig{
		ClientID:    "nr-cli",
		AuthURL:     frontendURL + "/oauth2/authorize",
		TokenURL:    apiURL + "/v1/oauth2/token",
		RedirectURL: "http://localhost:22222/callback", // Fixed port for callback server
		Scopes:      []string{"read", "write"},
		APIBaseURL:  apiURL,
		Debug:       debug || verbose,
		TLSInsecure: tlsInsecure,
	})

	// Initialize PKCE and state before starting callback server
	if err := oauthClient.InitAuthFlow(); err != nil {
		return fmt.Errorf("initialize auth flow: %w", err)
	}

	// Start auth flow
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle interrupt signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		cancel()
	}()

	// Start callback server first to get the actual port
	// This will update the redirect URL with the available port
	fmt.Println("Starting local callback server...")
	callbackCtx, callbackCancel := context.WithTimeout(ctx, 5*time.Minute)
	defer callbackCancel()

	// Start callback server in goroutine (it will find available port and update redirect URL)
	codeChan := make(chan string, 1)
	errChan := make(chan error, 1)

	go func() {
		code, err := oauthClient.StartCallbackServer(callbackCtx, oauthClient.GetState())
		if err != nil {
			errChan <- err
			return
		}
		codeChan <- code
	}()

	// Give server a moment to start and find available port
	time.Sleep(500 * time.Millisecond)

	// Now generate authorization URL with the updated redirect URL
	authURL, err := oauthClient.StartAuthFlow(ctx)
	if err != nil {
		return fmt.Errorf("start auth flow: %w", err)
	}

	// Give server a moment to start
	time.Sleep(500 * time.Millisecond)

	// Open browser (unless --no-browser). Always print the URL once so headless
	// hosts (VMs, CI) can copy it; avoid duplicating it inside OpenBrowser errors.
	noBrowser, _ := cmd.Flags().GetBool("no-browser")
	if !noBrowser {
		fmt.Println("Opening browser for authentication...")
		if err := auth.OpenBrowser(authURL); err != nil {
			fmt.Printf("Warning: Could not open browser automatically: %v\n", err)
			printServiceAccountAuthHint()
		}
	} else {
		printServiceAccountAuthHint()
	}
	fmt.Printf("If the browser did not open, visit:\n%s\n\n", authURL)

	fmt.Println("Waiting for authorization...")

	// Wait for authorization code
	select {
	case code := <-codeChan:
		// Exchange code for token
		fmt.Println("Authorization received. Exchanging code for token...")
		token, err := oauthClient.ExchangeCode(ctx, code)
		if err != nil {
			return fmt.Errorf("exchange code: %w", err)
		}

		// Save token
		storage, err := auth.NewStorage(configDir)
		if err != nil {
			return fmt.Errorf("create storage: %w", err)
		}

		if err := storage.SaveToken(token); err != nil {
			return fmt.Errorf("save token: %w", err)
		}

		fmt.Println("✓ Authentication successful! Token saved.")
		return nil

	case err := <-errChan:
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return fmt.Errorf("authorization timed out waiting for browser callback (use a service account token on headless hosts: nr auth set-token)")
		}
		return fmt.Errorf("callback error: %w", err)

	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("authorization timed out (use a service account token on headless hosts: nr auth set-token)")
		}
		return fmt.Errorf("authentication canceled")
	}
}

func printServiceAccountAuthHint() {
	fmt.Println("On headless hosts, prefer a service account token instead:")
	fmt.Println("  1. UI → Access Control → Service Accounts → create + generate token")
	fmt.Println("  2. nr auth set-token --token '<token>'")
	fmt.Println("  Or set NR_API_TOKEN in the environment.")
}

func runAuthStatus(cmd *cobra.Command, args []string) error {
	output, err := resolveOutputFlag(cmd)
	if err != nil {
		return err
	}

	// Load configuration
	cfgLoader := config.NewLoader()
	cfg, err := cfgLoader.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// Get config directory
	configDir, _ := cmd.Flags().GetString("config-dir")
	if configDir == "" {
		configDir = config.GetConfigDir()
	}

	// Load token info from all sources
	tokenInfo, err := auth.LoadTokenInfo(cfg, configDir)
	if err != nil {
		if output == config.OutputFormatJSON {
			return writeJSON(map[string]any{"authenticated": false})
		}
		fmt.Println("Not authenticated.")
		fmt.Println("Authentication options (all are fully supported):")
		fmt.Println("  Service Account Token:")
		fmt.Println("    1. Set NR_API_TOKEN environment variable")
		fmt.Println("    2. Add 'auth.token' to config file (~/.nr/config.yaml)")
		fmt.Println("    3. Run 'nr auth set-token' to save token to config")
		fmt.Println("  OAuth Token:")
		fmt.Println("    4. Run 'nr auth login' for interactive OAuth authentication")
		return nil
	}

	status := map[string]any{
		"authenticated": true,
		"source":        string(tokenInfo.Source),
	}

	// A token that parses locally can still be revoked, deleted, or issued against another
	// environment, so ask the server unless the caller opted out.
	offline, _ := cmd.Flags().GetBool("offline")
	var verifyErr error
	verified := false
	providerReviewPending := false
	if !offline {
		probe := probeAuthWithServer(cmd)
		verifyErr = probe.err
		providerReviewPending = probe.providerReviewPending
		verified = verifyErr == nil
		status["server_verified"] = verified
		if providerReviewPending {
			status["provider_review_pending"] = true
			status["provider_review_message"] = api.ProviderReviewPendingMessage
		}
		if verifyErr != nil {
			status["server_error"] = verifyErr.Error()
			// Only the server refusing the token disproves authentication; an unreachable
			// endpoint or a 5xx leaves it unknown rather than false.
			if isAuthRejection(verifyErr) {
				status["authenticated"] = false
			}
		}
	}

	if tokenInfo.IsOAuthToken && tokenInfo.OAuthToken != nil {
		token := tokenInfo.OAuthToken
		status["token_type"] = token.TokenType
		status["expires_at"] = token.ExpiresAt.Format(time.RFC3339)
		status["expired"] = token.IsExpired()
		if token.Scope != "" {
			status["scope"] = token.Scope
		}
		if !token.IsExpired() {
			status["time_until_expiry_seconds"] = int(time.Until(token.ExpiresAt).Seconds())
		}

		if output == config.OutputFormatJSON {
			return writeJSON(status)
		}

		printAuthHeadline(offline, verified, verifyErr)
		fmt.Printf("Token Source: %s\n", formatTokenSource(tokenInfo.Source))
		if token.IsExpired() {
			fmt.Println("Token expired. Run 'nr auth refresh' or 'nr auth login'.")
			return nil
		}
		fmt.Printf("Token Type: %s\n", token.TokenType)
		fmt.Printf("Expires At: %s\n", token.ExpiresAt.Format(time.RFC3339))
		if token.Scope != "" {
			fmt.Printf("Scopes: %s\n", token.Scope)
		}
		timeUntilExpiry := time.Until(token.ExpiresAt)
		if timeUntilExpiry > 0 {
			fmt.Printf("Time Until Expiry: %s\n", timeUntilExpiry.Round(time.Second))
		}
		if verifyErr != nil {
			printAuthVerifyFailure(verifyErr, true)
		}
		if providerReviewPending {
			printProviderReviewPendingNote()
		}
		return nil
	}

	status["token_type"] = "service_account_jwt"
	if output == config.OutputFormatJSON {
		return writeJSON(status)
	}

	printAuthHeadline(offline, verified, verifyErr)
	fmt.Printf("Token Source: %s\n", formatTokenSource(tokenInfo.Source))
	fmt.Println("Token Type: Service Account (JWT)")
	if verifyErr != nil {
		printAuthVerifyFailure(verifyErr, false)
		return nil
	}
	fmt.Println("Note: Service account tokens are long-lived and don't expire automatically.")
	fmt.Println("      If authentication fails, regenerate the token from the service account.")
	if providerReviewPending {
		printProviderReviewPendingNote()
	}
	return nil
}

// printAuthHeadline reports the token's standing, distinguishing "the server accepts this"
// from "this parses locally" so a revoked or foreign token is never shown as authenticated.
// A probe that never reached a verdict is reported as unknown, not as a rejection.
func printAuthHeadline(offline, verified bool, verifyErr error) {
	switch {
	case offline:
		fmt.Printf("Authentication Status: %s Token present (not verified — offline)\n", markWarn())
	case verified:
		fmt.Printf("Authentication Status: %s Authenticated (verified with server)\n", markPass())
	case isAuthRejection(verifyErr):
		fmt.Printf("Authentication Status: %s Token rejected by server\n", markFail())
	default:
		fmt.Printf("Authentication Status: %s Could not verify with server\n", markWarn())
	}
}

// isAuthRejection reports whether the server refused the token itself, as opposed to the probe
// failing for an unrelated reason such as an unreachable host, a timeout, or a 5xx.
func isAuthRejection(err error) bool {
	var apiErr *api.APIError
	return errors.As(err, &apiErr) && apiErr.IsUnauthorized()
}

// printAuthVerifyFailure explains why verification did not succeed, after the token details so
// the reader sees which token was tried before being told what to do about it. Remediation
// differs by credential kind: an OAuth session is renewed, a service account token is replaced.
func printAuthVerifyFailure(verifyErr error, oauth bool) {
	fmt.Printf("\nReason: %v\n", verifyErr)

	if !isAuthRejection(verifyErr) {
		fmt.Println("The server did not answer, so the token could not be checked. It may still be")
		fmt.Println("valid: confirm the API endpoint is reachable, then retry.")
		return
	}

	fmt.Println("The token parses locally but the server will not accept it.")
	if oauth {
		fmt.Println("The session was revoked or has expired. Start a new one:")
		fmt.Println("  nr auth refresh   # or, if that fails: nr auth login")
		return
	}
	fmt.Println("It may have been revoked or deleted, or issued against a different environment.")
	fmt.Println("Create a new one in the UI (Access Control → Service Accounts), then:")
	fmt.Println("  nr auth set-token --token '<token>'   # or export NR_API_TOKEN=...")
}

func printProviderReviewPendingNote() {
	fmt.Println(api.ProviderReviewPendingMessage)
	fmt.Println("Creating agents and registering a cluster stay blocked until the organization is approved.")
}

type authServerProbe struct {
	err                   error
	providerReviewPending bool
}

// probeAuthWithServer makes one authenticated request to establish whether the server
// accepts the token. ListOrganizations is allowlisted for unverified provider orgs
// (login/session bootstrap). ListAgents is not, so probing with it would treat a valid
// session as a failure while marketplace review is still pending.
//
// The call intentionally skips auto-refresh: status must report the token as it stands.
func probeAuthWithServer(cmd *cobra.Command) authServerProbe {
	apiClient, err := getAuthenticatedAPIClient(cmd, withAPITimeout(authVerifyTimeout), withoutOrganizationHeader())
	if err != nil {
		return authServerProbe{err: err}
	}

	ctx, cancel := context.WithTimeout(context.Background(), authVerifyTimeout)
	defer cancel()

	pageSize := int32(-1)
	resp, err := apiClient.GetGeneratedClient().OrganizationServiceListOrganizations(ctx,
		&generated.OrganizationServiceListOrganizationsParams{PageSize: &pageSize})
	if err != nil {
		return authServerProbe{err: err}
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		perr := api.ParseError(resp)
		if api.IsProviderReviewPending(perr) {
			return authServerProbe{providerReviewPending: true}
		}
		return authServerProbe{err: perr}
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return authServerProbe{err: err}
	}
	return authServerProbe{providerReviewPending: providerReviewPendingFromListOrgs(body)}
}

// formatTokenSource formats the token source for display
func formatTokenSource(source auth.TokenSource) string {
	switch source {
	case auth.TokenSourceEnvVar:
		return "Environment Variable (NR_API_TOKEN, NODERINGS_API_TOKEN, etc.)"
	case auth.TokenSourceConfigFile:
		return "Config File (~/.nr/config.yaml)"
	case auth.TokenSourceOAuth:
		return "OAuth Token Storage"
	default:
		return string(source)
	}
}

func runAuthRefresh(cmd *cobra.Command, args []string) error {
	// Load configuration
	cfgLoader := config.NewLoader()
	cfg, err := cfgLoader.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// Get API URL using global helper (flag -> dev -> config)
	apiURL := GetAPIURL(cmd, cfg.API.BaseURL)

	// Get dev settings (--dev flag enables all dev features)
	_, _, tlsInsecureFlag := GetDevSettings(cmd)

	// Get TLS insecure setting: --dev flag overrides config
	tlsInsecure := tlsInsecureFlag
	if !tlsInsecure {
		tlsInsecure = cfg.API.TLSInsecure
	}

	// Get config directory
	configDir, _ := cmd.Flags().GetString("config-dir")
	if configDir == "" {
		configDir = config.GetConfigDir()
	}

	// Load existing token
	storage, err := auth.NewStorage(configDir)
	if err != nil {
		return fmt.Errorf("create storage: %w", err)
	}

	token, err := storage.LoadToken()
	if err != nil {
		return fmt.Errorf("load token: %w - run 'nr auth login' first", err)
	}

	if token.RefreshToken == "" {
		return fmt.Errorf("no refresh token available - run 'nr auth login'")
	}

	// Create OAuth client
	oauthClient := auth.NewOAuthClient(&auth.OAuthConfig{
		ClientID:    "nr-cli",
		TokenURL:    apiURL + "/v1/oauth2/token",
		APIBaseURL:  apiURL,
		TLSInsecure: tlsInsecure,
	})

	// Refresh token
	ctx := context.Background()
	fmt.Println("Refreshing token...")
	newToken, err := oauthClient.RefreshToken(ctx, token.RefreshToken)
	if err != nil {
		return fmt.Errorf("refresh token: %w", err)
	}

	// Save new token
	if err := storage.SaveToken(newToken); err != nil {
		return fmt.Errorf("save token: %w", err)
	}

	fmt.Println("✓ Token refreshed successfully!")
	return nil
}

func runAuthLogout(cmd *cobra.Command, args []string) error {
	// Get config directory
	configDir, _ := cmd.Flags().GetString("config-dir")
	if configDir == "" {
		configDir = config.GetConfigDir()
	}

	// Load token to get access token for revocation
	storage, err := auth.NewStorage(configDir)
	if err != nil {
		return fmt.Errorf("create storage: %w", err)
	}

	token, err := storage.LoadToken()
	if err == nil && token.AccessToken != "" {
		// Try to revoke token (non-blocking)
		cfgLoader := config.NewLoader()
		cfg, _ := cfgLoader.Load()

		// Get API URL using global helper (flag -> dev -> config)
		apiURL := GetAPIURL(cmd, cfg.API.BaseURL)

		// Get dev settings (--dev flag enables TLS insecure and debug)
		debug, _, tlsInsecureFromDev := GetDevSettings(cmd)
		tlsInsecure := tlsInsecureFromDev
		if !tlsInsecure {
			tlsInsecure = cfg.API.TLSInsecure
		}

		oauthClient := auth.NewOAuthClient(&auth.OAuthConfig{
			ClientID:    "nr-cli",
			APIBaseURL:  apiURL,
			TLSInsecure: tlsInsecure,
			Debug:       debug,
		})

		// Revoke access token (best effort, don't fail if it errors)
		if err := oauthClient.RevokeToken(context.Background(), token.AccessToken, "access_token"); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to revoke access token: %v\n", err)
		}
		if token.RefreshToken != "" {
			if err := oauthClient.RevokeToken(context.Background(), token.RefreshToken, "refresh_token"); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to revoke refresh token: %v\n", err)
			}
		}
	}

	// Delete token from storage
	if err := storage.DeleteToken(); err != nil {
		return fmt.Errorf("delete token: %w", err)
	}

	fmt.Println("✓ Logged out successfully!")
	return nil
}

func runAuthSetToken(cmd *cobra.Command, args []string) error {
	// Get flags
	token, _ := cmd.Flags().GetString("token")
	fromEnv, _ := cmd.Flags().GetBool("from-env")

	// Get token value
	var tokenValue string
	if fromEnv {
		// Read from environment variable
		envToken, found := auth.GetTokenFromEnv()
		if !found {
			return fmt.Errorf("NR_API_TOKEN environment variable not set")
		}
		tokenValue = envToken
	} else {
		if token == "" {
			return RequiredFlagf("token", "or use --from-env to read from NR_API_TOKEN")
		}
		tokenValue = strings.TrimSpace(token)
		if tokenValue == "" {
			return fmt.Errorf("token cannot be empty")
		}
	}

	// Get config directory
	configDir, _ := cmd.Flags().GetString("config-dir")
	if configDir == "" {
		configDir = config.GetConfigDir()
	}

	// Load existing config file if it exists
	configPath := filepath.Join(configDir, "config.yaml")
	var existingConfig map[string]interface{}

	if data, err := os.ReadFile(configPath); err == nil {
		if err := yaml.Unmarshal(data, &existingConfig); err != nil {
			// If unmarshal fails, create new config
			existingConfig = make(map[string]interface{})
		}
	} else {
		// Config file doesn't exist, create new
		existingConfig = make(map[string]interface{})
	}

	// Update or create auth section
	if existingConfig == nil {
		existingConfig = make(map[string]interface{})
	}

	authSection, ok := existingConfig["auth"].(map[string]interface{})
	if !ok {
		authSection = make(map[string]interface{})
		existingConfig["auth"] = authSection
	}

	authSection["token"] = tokenValue

	// Ensure config directory exists
	if err := os.MkdirAll(configDir, 0700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}

	// Write config file
	data, err := yaml.Marshal(existingConfig)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	if err := os.WriteFile(configPath, data, 0600); err != nil {
		return fmt.Errorf("write config file: %w", err)
	}

	fmt.Printf("✓ Token saved to config file: %s\n", configPath)
	fmt.Println("The token will be used for authentication in future commands.")
	fmt.Println("Note: You can also set NR_API_TOKEN environment variable for higher priority.")

	return nil
}
