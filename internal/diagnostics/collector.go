package diagnostics

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/noderings/cli/internal/install"
	"github.com/noderings/cli/internal/k8s"
	"github.com/noderings/cli/internal/state"
)

// Collector collects diagnostic information about the local cluster.
type Collector struct {
	configDir string
	agentKey  string
}

// NewCollector creates a diagnostic collector for a cluster identity (name or id).
func NewCollector(configDir, agentKey string) *Collector {
	return &Collector{configDir: configDir, agentKey: agentKey}
}

// SystemInfo represents host system information.
type SystemInfo struct {
	OS     string `json:"os"`
	Kernel string `json:"kernel"`
	Arch   string `json:"arch"`
	CPUs   int    `json:"cpus"`
	GoOS   string `json:"go_os"`
}

// ClusterStatus is a summary used by `nr cluster status`.
type ClusterStatus struct {
	Phase          state.Phase `json:"phase"`
	AgentID        string      `json:"agent_id,omitempty"`
	AgentName      string      `json:"agent_name,omitempty"`
	APIReachable   bool        `json:"api_reachable"`
	K3sInstalled   bool        `json:"k3s_installed"`
	CalicoOK       bool        `json:"calico_ok"`
	LiqoOK         bool        `json:"liqo_ok"`
	PeeringSummary string      `json:"peering_summary,omitempty"`
	LastError      string      `json:"last_error,omitempty"`
}

// HealthCheck is one component health result.
type HealthCheck struct {
	Component string `json:"component"`
	Status    string `json:"status"` // pass|fail|warn
	Message   string `json:"message"`
}

// ClusterInfo holds metadata for `nr cluster info`.
type ClusterInfo struct {
	AgentID      string            `json:"agent_id,omitempty"`
	AgentName    string            `json:"agent_name,omitempty"`
	Phase        state.Phase       `json:"phase"`
	Versions     map[string]string `json:"versions,omitempty"`
	ClusterCIDR  string            `json:"cluster_cidr,omitempty"`
	ServiceCIDR  string            `json:"service_cidr,omitempty"`
	Kubeconfig   string            `json:"kubeconfig,omitempty"`
	StatePath    string            `json:"state_path,omitempty"`
	RegisteredAt time.Time         `json:"registered_at,omitempty"`
}

// CollectSystemInfo gathers OS/arch details.
func (c *Collector) CollectSystemInfo() (*SystemInfo, error) {
	info := &SystemInfo{
		OS:   runtime.GOOS,
		Arch: runtime.GOARCH,
		CPUs: runtime.NumCPU(),
		GoOS: runtime.GOOS,
	}
	if out, err := exec.Command("uname", "-r").Output(); err == nil {
		info.Kernel = strings.TrimSpace(string(out))
	}
	if runtime.GOOS == "linux" {
		if data, err := os.ReadFile("/etc/os-release"); err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				if strings.HasPrefix(line, "PRETTY_NAME=") {
					info.OS = strings.Trim(strings.TrimPrefix(line, "PRETTY_NAME="), `"`)
					break
				}
			}
		}
	}
	return info, nil
}

// CollectStatus builds cluster status from local state + live probes.
func (c *Collector) CollectStatus(ctx context.Context) (*ClusterStatus, error) {
	sm := state.NewManager(c.configDir, c.agentKey)
	_ = sm.Load()
	st := sm.GetState()

	k3sOK, calicoOK, liqoOK, apiOK := install.DetectInstalledComponents(ctx)
	status := &ClusterStatus{
		Phase:        st.Phase,
		AgentID:      st.AgentID,
		AgentName:    st.AgentName,
		APIReachable: apiOK,
		K3sInstalled: k3sOK,
		CalicoOK:     calicoOK,
		LiqoOK:       liqoOK,
	}
	if st.LastError != nil {
		status.LastError = st.LastError.Error
	}

	if apiOK {
		status.PeeringSummary = summarizePeering(ctx)
	}
	return status, nil
}

func summarizePeering(ctx context.Context) string {
	client, err := k8s.NewClient("")
	if err != nil {
		return "unavailable"
	}
	cs := client.GetClientset()
	list, err := cs.CoreV1().Namespaces().List(ctx, metav1.ListOptions{
		LabelSelector: "liqo.io/remote-cluster-id",
	})
	if err != nil {
		// Fallback: look for ForeignCluster via liqoctl if present.
		if _, lookErr := exec.LookPath("liqoctl"); lookErr == nil {
			cmd := exec.CommandContext(ctx, "liqoctl", "info", "peer")
			out, runErr := cmd.CombinedOutput()
			if runErr != nil {
				return "peering status unknown"
			}
			lines := strings.Split(strings.TrimSpace(string(out)), "\n")
			if len(lines) == 0 {
				return "no peers reported"
			}
			return fmt.Sprintf("%d peer line(s) from liqoctl", len(lines))
		}
		return "peering status unknown"
	}
	return fmt.Sprintf("%d remote namespace(s)", len(list.Items))
}

// CollectInfo builds cluster info from state and config hints.
func (c *Collector) CollectInfo(clusterCIDR, serviceCIDR string) (*ClusterInfo, error) {
	sm := state.NewManager(c.configDir, c.agentKey)
	_ = sm.Load()
	st := sm.GetState()
	return &ClusterInfo{
		AgentID:      st.AgentID,
		AgentName:    st.AgentName,
		Phase:        st.Phase,
		Versions:     st.Versions,
		ClusterCIDR:  clusterCIDR,
		ServiceCIDR:  serviceCIDR,
		Kubeconfig:   install.ResolveDefaultKubeconfig(),
		StatePath:    sm.StatePath(),
		RegisteredAt: st.RegistrationTime,
	}, nil
}

