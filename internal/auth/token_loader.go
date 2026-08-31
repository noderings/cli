package auth

import (
	"fmt"
	"os"
	"strings"

	"github.com/noderings/cli/internal/config"
)

// TokenSource represents where a token was loaded from
type TokenSource string

const (
	TokenSourceEnvVar     TokenSource = "environment_variable"
	TokenSourceConfigFile TokenSource = "config_file"
	TokenSourceOAuth      TokenSource = "oauth"
	TokenSourceUnknown    TokenSource = "unknown"
)

// TokenInfo contains token information and its source
type TokenInfo struct {
	Token        string
	Source       TokenSource
	IsOAuthToken bool   // true if this is an OAuth token with refresh capability
	OAuthToken   *Token // Only set if IsOAuthToken is true
}

// GetTokenFromEnv checks for token in environment variables
// Checks: NR_API_TOKEN, NODERINGS_API_TOKEN
func GetTokenFromEnv() (string, bool) {
	// Check common environment variable names (similar to AWS CLI, gcloud CLI)
	envVars := []string{
		"NR_API_TOKEN",
		"NODERINGS_API_TOKEN",
		"NR_TOKEN",
		"NODERINGS_TOKEN",
	}

	for _, envVar := range envVars {
		if token := os.Getenv(envVar); token != "" {
			// Remove any whitespace
			token = strings.TrimSpace(token)
			if token != "" {
				return token, true
			}
		}
	}

	return "", false
}

// GetTokenFromConfig checks for token in config file
func GetTokenFromConfig(cfg *config.Config) (string, bool) {
	// Check if token is set in config
	// Config path: auth.token
	if cfg.Auth.Token != "" {
		token := strings.TrimSpace(cfg.Auth.Token)
		if token != "" {
			return token, true
		}
	}

	return "", false
}

// LoadTokenInfo loads token from various sources in priority order:
// 1. Environment variables (highest priority) - for service account tokens
// 2. Config file - for service account tokens
// 3. OAuth token storage - for OAuth tokens (via 'nr auth login')
//
// Both OAuth and Service Account tokens are fully supported.
// The priority order determines which token is used when multiple are available.
func LoadTokenInfo(cfg *config.Config, configDir string) (*TokenInfo, error) {
	// Priority 1: Check environment variables (service account tokens)
	if token, found := GetTokenFromEnv(); found {
		return &TokenInfo{
			Token:        token,
			Source:       TokenSourceEnvVar,
			IsOAuthToken: false,
		}, nil
	}

	// Priority 2: Check config file (service account tokens)
	if token, found := GetTokenFromConfig(cfg); found {
		return &TokenInfo{
			Token:        token,
			Source:       TokenSourceConfigFile,
			IsOAuthToken: false,
		}, nil
	}

	// Priority 3: Check OAuth token storage (OAuth tokens from 'nr auth login')
	storage, err := NewStorage(configDir)
	if err != nil {
		return nil, fmt.Errorf("create storage: %w", err)
	}

	oauthToken, err := storage.LoadToken()
	if err != nil {
		return nil, fmt.Errorf("no token found in any source: %w", err)
	}

	return &TokenInfo{
		Token:        oauthToken.AccessToken,
		Source:       TokenSourceOAuth,
		IsOAuthToken: true,
		OAuthToken:   oauthToken,
	}, nil
}
