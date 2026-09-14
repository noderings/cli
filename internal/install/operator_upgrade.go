package install

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/noderings/cli/internal/config"
)

var (
	// ErrNoReusableOperator means no Helm release with existing instance Secrets was found.
	ErrNoReusableOperator = errors.New("no reusable operator helm release")
	// ErrAmbiguousOperatorRelease means more than one hypervisor operator release is installed.
	ErrAmbiguousOperatorRelease = errors.New("multiple operator helm releases found")
)

// OperatorUpgrade is a Helm in-place upgrade of an already-installed operator.
// It keeps hypervisor credentials in existing Secrets and Helm values.
type OperatorUpgrade struct {
	Driver                    string
	Chart                     string
	ChartVersion              string
	ImageTag                  string
	ImageRegistry             string
	ImageRepository           string
	Release                   string
	Namespace                 string
	VNCGatewayNamespace       string
	VNCGatewayImageTag        string
	VNCGatewayImageRepository string
	KubeconfigPath            string
	Workloads                 []string
	VNCDeployment             string
}

// Validate reports whether the upgrade can be applied.
func (u OperatorUpgrade) Validate() error {
	if strings.TrimSpace(u.Chart) == "" {
		return fmt.Errorf("operator chart is required")
	}
	if strings.TrimSpace(u.Release) == "" || strings.TrimSpace(u.Namespace) == "" {
		return fmt.Errorf("helm release and namespace are required")
	}
	tag := strings.TrimSpace(u.ImageTag)
	if tag == "" {
		return fmt.Errorf("operator image tag is required")
	}
	ver := strings.TrimSpace(u.ChartVersion)
	wantTag := config.HarborOperatorImageTag(ver)
	if ver != "" && tag == ver && tag != wantTag {
		return fmt.Errorf("refusing image.tag=%s: Harbor chart tags must not be used as image tags (want %s)",
			tag, wantTag)
	}
	return nil
}

// BindLiveRelease pins the upgrade to the Helm release DetectReusableOperator found
// and refreshes workload names for that release.
func (u *OperatorUpgrade) BindLiveRelease(release, namespace string) error {
	if u == nil {
		return fmt.Errorf("operator upgrade is required")
	}
	if rel := strings.TrimSpace(release); rel != "" {
		u.Release = rel
	}
	if ns := strings.TrimSpace(namespace); ns != "" {
		u.Namespace = ns
	}
	spec, err := specForDriver(u.Driver)
	if err != nil {
		return err
	}
	u.Workloads = spec.workloads(u.Release)
	u.VNCDeployment = spec.vncDeployment(u.Release)
	return u.Validate()
}

// ReusableOperator is a live Helm release whose instance Secrets still exist.
type ReusableOperator struct {
	Driver      string
	Release     string
	Namespace   string
	SecretNames []string
}

type helmInstanceRef struct {
	ExistingSecret string `yaml:"existingSecret"`
}

type helmValuesSecrets struct {
	Proxmox    helmDriverInstances `yaml:"proxmox"`
	VirtFusion helmDriverInstances `yaml:"virtfusion"`
	SolusVM    helmDriverInstances `yaml:"solusvm"`
}

type helmDriverInstances struct {
	Instances []helmInstanceRef `yaml:"instances"`
}

// ParseHelmInstanceSecrets returns existingSecret names from user-supplied Helm values.
func ParseHelmInstanceSecrets(valuesYAML []byte, driver string) ([]string, error) {
	raw := bytes.TrimSpace(valuesYAML)
	raw = bytes.TrimPrefix(raw, []byte(helmUserSuppliedValuesBanner))
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == helmNullValues {
		return nil, nil
	}
	var vals helmValuesSecrets
	if err := yaml.Unmarshal(raw, &vals); err != nil {
		return nil, fmt.Errorf("parse helm values: %w", err)
	}
	var refs []helmInstanceRef
	switch {
	case config.IsVirtFusionHypervisor(driver):
		refs = vals.VirtFusion.Instances
	case config.IsSolusVMHypervisor(driver):
		refs = vals.SolusVM.Instances
	default:
		refs = vals.Proxmox.Instances
	}
	names := make([]string, 0, len(refs))
	for _, r := range refs {
		name := strings.TrimSpace(r.ExistingSecret)
		if name == "" || slices.Contains(names, name) {
			continue
		}
		names = append(names, name)
	}
	return names, nil
}

