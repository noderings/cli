package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/noderings/cli/internal/api"
	generated "github.com/noderings/cli/internal/api/generated"
	"github.com/noderings/cli/internal/config"
	"github.com/noderings/cli/internal/install"
	"github.com/noderings/cli/internal/logger"
	"github.com/noderings/cli/internal/state"
)

const (
	inboundPeeringPollInterval = 5 * time.Second
	inboundPeeringPollTimeout  = 15 * time.Minute
)

func runInboundPeeringPhase(
	ctx context.Context,
	apiClient *api.Client,
	log *logger.Logger,
	stateManager *state.Manager,
	liqoManager *install.LiqoManager,
	agentID, configDir string,
	opts clusterRegisterOpts,
) error {
	log.Info("Starting inbound peering (provider → remote control plane)...")
	stateManager.SetPhase(state.PhaseInboundPeering)
	if err := stateManager.Save(); err != nil {
		log.Warnf("Failed to save state: %v", err)
	}

	detectedRemoteClusterID, detectErr := liqoManager.GetPeeredClusterID(ctx)
	neoClusterID, err := resolveRemoteClusterID(log, opts, detectedRemoteClusterID, detectErr)
	if err != nil {
		stateManager.SetError(state.PhaseInboundPeering, err.Error(), true)
		_ = stateManager.Save()
		return fmt.Errorf("resolve remote Liqo cluster ID for inbound peering-user: %w", err)
	}
	log.Infof("Generating peering-user for remote consumer cluster ID %s", neoClusterID)

	outPath := filepath.Join(configDir, fmt.Sprintf("inbound-peering-%s-kubeconfig.yaml", agentID))
	if err := liqoManager.GeneratePeeringUser(ctx, neoClusterID, outPath); err != nil {
		stateManager.SetError(state.PhaseInboundPeering, err.Error(), true)
		_ = stateManager.Save()
		return fmt.Errorf("generate inbound peering-user: %w", err)
	}

	kubeconfigBytes, err := os.ReadFile(outPath)
	if err != nil {
		stateManager.SetError(state.PhaseInboundPeering, err.Error(), true)
		_ = stateManager.Save()
		return fmt.Errorf("read inbound peering kubeconfig: %w", err)
	}
	kubeconfig := string(kubeconfigBytes)

	if err := uploadInboundPeeringConfig(ctx, apiClient, agentID, kubeconfig); err != nil {
		stateManager.SetError(state.PhaseInboundPeering, err.Error(), true)
		_ = stateManager.Save()
		return fmt.Errorf("upload inbound peering config: %w", err)
	}
	log.Info("Uploaded inbound peering kubeconfig; waiting for remote peer...")

	if err := waitForInboundPeeringReady(ctx, apiClient, log, agentID); err != nil {
		stateManager.SetError(state.PhaseInboundPeering, err.Error(), true)
		_ = stateManager.Save()
		return err
	}

	stateManager.AddCheckpoint(state.PhaseInboundPeering, state.CheckpointStatusSuccess, "")
	log.Info("✓ Inbound peering complete")
	return nil
}

func uploadInboundPeeringConfig(ctx context.Context, apiClient *api.Client, agentID, kubeconfig string) error {
	genClient := apiClient.GetGeneratedClient()
	body := generated.AgentServiceUploadInboundPeeringConfigJSONRequestBody{
		Kubeconfig: &kubeconfig,
	}
	resp, err := apiClient.DoWithAutoRefresh(ctx, 3, func() (*http.Response, error) {
		return genClient.AgentServiceUploadInboundPeeringConfig(ctx, agentID, body)
	})
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		return api.ParseError(resp)
	}
	return nil
}

func waitForInboundPeeringReady(ctx context.Context, apiClient *api.Client, log *logger.Logger, agentID string) error {
	deadline := time.Now().Add(inboundPeeringPollTimeout)
	ticker := time.NewTicker(inboundPeeringPollInterval)
	defer ticker.Stop()

	for {
		provisioned, stateName, errMsg, err := getAgentInboundStatus(ctx, apiClient, agentID)
		if err != nil {
			log.Warnf("Failed to poll agent inbound peering status: %v", err)
		} else {
			log.Infof("Inbound peering state: %s (provisioned=%v)", stateName, provisioned)
			if provisioned || strings.EqualFold(stateName, config.InboundPeeringStateReadyProto) || strings.EqualFold(stateName, config.InboundPeeringStateReadyShort) {
				return nil
			}
			if strings.Contains(strings.ToUpper(stateName), "FAILED") {
				if errMsg == "" {
					errMsg = "inbound peering failed on the remote control plane"
				}
				return fmt.Errorf("%s", errMsg)
			}
		}

		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for inbound peering after %s", inboundPeeringPollTimeout)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func getAgentInboundStatus(ctx context.Context, apiClient *api.Client, agentID string) (provisioned bool, stateName, errMsg string, err error) {
	genClient := apiClient.GetGeneratedClient()
	resp, err := apiClient.DoWithAutoRefresh(ctx, 3, func() (*http.Response, error) {
		return genClient.AgentServiceGetAgent(ctx, agentID)
	})
	if err != nil {
		return false, "", "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		return false, "", "", api.ParseError(resp)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, "", "", err
	}

	var parsed generated.V1GetAgentResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return false, "", "", fmt.Errorf("parse get agent response: %w", err)
	}
	if parsed.Agent == nil {
		return false, "", "", fmt.Errorf("agent missing in response")
	}
	if parsed.Agent.Provisioned != nil {
		provisioned = *parsed.Agent.Provisioned
	}
	if parsed.Agent.InboundPeeringState != nil {
		stateName = string(*parsed.Agent.InboundPeeringState)
	}
	if parsed.Agent.InboundPeeringError != nil {
		errMsg = *parsed.Agent.InboundPeeringError
	}
	return provisioned, stateName, errMsg, nil
}
