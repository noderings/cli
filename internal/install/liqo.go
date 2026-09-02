package install

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/noderings/cli/internal/config"
	"github.com/noderings/cli/internal/k8s"
)

// LiqoManager handles Liqo installation, peering, and namespace offloading
type LiqoManager struct {
	validator *Validator
	k8sClient *k8s.Client
	config    *LiqoConfig
	logger    Logger
}

// LiqoConfig holds Liqo configuration
type LiqoConfig struct {
	Version                     string
	Timeout                     string
	GWServiceType               string
	GWServerServiceLocation     string
	PodOffloadingStrategy       string
	PodCIDR                     string
	ServiceCIDR                 string
	ClusterID                   string // Cluster/Agent ID for liqoctl install
	KubeconfigPath              string // Path to kubeconfig file (optional, liqoctl will use default if not provided)
	ProxyURL                    string // Proxy URL for liqo peer command (optional)
	APIServerURL                string
	GWServerServiceNodePort     string
	GWClientAddress             string
	GWClientPort                string
	DisableAPIServerSanityCheck bool
	// ChartOCI is the Helm OCI chart reference without version (e.g. oci://harbor.noderings.com/nrings/liqo).
	ChartOCI string
	// ChartVersion is the Helm chart version (usually Version without a leading v).
	ChartVersion string
}

// NewLiqoManager creates a new Liqo manager
func NewLiqoManager(validator *Validator, k8sClient *k8s.Client, config *LiqoConfig, logger Logger) *LiqoManager {
	return &LiqoManager{
		validator: validator,
		k8sClient: k8sClient,
		config:    config,
		logger:    logger,
	}
}

// getKubeconfigPath determines a user-readable kubeconfig path for liqoctl.
func (l *LiqoManager) getKubeconfigPath() string {
	return EnsureReadableKubeconfig(context.Background(), l.config.KubeconfigPath, l.logger)
}

// Install installs Liqo using system liqoctl binary
func (l *LiqoManager) Install(ctx context.Context) error {
	// Validate liqoctl is available
	if err := l.validator.CheckTool(ctx, "liqoctl"); err != nil {
		return fmt.Errorf("liqoctl is required: %w", err)
	}

	// Check if Liqo is already installed
	if l.isInstalled(ctx) {
		l.logger.Info("Liqo appears to be already installed, skipping installation")
		return nil
	}

	// Execute liqoctl install k3s with proper flags
	l.logger.Info("Installing Liqo using liqoctl...")

	_, err := time.ParseDuration(l.config.Timeout)
	if err != nil {
		// Invalid timeout format, use default
		l.config.Timeout = "10m"
	}

	chartPath, cleanupChart, err := l.prepareLocalChart(ctx)
	if err != nil {
		return err
	}
	if cleanupChart != nil {
		defer cleanupChart()
	}

	valuesPath, err := writeProviderValuesFile()
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(valuesPath) }()

	args := []string{"install", "k3s", "--pod-cidr", l.config.PodCIDR, "--timeout", l.config.Timeout}
	if l.config.ServiceCIDR != "" {
		args = append(args, "--service-cidr", l.config.ServiceCIDR)
	}
	if l.config.Version != "" {
		args = append(args, "--version", l.config.Version)
	}
	if chartPath != "" {
		args = append(args, "--local-chart-path", chartPath)
		l.logger.Infof("Using local Liqo chart: %s", chartPath)
	}
	args = append(args, "--values", valuesPath)
	l.logger.Info("Applying provider Liqo values (gatewayMasqueradeBypass, custom CR reflection, externalCIDR 10.72.0.0/16)")
	if l.config.APIServerURL != "" {
		args = append(args, "--api-server-url", l.config.APIServerURL)
	}
	if l.config.DisableAPIServerSanityCheck {
		args = append(args, "--disable-api-server-sanity-check")
	}

	// Determine and add kubeconfig path
	kubeconfigPath := l.getKubeconfigPath()
	if kubeconfigPath != "" {
		args = append(args, "--kubeconfig", kubeconfigPath)
		l.logger.Debugf("Using kubeconfig: %s", kubeconfigPath)
	}

	if l.config.ClusterID == "" {
		return fmt.Errorf("liqo cluster ID is required for install")
	}
	args = append(args, "--cluster-id", l.config.ClusterID)

	cmd := exec.CommandContext(ctx, "liqoctl", args...)

	// Capture both stdout and stderr to see what went wrong
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		// Include the actual error output in the error message
		errorOutput := stderr.String()
		if errorOutput == "" {
			errorOutput = stdout.String()
		}
		if errorOutput != "" {
			return fmt.Errorf("liqoctl install failed: %w\nOutput: %s", err, errorOutput)
		}
		return fmt.Errorf("liqoctl install failed: %w", err)
	}

	// Log successful output if any
	if output := stdout.String(); output != "" {
		l.logger.Debugf("liqoctl install output: %s", output)
	}

	// Wait for Liqo to be ready
	l.logger.Info("Waiting for Liqo to be ready...")

	timeout, err := time.ParseDuration(l.config.Timeout)
	if err != nil {
		timeout = 10 * time.Minute // Default timeout
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Use liqoctl status as the primary verification method (recommended by Liqo docs)
	if err := l.waitForLiqoReady(timeoutCtx); err != nil {
		return fmt.Errorf("wait for Liqo to be ready: %w", err)
	}

	l.logger.Info("✓ Liqo installed successfully")
	return nil
}

