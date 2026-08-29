package config

import (
	"fmt"
	"os"
	"strings"
)

// Shared configuration and protocol defaults used across the CLI.
const (
	// DefaultAPIBaseURL is the production API base URL.
	DefaultAPIBaseURL = "https://api.noderings.com"

	// DefaultMothershipAPIPort is the default HTTP gateway port for a local control-plane host.
	DefaultMothershipAPIPort = 20000
	// DefaultMothershipFrontendPort is the default frontend port for a local control-plane host.
	DefaultMothershipFrontendPort = 3000
	// DefaultMothershipK8sAPIPort is the default Kubernetes API port exposed on a local control-plane host.
	DefaultMothershipK8sAPIPort = 6443

	// DefaultAgentK8sAPIPort is the provider-cluster Kubernetes API port used for Liqo install.
	DefaultAgentK8sAPIPort = 6443

	// GWServiceTypeNodePort is the Liqo gateway service type that exposes the WireGuard
	// server on a node port; peering then needs --gw-server-service-nodeport.
	GWServiceTypeNodePort = "NodePort"
	// DefaultGWServiceType is the default Liqo gateway service type.
	DefaultGWServiceType = GWServiceTypeNodePort
	// DevGWServiceType is the Liqo gateway service type forced with --dev.
	DevGWServiceType = GWServiceTypeNodePort
	// DefaultGWServerServiceLocation is the default Liqo gateway server location.
	DefaultGWServerServiceLocation = "Provider"
	// DefaultPodOffloadingStrategy is the default Liqo pod offloading strategy.
	DefaultPodOffloadingStrategy = "Local"
	// DefaultNamespaceMappingStrategy is the default Liqo namespace mapping strategy.
	DefaultNamespaceMappingStrategy = "SelectedName"

	// DefaultLiqoVersion is the default Liqo / liqoctl version pin.
	DefaultLiqoVersion = "v0.0.0-4600ebb8"
	// DefaultLiqoChartVersion is the Helm chart version (no leading v).
	DefaultLiqoChartVersion = "0.0.0-4600ebb8"
	// DefaultLiqoChartOCI is the OCI chart reference (without version tag).
	DefaultLiqoChartOCI = "oci://harbor.noderings.com/noderings/liqo"
	// DefaultLiqoctlOCIRepo is the OCI project/repo prefix for liqoctl artifacts.
	DefaultLiqoctlOCIRepo = "harbor.noderings.com/noderings"
	// DefaultLiqoNamespace is the Kubernetes namespace for Liqo components.
	DefaultLiqoNamespace = "liqo"
	// LiqoctlBinary is the Liqo CLI binary name expected on PATH.
	LiqoctlBinary = "liqoctl"
	// LiqoctlOutputJSON is the liqoctl -o value for machine-readable output.
	LiqoctlOutputJSON = "json"
	// LiqoLabelNodeType is the node label key Liqo uses for node kind.
	LiqoLabelNodeType = "liqo.io/type"
	// LiqoNodeTypeVirtual is the LiqoLabelNodeType value for virtual nodes.
	LiqoNodeTypeVirtual = "virtual-node"
	// LiqoLabelRemoteClusterID is the node label key for the peered remote cluster.
	LiqoLabelRemoteClusterID = "liqo.io/remote-cluster-id"
	// LiqoOffloadingAPIGroup is the API group for NamespaceOffloading.
	LiqoOffloadingAPIGroup = "offloading.liqo.io"
	// LiqoOffloadingAPIVersion is the API version for NamespaceOffloading.
	LiqoOffloadingAPIVersion = "v1beta1"
	// LiqoNamespaceOffloadingResource is the plural resource name for NamespaceOffloading.
	LiqoNamespaceOffloadingResource = "namespaceoffloadings"

	// DefaultCalicoSystemNamespace is the Calico dataplane namespace.
	DefaultCalicoSystemNamespace = "calico-system"
	// DefaultCalicoAPIServerNamespace is the Calico aggregated API server namespace.
	DefaultCalicoAPIServerNamespace = "calico-apiserver"
	// DefaultTigeraOperatorNamespace is the Tigera operator namespace.
	DefaultTigeraOperatorNamespace = "tigera-operator"
	// DefaultCalicoNodeDaemonSet is the Calico node DaemonSet name.
	DefaultCalicoNodeDaemonSet = "calico-node"
	// DefaultTigeraOperatorDeployment is the Tigera operator Deployment name.
	DefaultTigeraOperatorDeployment = "tigera-operator"

	// DefaultVNCGatewayNamespace is the provider-local namespace offloaded to the remote cluster.
	DefaultVNCGatewayNamespace = "vnc-gateway"
	// DefaultVNCGatewayImageRepository is the Harbor image RFB and Proxmox charts pull.
	DefaultVNCGatewayImageRepository = "harbor.noderings.com/noderings/vnc-gateway"
	// DefaultVNCGatewayImageTagProxmox is the stock Harbor tag. Proxmox readiness
	// and /v1/vnc/ws need this digest — do not retag a local-rfb build over it.
	DefaultVNCGatewayImageTagProxmox = "main"
	// DefaultHarborRegistry is the container registry host for operator images
	// and charts. harbor.nrings.io is retired; do not use it.
	DefaultHarborRegistry = "harbor.noderings.com"
	// DefaultVNCGatewayImageTagRFB is the local-rfb / current-gateway tag used by
	// VirtFusion and SolusVM (ready without Proxmox instances). Harbor publishes
	// both :main (Proxmox) and :rfb (VF/SVM). The CLI still loads a local build
	// as :rfb onto the agent when Docker/source is available.
	DefaultVNCGatewayImageTagRFB = "rfb"
	// LocalRFBVNCGatewayImage is the docker tag for a locally built RFB gateway.
	LocalRFBVNCGatewayImage = "vnc-gateway:local-rfb"
	// SuffixVNCGatewayNamespace is appended to the agent ID for the remote VNC namespace.
	SuffixVNCGatewayNamespace = "-vnc-gateway"
	// SuffixOperatorNamespace is appended to the agent ID for the remote operator namespace.
	// Operator offload is performed by the control plane (inbound peering), not by the CLI.
	SuffixOperatorNamespace = "-operator"
	// DefaultOperatorRemoteNamespace is the provider-side name for the remote-offloaded operator namespace.
	DefaultOperatorRemoteNamespace = "operator"

	// MaxK8sNamespaceLen is the Kubernetes DNS-1123 label length limit for namespace names.
	MaxK8sNamespaceLen = 63

	// DefaultOAuthCallbackPort is the fixed local port for the OAuth2 callback server.
	DefaultOAuthCallbackPort = 22222
	// DefaultOAuthCallbackHost is the loopback host for the OAuth2 callback server.
	DefaultOAuthCallbackHost = "127.0.0.1"

	// DefaultKeyringService is the OS keyring service name for token storage.
	DefaultKeyringService = "nr-cli"
	// DefaultKeyringTokenKey is the OS keyring account/key name for the stored token.
	DefaultKeyringTokenKey = "token"

	// DefaultFrontendBaseURL is the production dashboard used for OAuth login.
	DefaultFrontendBaseURL = "https://noderings.com"

	// DefaultProxmoxOperatorChartOCI is the OCI chart for provider operator install.
	DefaultProxmoxOperatorChartOCI = "oci://harbor.noderings.com/noderings/proxmox-operator"
	// DefaultProxmoxOperatorCRDsChartOCI is CRDs-only; install on every provider so Liqo can watch both API groups.
	DefaultProxmoxOperatorCRDsChartOCI = "oci://harbor.noderings.com/noderings/proxmox-operator-crds"
	// DefaultProxmoxOperatorChartVersion is the chart version (no leading v).
	DefaultProxmoxOperatorChartVersion = "0.1.2"
	// DefaultProxmoxOperatorHelmRelease is the Helm release name (workload = release-chart).
	DefaultProxmoxOperatorHelmRelease = "operator"
	// DefaultProxmoxOperatorHelmNamespace is the runtime namespace for the operator chart.
	DefaultProxmoxOperatorHelmNamespace = "proxmox-system"
	// ProxmoxCRDAPIGroup is the API group registered by proxmox-operator CRDs.
	ProxmoxCRDAPIGroup = "vm.proxmox.com"

	// DefaultVirtFusionOperatorChartOCI is the OCI chart for VirtFusion operator install.
	DefaultVirtFusionOperatorChartOCI = "oci://harbor.noderings.com/noderings/virtfusion-operator"
	// DefaultVirtFusionOperatorCRDsChartOCI is CRDs-only (install alongside Proxmox CRDs on every provider).
	DefaultVirtFusionOperatorCRDsChartOCI = "oci://harbor.noderings.com/noderings/virtfusion-operator-crds"
	// DefaultVirtFusionOperatorChartVersion is the VirtFusion chart version (no leading v).
	DefaultVirtFusionOperatorChartVersion = "0.1.4"
	// DefaultVirtFusionOperatorHelmNamespace is the runtime namespace for the VirtFusion operator chart.
	DefaultVirtFusionOperatorHelmNamespace = "virtfusion-system"
	// VirtFusionCRDAPIGroup is the API group registered by virtfusion-operator CRDs.
	VirtFusionCRDAPIGroup = "vm.virtfusion.com"

	// DefaultSolusVMOperatorChartOCI is the OCI chart for SolusVM 2 operator install.
	DefaultSolusVMOperatorChartOCI = "oci://harbor.noderings.com/noderings/solusvm-operator"
	// DefaultSolusVMOperatorCRDsChartOCI is CRDs-only (install alongside other hypervisor CRDs on every provider).
	DefaultSolusVMOperatorCRDsChartOCI = "oci://harbor.noderings.com/noderings/solusvm-operator-crds"
	// DefaultSolusVMOperatorChartVersion is the SolusVM chart version (no leading v).
	DefaultSolusVMOperatorChartVersion = "0.1.3"
	// DefaultSolusVMOperatorHelmNamespace is the runtime namespace for the SolusVM operator chart.
	DefaultSolusVMOperatorHelmNamespace = "solusvm-system"
	// SolusVMCRDAPIGroup is the API group registered by solusvm-operator CRDs.
	SolusVMCRDAPIGroup = "vm.solusvm.com"

	// Hypervisor driver identifiers for cluster register / verify.
	HypervisorDriverProxmox    = "proxmox"
	HypervisorDriverVirtFusion = "virtfusion"
	HypervisorDriverSolusVM    = "solusvm"

	// DefaultMimirCredentialsSecret is the Secret name for Alloy → Mimir credentials.
	//nolint:gosec // G101: Kubernetes Secret object name, not a credential value
	DefaultMimirCredentialsSecret = "mimir-credentials"
	// DefaultMimirServiceEndpoint is the production Mimir push endpoint host[:port].
	DefaultMimirServiceEndpoint = "metrics.noderings.com"

	// DefaultHelmRolloutTimeout is the kubectl rollout status timeout after operator install.
	DefaultHelmRolloutTimeout = "120s"

	// DefaultOrasVersion is the oras release used to pull OCI tool/chart artifacts.
	DefaultOrasVersion = "v1.2.2"

	// DefaultDNSCheckImage runs the post-Calico ClusterIP DNS probe.
	DefaultDNSCheckImage = "busybox:1.36"
	// EnvDNSCheckImage overrides DefaultDNSCheckImage for air-gapped/private registries.
	EnvDNSCheckImage = "NR_DNS_CHECK_IMAGE"

	// Helm chart workload name suffixes (release + suffix).
	SuffixHelmProxmoxOperator    = "-proxmox-operator"
	SuffixHelmVirtFusionOperator = "-virtfusion-operator"
	SuffixHelmSolusVMOperator    = "-solusvm-operator"
	SuffixHelmAlloy              = "-alloy"
	SuffixHelmExporter           = "-exporter"
	SuffixHelmVNCGateway         = "-vnc-gateway"

	// Environment variable names shared by install and verify.
	EnvHelmNamespace       = "HELM_NAMESPACE"
	EnvHelmRelease         = "HELM_RELEASE"
	EnvVNCGatewayNamespace = "VNC_GATEWAY_NAMESPACE"
	EnvVNCGatewayImageTag  = "VNC_GATEWAY_IMAGE_TAG"
	// Driver-specific tag overrides win over EnvVNCGatewayImageTag.
	EnvProxmoxVNCGatewayImageTag    = "PROXMOX_VNC_GATEWAY_IMAGE_TAG"
	EnvVirtFusionVNCGatewayImageTag = "VIRTFUSION_VNC_GATEWAY_IMAGE_TAG"
	EnvSolusVMVNCGatewayImageTag    = "SOLUSVM_VNC_GATEWAY_IMAGE_TAG"
	EnvSkipVNCGatewayImageLoad      = "SKIP_VNC_GATEWAY_IMAGE_LOAD"
	EnvMimirServiceEndpoint         = "MIMIR_SERVICE_ENDPOINT"
	//nolint:gosec // G101: environment variable name, not a credential value
	EnvMimirBearerToken = "MIMIR_BEARER_TOKEN"
	EnvMimirTLSEnabled  = "MIMIR_TLS_ENABLED"

	// CLI output formats.
	OutputFormatText = "text"
	OutputFormatJSON = "json"

	// Platform API agent status values.
	InboundPeeringStateReadyProto = "INBOUND_PEERING_STATE_READY"
	InboundPeeringStateReadyShort = "READY"
	ServiceStatusUnspecified      = "SERVICE_STATUS_UNSPECIFIED"
	ServiceStatusOnlineToken      = "ONLINE"
)

