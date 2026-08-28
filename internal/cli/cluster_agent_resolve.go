package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/noderings/cli/internal/api"
	generated "github.com/noderings/cli/internal/api/generated"
	"github.com/noderings/cli/internal/logger"
)

// createOrResolveAgent looks up an agent by name before CreateAgent so plan-quota
// (429) is not hit when re-registering. When a name collision exists, prompts to
// reuse (or auto-reuses with --force/--yes).
func createOrResolveAgent(
	ctx context.Context,
	apiClient *api.Client,
	name, agentIP, gatewayRegion, description, hypervisorDriver string,
	force, yes bool,
	log *logger.Logger,
) (agentID string, reused bool, err error) {
	existing, findErr := findAgentByName(ctx, apiClient, name)
	if findErr != nil && !isAgentNotFound(findErr) {
		return "", false, fmt.Errorf("lookup agent by name: %w", findErr)
	}
	if existing != nil {
		return reuseExistingAgent(existing, name, force, yes, log)
	}

	agentID, reused, err = createAgent(ctx, apiClient, name, agentIP, gatewayRegion, description, hypervisorDriver)
	if err == nil {
		return agentID, reused, nil
	}

	// Race / entitlement ordering: CreateAgent may return 429 while the name already exists.
	if isQuotaOrConflictError(err) {
		existing, findErr = findAgentByName(ctx, apiClient, name)
		if findErr == nil && existing != nil {
			log.Warnf("CreateAgent failed (%v); found existing agent with the same name", err)
			return reuseExistingAgent(existing, name, force, yes, log)
		}
		return "", false, formatAgentCreateQuotaError(err, name)
	}
	return "", false, err
}

func reuseExistingAgent(
	existing *generated.V1Agent,
	name string,
	force, yes bool,
	log *logger.Logger,
) (string, bool, error) {
	if existing == nil || existing.Id == nil || *existing.Id == "" {
		return "", false, fmt.Errorf("existing agent %q has no id", name)
	}
	id := *existing.Id
	provisioned := existing.Provisioned != nil && *existing.Provisioned

	if force || yes {
		log.Infof("Reusing existing agent %q (id=%s, provisioned=%v)", name, id, provisioned)
		return id, true, nil
	}

	question := fmt.Sprintf(
		"Agent %q already exists (id=%s, provisioned=%v). Reuse it for this registration?",
		name, id, provisioned,
	)
	ok, err := confirmYesNo(question, "re-run with --yes to reuse without prompting")
	if err != nil {
		return "", false, fmt.Errorf("%w\nDelete first with: nr agent delete --name %s", err, name)
	}
	if !ok {
		return "", false, fmt.Errorf(
			"aborted: agent %q already exists\nDelete it first, then register again:\n  nr agent delete --name %s\n  # or: nr cluster deregister --name %s",
			name, name, name,
		)
	}
	log.Infof("Reusing existing agent %q (id=%s)", name, id)
	return id, true, nil
}

func formatAgentCreateQuotaError(err error, name string) error {
	return fmt.Errorf(
		"%w\nHint: your plan limit may already include an agent named %q. "+
			"Re-run with the same --name (add --yes to reuse non-interactively), pass --agent-id, or delete it first:\n"+
			"  nr agent delete --name %s",
		err, name, name,
	)
}

func isQuotaOrConflictError(err error) bool {
	// errors.As traverses fmt.Errorf wraps and retry-go's multi-error Unwrap() []error,
	// which a hand-rolled single-Unwrap loop cannot.
	var apiErr *api.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	return apiErr.IsAlreadyExists() || apiErr.IsQuotaExceeded()
}

// ErrAgentNotFound reports that no agent matched the requested name.
var ErrAgentNotFound = errors.New("agent not found")

func isAgentNotFound(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrAgentNotFound) {
		return true
	}
	var apiErr *api.APIError
	if errors.As(err, &apiErr) {
		// IsNotFound also covers a gRPC NOT_FOUND carried on a non-404 status.
		return apiErr.IsNotFound()
	}
	return false
}

// findAgentByName returns the first agent matching name, or nil if none.
func findAgentByName(ctx context.Context, client *api.Client, name string) (*generated.V1Agent, error) {
	genClient := client.GetGeneratedClient()
	pageSize := int32(-1)
	params := &generated.AgentServiceListAgentsParams{
		PageSize: &pageSize,
	}
	resp, err := client.DoWithAutoRefresh(ctx, 3, func() (*http.Response, error) {
		return genClient.AgentServiceListAgents(ctx, params)
	})
	if err != nil {
		return nil, fmt.Errorf("list agents: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, api.ParseError(resp)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var listResponse generated.V1ListAgentsResponse
	if err := json.Unmarshal(body, &listResponse); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	if listResponse.Agents == nil {
		return nil, fmt.Errorf("agent with name %q: %w", name, ErrAgentNotFound)
	}
	for _, agentResp := range *listResponse.Agents {
		if agentResp.Agent == nil || agentResp.Agent.Name == nil {
			continue
		}
		if *agentResp.Agent.Name == name {
			return agentResp.Agent, nil
		}
	}
	return nil, fmt.Errorf("agent with name %q: %w", name, ErrAgentNotFound)
}
