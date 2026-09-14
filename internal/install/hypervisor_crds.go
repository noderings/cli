package install

import (
	"bytes"
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

func crdChartVersion(def string) string {
	if v := strings.TrimSpace(os.Getenv(config.EnvHypervisorOperatorCRDsChartVersion)); v != "" {
		return v
	}
	return def
}

func hypervisorCRDCharts() []hypervisorCRDChart {
	return []hypervisorCRDChart{
		{
			release: helmReleaseProxmoxCRDs,
			envKey:  config.EnvProxmoxOperatorCRDsChart,
			sibling: operatorChartSiblings(helmReleaseProxmoxCRDs),
			oci:     config.DefaultProxmoxOperatorCRDsChartOCI,
			version: crdChartVersion(config.DefaultProxmoxOperatorChartVersion),
		},
		{
			release: helmReleaseVirtFusionCRDs,
			envKey:  config.EnvVirtFusionOperatorCRDsChart,
			sibling: operatorChartSiblings(helmReleaseVirtFusionCRDs),
			oci:     config.DefaultVirtFusionOperatorCRDsChartOCI,
			version: crdChartVersion(config.DefaultVirtFusionOperatorChartVersion),
		},
		{
			release: helmReleaseSolusVMCRDs,
			envKey:  config.EnvSolusVMOperatorCRDsChart,
			sibling: operatorChartSiblings(helmReleaseSolusVMCRDs),
			oci:     config.DefaultSolusVMOperatorCRDsChartOCI,
			version: crdChartVersion(config.DefaultSolusVMOperatorChartVersion),
		},
	}
}

// localOperatorChartEnvs are chart directories that imply an operator/charts layout.
// A VirtFusion-only checkout still ships both CRD charts next to the operator chart;
// Liqo on the provider must serve both API groups.
func localOperatorChartEnvs() []string {
	return []string{
		config.EnvProxmoxOperatorCRDsChart,
		config.EnvVirtFusionOperatorCRDsChart,
		config.EnvSolusVMOperatorCRDsChart,
		config.EnvProxmoxOperatorChart,
		config.EnvVirtFusionOperatorChart,
		config.EnvSolusVMOperatorChart,
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
		if env == "" || isOCIRef(env) {
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
	st, err := os.Stat(filepath.Join(path, helmChartFile))
	return err == nil && !st.IsDir()
}

func resolveLocalCRDChart(c hypervisorCRDChart) string {
	if env := strings.TrimSpace(os.Getenv(c.envKey)); env != "" && !isOCIRef(env) {
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
	if env := strings.TrimSpace(os.Getenv(c.envKey)); env != "" && isOCIRef(env) {
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

func appendOCIChartVersion(args []string, chart, version string) []string {
	if isOCIRef(chart) && version != "" {
		return append(args, helmFlagVersion, version)
	}
	return args
}

func helmCRDInstallArgs(c hypervisorCRDChart, kubeconfig string) []string {
	chart, version := resolveCRDChart(c)
	args := []string{
		helmCmdUpgrade, helmFlagInstall, c.release, chart,
		helmFlagNamespace, config.DefaultHypervisorCRDsHelmNamespace,
		helmFlagCreateNamespace,
	}
	args = appendOCIChartVersion(args, chart, version)
	return withKubeconfig(kubeconfig, args)
}

func helmCRDTemplateArgs(c hypervisorCRDChart) []string {
	chart, version := resolveCRDChart(c)
	args := []string{helmCmdTemplate, c.release, chart}
	return appendOCIChartVersion(args, chart, version)
}

func isHelmCRDOwnershipConflict(err error, output []byte) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error() + "\n" + string(output))
	return strings.Contains(msg, helmCRDOwnershipMetadata) ||
		strings.Contains(msg, helmCRDCannotImport)
}

func crdBareName(name string) string {
	name = strings.TrimSpace(name)
	if _, rest, ok := strings.Cut(name, kubeResourceNameSeparator); ok {
		return rest
	}
	return name
}

func hypervisorCRDAPIGroups() []string {
	return []string{
		config.ProxmoxCRDAPIGroup,
		config.VirtFusionCRDAPIGroup,
		config.SolusVMCRDAPIGroup,
	}
}

func isHypervisorCRDName(name string) bool {
	name = crdBareName(name)
	for _, group := range hypervisorCRDAPIGroups() {
		if strings.HasSuffix(name, kubeAPIGroupSeparator+group) {
			return true
		}
	}
	return false
}

func parseHypervisorCRDNames(kubectlOutput string) []string {
	var names []string
	for _, name := range strings.Split(kubectlOutput, "\n") {
		name = crdBareName(name)
		if isHypervisorCRDName(name) {
			names = append(names, name)
		}
	}
	return names
}

func crdKeepAnnotationArgs(names []string, kubeconfig string) []string {
	args := []string{kubectlCmdAnnotate, kubectlResourceCRD}
	args = append(args, names...)
	args = append(args, helmResourcePolicyAnnotation(), kubectlFlagOverwrite)
	return withKubeconfig(kubeconfig, args)
}

func applyCRDChartManifests(ctx context.Context, kubeconfig string, c hypervisorCRDChart, logger Logger) error {
	args := helmCRDTemplateArgs(c)
	logger.Infof("Applying %s CRDs in place (existing Helm ownership)...", c.release)
	template := exec.CommandContext(ctx, binHelm, args...)
	var stdout, stderr bytes.Buffer
	template.Stdout = &stdout
	template.Stderr = &stderr
	if err := template.Run(); err != nil {
		return fmt.Errorf("helm template %s: %w\n%s", c.release, err, strings.TrimSpace(stderr.String()+"\n"+stdout.String()))
	}
	manifests := stdout.Bytes()
	if len(bytes.TrimSpace(manifests)) == 0 {
		return fmt.Errorf("helm template %s produced no manifests", c.release)
	}
	// Server-side apply with force-conflicts updates CRD schemas without
	// taking Helm release ownership (Helm 3.16 has no --take-ownership).
	applyArgs := withKubeconfig(kubeconfig, []string{
		kubectlCmdApply, kubectlFlagServerSide, kubectlFlagForceConflicts,
		kubectlFlagFilename, kubectlStdinFilename,
	})
	apply := exec.CommandContext(ctx, binKubectl, applyArgs...)
	apply.Stdin = bytes.NewReader(manifests)
	out, err := apply.CombinedOutput()
	if err != nil {
		return fmt.Errorf("kubectl apply %s CRDs: %w\n%s", c.release, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func keepHypervisorCRDsFromHelmPrune(ctx context.Context, kubeconfig string, logger Logger) error {
	if logger == nil {
		logger = noopLogger{}
	}
	listArgs := withKubeconfig(kubeconfig, []string{
		kubectlCmdGet, kubectlResourceCRD, helmFlagOutput, kubectlCRDNamesJSONPath,
	})
	out, err := cmdOutput(ctx, binKubectl, listArgs...)
	if err != nil {
		return fmt.Errorf("list CRDs: %w", err)
	}
	names := parseHypervisorCRDNames(string(out))
	if len(names) == 0 {
		return nil
	}
	logger.Info("Keeping hypervisor CRDs if the operator chart drops the CRD subchart...")
	return runCmd(ctx, binKubectl, crdKeepAnnotationArgs(names, kubeconfig)...)
}

func helmCRDOCIHint(c hypervisorCRDChart, chart string) string {
	if !isOCIRef(chart) {
		return ""
	}
	return fmt.Sprintf("\nset %s to a local chart dir, or place %s next to %s / %s / %s",
		c.envKey, c.release,
		config.EnvVirtFusionOperatorChart, config.EnvProxmoxOperatorChart, config.EnvSolusVMOperatorChart)
}

func ensureOneCRDChart(ctx context.Context, kubeconfig string, c hypervisorCRDChart, logger Logger) error {
	chart, _ := resolveCRDChart(c)
	args := helmCRDInstallArgs(c, kubeconfig)
	logger.Infof("Ensuring CRDs (%s) via Helm %s...", c.release, chart)
	cmd := exec.CommandContext(ctx, binHelm, args...)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	if isHelmCRDOwnershipConflict(err, out) {
		logger.Warnf("CRDs for %s already owned by another Helm release; applying manifests in place", c.release)
		if applyErr := applyCRDChartManifests(ctx, kubeconfig, c, logger); applyErr != nil {
			return fmt.Errorf("adopt %s CRDs: %w", c.release, applyErr)
		}
		return nil
	}
	return fmt.Errorf("helm %s: %w\n%s%s", c.release, err, out, helmCRDOCIHint(c, chart))
}

// EnsureHypervisorCRDs installs the Proxmox, VirtFusion, and SolusVM CRD charts.
// Mothership Liqo AllowList always includes those API groups; missing CRDs on a
// provider stall custom-resource informers with "the server could not find the requested resource".
//
// Agents that installed CRDs as the operator chart subchart already own those
// objects. Helm 3.16 cannot --take-ownership, so we apply the CRD chart in
// place instead of failing the upgrade.
func EnsureHypervisorCRDs(ctx context.Context, kubeconfig string, logger Logger) error {
	if logger == nil {
		logger = noopLogger{}
	}
	for _, c := range hypervisorCRDCharts() {
		if err := ensureOneCRDChart(ctx, kubeconfig, c, logger); err != nil {
			return err
		}
	}
	return nil
}