type operatorDriverSpec struct {
	driver        string
	namespace     string
	chartVersion  string
	imageRepoEnv  string
	resolveChart  func(string) (string, string)
	workloads     func(release string) []string
	vncDeployment func(release string) string
}

func specForDriver(driver string) (operatorDriverSpec, error) {
	switch {
	case config.IsVirtFusionHypervisor(driver):
		return operatorDriverSpec{
			driver:       config.HypervisorDriverVirtFusion,
			namespace:    config.DefaultVirtFusionOperatorHelmNamespace,
			chartVersion: config.DefaultVirtFusionOperatorChartVersion,
			imageRepoEnv: config.EnvVirtFusionOperatorImageRepository,
			resolveChart: ResolveVirtFusionOperatorChart,
			workloads: func(rel string) []string {
				return []string{
					kubeDeployment(config.HelmVirtFusionOperatorDeployName(rel)),
					kubeDaemonSet(config.HelmAlloyDaemonSetName(rel)),
					kubeDeployment(config.HelmVirtFusionExporterDeployName(rel)),
				}
			},
			vncDeployment: func(rel string) string {
				return kubeDeployment(config.HelmVirtFusionVNCGatewayDeployName(rel))
			},
		}, nil
	case config.IsSolusVMHypervisor(driver):
		return operatorDriverSpec{
			driver:       config.HypervisorDriverSolusVM,
			namespace:    config.DefaultSolusVMOperatorHelmNamespace,
			chartVersion: config.DefaultSolusVMOperatorChartVersion,
			imageRepoEnv: config.EnvSolusVMOperatorImageRepository,
			resolveChart: ResolveSolusVMOperatorChart,
			workloads: func(rel string) []string {
				return []string{
					kubeDeployment(config.HelmSolusVMOperatorDeployName(rel)),
					kubeDaemonSet(config.HelmAlloyDaemonSetName(rel)),
					kubeDeployment(config.HelmSolusVMExporterDeployName(rel)),
				}
			},
			vncDeployment: func(rel string) string {
				return kubeDeployment(config.HelmSolusVMVNCGatewayDeployName(rel))
			},
		}, nil
	case strings.EqualFold(strings.TrimSpace(driver), config.HypervisorDriverProxmox):
		ns := getenvDefault(config.EnvHelmNamespace, config.DefaultProxmoxOperatorHelmNamespace)
		return operatorDriverSpec{
			driver:       config.HypervisorDriverProxmox,
			namespace:    ns,
			chartVersion: config.DefaultProxmoxOperatorChartVersion,
			imageRepoEnv: config.EnvProxmoxOperatorImageRepository,
			resolveChart: ResolveOperatorChart,
			workloads: func(rel string) []string {
				return []string{
					kubeDeployment(config.HelmProxmoxOperatorDeployName(rel)),
					kubeDaemonSet(config.HelmAlloyDaemonSetName(rel)),
					kubeDeployment(config.HelmExporterDeployName(rel)),
				}
			},
			vncDeployment: func(rel string) string {
				return kubeDeployment(config.HelmVNCGatewayDeployName(rel))
			},
		}, nil
	default:
		return operatorDriverSpec{}, fmt.Errorf("unsupported hypervisor driver %q", driver)
	}
}

