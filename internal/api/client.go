package api

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/avast/retry-go/v5"

	generated "github.com/noderings/cli/internal/api/generated"
	cfgpkg "github.com/noderings/cli/internal/config"
)

// TokenRefreshFunc is a function that refreshes an expired token and returns the new token
type TokenRefreshFunc func(ctx context.Context) (string, error)

// refreshWaitTimeout bounds how long a request waits for another goroutine's token refresh.
const refreshWaitTimeout = 30 * time.Second

// OrganizationIDHeader is the gateway tenant header. OAuth sessions bind to the
// user's home org (often a client org); provider APIs require this header.
const OrganizationIDHeader = "X-Organization-ID"

// Client is the API client wrapper for the platform API
// It wraps the generated oapi-codegen client with token authentication, retry logic, and error handling
type Client struct {
	generated      *generated.Client
	config         *Config
	tokenMu        sync.RWMutex // guards token and organizationID; request editors read them per request
	token          string
	organizationID string
	refreshFunc    TokenRefreshFunc
	refreshMutex   chan struct{} // Mutex to prevent concurrent refresh attempts
}

// Config holds API client configuration
type Config struct {
	BaseURL     string
	Timeout     time.Duration
	TLSInsecure bool
	CACertPath  string
}

// NewClient creates a new API client with the generated oapi-codegen client
func NewClient(config *Config) (*Client, error) {
	// Build server URL
	serverURL := config.BaseURL
	if serverURL == "" {
		serverURL = cfgpkg.DefaultAPIBaseURL
	}
	if !hasScheme(serverURL) {
		serverURL = "https://" + serverURL
	}

	tlsConfig, err := buildTLSConfig(config.TLSInsecure, config.CACertPath)
	if err != nil {
		return nil, err
	}

	transport := &http.Transport{}
	if tlsConfig != nil {
		transport.TLSClientConfig = tlsConfig
	}

	httpClient := &http.Client{
		Timeout:   config.Timeout,
		Transport: transport,
	}

	// Create generated API client
	apiClient, err := generated.NewClient(serverURL, generated.WithHTTPClient(httpClient))
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	return &Client{
		generated:    apiClient,
		config:       config,
		refreshMutex: make(chan struct{}, 1), // Buffered channel for mutex
	}, nil
}

// SetToken sets the authentication token used by every request.
// The request editor is installed once and reads the current token per request,
// so refreshing a token never mutates the editor slice while requests are in flight.
func (c *Client) SetToken(token string) {
	c.tokenMu.Lock()
	c.token = token
	c.tokenMu.Unlock()

	if len(c.generated.RequestEditors) == 0 {
		c.generated.RequestEditors = []generated.RequestEditorFn{
			func(ctx context.Context, req *http.Request) error {
				if current := c.GetToken(); current != "" {
					req.Header.Set("Authorization", "Bearer "+current)
				}
				if orgID := c.GetOrganizationID(); orgID != "" {
					req.Header.Set(OrganizationIDHeader, orgID)
				}
				return nil
			},
		}
	}
}

// SetOrganizationID sets the tenant sent as X-Organization-ID on every request.
func (c *Client) SetOrganizationID(id string) {
	c.tokenMu.Lock()
	c.organizationID = strings.TrimSpace(id)
	c.tokenMu.Unlock()
}

// GetOrganizationID returns the tenant UUID sent as X-Organization-ID, if any.
func (c *Client) GetOrganizationID() string {
	c.tokenMu.RLock()
	defer c.tokenMu.RUnlock()
	return c.organizationID
}

// SetTokenRefreshFunc sets the function to call when token needs to be refreshed
func (c *Client) SetTokenRefreshFunc(fn TokenRefreshFunc) {
	c.refreshFunc = fn
}

// GetToken returns the current authentication token
func (c *Client) GetToken() string {
	c.tokenMu.RLock()
	defer c.tokenMu.RUnlock()
	return c.token
}

// GetGeneratedClient returns the underlying generated API client (for advanced usage)
func (c *Client) GetGeneratedClient() *generated.Client {
	return c.generated
}

// GetBaseURL returns the base URL of the API client
func (c *Client) GetBaseURL() string {
	return c.config.BaseURL
}

// WithRetry wraps an API call with retry logic using retry-go library
// It retries on network errors and 5xx status codes with exponential backoff
func (c *Client) WithRetry(ctx context.Context, maxRetries uint, fn func() error) error {
	return retry.New(
		retry.Context(ctx),
		retry.Attempts(maxRetries),
		retry.Delay(100*time.Millisecond),
		retry.DelayType(retry.BackOffDelay),
		retry.MaxDelay(5*time.Second),
		retry.RetryIf(isRetryableError),
		retry.OnRetry(func(n uint, err error) {
			// Optional: log retry attempts
			// This can be enhanced with structured logging later
		}),
	).Do(fn)
}

// isRetryableError checks if an error should be retried
func isRetryableError(err error) bool {
	// Don't retry if error is marked as unrecoverable
	if !retry.IsRecoverable(err) {
		return false
	}

	// Network errors (timeouts, refused connections, resets, truncated responses) are retryable
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ECONNRESET) {
		return true
	}

	// Check for API errors with retryable status codes
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		// Never retry plan/quota exhaustion (also mapped to HTTP 429).
		if apiErr.IsQuotaExceeded() {
			return false
		}
		// Retry on 5xx errors and transient 429 (rate limit)
		return apiErr.StatusCode >= 500 || apiErr.StatusCode == http.StatusTooManyRequests
	}

	// Default: don't retry
	return false
}

