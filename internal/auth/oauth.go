package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/noderings/cli/internal/config"
)

// OAuthClient handles OAuth2 authentication flows with PKCE
type OAuthClient struct {
	config     *OAuthConfig
	httpClient *http.Client
	state      string
	verifier   string
	challenge  string
	debug      bool
}

// GetState returns the current state value (for callback verification)
func (c *OAuthClient) GetState() string {
	return c.state
}

// OAuthConfig holds OAuth2 configuration
type OAuthConfig struct {
	ClientID    string
	AuthURL     string
	TokenURL    string
	RedirectURL string
	Scopes      []string
	APIBaseURL  string
	Debug       bool // Enable debug logging for troubleshooting
	TLSInsecure bool // Skip TLS certificate verification (development only)
}

// PKCEParams holds PKCE code verifier and challenge
type PKCEParams struct {
	CodeVerifier  string
	CodeChallenge string
	Method        string
}

// NewOAuthClient creates a new OAuth client
func NewOAuthClient(config *OAuthConfig) *OAuthClient {
	// Configure HTTP client with TLS settings
	httpClient := &http.Client{Timeout: 30 * time.Second}

	// If TLS insecure is enabled, configure transport to skip verification
	if config.TLSInsecure {
		transport := &http.Transport{
			TLSClientConfig: &tls.Config{
				//nolint:gosec // G402: TLSInsecure is an explicit opt-in for local/dev OAuth endpoints
				InsecureSkipVerify: true,
			},
		}
		httpClient.Transport = transport
	}

	return &OAuthClient{
		config:     config,
		httpClient: httpClient,
		debug:      config.Debug,
	}
}

// GeneratePKCE generates PKCE code verifier and challenge
func GeneratePKCE() (*PKCEParams, error) {
	// Generate 43-128 character random string for code verifier
	// Using 32 bytes (256 bits) gives us 43 characters when base64url encoded
	verifierBytes := make([]byte, 32)
	if _, err := rand.Read(verifierBytes); err != nil {
		return nil, fmt.Errorf("generate code verifier: %w", err)
	}

	// Base64URL encode (without padding)
	verifier := base64.RawURLEncoding.EncodeToString(verifierBytes)

	// Generate code challenge using S256 (SHA256)
	hash := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(hash[:])

	return &PKCEParams{
		CodeVerifier:  verifier,
		CodeChallenge: challenge,
		Method:        "S256",
	}, nil
}