// NewOperatorUpgrade builds an in-place upgrade from CLI pins for driver.
func NewOperatorUpgrade(driver, kubeconfig, chart, version string) (OperatorUpgrade, error) {
	spec, err := specForDriver(driver)
	if err != nil {
		return OperatorUpgrade{}, err
	}
	release := getenvDefault(config.EnvHelmRelease, config.DefaultProxmoxOperatorHelmRelease)
	u := OperatorUpgrade{
		Driver:                    spec.driver,
		Chart:                     strings.TrimSpace(chart),
		ChartVersion:              strings.TrimSpace(version),
		Release:                   release,
		Namespace:                 spec.namespace,
		KubeconfigPath:            kubeconfig,
		ImageRegistry:             getenvDefault(config.EnvOperatorImageRegistry, config.DefaultHarborRegistry),
		ImageRepository:           strings.TrimSpace(os.Getenv(spec.imageRepoEnv)),
		VNCGatewayNamespace:       getenvDefault(config.EnvVNCGatewayNamespace, config.DefaultVNCGatewayNamespace),
		VNCGatewayImageTag:        config.ResolveVNCGatewayImageTag(spec.driver),
		VNCGatewayImageRepository: resolveVNCGatewayImageRepository(spec.driver),
		Workloads:                 spec.workloads(release),
		VNCDeployment:             spec.vncDeployment(release),
	}
	if u.Chart == "" {
		resolved, resolvedVer := spec.resolveChart("")
		u.Chart = resolved
		if u.ChartVersion == "" {
			u.ChartVersion = resolvedVer
		}
	}
	if u.ChartVersion == "" {
		u.ChartVersion = spec.chartVersion
	}
	u.ImageTag = config.ResolveOperatorImageTag(spec.driver, u.ChartVersion)
	return u, u.Validate()
}

// InPlaceUpgradeArgs is the helm argv for a credential-preserving chart/image bump.
// --reset-then-reuse-values keeps existingSecret / instances while picking up new
// chart keys (hooks, exporter image). --reuse-values alone drops those defaults
// and 0.1.2→0.1.3 fails on .Values.hooks.enabled. image.tag is always set so an
// old reused tag cannot stick (never the bare chart version).
//
// This is `helm upgrade` without `--install`: a missing release must fail closed
// rather than deploying an operator with empty instance Secrets.
func InPlaceUpgradeArgs(u OperatorUpgrade) []string {
	args := []string{
		helmCmdUpgrade, u.Release, u.Chart,
		helmFlagNamespace, u.Namespace,
		helmFlagResetThenReuseValues,
		helmFlagSet, helmDisableCRDSubchart,
	}
	args = appendOCIChartVersion(args, u.Chart, u.ChartVersion)
	args = appendSetString(args, helmImageRegistry, u.ImageRegistry)
	args = appendSetString(args, helmImageRepository, u.ImageRepository)
	args = appendSetString(args, helmImageTag, u.ImageTag)
	args = appendSetString(args, helmVNCGatewayNamespace, u.VNCGatewayNamespace)
	args = appendSetString(args, helmVNCGatewayImageRepo, u.VNCGatewayImageRepository)
	args = appendSetString(args, helmVNCGatewayImageTag, u.VNCGatewayImageTag)
	return withKubeconfig(u.KubeconfigPath, args)
}

func resolveVNCGatewayImageRepository(driver string) string {
	var env string
	switch {
	case config.IsVirtFusionHypervisor(driver):
		env = config.EnvVirtFusionVNCGatewayImageRepository
	case config.IsSolusVMHypervisor(driver):
		env = config.EnvSolusVMVNCGatewayImageRepository
	default:
		env = config.EnvProxmoxVNCGatewayImageRepository
	}
	if v := strings.TrimSpace(os.Getenv(env)); v != "" {
		return v
	}
	return config.DefaultVNCGatewayImageRepository
}

func appendSetString(args []string, key, value string) []string {
	if strings.TrimSpace(value) == "" {
		return args
	}
	return append(args, helmFlagSetString, key+"="+value)
}

func withKubeconfig(kubeconfig string, args []string) []string {
	if strings.TrimSpace(kubeconfig) == "" {
		return args
	}
	return append(args, helmFlagKubeconfig, kubeconfig)
}

