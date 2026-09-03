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

	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

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

	remotePeeringKubeconfig := filepath.Join(configDir, fmt.Sprintf("peering-%s-kubeconfig.yaml", agentID))
	kubeconfig, err = applyInboundAPIServerProxy(ctx, log, opts.inboundAPIProxy, kubeconfig,
		func(ctx context.Context) (string, error) {
			return liqoManager.ResolveInboundAPIServerProxyURL(ctx, remotePeeringKubeconfig, agentID)
		})
	if err != nil {
		stateManager.SetError(state.PhaseInboundPeering, err.Error(), true)
		_ = stateManager.Save()
		return err
	}
	if err := os.WriteFile(outPath, []byte(kubeconfig), 0o600); err != nil {
		stateManager.SetError(state.PhaseInboundPeering, err.Error(), true)
		_ = stateManager.Save()
		return fmt.Errorf("write inbound peering kubeconfig: %w", err)
	}

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

// applyInboundAPIServerProxy points the control plane at this cluster's api-server-proxy so it
// can peer back over the existing Liqo tunnel. Without it the control plane dials the API
// server address advertised at install time, which a NAT'd provider cannot expose.
//
// resolveProxyURL is injected so the decision logic stays independent of cluster access.
func applyInboundAPIServerProxy(
	ctx context.Context,
	log *logger.Logger,
	mode, kubeconfig string,
	resolveProxyURL func(context.Context) (string, error),
) (string, error) {
	if mode == config.InboundAPIProxyNever {
		return kubeconfig, nil
	}

	cfg, err := clientcmd.Load([]byte(kubeconfig))
	if err != nil {
		return "", fmt.Errorf("parse inbound peering kubeconfig: %w", err)
	}
	cluster, err := currentCluster(cfg)
	if err != nil {
		return "", err
	}

	if mode == config.InboundAPIProxyAuto && !install.APIServerNeedsProxy(cluster.Server) {
		log.Infof("API server %s is publicly routable; the control plane will peer back directly", cluster.Server)
		return kubeconfig, nil
	}

	proxyURL, err := resolveProxyURL(ctx)
	if err != nil {
		return "", fmt.Errorf("resolve API server proxy URL for inbound peering: %w", err)
	}

	// Only proxy-url is set: the Liqo proxy pins the CONNECT target to the in-cluster API
	// server, so server must keep the advertised address for TLS verification to succeed.
	cluster.ProxyURL = proxyURL

	out, err := clientcmd.Write(*cfg)
	if err != nil {
		return "", fmt.Errorf("serialize inbound peering kubeconfig: %w", err)
	}
	log.Infof("Control plane will reach API server %s through %s", cluster.Server, proxyURL)
	return string(out), nil
}

// currentCluster returns the cluster that the kubeconfig's current context points at.
func currentCluster(cfg *clientcmdapi.Config) (*clientcmdapi.Cluster, error) {
	kubeCtx, ok := cfg.Contexts[cfg.CurrentContext]
	if !ok || kubeCtx == nil {
		return nil, fmt.Errorf("kubeconfig has no context named %q", cfg.CurrentContext)
	}
	cluster, ok := cfg.Clusters[kubeCtx.Cluster]
	if !ok || cluster == nil {
		return nil, fmt.Errorf("kubeconfig context %q references unknown cluster %q", cfg.CurrentContext, kubeCtx.Cluster)
	}
	return cluster, nil
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