// GenerateState generates a random state parameter for CSRF protection
func GenerateState() (string, error) {
	stateBytes := make([]byte, 16)
	if _, err := rand.Read(stateBytes); err != nil {
		return "", fmt.Errorf("generate state: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(stateBytes), nil
}

// InitAuthFlow initializes PKCE and state parameters (called before starting callback server)
func (c *OAuthClient) InitAuthFlow() error {
	// Generate PKCE parameters
	pkce, err := GeneratePKCE()
	if err != nil {
		return fmt.Errorf("generate PKCE: %w", err)
	}
	c.verifier = pkce.CodeVerifier
	c.challenge = pkce.CodeChallenge

	// Generate state for CSRF protection
	state, err := GenerateState()
	if err != nil {
		return fmt.Errorf("generate state: %w", err)
	}
	c.state = state

	return nil
}

// StartAuthFlow generates the authorization URL (called after callback server has updated redirect URL)
func (c *OAuthClient) StartAuthFlow(ctx context.Context) (string, error) {
	// Build authorization URL
	authURL, err := url.Parse(c.config.AuthURL)
	if err != nil {
		return "", fmt.Errorf("parse auth URL: %w", err)
	}

	query := authURL.Query()
	query.Set("client_id", c.config.ClientID)
	query.Set("response_type", "code")
	query.Set("redirect_uri", c.config.RedirectURL)
	query.Set("code_challenge", c.challenge)
	query.Set("code_challenge_method", "S256")
	query.Set("state", c.state)
	query.Set("scope", strings.Join(c.config.Scopes, " "))
	authURL.RawQuery = query.Encode()

	return authURL.String(), nil
}

// DefaultCallbackPort is the default port for the OAuth2 callback server.
const DefaultCallbackPort = config.DefaultOAuthCallbackPort

func localCallbackRedirectURL(rawRedirectURL string, port int) (string, error) {
	redirectURL, err := url.Parse(rawRedirectURL)
	if err != nil {
		return "", fmt.Errorf("parse redirect URL: %w", err)
	}

	redirectURL.Scheme = "http"
	redirectURL.Host = fmt.Sprintf("%s:%d", config.DefaultOAuthCallbackHost, port)
	if redirectURL.Path == "" {
		redirectURL.Path = "/callback"
	}

	return redirectURL.String(), nil
}

func writeCallbackPage(w http.ResponseWriter, statusCode int, title, headline, message, tone string) {
	accent := "#3b82f6"
	badgeBackground := "rgba(59, 130, 246, 0.14)"
	badgeBorder := "rgba(59, 130, 246, 0.28)"
	if tone == "error" {
		accent = "#ef4444"
		badgeBackground = "rgba(239, 68, 68, 0.14)"
		badgeBorder = "rgba(239, 68, 68, 0.28)"
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(statusCode)
	_, _ = fmt.Fprintf(w, `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>%s</title>
  <style>
    :root {
      color-scheme: dark;
      --background: #1f1e1e;
      --card: #222222;
      --card-border: rgba(255, 255, 255, 0.10);
      --foreground: #f5f5f5;
      --muted: #a3a3a3;
      --accent: %s;
      --badge-bg: %s;
      --badge-border: %s;
    }
    * { box-sizing: border-box; }
    body {
      margin: 0;
      min-height: 100vh;
      display: grid;
      place-items: center;
      padding: 32px;
      background:
        radial-gradient(circle at top left, rgba(59, 130, 246, 0.16), transparent 34rem),
        radial-gradient(circle at bottom right, rgba(255, 255, 255, 0.06), transparent 26rem),
        var(--background);
      color: var(--foreground);
      font-family: ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
    }
    main {
      width: min(100%%, 480px);
      border: 1px solid var(--card-border);
      border-radius: 22px;
      background: color-mix(in srgb, var(--card) 92%%, transparent);
      box-shadow: 0 24px 80px rgba(0, 0, 0, 0.35);
      padding: 28px;
    }
    .brand {
      display: inline-flex;
      align-items: center;
      gap: 10px;
      color: var(--muted);
      font-size: 14px;
      font-weight: 500;
      letter-spacing: 0.01em;
    }
    .mark {
      display: grid;
      place-items: center;
      width: 34px;
      height: 34px;
      border-radius: 10px;
      background: #f5f5f5;
      color: #171717;
      font-size: 12px;
      font-weight: 700;
    }
    .status {
      display: inline-flex;
      align-items: center;
      gap: 8px;
      margin-top: 32px;
      padding: 6px 10px;
      border: 1px solid var(--badge-border);
      border-radius: 999px;
      background: var(--badge-bg);
      color: var(--accent);
      font-size: 13px;
      font-weight: 600;
    }
    .dot {
      width: 8px;
      height: 8px;
      border-radius: 999px;
      background: var(--accent);
      box-shadow: 0 0 0 4px color-mix(in srgb, var(--accent) 18%%, transparent);
    }
    h1 {
      margin: 18px 0 10px;
      font-size: clamp(28px, 7vw, 42px);
      line-height: 1;
      letter-spacing: -0.04em;
    }
    p {
      margin: 0;
      color: var(--muted);
      font-size: 15px;
      line-height: 1.65;
    }
    .hint {
      margin-top: 24px;
      padding-top: 20px;
      border-top: 1px solid var(--card-border);
      color: #d4d4d4;
      font-size: 13px;
    }
  </style>
</head>
<body>
  <main>
    <div class="brand"><span class="mark">NR</span><span>NodeRings CLI</span></div>
    <div class="status"><span class="dot"></span><span>%s</span></div>
    <h1>%s</h1>
    <p>%s</p>
    <p class="hint">You can close this browser window and return to your terminal.</p>
  </main>
</body>
</html>`,
		html.EscapeString(title),
		accent,
		badgeBackground,
		badgeBorder,
		html.EscapeString(title),
		html.EscapeString(headline),
		html.EscapeString(message),
	)
}

// StartCallbackServer starts a local HTTP server to receive the authorization callback
// Uses a fixed port (DefaultCallbackPort) for simplicity and consistency
func (c *OAuthClient) StartCallbackServer(ctx context.Context, expectedState string) (string, error) {
	port := DefaultCallbackPort

	// The OAuth provider can use HTTPS, but the CLI verifier is a loopback HTTP server.
	redirectURL, err := localCallbackRedirectURL(c.config.RedirectURL, port)
	if err != nil {
		return "", err
	}
	c.config.RedirectURL = redirectURL

	// Create channel to receive authorization code
	codeChan := make(chan string, 1)
	stateChan := make(chan string, 1)
	errChan := make(chan error, 1)

	// Create HTTP server
	mux := http.NewServeMux()
	server := &http.Server{
		Addr:              fmt.Sprintf("%s:%d", config.DefaultOAuthCallbackHost, port),
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Handle callback
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		// Extract OAuth2 callback parameters
		code := r.URL.Query().Get("code")
		state := r.URL.Query().Get("state")
		errorParam := r.URL.Query().Get("error")
		errorDesc := r.URL.Query().Get("error_description")

		// Handle authorization errors
		if errorParam != "" {
			if c.debug {
				fmt.Fprintf(os.Stderr, "[DEBUG] Authorization error: %s - %s\n", errorParam, errorDesc)
			}
			errChan <- fmt.Errorf("authorization error: %s - %s", errorParam, errorDesc)
			writeCallbackPage(
				w,
				http.StatusBadRequest,
				"Authorization failed",
				"Authorization failed",
				fmt.Sprintf("%s: %s", errorParam, errorDesc),
				"error",
			)
			return
		}

		// Validate authorization code
		if code == "" {
			errChan <- fmt.Errorf("authorization code not provided")
			writeCallbackPage(
				w,
				http.StatusBadRequest,
				"Authorization failed",
				"Missing authorization code",
				"The callback did not include an authorization code.",
				"error",
			)
			return
		}

		// Verify state parameter for CSRF protection
		if state != expectedState {
			if c.debug {
				fmt.Fprintf(os.Stderr, "[DEBUG] State mismatch: expected %s, got %s\n", expectedState, state)
			}
			errChan <- fmt.Errorf("state mismatch: expected %s, got %s", expectedState, state)
			writeCallbackPage(
				w,
				http.StatusBadRequest,
				"Authorization failed",
				"State verification failed",
				"The callback state did not match the original login request.",
				"error",
			)
			return
		}

		// Send success response
		writeCallbackPage(
			w,
			http.StatusOK,
			"Authorization successful",
			"You're signed in",
			"NodeRings CLI received the authorization code and is finishing setup in your terminal.",
			"success",
		)

		// Send code and state to channels
		codeChan <- code
		stateChan <- state
	})

	// Start server in goroutine
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errChan <- fmt.Errorf("callback server error: %w", err)
		}
	}()

	// Wait for callback or context cancellation
	select {
	case code := <-codeChan:
		// Shutdown server gracefully
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
		return code, nil
	case err := <-errChan:
		_ = server.Shutdown(context.Background())
		return "", err
	case <-ctx.Done():
		_ = server.Shutdown(context.Background())
		return "", ctx.Err()
	}
}

