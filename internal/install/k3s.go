package install

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/noderings/cli/internal/k8s"
)

// Logger interface for install components
type Logger interface {
	Info(args ...any)
	Infof(format string, args ...any)
	Warn(args ...any)
	Warnf(format string, args ...any)
	Error(args ...any)
	Errorf(format string, args ...any)
	Debug(args ...any)
	Debugf(format string, args ...any)
}

// K3sInstaller handles k3s installation
type K3sInstaller struct {
	config               *K3sConfig
	logger               Logger
	k8sClient            *k8s.Client
	sudo                 *SudoManager
	actualKubeconfigPath string // The actual kubeconfig path used (may be temp file)
}

// K3sConfig holds k3s installation configuration
type K3sConfig struct {
	InstallScriptURL string
	KubeconfigMode   string
	ClusterCIDR      string
	ServiceCIDR      string
	FlannelBackend   string
	InstallDisables  string
	InstallChannel   string
	Version          string // Exact k3s version tag (INSTALL_K3S_VERSION)
	KubeconfigPath   string // Path to kubeconfig after installation
	NodeIP           string
	NodeExternalIP   string
}

// NewK3sInstaller creates a new k3s installer
func NewK3sInstaller(config *K3sConfig, log Logger) (*K3sInstaller, error) {
	sudo, err := NewSudoManager(log)
	if err != nil {
		return nil, fmt.Errorf("initialize sudo: %w", err)
	}

	return &K3sInstaller{
		config: config,
		logger: log,
		sudo:   sudo,
	}, nil
}

// Install installs k3s by downloading the script and executing it
func (k *K3sInstaller) Install(ctx context.Context) error {
	// Check if k3s is already installed
	if k.isInstalled() {
		k.logger.Info("k3s appears to be already installed, skipping installation")
		return nil
	}

	// Download k3s installation script
	k.logger.Info("Downloading k3s installation script...")

	scriptURL := k.config.InstallScriptURL
	if scriptURL == "" {
		scriptURL = "https://get.k3s.io"
	}

	script, err := k.downloadScript(ctx, scriptURL)
	if err != nil {
		return fmt.Errorf("download k3s script: %w", err)
	}

	// Execute script
	k.logger.Info("Installing k3s...")

	if err := k.executeScript(ctx, script); err != nil {
		return fmt.Errorf("execute k3s install script: %w", err)
	}

	// Wait for k3s API to be ready
	k.logger.Info("Waiting for k3s API to be ready...")

	if err := k.waitForAPI(ctx); err != nil {
		return fmt.Errorf("wait for k3s API: %w", err)
	}

	k.logger.Info("✓ k3s installed successfully")
	return nil
}

// downloadScript downloads the k3s installation script
func (k *K3sInstaller) downloadScript(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "NodeRings-CLI/1.0")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	// Read script content
	data, err := io.ReadAll(resp.Body)
	return data, err
}

// executeScript executes the k3s installation script
func (k *K3sInstaller) executeScript(ctx context.Context, script []byte) error {
	// Build environment variables
	env := os.Environ()
	if k.config.KubeconfigMode != "" {
		env = append(env, fmt.Sprintf("K3S_KUBECONFIG_MODE=%s", k.config.KubeconfigMode))
	}

	// Build INSTALL_K3S_EXEC with options
	var execArgs []string
	if k.config.ClusterCIDR != "" {
		execArgs = append(execArgs, fmt.Sprintf("--cluster-cidr=%s", k.config.ClusterCIDR))
	}
	if k.config.ServiceCIDR != "" {
		execArgs = append(execArgs, fmt.Sprintf("--service-cidr=%s", k.config.ServiceCIDR))
	}
	if k.config.FlannelBackend != "" {
		execArgs = append(execArgs, fmt.Sprintf("--flannel-backend=%s", k.config.FlannelBackend))
	}
	if k.config.InstallDisables != "" {
		execArgs = append(execArgs, k.config.InstallDisables)
	}
	if k.config.NodeIP != "" {
		execArgs = append(execArgs, fmt.Sprintf("--node-ip=%s", k.config.NodeIP))
	}
	if k.config.NodeExternalIP != "" {
		execArgs = append(execArgs, fmt.Sprintf("--node-external-ip=%s", k.config.NodeExternalIP))
	}
	if k.config.InstallChannel != "" {
		env = append(env, fmt.Sprintf("INSTALL_K3S_CHANNEL=%s", k.config.InstallChannel))
	}
	if k.config.Version != "" {
		env = append(env, fmt.Sprintf("INSTALL_K3S_VERSION=%s", k.config.Version))
	}

	if len(execArgs) > 0 {
		env = append(env, fmt.Sprintf("INSTALL_K3S_EXEC=%s", strings.Join(execArgs, " ")))
	}

	// Use sudo manager to run the script
	cmd := k.sudo.RunCommand(ctx, "sh", "-s", "-")
	cmd.Env = env
	cmd.Stdin = k.sudo.PrepareStdin(script)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("script execution failed: %w", err)
	}

	return nil
}

// isInstalled checks if k3s is already installed
func (k *K3sInstaller) isInstalled() bool {
	// Check if k3s binary exists
	if _, err := exec.LookPath("k3s"); err == nil {
		return true
	}

	// Check if k3s service is running (systemd)
	cmd := exec.Command("systemctl", "is-active", "--quiet", "k3s")
	if err := cmd.Run(); err == nil {
		return true
	}

	// Check if kubeconfig exists
	kubeconfigPath := k.config.KubeconfigPath
	if kubeconfigPath == "" {
		// Try standard locations
		home, _ := os.UserHomeDir()
		if home != "" {
			kubeconfigPath = filepath.Join(home, ".kube", "config")
		}
		if _, err := os.Stat(kubeconfigPath); err == nil {
			return true
		}
		// Try k3s default
		if _, err := os.Stat("/etc/rancher/k3s/k3s.yaml"); err == nil {
			return true
		}
	} else {
		if _, err := os.Stat(kubeconfigPath); err == nil {
			return true
		}
	}

	return false
}

// waitForAPI waits for k3s API to be ready
func (k *K3sInstaller) waitForAPI(ctx context.Context) error {
	kubeconfigPath := EnsureReadableKubeconfig(ctx, k.config.KubeconfigPath, k.logger)
	k.actualKubeconfigPath = kubeconfigPath

	// Create k8s client
	client, err := k8s.NewClient(kubeconfigPath)
	if err != nil {
		return fmt.Errorf("create k8s client: %w", err)
	}

	// Wait for API
	timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	if err := client.WaitForAPI(timeoutCtx); err != nil {
		return err
	}

	// Store client for later use
	k.k8sClient = client

	return nil
}

// GetK8sClient returns the Kubernetes client (after installation)
func (k *K3sInstaller) GetK8sClient() *k8s.Client {
	return k.k8sClient
}

// GetKubeconfigPath returns the kubeconfig path that was actually used.
// This may be ~/.nr/k3s.kubeconfig when sudo was needed to read k3s.yaml.
func (k *K3sInstaller) GetKubeconfigPath() string {
	if k.actualKubeconfigPath != "" {
		return k.actualKubeconfigPath
	}
	return k.config.KubeconfigPath
}