// prepareLocalChart pulls the Harbor OCI chart (when configured) and returns an extracted chart path.
func (l *LiqoManager) prepareLocalChart(ctx context.Context) (string, func(), error) {
	chartOCI := strings.TrimSpace(l.config.ChartOCI)
	if chartOCI == "" {
		chartOCI = config.DefaultLiqoChartOCI
	}
	chartVersion := strings.TrimSpace(l.config.ChartVersion)
	if chartVersion == "" && l.config.Version != "" {
		chartVersion = strings.TrimPrefix(l.config.Version, "v")
	}
	if chartVersion == "" {
		chartVersion = config.DefaultLiqoChartVersion
	}

	tmpDir, err := os.MkdirTemp("", "nr-liqo-chart-*")
	if err != nil {
		return "", nil, fmt.Errorf("create chart temp dir: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(tmpDir) }

	l.logger.Infof("Pulling Liqo Helm chart %s:%s", chartOCI, chartVersion)
	cmd := exec.CommandContext(ctx, "helm", "pull", chartOCI, "--version", chartVersion, "-d", tmpDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		cleanup()
		return "", nil, fmt.Errorf("helm pull %s:%s: %w\n%s", chartOCI, chartVersion, err, strings.TrimSpace(string(out)))
	}

	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		cleanup()
		return "", nil, fmt.Errorf("list pulled chart dir: %w", err)
	}
	var tgz string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".tgz") {
			tgz = filepath.Join(tmpDir, e.Name())
			break
		}
	}
	if tgz == "" {
		cleanup()
		return "", nil, fmt.Errorf("helm pull produced no .tgz in %s", tmpDir)
	}

	extractDir := filepath.Join(tmpDir, "extracted")
	//nolint:gosec // G301: temp extract dir under os.TempDir; 0755 matches tar defaults
	if err := os.MkdirAll(extractDir, 0755); err != nil {
		cleanup()
		return "", nil, err
	}
	untar := exec.CommandContext(ctx, "tar", "-xzf", tgz, "-C", extractDir)
	if out, err := untar.CombinedOutput(); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("extract chart tarball: %w\n%s", err, strings.TrimSpace(string(out)))
	}

	chartPath := filepath.Join(extractDir, "liqo")
	if _, err := os.Stat(chartPath); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("extracted chart path %s: %w", chartPath, err)
	}
	return chartPath, cleanup, nil
}

// IsInstalled reports whether Liqo is present and has a real cluster ID.
func (l *LiqoManager) IsInstalled(ctx context.Context) bool {
	return l.isInstalled(ctx)
}