// ExchangeCode exchanges authorization code for access token using PKCE
func (c *OAuthClient) ExchangeCode(ctx context.Context, code string) (*Token, error) {
	// Prepare form-encoded token request
	data := url.Values{}
	data.Set("grant_type", "authorization_code")
	data.Set("code", code)
	data.Set("redirect_uri", c.config.RedirectURL)
	data.Set("client_id", c.config.ClientID)
	data.Set("code_verifier", c.verifier)

	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, "POST", c.config.TokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	// Execute request
	resp, err := c.httpClient.Do(req)
	if err != nil {
		if c.debug {
			fmt.Fprintf(os.Stderr, "[DEBUG] Token request failed: %v\n", err)
		}
		return nil, fmt.Errorf("token request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	// Check for HTTP errors
	if resp.StatusCode != http.StatusOK {
		if c.debug {
			fmt.Fprintf(os.Stderr, "[DEBUG] Token exchange failed: status %d, body: %s\n", resp.StatusCode, string(body))
		}
		return nil, fmt.Errorf("token exchange failed: status %d, body: %s", resp.StatusCode, string(body))
	}

	// Parse token response
	var tokenResp struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
		TokenType    string `json:"token_type"`
		Scope        string `json:"scope"`
	}

	if err := json.Unmarshal(body, &tokenResp); err != nil {
		if c.debug {
			fmt.Fprintf(os.Stderr, "[DEBUG] Failed to parse token response: %v\n", err)
			fmt.Fprintf(os.Stderr, "[DEBUG] Response body: %s\n", string(body))
		}
		return nil, fmt.Errorf("parse token response: %w", err)
	}

	// Create token with expiration time
	expiresAt := time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
	token := &Token{
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		ExpiresAt:    expiresAt,
		TokenType:    tokenResp.TokenType,
		Scope:        tokenResp.Scope,
	}

	return token, nil
}

// RefreshToken exchanges refresh token for new access token
func (c *OAuthClient) RefreshToken(ctx context.Context, refreshToken string) (*Token, error) {
	// Prepare form data
	data := url.Values{}
	data.Set("grant_type", "refresh_token")
	data.Set("refresh_token", refreshToken)
	data.Set("client_id", c.config.ClientID)

	// Create request
	req, err := http.NewRequestWithContext(ctx, "POST", c.config.TokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	// Execute request
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("refresh request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Read response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		// Parse error response to provide better error messages
		var oauthErr struct {
			Error            string `json:"error"`
			ErrorDescription string `json:"error_description"`
		}
		if err := json.Unmarshal(body, &oauthErr); err == nil {
			if oauthErr.Error == "invalid_grant" {
				return nil, fmt.Errorf("refresh token is invalid or expired - please run 'nr auth login' to re-authenticate")
			}
			if oauthErr.ErrorDescription != "" {
				return nil, fmt.Errorf("token refresh failed: %s", oauthErr.ErrorDescription)
			}
		}
		return nil, fmt.Errorf("token refresh failed: status %d, body: %s", resp.StatusCode, string(body))
	}

	// Parse token response
	var tokenResp struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
		TokenType    string `json:"token_type"`
		Scope        string `json:"scope"`
	}

	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("parse token response: %w", err)
	}

	// Calculate expiration time
	expiresAt := time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)

	return &Token{
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken, // May be rotated
		ExpiresAt:    expiresAt,
		TokenType:    tokenResp.TokenType,
	}, nil
}

