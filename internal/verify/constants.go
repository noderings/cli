package verify

import "time"

// Verify package defaults for timeouts and check names.
const (
	// DefaultAPITimeout is how long checkKubernetes waits for the API to become reachable.
	DefaultAPITimeout = 10 * time.Second
	// APIPollInterval is the waitForAPI ticker interval.
	APIPollInterval = 1 * time.Second

	// DefaultDeploymentReplicas is the expected replica count when Spec.Replicas is nil.
	DefaultDeploymentReplicas int32 = 1

	// Check display names (stable report identifiers, not Kubernetes object names).
	CheckNameScope           = "scope"
	CheckNameAPI             = "api"
	CheckNameNodes           = "nodes"
	CheckNameNamespace       = "namespace"
	CheckNameNamespaces      = "namespaces"
	CheckNameCalicoNode      = "calico-node"
	CheckNameHealth          = "health"
	CheckNamePeer            = "peer"
	CheckNameClient          = "client"
	CheckNameProvisioned     = "provisioned"
	CheckNameInboundPeering  = "inbound-peering"
	CheckNameServiceStatus   = "service-status"
	CheckNameRemoteNamespace = "remote-namespace"
	CheckNameHelmWorkloads   = "helm-workloads"
	CheckNameHelmNamespace   = "helm-namespace"
	CheckNameCRDs            = "crds"
	CheckNameOperatorDeploy  = "operator-deployment"
	CheckNameAlloy           = "alloy"
	CheckNameExporter        = "exporter"
	CheckNameVNCNamespace    = "vnc-namespace"
	CheckNameVNCGateway      = "vnc-gateway"
	CheckNameMimirSecret     = "mimir-secret"
)