// isInstalled checks if Liqo is already installed
func (l *LiqoManager) isInstalled(ctx context.Context) bool {
	// Prefer liqoctl info since resource/deployment names can vary across Liqo versions.
	if l.isInstalledViaLiqoctl(ctx) {
		return true
	}

	clientset := l.k8sClient.GetClientset()

	// Check for liqo namespace (default namespace)
	_, err := clientset.CoreV1().Namespaces().Get(ctx, config.DefaultLiqoNamespace, metav1.GetOptions{})
	if err != nil {
		return false
	}

	// Fallback: verify at least one Liqo deployment is ready.
	deployments, err := clientset.AppsV1().Deployments(config.DefaultLiqoNamespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return false
	}

	for _, deployment := range deployments.Items {
		if deployment.Status.ReadyReplicas > 0 {
			return true
		}
	}

	return false
}

func (l *LiqoManager) isInstalledViaLiqoctl(ctx context.Context) bool {
	args := []string{"info", "-o", config.LiqoctlOutputJSON, "-n", config.DefaultLiqoNamespace}
	if kubeconfigPath := l.getKubeconfigPath(); kubeconfigPath != "" {
		args = append(args, "--kubeconfig", kubeconfigPath)
	}

	cmd := exec.CommandContext(ctx, "liqoctl", args...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		errorOutput := strings.TrimSpace(stderr.String())
		if errorOutput == "" {
			errorOutput = strings.TrimSpace(stdout.String())
		}
		if errorOutput != "" {
			l.logger.Debugf("liqoctl info install detection failed: %s", errorOutput)
		}
		return false
	}

	return liqoInfoIndicatesReady(stdout.String())
}

// waitForLiqoReady waits for Liqo components to be ready
// Uses liqoctl info as primary check with fallback to direct K8s checks
func (l *LiqoManager) waitForLiqoReady(ctx context.Context) error {
	// First, try using liqoctl info (official method to check Liqo status)
	if err := l.waitForLiqoInfo(ctx); err == nil {
		return nil
	}
	// If liqoctl info fails, fallback to direct Kubernetes checks
	l.logger.Debugf("liqoctl info check not available, using direct K8s checks")

	// Fallback to direct Kubernetes checks
	return l.waitForLiqoK8sReady(ctx)
}

// waitForLiqoInfo uses liqoctl info to verify Liqo is ready (official method)
// Uses JSON output format for programmatic parsing as per https://docs.liqo.io/en/v1.0.1/usage/liqoctl/liqoctl_info.html
func (l *LiqoManager) waitForLiqoInfo(ctx context.Context) error {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	// Determine kubeconfig path
	kubeconfigPath := l.getKubeconfigPath()
	args := []string{"info", "-o", config.LiqoctlOutputJSON, "-n", config.DefaultLiqoNamespace}
	if kubeconfigPath != "" {
		args = append(args, "--kubeconfig", kubeconfigPath)
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			cmd := exec.CommandContext(ctx, "liqoctl", args...)
			var stdout, stderr strings.Builder
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr

			err := cmd.Run()
			if err != nil {
				// Command failed, log and continue waiting
				errorOutput := stderr.String()
				if errorOutput == "" {
					errorOutput = stdout.String()
				}
				if errorOutput != "" {
					l.logger.Debugf("liqoctl info not ready yet: %s", errorOutput)
				}
				continue
			}

			// Parse JSON output to verify Liqo is actually ready
			output := stdout.String()
			if output == "" {
				l.logger.Debugf("liqoctl info returned empty output, continuing to wait")
				continue
			}

			if liqoInfoIndicatesReady(output) {
				return nil
			}

			// If JSON doesn't contain expected fields, continue waiting
			l.logger.Debugf("liqoctl info output doesn't indicate readiness yet")
		}
	}
}

func liqoInfoIndicatesReady(output string) bool {
	// Parse JSON to verify Liqo is responding with valid data.
	// Expected structure: {"health": {"healthy": true}, "local": {"clusterID": "...", ...}, ...}
	var infoData map[string]any
	if err := json.Unmarshal([]byte(output), &infoData); err != nil {
		return false
	}

	// Empty / rolled-back installs still report health.healthy=true with a blank
	// clusterID (only the liqo namespace remains). Require a real cluster ID.
	local, _ := infoData["local"].(map[string]any)
	clusterID, _ := local["clusterID"].(string)
	if strings.TrimSpace(clusterID) == "" {
		return false
	}

	if health, ok := infoData["health"].(map[string]any); ok {
		healthy, ok := health["healthy"].(bool)
		return ok && healthy
	}

	return true
}

