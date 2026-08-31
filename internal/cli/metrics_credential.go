package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/noderings/cli/internal/api"
	generated "github.com/noderings/cli/internal/api/generated"
	"github.com/noderings/cli/internal/logger"
)

const (
	maxAPIResponseBytes     = 1 << 20 // 1 MiB
	metricsCredNameDefault  = "default"
	metricsCredNamePrefix   = "agent-"
	metricsCredListPageSize = int32(100)
	// metricsAPIRetries bounds auth-refresh retries for metrics-credential API calls.
	metricsAPIRetries = 3

	metricsCredOpCreate = "create"
	metricsCredOpRotate = "rotate"
)

// issueMetricsWriteCredential returns a plaintext Mimir remote-write token for the agent.
// If an active credential already exists for the agent, it is rotated; otherwise a new one is created.
func issueMetricsWriteCredential(
	ctx context.Context,
	apiClient *api.Client,
	agentID string,
	log *logger.Logger,
) (string, error) {
	if apiClient == nil {
		return "", fmt.Errorf("api client is required")
	}

	aid := strings.TrimSpace(agentID)
	name := metricsCredNameDefault
	if aid != "" {
		name = metricsCredNamePrefix + aid
	}

	genClient := apiClient.GetGeneratedClient()
	if genClient == nil {
		return "", fmt.Errorf("generated API client is required")
	}

	if existingID, err := findActiveMetricsWriteCredentialID(ctx, apiClient, genClient, aid, name); err != nil {
		return "", err
	} else if existingID != "" {
		token, rotateErr := rotateMetricsWriteCredential(ctx, apiClient, genClient, existingID)
		if rotateErr != nil {
			return "", rotateErr
		}
		log.Infof("Rotated metrics write credential %q", name)
		return token, nil
	}

	token, createErr := createMetricsWriteCredential(ctx, apiClient, genClient, aid, name)
	if createErr != nil {
		return "", createErr
	}
	log.Infof("Created metrics write credential %q", name)
	return token, nil
}

func findActiveMetricsWriteCredentialID(
	ctx context.Context,
	apiClient *api.Client,
	genClient *generated.Client,
	agentID, name string,
) (string, error) {
	pageSize := metricsCredListPageSize
	params := &generated.MetricsCredentialServiceListMetricsWriteCredentialsParams{
		PageSize: &pageSize,
	}
	if agentID != "" {
		params.AgentId = &agentID
	}

	resp, err := apiClient.DoWithAutoRefresh(ctx, metricsAPIRetries, func() (*http.Response, error) {
		return genClient.MetricsCredentialServiceListMetricsWriteCredentials(ctx, params)
	})
	if err != nil {
		return "", fmt.Errorf("list metrics write credentials: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		return "", api.ParseError(resp)
	}

	body, err := readLimitedJSON(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read list metrics write credentials response: %w", err)
	}

	var parsed generated.V1ListMetricsWriteCredentialsResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("decode list metrics write credentials response: %w", err)
	}
	if parsed.Credentials == nil {
		return "", nil
	}

	var bestID string
	var bestTime time.Time
	for _, cred := range *parsed.Credentials {
		if cred.RevokedAt != nil {
			continue
		}
		if cred.Id == nil || strings.TrimSpace(*cred.Id) == "" {
			continue
		}
		// Prefer exact name match for this agent (or metricsCredNameDefault when unscoped).
		if cred.Name != nil && *cred.Name == name {
			return strings.TrimSpace(*cred.Id), nil
		}
		ct := time.Time{}
		if cred.CreateTime != nil {
			ct = *cred.CreateTime
		}
		if bestID == "" || ct.After(bestTime) {
			bestID = strings.TrimSpace(*cred.Id)
			bestTime = ct
		}
	}
	return bestID, nil
}

func rotateMetricsWriteCredential(
	ctx context.Context,
	apiClient *api.Client,
	genClient *generated.Client,
	id string,
) (string, error) {
	body := generated.MetricsCredentialServiceRotateMetricsWriteCredentialJSONRequestBody{}
	resp, err := apiClient.DoWithAutoRefresh(ctx, metricsAPIRetries, func() (*http.Response, error) {
		return genClient.MetricsCredentialServiceRotateMetricsWriteCredential(ctx, id, body)
	})
	if err != nil {
		return "", fmt.Errorf("rotate metrics write credential: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		return "", api.ParseError(resp)
	}
	return decodeMetricsWriteToken(resp.Body, metricsCredOpRotate)
}

func createMetricsWriteCredential(
	ctx context.Context,
	apiClient *api.Client,
	genClient *generated.Client,
	agentID, name string,
) (string, error) {
	body := generated.MetricsCredentialServiceCreateMetricsWriteCredentialJSONRequestBody{
		Name: &name,
	}
	if agentID != "" {
		body.AgentId = &agentID
	}

	resp, err := apiClient.DoWithAutoRefresh(ctx, metricsAPIRetries, func() (*http.Response, error) {
		return genClient.MetricsCredentialServiceCreateMetricsWriteCredential(ctx, body)
	})
	if err != nil {
		return "", fmt.Errorf("create metrics write credential: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		return "", api.ParseError(resp)
	}
	return decodeMetricsWriteToken(resp.Body, metricsCredOpCreate)
}

func decodeMetricsWriteToken(r io.Reader, op string) (string, error) {
	body, err := readLimitedJSON(r)
	if err != nil {
		return "", fmt.Errorf("read %s metrics write credential response: %w", op, err)
	}
	var parsed generated.V1CreateMetricsWriteCredentialResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("decode %s metrics write credential response: %w", op, err)
	}
	if parsed.Token == nil || strings.TrimSpace(*parsed.Token) == "" {
		return "", fmt.Errorf("%s metrics write credential: empty token in response", op)
	}
	return strings.TrimSpace(*parsed.Token), nil
}

func readLimitedJSON(r io.Reader) ([]byte, error) {
	return io.ReadAll(io.LimitReader(r, maxAPIResponseBytes))
}
