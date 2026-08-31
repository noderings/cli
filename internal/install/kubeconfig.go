package install

import (
	"context"
	"os"
	"path/filepath"

	"github.com/noderings/cli/internal/config"
)

const k3sDefaultKubeconfig = "/etc/rancher/k3s/k3s.yaml"

// EnsureReadableKubeconfig returns a kubeconfig path the current user can read.
// If preferred (or a discovered path) exists but is not readable (typical for
// root-owned /etc/rancher/k3s/k3s.yaml with mode 600), it copies the file via
// sudo into ~/.nr/k3s.kubeconfig (mode 0600) and returns that path.
//
// When /etc/rancher/k3s/k3s.yaml exists and is newer than the cached copy (e.g.
// after k3s reinstall), the cache is refreshed so TLS clients do not keep using
// a stale CA from a previous cluster.
func EnsureReadableKubeconfig(ctx context.Context, preferred string, logger Logger) string {
	if logger == nil {
		logger = noopLogger{}
	}

	if refreshed := refreshK3sKubeconfigCacheIfStale(ctx, logger); refreshed != "" {
		if preferred == "" || preferred == refreshed || preferred == k3sDefaultKubeconfig {
			return refreshed
		}
	}

	candidates := kubeconfigCandidates(preferred)
	for _, path := range candidates {
		if path == "" {
			continue
		}
		if _, err := os.Stat(path); err != nil {
			continue
		}
		if isReadableFile(path) {
			return path
		}
		if copied := copyKubeconfigWithSudo(ctx, path, logger); copied != "" {
			return copied
		}
	}
	return ""
}

// refreshK3sKubeconfigCacheIfStale re-copies k3s.yaml into ~/.nr/k3s.kubeconfig
// when the system file is newer than the cache (or the cache is missing).
func refreshK3sKubeconfigCacheIfStale(ctx context.Context, logger Logger) string {
	srcInfo, err := os.Stat(k3sDefaultKubeconfig)
	if err != nil {
		return ""
	}
	dest := cachedReadableKubeconfigPath()
	dstInfo, dstErr := os.Stat(dest)
	if dstErr == nil && !srcInfo.ModTime().After(dstInfo.ModTime()) {
		return ""
	}
	copied := copyKubeconfigWithSudo(ctx, k3sDefaultKubeconfig, logger)
	if copied != "" {
		logger.Infof("Refreshed kubeconfig cache from %s (k3s reinstall or first copy)", k3sDefaultKubeconfig)
	}
	return copied
}

// ResolveDefaultKubeconfig returns a best-effort kubeconfig path that is
// readable by the current user when possible.
func ResolveDefaultKubeconfig() string {
	return EnsureReadableKubeconfig(context.Background(), "", nil)
}

func kubeconfigCandidates(preferred string) []string {
	out := make([]string, 0, 5)
	if preferred != "" {
		out = append(out, preferred)
	}
	if kc := os.Getenv("KUBECONFIG"); kc != "" {
		out = append(out, kc)
	}
	// Prefer a previously sudo-copied user-readable cache before root-only k3s.yaml.
	out = append(out, cachedReadableKubeconfigPath())
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		out = append(out, filepath.Join(home, ".kube", "config"))
	}
	out = append(out, k3sDefaultKubeconfig)
	return out
}

func isReadableFile(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	_ = f.Close()
	return true
}

func cachedReadableKubeconfigPath() string {
	return filepath.Join(config.GetConfigDir(), "k3s.kubeconfig")
}

func copyKubeconfigWithSudo(ctx context.Context, source string, logger Logger) string {
	dest := cachedReadableKubeconfigPath()
	if err := os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
		logger.Debugf("Failed to create config dir for kubeconfig: %v", err)
		return ""
	}

	sudo, err := NewSudoManager(logger)
	if err != nil {
		logger.Debugf("Failed to initialize sudo for kubeconfig copy: %v", err)
		return ""
	}

	cmd := sudo.RunCommand(ctx, "cat", source)
	cmd.Stdin = sudo.PrepareStdin(nil)
	output, err := cmd.Output()
	if err != nil {
		logger.Debugf("Failed to read kubeconfig with sudo (%s): %v", source, err)
		return ""
	}
	if len(output) == 0 {
		logger.Debugf("Empty kubeconfig read from %s", source)
		return ""
	}

	if err := os.WriteFile(dest, output, 0600); err != nil {
		logger.Debugf("Failed to write readable kubeconfig to %s: %v", dest, err)
		return ""
	}

	logger.Debugf("Copied kubeconfig %s -> %s (user-readable)", source, dest)
	return dest
}

type noopLogger struct{}

func (noopLogger) Info(args ...any)                  {}
func (noopLogger) Infof(format string, args ...any)  {}
func (noopLogger) Warn(args ...any)                  {}
func (noopLogger) Warnf(format string, args ...any)  {}
func (noopLogger) Error(args ...any)                 {}
func (noopLogger) Errorf(format string, args ...any) {}
func (noopLogger) Debug(args ...any)                 {}
func (noopLogger) Debugf(format string, args ...any) {}

var _ Logger = noopLogger{}