// GetPeeredClusterID returns the remote Liqo cluster ID from the local agent cluster
// after peering. It uses liqoctl info peer on the local kubeconfig (not the restricted
// remote peering-user kubeconfig).
func (l *LiqoManager) GetPeeredClusterID(ctx context.Context) (string, error) {
	if err := l.validator.CheckTool(ctx, "liqoctl"); err != nil {
		return "", fmt.Errorf("liqoctl is required: %w", err)
	}

	localClusterID, err := l.getLocalClusterID(ctx)
	if err != nil {
		l.logger.Debugf("could not resolve local Liqo cluster ID: %v", err)
	}

	if clusterID, err := l.getPeeredClusterIDFromLiqoctl(ctx, localClusterID); err == nil && clusterID != "" {
		return clusterID, nil
	} else if err != nil {
		l.logger.Debugf("liqoctl info peer cluster ID detection failed: %v", err)
	}

	return l.getPeeredClusterIDFromForeignClusters(ctx, localClusterID)
}

func (l *LiqoManager) getLocalClusterID(ctx context.Context) (string, error) {
	args := []string{"info", "-o", config.LiqoctlOutputJSON, "-n", config.DefaultLiqoNamespace, "--get", "local.clusterID"}
	if kubeconfigPath := l.getKubeconfigPath(); kubeconfigPath != "" {
		args = append(args, "--kubeconfig", kubeconfigPath)
	}

	cmd := exec.CommandContext(ctx, "liqoctl", args...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		errorOutput := strings.TrimSpace(stderr.String())
		if errorOutput == "" {
			errorOutput = strings.TrimSpace(stdout.String())
		}
		if errorOutput != "" {
			return "", fmt.Errorf("liqoctl info --get local.clusterID failed: %s", errorOutput)
		}
		return "", fmt.Errorf("liqoctl info --get local.clusterID failed: %w", err)
	}

	clusterID := strings.TrimSpace(stdout.String())
	if clusterID == "" {
		return "", fmt.Errorf("liqoctl info returned empty local.clusterID")
	}
	return clusterID, nil
}

func (l *LiqoManager) getPeeredClusterIDFromLiqoctl(ctx context.Context, localClusterID string) (string, error) {
	args := []string{"info", "peer", "-o", config.LiqoctlOutputJSON, "-n", config.DefaultLiqoNamespace}
	if kubeconfigPath := l.getKubeconfigPath(); kubeconfigPath != "" {
		args = append(args, "--kubeconfig", kubeconfigPath)
	}

	cmd := exec.CommandContext(ctx, "liqoctl", args...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		errorOutput := strings.TrimSpace(stderr.String())
		if errorOutput == "" {
			errorOutput = strings.TrimSpace(stdout.String())
		}
		if errorOutput != "" {
			return "", fmt.Errorf("liqoctl info peer failed: %s", errorOutput)
		}
		return "", fmt.Errorf("liqoctl info peer failed: %w", err)
	}

	peerIDs, err := parsePeeredClusterIDsFromInfoPeerOutput(stdout.String(), localClusterID)
	if err != nil {
		return "", err
	}
	if len(peerIDs) == 0 {
		return "", fmt.Errorf("no peered clusters found in liqoctl info peer output")
	}
	if len(peerIDs) > 1 {
		return "", fmt.Errorf("multiple peered clusters found (%v); set --remote-cluster-id explicitly", peerIDs)
	}
	return peerIDs[0], nil
}

func parsePeeredClusterIDsFromInfoPeerOutput(output, localClusterID string) ([]string, error) {
	output = strings.TrimSpace(output)
	if output == "" {
		return nil, fmt.Errorf("liqoctl info peer returned empty output")
	}

	var infoData map[string]any
	if err := json.Unmarshal([]byte(output), &infoData); err != nil {
		return nil, fmt.Errorf("parse liqoctl info peer output: %w", err)
	}

	skipKeys := map[string]struct{}{
		"health": {}, "local": {}, "modules": {}, "network": {}, "peers": {},
	}

	peerIDs := make([]string, 0, len(infoData))
	for key, value := range infoData {
		if _, skip := skipKeys[key]; skip {
			continue
		}
		if key == localClusterID {
			continue
		}
		if _, ok := value.(map[string]any); !ok {
			continue
		}
		if strings.TrimSpace(key) == "" {
			continue
		}
		peerIDs = append(peerIDs, key)
	}

	// Some Liqo versions wrap peers under a top-level "peers" object.
	if peersSection, ok := infoData["peers"].(map[string]any); ok {
		for key := range peersSection {
			if key == localClusterID || strings.TrimSpace(key) == "" {
				continue
			}
			peerIDs = append(peerIDs, key)
		}
	}

	peerIDs = uniqueStrings(peerIDs)
	if len(peerIDs) == 0 {
		return nil, fmt.Errorf("no peer cluster IDs found in liqoctl info peer output")
	}
	return peerIDs, nil
}

