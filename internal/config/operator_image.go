package config

import (
	"os"
	"strings"
	"unicode"
)

// harborOperatorImageTagPrefix is the Harbor container tag prefix. Charts use
// the bare SemVer (0.1.3); images in the same OCI repo use v0.1.3.
const harborOperatorImageTagPrefix = "v"

// HarborOperatorImageTag is the container tag for an operator chart version.
// Harbor stores the Helm chart and the image in the same OCI repository, so the
// image keeps a "v" prefix (v0.1.3) and the chart uses the bare version (0.1.3).
func HarborOperatorImageTag(chartVersion string) string {
	v := strings.TrimSpace(chartVersion)
	if v == "" {
		return ""
	}
	rest, ok := strings.CutPrefix(strings.ToLower(v), harborOperatorImageTagPrefix)
	if ok && rest != "" && unicode.IsDigit(rune(rest[0])) {
		return harborOperatorImageTagPrefix + rest
	}
	return harborOperatorImageTagPrefix + v
}

// ResolveOperatorImageTag returns the operator image tag for a Helm upgrade/install.
// Driver-specific env overrides win; otherwise the Harbor v-prefix of the chart version.
func ResolveOperatorImageTag(driver, chartVersion string) string {
	if v := strings.TrimSpace(os.Getenv(operatorImageTagEnv(driver))); v != "" {
		return v
	}
	return HarborOperatorImageTag(chartVersion)
}

func operatorImageTagEnv(driver string) string {
	switch {
	case IsVirtFusionHypervisor(driver):
		return EnvVirtFusionOperatorImageTag
	case IsSolusVMHypervisor(driver):
		return EnvSolusVMOperatorImageTag
	default:
		return EnvProxmoxOperatorImageTag
	}
}