// DetectReusableOperator returns the live operator Helm release when instance Secrets exist.
func DetectReusableOperator(ctx context.Context, kubeconfig, driver string) (*ReusableOperator, error) {
	driver = strings.ToLower(strings.TrimSpace(driver))
	candidates := operatorReleaseCandidates()
	if driver != "" {
		candidates = slices.DeleteFunc(candidates, func(c operatorReleaseCandidate) bool {
			return c.driver != driver
		})
	}

	var found []ReusableOperator
	for _, c := range candidates {
		ok, err := helmReleaseExists(ctx, kubeconfig, c.release, c.namespace)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		values, err := helmGetValues(ctx, kubeconfig, c.release, c.namespace)
		if err != nil {
			return nil, err
		}
		secrets, err := ParseHelmInstanceSecrets(values, c.driver)
		if err != nil {
			return nil, fmt.Errorf("parse helm values for %s/%s: %w", c.namespace, c.release, err)
		}
		if len(secrets) == 0 {
			continue
		}
		namespaces := []string{c.namespace}
		if c.driver == config.HypervisorDriverProxmox {
			namespaces = append(namespaces, getenvDefault(config.EnvVNCGatewayNamespace, config.DefaultVNCGatewayNamespace))
		}
		ready, err := instanceSecretsExist(ctx, kubeconfig, secrets, namespaces)
		if err != nil {
			return nil, err
		}
		if !ready {
			continue
		}
		found = append(found, ReusableOperator{
			Driver:      c.driver,
			Release:     c.release,
			Namespace:   c.namespace,
			SecretNames: secrets,
		})
	}
	switch len(found) {
	case 0:
		return nil, ErrNoReusableOperator
	case expectedReusableOperatorCount:
		return &found[0], nil
	default:
		return nil, fmt.Errorf("%w (%s and %s); pass --%s",
			ErrAmbiguousOperatorRelease, found[0].Driver, found[1].Driver, cliFlagHypervisorDriver)
	}
}

type operatorReleaseCandidate struct {
	driver, release, namespace string
}

func operatorReleaseCandidates() []operatorReleaseCandidate {
	release := getenvDefault(config.EnvHelmRelease, config.DefaultProxmoxOperatorHelmRelease)
	return []operatorReleaseCandidate{
		{config.HypervisorDriverProxmox, release, getenvDefault(config.EnvHelmNamespace, config.DefaultProxmoxOperatorHelmNamespace)},
		{config.HypervisorDriverVirtFusion, release, config.DefaultVirtFusionOperatorHelmNamespace},
		{config.HypervisorDriverSolusVM, release, config.DefaultSolusVMOperatorHelmNamespace},
	}
}

func instanceSecretsExist(ctx context.Context, kubeconfig string, names, namespaces []string) (bool, error) {
	present := make(map[string]map[string]struct{}, len(namespaces))
	for _, ns := range namespaces {
		present[ns] = make(map[string]struct{}, len(names))
		for _, name := range names {
			exists, err := kubeSecretExists(ctx, kubeconfig, ns, name)
			if err != nil {
				return false, err
			}
			if exists {
				present[ns][name] = struct{}{}
			}
		}
	}
	return secretsExistInEveryNamespace(present, names, namespaces), nil
}

func secretsExistInEveryNamespace(present map[string]map[string]struct{}, names, namespaces []string) bool {
	for _, ns := range namespaces {
		inNS := present[ns]
		for _, name := range names {
			if _, ok := inNS[name]; !ok {
				return false
			}
		}
	}
	return true
}

// UpgradeOperatorRelease upgrades CRDs and the operator chart without rewriting Secrets.
func UpgradeOperatorRelease(ctx context.Context, u OperatorUpgrade, logger Logger) error {
	if logger == nil {
		logger = noopLogger{}
	}
	if err := u.Validate(); err != nil {
		return err
	}

	logger.Info("Ensuring hypervisor CRDs...")
	if err := EnsureHypervisorCRDs(ctx, u.KubeconfigPath, logger); err != nil {
		return fmt.Errorf("ensure hypervisor CRDs: %w", err)
	}
	if err := keepHypervisorCRDsFromHelmPrune(ctx, u.KubeconfigPath, logger); err != nil {
		return fmt.Errorf("annotate hypervisor CRDs %s: %w", helmResourcePolicyAnnotation(), err)
	}

	args := InPlaceUpgradeArgs(u)
	logger.Infof("Upgrading %s chart %s image.tag=%s (reusing cluster Secrets)...", u.Driver, u.ChartVersion, u.ImageTag)
	if err := runCmd(ctx, binHelm, args...); err != nil {
		return fmt.Errorf("helm upgrade: %w", err)
	}

	for _, workload := range u.Workloads {
		if err := waitWorkload(ctx, u.KubeconfigPath, workload, u.Namespace, logger); err != nil {
			return err
		}
	}
	if u.VNCDeployment != "" && u.VNCGatewayNamespace != "" {
		if err := waitWorkload(ctx, u.KubeconfigPath, u.VNCDeployment, u.VNCGatewayNamespace, logger); err != nil {
			return err
		}
	}
	logger.Infof("✓ %s operator chart %s / image %s", u.Driver, u.ChartVersion, u.ImageTag)
	return nil
}