func (l *LiqoManager) getPeeredClusterIDFromForeignClusters(ctx context.Context, localClusterID string) (string, error) {
	if err := l.validator.CheckTool(ctx, "kubectl"); err != nil {
		return "", fmt.Errorf("kubectl is required for ForeignCluster fallback: %w", err)
	}

	args := []string{
		"get", "foreignclusters.core.liqo.io",
		"-o", "jsonpath={.items[*].metadata.name}",
	}
	if kubeconfigPath := l.getKubeconfigPath(); kubeconfigPath != "" {
		args = append(args, "--kubeconfig", kubeconfigPath)
	}

	cmd := exec.CommandContext(ctx, "kubectl", args...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		errorOutput := strings.TrimSpace(stderr.String())
		if errorOutput == "" {
			errorOutput = strings.TrimSpace(stdout.String())
		}
		if errorOutput != "" {
			return "", fmt.Errorf("kubectl get foreignclusters failed: %s", errorOutput)
		}
		return "", fmt.Errorf("kubectl get foreignclusters failed: %w", err)
	}

	names := strings.Fields(strings.TrimSpace(stdout.String()))
	peerIDs := make([]string, 0, len(names))
	for _, name := range names {
		if name == localClusterID {
			continue
		}
		peerIDs = append(peerIDs, name)
	}
	peerIDs = uniqueStrings(peerIDs)

	if len(peerIDs) == 0 {
		return "", fmt.Errorf("no ForeignCluster resources found")
	}
	if len(peerIDs) > 1 {
		return "", fmt.Errorf("multiple ForeignCluster resources found (%v); set --remote-cluster-id explicitly", peerIDs)
	}
	return peerIDs[0], nil
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

// waitForLiqoK8sReady performs direct Kubernetes checks as fallback
// Checks only the liqo namespace (default namespace)
func (l *LiqoManager) waitForLiqoK8sReady(ctx context.Context) error {
	clientset := l.k8sClient.GetClientset()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	// Track if we've seen the namespace at least once
	liqoSeen := false

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			// Check if liqo namespace exists (default namespace)
			_, err := clientset.CoreV1().Namespaces().Get(ctx, config.DefaultLiqoNamespace, metav1.GetOptions{})
			if err != nil {
				if !liqoSeen {
					// Namespace doesn't exist yet, keep waiting
					continue
				}
				// Namespace existed but now doesn't - this is unexpected
				return fmt.Errorf("liqo namespace disappeared: %w", err)
			}
			liqoSeen = true

			// Check all deployments in liqo namespace
			deployments, err := clientset.AppsV1().Deployments(config.DefaultLiqoNamespace).List(ctx, metav1.ListOptions{})
			if err != nil {
				l.logger.Debugf("Waiting for liqo deployments: %v", err)
				continue
			}

			if len(deployments.Items) == 0 {
				l.logger.Debugf("No deployments found in liqo namespace yet")
				continue
			}

			// Check if all deployments are ready
			allReady := true
			for _, deployment := range deployments.Items {
				readyReplicas := deployment.Status.ReadyReplicas
				desiredReplicas := int32(0)
				if deployment.Spec.Replicas != nil {
					desiredReplicas = *deployment.Spec.Replicas
				}

				l.logger.Debugf("Liqo deployment %s status: %d/%d replicas ready", deployment.Name, readyReplicas, desiredReplicas)

				if readyReplicas == 0 || readyReplicas != desiredReplicas {
					allReady = false
					break
				}
			}

			if allReady {
				// Also verify configmaps/services exist in liqo namespace
				configmaps, err := clientset.CoreV1().ConfigMaps(config.DefaultLiqoNamespace).List(ctx, metav1.ListOptions{})
				if err == nil && len(configmaps.Items) > 0 {
					services, err := clientset.CoreV1().Services(config.DefaultLiqoNamespace).List(ctx, metav1.ListOptions{})
					if err == nil && len(services.Items) > 0 {
						l.logger.Info("✓ Liqo is ready (all deployments ready, configmaps and services present)")
						return nil
					}
				}
				// If configmaps/services check fails, still consider ready if deployments are ready
				l.logger.Info("✓ Liqo deployments are ready")
				return nil
			}
		}
	}
}