// DevAPIURL is the localhost API URL used when --dev is set without a control-plane host config.
func DevAPIURL() string {
	return fmt.Sprintf("https://localhost:%d", DefaultMothershipAPIPort)
}

// DevFrontendURL is the localhost frontend URL used when --dev is set without a control-plane host config.
func DevFrontendURL() string {
	return fmt.Sprintf("http://localhost:%d", DefaultMothershipFrontendPort)
}

func helmReleaseOrDefault(release string) string {
	if release == "" {
		return DefaultProxmoxOperatorHelmRelease
	}
	return release
}

// HelmProxmoxOperatorDeployName returns the operator Deployment name for a Helm release.
func HelmProxmoxOperatorDeployName(release string) string {
	return helmReleaseOrDefault(release) + SuffixHelmProxmoxOperator
}

// HelmAlloyDaemonSetName returns the Alloy DaemonSet name for a Helm release.
func HelmAlloyDaemonSetName(release string) string {
	return helmReleaseOrDefault(release) + SuffixHelmAlloy
}

// HelmExporterDeployName returns the metrics exporter Deployment name for a Helm release.
func HelmExporterDeployName(release string) string {
	return HelmProxmoxOperatorDeployName(release) + SuffixHelmExporter
}

// HelmVNCGatewayDeployName returns the VNC gateway Deployment name for a Helm release.
func HelmVNCGatewayDeployName(release string) string {
	return HelmProxmoxOperatorDeployName(release) + SuffixHelmVNCGateway
}