func waitWorkload(ctx context.Context, kubeconfig, workload, namespace string, logger Logger) error {
	if err := runCmd(ctx, binKubectl, withKubeconfig(kubeconfig, []string{
		kubectlCmdRollout, kubectlRolloutRestart, workload, kubectlFlagNamespaceShort, namespace,
	})...); err != nil {
		if isKubeNotFound(err) {
			logger.Warnf("Skipping %s (not installed)", workload)
			return nil
		}
		return fmt.Errorf("restart %s: %w", workload, err)
	}
	if err := runCmd(ctx, binKubectl, withKubeconfig(kubeconfig, []string{
		kubectlCmdRollout, kubectlRolloutStatus, workload, kubectlFlagNamespaceShort, namespace,
		kubectlFlagTimeoutPrefix + config.DefaultHelmRolloutTimeout,
	})...); err != nil {
		if isKubeNotFound(err) {
			logger.Warnf("Skipping wait for %s (not installed)", workload)
			return nil
		}
		return fmt.Errorf("%s did not become ready: %w", workload, err)
	}
	return nil
}

func isKubeNotFound(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), kubeNotFoundToken)
}

func helmReleaseExists(ctx context.Context, kubeconfig, release, namespace string) (bool, error) {
	out, err := cmdOutput(ctx, binHelm, withKubeconfig(kubeconfig, []string{
		helmCmdStatus, release, helmFlagNamespace, namespace,
	})...)
	if err == nil {
		return true, nil
	}
	if isHelmReleaseNotFound(err, out) {
		return false, nil
	}
	return false, fmt.Errorf("helm status %s/%s: %w\n%s", namespace, release, err, strings.TrimSpace(string(out)))
}

func isHelmReleaseNotFound(err error, out []byte) bool {
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		return false
	}
	return strings.Contains(strings.ToLower(string(out)), helmReleaseNotFoundToken)
}

func helmGetValues(ctx context.Context, kubeconfig, release, namespace string) ([]byte, error) {
	out, err := cmdOutput(ctx, binHelm, withKubeconfig(kubeconfig, []string{
		helmCmdGet, helmGetSubcmdValues, release, helmFlagNamespace, namespace, helmFlagOutput, helmOutputYAML,
	})...)
	if err != nil {
		return nil, fmt.Errorf("helm get values %s/%s: %w", namespace, release, err)
	}
	return out, nil
}

func kubeSecretExists(ctx context.Context, kubeconfig, namespace, name string) (bool, error) {
	out, err := cmdOutput(ctx, binKubectl, withKubeconfig(kubeconfig, []string{
		kubectlCmdGet, kubectlResourceSecret, name, kubectlFlagNamespaceShort, namespace,
		kubectlFlagIgnoreNotFound, helmFlagOutput, kubectlOutputName,
	})...)
	if err != nil {
		return false, fmt.Errorf("kubectl get secret %s/%s: %w", namespace, name, err)
	}
	return strings.TrimSpace(string(out)) != "", nil
}

func runCmd(ctx context.Context, name string, args ...string) error {
	_, err := cmdOutput(ctx, name, args...)
	return err
}

func cmdOutput(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("%s failed: %w\n%s", name, err, strings.TrimSpace(string(out)))
	}
	return out, nil
}