// Peer configures and peers clusters using liqoctl.
// gwServerServicePort is the agent-allocated gateway port: NodePort value when
// service type is NodePort, otherwise --gw-server-service-port for LoadBalancer.
// Optional gw-client-* overrides come only from LiqoConfig (NAT edge cases);
// test-k3d.md-style peer does not set them — the client discovers the NodePort.
func (l *LiqoManager) Peer(ctx context.Context, kubeconfigPath string, gwServerServicePort string) error {
	return l.peer(ctx, kubeconfigPath, gwServerServicePort, true)
}

func (l *LiqoManager) PeerAfterReset(ctx context.Context, kubeconfigPath string, gwServerServicePort string) error {
	return l.peer(ctx, kubeconfigPath, gwServerServicePort, false)
}

func (l *LiqoManager) peer(ctx context.Context, kubeconfigPath string, gwServerServicePort string, resetBeforePeer bool) error {
	// Validate liqoctl is available
	if err := l.validator.CheckTool(ctx, "liqoctl"); err != nil {
		return fmt.Errorf("liqoctl is required: %w", err)
	}

	if complete, err := l.IsPeeringComplete(ctx); err != nil {
		l.logger.Warnf("Could not determine peering status: %v", err)
	} else if complete {
		l.logger.Info("Liqo peering already complete, skipping liqoctl peer")
		return nil
	}

	if l.hasPartialPeeringState(ctx) {
		if resetBeforePeer {
			l.ResetPartialPeering(ctx, kubeconfigPath)
		}
	}

	// Execute liqoctl peer with the provided configuration
	l.logger.Info("Configuring Liqo peering...")

	args := []string{
		"peer",
		"--remote-kubeconfig", kubeconfigPath,
		"--gw-server-service-type", l.config.GWServiceType,
		"--timeout", l.config.Timeout,
	}
	if l.config.GWServerServiceLocation != "" {
		args = append(args, "--gw-server-service-location", l.config.GWServerServiceLocation)
	}

	isNodePort := strings.EqualFold(l.config.GWServiceType, config.GWServiceTypeNodePort)
	if isNodePort {
		// Match liqo/test-k3d.md: only --gw-server-service-nodeport (not --gw-server-service-port).
		nodePort := l.config.GWServerServiceNodePort
		if nodePort == "" || nodePort == "0" {
			nodePort = gwServerServicePort
		}
		if nodePort != "" && nodePort != "0" {
			args = append(args, "--gw-server-service-nodeport", nodePort)
		}
	} else if gwServerServicePort != "" && gwServerServicePort != "0" {
		args = append(args, "--gw-server-service-port", gwServerServicePort)
	}

	// Optional --gw-client-*: set from config/env only (never invented by --dev).
	if l.config.GWClientAddress != "" {
		l.logger.Infof("Using WireGuard client endpoint override %s", l.config.GWClientAddress)
		args = append(args, "--gw-client-address", l.config.GWClientAddress)
	}
	if l.config.GWClientPort != "" && l.config.GWClientPort != "0" {
		args = append(args, "--gw-client-port", l.config.GWClientPort)
	} else if l.config.GWClientAddress != "" && isNodePort {
		// Pair address override with the NodePort Liqo actually exposes.
		nodePort := l.config.GWServerServiceNodePort
		if nodePort == "" || nodePort == "0" {
			nodePort = gwServerServicePort
		}
		if nodePort != "" && nodePort != "0" {
			args = append(args, "--gw-client-port", nodePort)
		}
	}

	// Add proxy URL if configured
	if l.config.ProxyURL != "" {
		args = append(args, "--proxy-url", l.config.ProxyURL)
	}

	if kubeconfigPathLocal := l.getKubeconfigPath(); kubeconfigPathLocal != "" {
		args = append(args, "--kubeconfig", kubeconfigPathLocal)
	}

	if err := l.runLiqoctl(ctx, args...); err != nil {
		return fmt.Errorf("liqoctl peer failed: %w", err)
	}

	l.logger.Info("✓ Liqo peering configured successfully")
	return nil
}

