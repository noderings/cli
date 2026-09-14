package install

import (
	"path/filepath"
	"strings"

	"github.com/noderings/cli/internal/config"
)

// Helm and kubectl argv used by operator upgrade / CRD ensure.
const (
	binHelm    = "helm"
	binKubectl = "kubectl"

	helmCmdUpgrade      = "upgrade"
	helmCmdTemplate     = "template"
	helmCmdStatus       = "status"
	helmCmdGet          = "get"
	helmGetSubcmdValues = "values"

	helmFlagInstall              = "--install"
	helmFlagNamespace            = "--namespace"
	helmFlagCreateNamespace      = "--create-namespace"
	helmFlagVersion              = "--version"
	helmFlagResetThenReuseValues = "--reset-then-reuse-values"
	helmFlagReuseValues          = "--reuse-values"
	helmFlagSet                  = "--set"
	helmFlagSetString            = "--set-string"
	helmFlagKubeconfig           = "--kubeconfig"
	helmFlagOutput               = "-o"
	helmOutputYAML               = "yaml"
	helmFlagTakeOwnership        = "--take-ownership"

	helmOCIScheme                = "oci://"
	helmUserSuppliedValuesBanner = "USER-SUPPLIED VALUES:"
	helmNullValues               = "null"
	//nolint:gosec // G101: Helm CLI error substring, not a credential
	helmReleaseNotFoundToken = "release: not found"
	helmCRDOwnershipMetadata = "invalid ownership metadata"
	helmCRDCannotImport      = "exists and cannot be imported"
	helmChartFile            = "Chart.yaml"

	helmReleaseProxmoxCRDs    = "proxmox-operator-crds"
	helmReleaseVirtFusionCRDs = "virtfusion-operator-crds"
	helmReleaseSolusVMCRDs    = "solusvm-operator-crds"

	kubectlCmdApply       = "apply"
	kubectlCmdAnnotate    = "annotate"
	kubectlCmdGet         = "get"
	kubectlCmdRollout     = "rollout"
	kubectlRolloutRestart = "restart"
	kubectlRolloutStatus  = "status"

	kubectlResourceCRD    = "crd"
	kubectlResourceSecret = "secret"
	kubectlKindDeployment = "deployment"
	kubectlKindDaemonSet  = "daemonset"

	kubectlFlagServerSide     = "--server-side"
	kubectlFlagForceConflicts = "--force-conflicts"
	kubectlFlagFilename       = "-f"
	kubectlStdinFilename      = "-"
	kubectlFlagOverwrite      = "--overwrite"
	kubectlFlagIgnoreNotFound = "--ignore-not-found"
	kubectlFlagNamespaceShort = "-n"
	kubectlFlagTimeoutPrefix  = "--timeout="
	kubectlOutputName         = "name"
	kubectlCRDNamesJSONPath   = `jsonpath={range .items[*]}{.metadata.name}{"\n"}{end}`
	kubeNotFoundToken         = "(NotFound)"
	kubeResourceNameSeparator = "/"
	kubeAPIGroupSeparator     = "."

	relParentDir    = ".."
	relOperatorRepo = "operator"
	relChartsDir    = "charts"

	expectedReusableOperatorCount = 1

	cliFlagHypervisorDriver = "hypervisor-driver"
)

func helmResourcePolicyAnnotation() string {
	return config.HelmResourcePolicyKey + "=" + config.HelmResourcePolicyKeep
}

func isOCIRef(ref string) bool {
	return strings.HasPrefix(ref, helmOCIScheme)
}

func kubeDeployment(name string) string {
	return kubectlKindDeployment + kubeResourceNameSeparator + name
}

func kubeDaemonSet(name string) string {
	return kubectlKindDaemonSet + kubeResourceNameSeparator + name
}

func operatorChartSiblings(release string) []string {
	return []string{
		filepath.Join(relParentDir, relOperatorRepo, relChartsDir, release),
		filepath.Join(relParentDir, relParentDir, relOperatorRepo, relChartsDir, release),
	}
}
