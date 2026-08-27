package install

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/noderings/cli/internal/config"
)

type hypervisorCRDChart struct {
	release string
	envKey  string
	sibling []string
	oci     string
	version string
}

func hypervisorCRDCharts() []hypervisorCRDChart {
	ver := getenvDefault("HYPERVISOR_OPERATOR_CRDS_CHART_VERSION", config.DefaultProxmoxOperatorChartVersion)
	return []hypervisorCRDChart{
		{
			release: "proxmox-operator-crds",
			envKey:  "PROXMOX_OPERATOR_CRDS_CHART",
			sibling: []string{
				filepath.Join("..", "operator", "charts", "proxmox-operator-crds"),
				filepath.Join("..", "..", "operator", "charts", "proxmox-operator-crds"),
			},
			oci:     config.DefaultProxmoxOperatorCRDsChartOCI,
			version: ver,
		},
		{
			release: "virtfusion-operator-crds",
			envKey:  "VIRTFUSION_OPERATOR_CRDS_CHART",
			sibling: []string{
				filepath.Join("..", "operator", "charts", "virtfusion-operator-crds"),
				filepath.Join("..", "..", "operator", "charts", "virtfusion-operator-crds"),
			},
			oci:     config.DefaultVirtFusionOperatorCRDsChartOCI,
			version: ver,
		},
		{
			release: "solusvm-operator-crds",
			envKey:  "SOLUSVM_OPERATOR_CRDS_CHART",
			sibling: []string{
				filepath.Join("..", "operator", "charts", "solusvm-operator-crds"),
				filepath.Join("..", "..", "operator", "charts", "solusvm-operator-crds"),
			},
			oci:     config.DefaultSolusVMOperatorCRDsChartOCI,
			version: ver,
		},
	}
}

// localOperatorChartEnvs are chart directories that imply an operator/charts layout.
// A VirtFusion-only checkout still ships both CRD charts next to the operator chart;
// Liqo on the provider must serve both API groups.
func localOperatorChartEnvs() []string {
	return []string{
		"PROXMOX_OPERATOR_CRDS_CHART",
		"VIRTFUSION_OPERATOR_CRDS_CHART",
		"SOLUSVM_OPERATOR_CRDS_CHART",
		"PROXMOX_OPERATOR_CHART",
		"VIRTFUSION_OPERATOR_CHART",
		"SOLUSVM_OPERATOR_CHART",
	}
}

func localChartParentDirs() []string {
	var parents []string
	seen := map[string]struct{}{}
	add := func(p string) {
		p = filepath.Clean(p)
		if p == "" || p == "." {
			return
		}
		if _, ok := seen[p]; ok {
			return
		}
		seen[p] = struct{}{}
		parents = append(parents, p)
	}
	for _, key := range localOperatorChartEnvs() {
		env := strings.TrimSpace(os.Getenv(key))
		if env == "" || strings.HasPrefix(env, "oci://") {
			continue
		}
		if isLocalHelmChart(env) {
			add(filepath.Dir(env))
			continue
		}
		if st, err := os.Stat(env); err == nil && st.IsDir() {
			add(env)
		}
	}
	return parents
}

func isLocalHelmChart(path string) bool {
	st, err := os.Stat(filepath.Join(path, "Chart.yaml"))
	return err == nil && !st.IsDir()
}

func resolveLocalCRDChart(c hypervisorCRDChart) string {
	if env := strings.TrimSpace(os.Getenv(c.envKey)); env != "" && !strings.HasPrefix(env, "oci://") {
		if isLocalHelmChart(env) {
			return env
		}
		if st, err := os.Stat(env); err == nil && !st.IsDir() {
			return env
		}
	}
	for _, parent := range localChartParentDirs() {
		p := filepath.Join(parent, c.release)
		if isLocalHelmChart(p) {
			return p
		}
	}
	for _, p := range c.sibling {
		if isLocalHelmChart(p) {
			return p
		}
	}
	return ""
}

func resolveCRDChart(c hypervisorCRDChart) (chart string, version string) {
	if env := strings.TrimSpace(os.Getenv(c.envKey)); env != "" && strings.HasPrefix(env, "oci://") {
		return env, c.version
	}
	if local := resolveLocalCRDChart(c); local != "" {
		return local, ""
	}
	if env := strings.TrimSpace(os.Getenv(c.envKey)); env != "" {
		return env, c.version
	}
	return c.oci, c.version
}

// EnsureHypervisorCRDs installs both Proxmox and VirtFusion CRD charts.
// Mothership Liqo AllowList always includes both API groups; missing CRDs on a
// provider stall custom-resource informers with "the server could not find the requested resource".
func EnsureHypervisorCRDs(ctx context.Context, kubeconfig string, logger Logger) error {
	for _, c := range hypervisorCRDCharts() {
		chart, version := resolveCRDChart(c)
		args := []string{
			"upgrade", "--install", c.release, chart,
			"--namespace", "kube-system",
			"--create-namespace",
		}
		if strings.HasPrefix(chart, "oci://") && version != "" {
			args = append(args, "--version", version)
		}
		if kubeconfig != "" {
			args = append(args, "--kubeconfig", kubeconfig)
		}
		logger.Infof("Ensuring CRDs (%s) via Helm %s...", c.release, chart)
		cmd := exec.CommandContext(ctx, "helm", args...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			hint := ""
			if strings.HasPrefix(chart, "oci://") {
				hint = fmt.Sprintf("\nset %s to a local chart dir, or place %s next to VIRTFUSION_OPERATOR_CHART / PROXMOX_OPERATOR_CHART / SOLUSVM_OPERATOR_CHART", c.envKey, c.release)
			}
			return fmt.Errorf("helm %s: %w\n%s%s", c.release, err, string(out), hint)
		}
	}
	return nil
}