// RevokeToken revokes an access or refresh token
func (c *OAuthClient) RevokeToken(ctx context.Context, token string, tokenTypeHint string) error {
	var revokeURL string
	if c.config.TokenURL != "" {
		revokeURL = strings.TrimSuffix(c.config.TokenURL, "/token") + "/revoke"
		if !strings.HasSuffix(revokeURL, "/revoke") {
			revokeURL = c.config.APIBaseURL + "/v1/oauth2/revoke"
		}
	} else {
		// If TokenURL is not set, use APIBaseURL directly
		revokeURL = c.config.APIBaseURL + "/v1/oauth2/revoke"
	}

	if c.debug {
		fmt.Fprintf(os.Stderr, "DEBUG: Revoking token at %s (type: %s)\n", revokeURL, tokenTypeHint)
	}

	// Prepare form data
	data := url.Values{}
	data.Set("token", token)
	if tokenTypeHint != "" {
		data.Set("token_type_hint", tokenTypeHint)
	}
	data.Set("client_id", c.config.ClientID)

	// Create request
	req, err := http.NewRequestWithContext(ctx, "POST", revokeURL, strings.NewReader(data.Encode()))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	// Execute request
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("revoke request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("token revocation failed: status %d, body: %s", resp.StatusCode, string(body))
	}

	return nil
}

// Note: OpenBrowser is implemented in browser.go