// HelmVirtFusionOperatorDeployName returns the VirtFusion operator Deployment name for a Helm release.
func HelmVirtFusionOperatorDeployName(release string) string {
	return helmReleaseOrDefault(release) + SuffixHelmVirtFusionOperator
}

// HelmVirtFusionExporterDeployName returns the VirtFusion metrics exporter Deployment name.
func HelmVirtFusionExporterDeployName(release string) string {
	return HelmVirtFusionOperatorDeployName(release) + SuffixHelmExporter
}

// HelmVirtFusionVNCGatewayDeployName returns the VirtFusion VNC gateway Deployment name.
func HelmVirtFusionVNCGatewayDeployName(release string) string {
	return HelmVirtFusionOperatorDeployName(release) + SuffixHelmVNCGateway
}

// HelmSolusVMOperatorDeployName returns the SolusVM operator Deployment name for a Helm release.
func HelmSolusVMOperatorDeployName(release string) string {
	return helmReleaseOrDefault(release) + SuffixHelmSolusVMOperator
}

// HelmSolusVMExporterDeployName returns the SolusVM metrics exporter Deployment name.
func HelmSolusVMExporterDeployName(release string) string {
	return HelmSolusVMOperatorDeployName(release) + SuffixHelmExporter
}

// HelmSolusVMVNCGatewayDeployName returns the SolusVM VNC gateway Deployment name.
func HelmSolusVMVNCGatewayDeployName(release string) string {
	return HelmSolusVMOperatorDeployName(release) + SuffixHelmVNCGateway
}