// OffloadNamespace offloads a namespace to peered clusters
// CLI manages the entire offloading operation
func (l *LiqoManager) OffloadNamespace(ctx context.Context, ns, remoteNamespaceName, namespaceMappingStrategy, selector string) error {
	// Validate liqoctl is available
	if err := l.validator.CheckTool(ctx, "liqoctl"); err != nil {
		return fmt.Errorf("liqoctl is required: %w", err)
	}

	// Ensure namespace exists
	clientset := l.k8sClient.GetClientset()
	_, err := clientset.CoreV1().Namespaces().Get(ctx, ns, metav1.GetOptions{})
	if err != nil {
		// Try to create namespace
		_, createErr := clientset.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: ns,
			},
		}, metav1.CreateOptions{})
		if createErr != nil {
			return fmt.Errorf("namespace %s does not exist and could not be created: %w", ns, createErr)
		}
		l.logger.Infof("Created namespace %s", ns)
	}

	// Execute liqoctl offload with proper flags
	l.logger.Infof("Offloading namespace %s...", ns)

	args := []string{
		"offload", "namespace", ns,
		"--namespace-mapping-strategy", namespaceMappingStrategy,
		"--timeout", l.config.Timeout,
	}
	if remoteNamespaceName != "" {
		args = append(args, "--remote-namespace-name", remoteNamespaceName)
	}
	if selector != "" {
		args = append(args, "--selector", selector)
	}
	if l.config.PodOffloadingStrategy != "" {
		args = append(args, "--pod-offloading-strategy", l.config.PodOffloadingStrategy)
	}

	if kubeconfigPath := l.getKubeconfigPath(); kubeconfigPath != "" {
		args = append(args, "--kubeconfig", kubeconfigPath)
	}

	if err := l.runLiqoctl(ctx, args...); err != nil {
		return fmt.Errorf("liqoctl offload failed: %w", err)
	}

	l.logger.Infof("✓ Namespace %s offloaded successfully", ns)
	return nil
}

// GeneratePeeringUser runs liqoctl generate peering-user and writes the kubeconfig to outPath.
func (l *LiqoManager) GeneratePeeringUser(ctx context.Context, consumerClusterID, outPath string) error {
	if err := l.validator.CheckTool(ctx, "liqoctl"); err != nil {
		return fmt.Errorf("liqoctl is required: %w", err)
	}
	consumerClusterID = strings.TrimSpace(consumerClusterID)
	if consumerClusterID == "" {
		return fmt.Errorf("consumer cluster ID is required")
	}
	if strings.TrimSpace(outPath) == "" {
		return fmt.Errorf("output path is required")
	}

	l.logger.Infof("Generating Liqo peering-user for consumer cluster %s...", consumerClusterID)
	args := []string{"generate", "peering-user", "--consumer-cluster-id", consumerClusterID}
	if kubeconfigPath := l.getKubeconfigPath(); kubeconfigPath != "" {
		args = append(args, "--kubeconfig", kubeconfigPath)
	}

	cmd := exec.CommandContext(ctx, "liqoctl", args...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		out := stderr.String()
		if out == "" {
			out = stdout.String()
		}
		return fmt.Errorf("liqoctl generate peering-user failed: %w\nOutput: %s", err, out)
	}
	kubeconfig := strings.TrimSpace(stdout.String())
	if kubeconfig == "" {
		return fmt.Errorf("liqoctl generate peering-user returned empty kubeconfig")
	}
	if err := os.WriteFile(outPath, []byte(kubeconfig+"\n"), 0o600); err != nil {
		return fmt.Errorf("write peering-user kubeconfig: %w", err)
	}
	l.logger.Infof("✓ Wrote inbound peering-user kubeconfig to %s", outPath)
	return nil
}
