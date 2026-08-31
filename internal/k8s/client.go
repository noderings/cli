package k8s

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// Client wraps Kubernetes client-go for type-safe operations
type Client struct {
	clientset kubernetes.Interface
	config    *rest.Config
}

// NewClient creates a new Kubernetes client
// If kubeconfigPath is empty, it tries standard locations:
// 1. ~/.kube/config
// 2. /etc/rancher/k3s/k3s.yaml (k3s default)
func NewClient(kubeconfigPath string) (*Client, error) {
	var config *rest.Config
	var err error

	if kubeconfigPath != "" {
		// Use provided kubeconfig path
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfigPath)
		if err != nil {
			return nil, fmt.Errorf("load kubeconfig from %s: %w", kubeconfigPath, err)
		}
	} else {
		// Try standard / nr-managed locations. Prefer paths we can actually open
		// (k3s.yaml is often root-owned mode 600).
		for _, candidate := range defaultKubeconfigCandidates() {
			if !fileExistsAndReadable(candidate) {
				continue
			}
			config, err = clientcmd.BuildConfigFromFlags("", candidate)
			if err == nil {
				return NewClientFromConfig(config)
			}
		}

		// Try in-cluster config (if running in a pod)
		config, err = rest.InClusterConfig()
		if err != nil {
			return nil, fmt.Errorf("could not find a readable kubeconfig (tried KUBECONFIG, ~/.nr/k3s.kubeconfig, ~/.kube/config, /etc/rancher/k3s/k3s.yaml) and not running in-cluster: %w", err)
		}
	}

	return NewClientFromConfig(config)
}

// NewClientFromConfig creates a client from rest.Config
func NewClientFromConfig(config *rest.Config) (*Client, error) {
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("create clientset: %w", err)
	}

	return &Client{
		clientset: clientset,
		config:    config,
	}, nil
}

func defaultKubeconfigCandidates() []string {
	out := make([]string, 0, 4)
	if kc := os.Getenv("KUBECONFIG"); kc != "" {
		out = append(out, kc)
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		out = append(out, filepath.Join(home, ".nr", "k3s.kubeconfig"))
		out = append(out, filepath.Join(home, ".kube", "config"))
	}
	out = append(out, "/etc/rancher/k3s/k3s.yaml")
	return out
}

func fileExistsAndReadable(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	_ = f.Close()
	return true
}

// GetClientset returns the underlying Kubernetes clientset
func (c *Client) GetClientset() kubernetes.Interface {
	return c.clientset
}

// GetConfig returns the underlying REST config
func (c *Client) GetConfig() *rest.Config {
	return c.config
}

// WaitForAPI waits for Kubernetes API to be ready
func (c *Client) WaitForAPI(ctx context.Context) error {
	timeout := 5 * time.Minute
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			// Try to get API version
			_, err := c.clientset.Discovery().ServerVersion()
			if err == nil {
				return nil
			}

			if time.Now().After(deadline) {
				return fmt.Errorf("timeout waiting for Kubernetes API to be ready: %w", err)
			}
		}
	}
}