// handleUnauthorizedError converts a 401 response into an unrecoverable error
func (c *Client) handleUnauthorizedError(resp *http.Response) error {
	apiErr := ParseError(resp)
	// Use IsUnauthorized to check error type reliably
	if apiErr, ok := apiErr.(*APIError); ok && apiErr.IsUnauthorized() {
		return retry.Unrecoverable(fmt.Errorf("authentication failed: %s - please run 'nr auth login' to re-authenticate", apiErr.Message))
	}
	return retry.Unrecoverable(apiErr)
}

// waitForRefresh blocks until a concurrent token refresh releases the lock, the context is
// done, or refreshWaitTimeout elapses.
func (c *Client) waitForRefresh(ctx context.Context) error {
	timer := time.NewTimer(refreshWaitTimeout)
	defer timer.Stop()

	select {
	case c.refreshMutex <- struct{}{}:
		<-c.refreshMutex
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return fmt.Errorf("timed out waiting for concurrent token refresh")
	}
}

// evaluateResponse maps a response onto a retryable error, an unrecoverable error, or nil.
// 401 handling is the caller's responsibility (it may trigger a token refresh first).
func (c *Client) evaluateResponse(resp *http.Response) error {
	if resp.StatusCode < 400 {
		return nil
	}
	apiErr := ParseError(resp)
	if isRetryableError(apiErr) {
		_ = resp.Body.Close()
		return apiErr
	}
	return retry.Unrecoverable(apiErr)
}

// DoWithAutoRefresh executes an API call function and automatically refreshes token on 401
// It wraps the call with retry logic and handles 401 by refreshing the token once
func (c *Client) DoWithAutoRefresh(ctx context.Context, maxRetries uint, fn func() (*http.Response, error)) (*http.Response, error) {
	var resp *http.Response
	var err error
	refreshed := false

	// Execute with retry logic
	err = c.WithRetry(ctx, maxRetries, func() error {
		// Make the API call
		var callErr error
		resp, callErr = fn()
		if callErr != nil {
			return callErr
		}

		// Check for 401 Unauthorized - try to refresh token once
		if resp.StatusCode == http.StatusUnauthorized && !refreshed && c.refreshFunc != nil {
			// Acquire mutex to prevent concurrent refresh attempts
			select {
			case c.refreshMutex <- struct{}{}:
				// Got the lock, proceed with refresh
				defer func() { <-c.refreshMutex }()

				// Close the response body before refreshing
				_ = resp.Body.Close()

				// Refresh token
				newToken, refreshErr := c.refreshFunc(ctx)
				if refreshErr != nil {
					// If refresh fails, mark as unrecoverable (don't retry)
					// This typically means refresh token is invalid/expired
					// The error message from refreshFunc already includes re-authentication instructions
					return retry.Unrecoverable(fmt.Errorf("token refresh failed: %w", refreshErr))
				}

				// Update token in client
				c.SetToken(newToken)
				refreshed = true

				// Retry the original request with new token
				resp, callErr = fn()
				if callErr != nil {
					return callErr
				}

				// If still 401 after refresh, return error (unrecoverable)
				if resp.StatusCode == http.StatusUnauthorized {
					return c.handleUnauthorizedError(resp)
				}

				// The retried call can still fail for unrelated reasons (5xx, 403, ...);
				// those must surface as errors rather than a successful-looking response.
				return c.evaluateResponse(resp)
			default:
				// Another refresh is in progress: wait for it to finish (bounded) instead of
				// racing ahead with the old token after a fixed sleep.
				_ = resp.Body.Close()
				if err := c.waitForRefresh(ctx); err != nil {
					return retry.Unrecoverable(err)
				}
				resp, callErr = fn()
				if callErr != nil {
					return callErr
				}
				if resp.StatusCode == http.StatusUnauthorized {
					return c.handleUnauthorizedError(resp)
				}
				return c.evaluateResponse(resp)
			}
		}

		// Don't retry 401 if we already tried refreshing
		if resp.StatusCode == http.StatusUnauthorized && refreshed {
			return retry.Unrecoverable(ParseError(resp))
		}

		return c.evaluateResponse(resp)
	})

	if err != nil {
		// If we have a response, close it
		if resp != nil {
			_ = resp.Body.Close()
		}
		return nil, err
	}

	return resp, nil
}

// hasScheme checks if a URL has a scheme (http:// or https://)
func hasScheme(url string) bool {
	return strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://")
}

func buildTLSConfig(insecure bool, caCertPath string) (*tls.Config, error) {
	if insecure {
		//nolint:gosec // G402: TLSInsecure is an explicit opt-in for local/dev endpoints
		return &tls.Config{InsecureSkipVerify: true}, nil
	}
	if caCertPath == "" {
		return nil, nil
	}

	pemData, err := os.ReadFile(caCertPath)
	if err != nil {
		return nil, fmt.Errorf("read CA certificate %s: %w", caCertPath, err)
	}

	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if !pool.AppendCertsFromPEM(pemData) {
		return nil, fmt.Errorf("failed to parse CA certificate from %s", caCertPath)
	}

	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		RootCAs:    pool,
	}, nil
}