// IsVirtFusionHypervisor reports whether driver is the VirtFusion hypervisor.
func IsVirtFusionHypervisor(driver string) bool {
	return strings.EqualFold(strings.TrimSpace(driver), HypervisorDriverVirtFusion)
}

// IsSolusVMHypervisor reports whether driver is the SolusVM 2 hypervisor.
func IsSolusVMHypervisor(driver string) bool {
	return strings.EqualFold(strings.TrimSpace(driver), HypervisorDriverSolusVM)
}

// ResolveVNCGatewayImageTag returns the vnc-gateway image tag for a hypervisor
// driver. Driver-specific env wins, then VNC_GATEWAY_IMAGE_TAG for RFB drivers
// only, then the driver default. Proxmox ignores the generic env so a leftover
// VNC_GATEWAY_IMAGE_TAG=rfb cannot replace Harbor stock :main.
func ResolveVNCGatewayImageTag(driver string) string {
	if IsVirtFusionHypervisor(driver) {
		return firstNonEmpty(
			os.Getenv(EnvVirtFusionVNCGatewayImageTag),
			os.Getenv(EnvVNCGatewayImageTag),
			DefaultVNCGatewayImageTagRFB,
		)
	}
	if IsSolusVMHypervisor(driver) {
		return firstNonEmpty(
			os.Getenv(EnvSolusVMVNCGatewayImageTag),
			os.Getenv(EnvVNCGatewayImageTag),
			DefaultVNCGatewayImageTagRFB,
		)
	}
	return firstNonEmpty(
		os.Getenv(EnvProxmoxVNCGatewayImageTag),
		DefaultVNCGatewayImageTagProxmox,
	)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