// CollectHealth runs component health checks.
func (c *Collector) CollectHealth(ctx context.Context) ([]HealthCheck, error) {
	var checks []HealthCheck

	k3sOK, calicoOK, liqoOK, apiOK := install.DetectInstalledComponents(ctx)
	checks = append(checks, HealthCheck{
		Component: "kubernetes-api",
		Status:    boolStatus(apiOK),
		Message:   boolMessage(apiOK, "API reachable", "API not reachable"),
	})
	checks = append(checks, HealthCheck{
		Component: "k3s",
		Status:    boolStatus(k3sOK),
		Message:   boolMessage(k3sOK, "k3s detected", "k3s not detected"),
	})
	checks = append(checks, HealthCheck{
		Component: "calico",
		Status:    boolStatus(calicoOK),
		Message:   boolMessage(calicoOK, "calico namespace present", "calico not detected"),
	})
	checks = append(checks, HealthCheck{
		Component: "liqo",
		Status:    boolStatus(liqoOK),
		Message:   boolMessage(liqoOK, "liqo namespace present", "liqo not detected"),
	})

	if apiOK {
		if err := checkDaemonSet(ctx, "calico-system", "calico-node"); err != nil {
			checks = append(checks, HealthCheck{Component: "calico-node", Status: "warn", Message: err.Error()})
		} else {
			checks = append(checks, HealthCheck{Component: "calico-node", Status: "pass", Message: "daemonset present"})
		}
	}

	return checks, nil
}

func checkDaemonSet(ctx context.Context, namespace, name string) error {
	client, err := k8s.NewClient("")
	if err != nil {
		return err
	}
	_, err = client.GetClientset().AppsV1().DaemonSets(namespace).Get(ctx, name, metav1.GetOptions{})
	return err
}

// CollectLogs gathers bounded recent logs from journalctl/k3s when available.
func (c *Collector) CollectLogs(ctx context.Context, lines int) (string, error) {
	if lines <= 0 {
		lines = 200
	}
	if lines > 2000 {
		lines = 2000
	}

	var b strings.Builder
	if _, err := exec.LookPath("journalctl"); err == nil {
		cmd := exec.CommandContext(ctx, "journalctl", "-u", "k3s", "-n", fmt.Sprintf("%d", lines), "--no-pager")
		out, err := cmd.CombinedOutput()
		b.WriteString("=== journalctl -u k3s ===\n")
		b.Write(out)
		if err != nil {
			fmt.Fprintf(&b, "\n(journalctl error: %v)\n", err)
		}
		b.WriteString("\n")
	}

	if path, err := exec.LookPath("k3s"); err == nil {
		cmd := exec.CommandContext(ctx, path, "kubectl", "get", "pods", "-A")
		out, runErr := cmd.CombinedOutput()
		b.WriteString("=== k3s kubectl get pods -A ===\n")
		b.Write(out)
		if runErr != nil {
			fmt.Fprintf(&b, "\n(k3s kubectl error: %v)\n", runErr)
		}
	}

	if b.Len() == 0 {
		return "", fmt.Errorf("no log sources available (journalctl/k3s)")
	}
	return b.String(), nil
}

// WriteDebugBundle writes a debug report under ~/.nr/debug-<timestamp>/.
func (c *Collector) WriteDebugBundle(ctx context.Context, clusterCIDR, serviceCIDR string) (string, error) {
	dir := filepath.Join(c.configDir, fmt.Sprintf("debug-%s", time.Now().Format("20060102-150405")))
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}

	sys, _ := c.CollectSystemInfo()
	status, _ := c.CollectStatus(ctx)
	info, _ := c.CollectInfo(clusterCIDR, serviceCIDR)
	health, _ := c.CollectHealth(ctx)
	logs, _ := c.CollectLogs(ctx, 200)

	writeJSON := func(name string, v any) error {
		data, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dir, name), data, 0600)
	}

	_ = writeJSON("system.json", sys)
	_ = writeJSON("status.json", status)
	_ = writeJSON("info.json", info)
	_ = writeJSON("health.json", health)
	if logs != "" {
		_ = os.WriteFile(filepath.Join(dir, "logs.txt"), []byte(logs), 0600)
	}

	sm := state.NewManager(c.configDir, c.agentKey)
	_ = sm.Load()
	if data, err := os.ReadFile(sm.StatePath()); err == nil {
		//nolint:gosec // G703: path is under a CLI-managed debug directory, not user input
		_ = os.WriteFile(filepath.Join(dir, "state.json"), data, 0600)
	}

	return dir, nil
}

// SummarizeHealth returns overall pass/fail from checks.
func SummarizeHealth(checks []HealthCheck) (allPass bool, failed int) {
	allPass = true
	for _, c := range checks {
		if c.Status == "fail" {
			allPass = false
			failed++
		}
	}
	return allPass, failed
}

func boolStatus(ok bool) string {
	if ok {
		return "pass"
	}
	return "fail"
}

func boolMessage(ok bool, passMsg, failMsg string) string {
	if ok {
		return passMsg
	}
	return failMsg
}
