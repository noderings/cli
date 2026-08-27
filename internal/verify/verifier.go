package verify

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/noderings/cli/internal/config"
	"github.com/noderings/cli/internal/k8s"
)

// AgentStatus is the platform API view of an agent used by the agent section.
type AgentStatus struct {
	Provisioned         bool
	InboundPeeringState string
	InboundPeeringError string
	ServiceStatus       string
	Name                string
}

// AgentStatusFetcher loads agent status from the platform API.
type AgentStatusFetcher interface {
	FetchAgentStatus(ctx context.Context, agentID string) (*AgentStatus, error)
}

// Verifier runs provider-cluster health subsections.
type Verifier struct {
	opts   Options
	client kubeClient
	agent  AgentStatusFetcher
}

type kubeClient interface {
	GetClientset() kubernetes.Interface
	GetConfig() *rest.Config
}

// New creates a Verifier. client may be nil; kubernetes section will fail until set.
func New(opts Options, client *k8s.Client, agent AgentStatusFetcher) *Verifier {
	return newVerifier(opts, client, agent)
}

func newVerifier(opts Options, client kubeClient, agent AgentStatusFetcher) *Verifier {
	return &Verifier{opts: opts, client: client, agent: agent}
}

// Run executes enabled subsections and returns a Report.
func (v *Verifier) Run(ctx context.Context) (*Report, error) {
	report := &Report{AgentID: v.opts.AgentID}

	runners := []struct {
		name string
		fn   func(context.Context) SectionResult
	}{
		{SectionKubernetes, v.checkKubernetes},
		{SectionCalico, v.checkCalico},
		{SectionLiqo, v.checkLiqo},
		{SectionPeering, v.checkPeering},
		{SectionOffloading, v.checkOffloading},
		{SectionAgent, v.checkAgent},
		{SectionOperator, v.checkOperator},
	}

	for _, r := range runners {
		if !v.opts.sectionEnabled(r.name) {
			continue
		}
		report.Sections = append(report.Sections, r.fn(ctx))
	}
	return report, nil
}

func (v *Verifier) cs() kubernetes.Interface {
	if v.client == nil {
		return nil
	}
	return v.client.GetClientset()
}

func skippedSection(name, reason string) SectionResult {
	return SectionResult{
		Name: name,
		Checks: []Check{{
			Section: name,
			Name:    CheckNameScope,
			Status:  StatusSkip,
			Message: reason,
		}},
	}
}

func (v *Verifier) checkKubernetes(ctx context.Context) SectionResult {
	sec := SectionResult{Name: SectionKubernetes}
	cs := v.cs()
	if cs == nil {
		sec.Checks = append(sec.Checks, Check{
			Section: SectionKubernetes, Name: CheckNameAPI, Status: StatusFail,
			Message: "kubernetes client not available",
		})
		return sec
	}

	if err := waitForAPI(ctx, cs, DefaultAPITimeout); err != nil {
		sec.Checks = append(sec.Checks, Check{
			Section: SectionKubernetes, Name: CheckNameAPI, Status: StatusFail,
			Message: fmt.Sprintf("API not reachable: %v", err),
		})
		return sec
	}
	sec.Checks = append(sec.Checks, Check{
		Section: SectionKubernetes, Name: CheckNameAPI, Status: StatusPass,
		Message: "API reachable",
	})

	nodes, err := cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		sec.Checks = append(sec.Checks, Check{
			Section: SectionKubernetes, Name: CheckNameNodes, Status: StatusFail,
			Message: fmt.Sprintf("list nodes: %v", err),
		})
		return sec
	}
	ready := 0
	physical := 0
	virtual := 0
	for _, n := range nodes.Items {
		if !nodeReady(&n) {
			continue
		}
		ready++
		if isLiqoVirtualNode(&n) {
			virtual++
		} else {
			physical++
		}
	}
	if physical == 0 {
		sec.Checks = append(sec.Checks, Check{
			Section: SectionKubernetes, Name: CheckNameNodes, Status: StatusFail,
			Message: fmt.Sprintf("no Ready physical nodes (ready=%d virtual=%d)", ready, virtual),
		})
	} else {
		msg := fmt.Sprintf("%d physical node(s) Ready", physical)
		if virtual > 0 {
			msg += fmt.Sprintf(" (+%d virtual)", virtual)
		}
		sec.Checks = append(sec.Checks, Check{
			Section: SectionKubernetes, Name: CheckNameNodes, Status: StatusPass,
			Message: msg,
		})
	}
	return sec
}

func (v *Verifier) checkCalico(ctx context.Context) SectionResult {
	sec := SectionResult{Name: SectionCalico}
	cs := v.cs()
	if cs == nil {
		sec.Checks = append(sec.Checks, Check{
			Section: SectionCalico, Name: CheckNameNamespace, Status: StatusFail,
			Message: "kubernetes client not available",
		})
		return sec
	}

	nsOK := false
	for _, ns := range []string{config.DefaultCalicoSystemNamespace, config.DefaultTigeraOperatorNamespace} {
		if _, err := cs.CoreV1().Namespaces().Get(ctx, ns, metav1.GetOptions{}); err == nil {
			nsOK = true
			break
		}
	}
	if !nsOK {
		sec.Checks = append(sec.Checks, Check{
			Section: SectionCalico, Name: CheckNameNamespace, Status: StatusFail,
			Message: fmt.Sprintf("%s / %s namespace missing", config.DefaultCalicoSystemNamespace, config.DefaultTigeraOperatorNamespace),
		})
		return sec
	}
	sec.Checks = append(sec.Checks, Check{
		Section: SectionCalico, Name: CheckNameNamespace, Status: StatusPass,
		Message: "Calico namespaces present",
	})

	ds, err := cs.AppsV1().DaemonSets(config.DefaultCalicoSystemNamespace).Get(ctx, config.DefaultCalicoNodeDaemonSet, metav1.GetOptions{})
	if err != nil {
		sec.Checks = append(sec.Checks, Check{
			Section: SectionCalico, Name: CheckNameCalicoNode, Status: StatusFail,
			Message: fmt.Sprintf("calico-node DaemonSet: %v", err),
		})
		return sec
	}

	physicalReady, virtualReady, countErr := countReadyNodesByKind(ctx, cs)
	if countErr != nil {
		sec.Checks = append(sec.Checks, Check{
			Section: SectionCalico, Name: CheckNameCalicoNode, Status: StatusFail,
			Message: fmt.Sprintf("list nodes for calico check: %v", countErr),
		})
		return sec
	}
	// Virtual nodes inflate DaemonSet desired count; Calico must only be
	// ready on real provider nodes (pods on virtual nodes often stay unschedulable).
	if physicalReady == 0 {
		sec.Checks = append(sec.Checks, Check{
			Section: SectionCalico, Name: CheckNameCalicoNode, Status: StatusFail,
			Message: "no Ready physical nodes to run calico-node",
		})
		return sec
	}
	if int(ds.Status.NumberReady) < physicalReady {
		sec.Checks = append(sec.Checks, Check{
			Section: SectionCalico, Name: CheckNameCalicoNode, Status: StatusFail,
			Message: fmt.Sprintf("calico-node not ready on physical nodes (ready=%d physical=%d desired=%d virtual_nodes=%d)",
				ds.Status.NumberReady, physicalReady, ds.Status.DesiredNumberScheduled, virtualReady),
		})
		return sec
	}

	msg := fmt.Sprintf("calico-node ready on %d physical node(s)", physicalReady)
	if virtualReady > 0 && ds.Status.DesiredNumberScheduled > ds.Status.NumberReady {
		msg += fmt.Sprintf(" (DaemonSet also targets %d virtual node(s); ignored)", virtualReady)
	}
	sec.Checks = append(sec.Checks, Check{
		Section: SectionCalico, Name: CheckNameCalicoNode, Status: StatusPass,
		Message: msg,
	})
	return sec
}

func (v *Verifier) checkLiqo(ctx context.Context) SectionResult {
	sec := SectionResult{Name: SectionLiqo}
	cs := v.cs()
	if cs == nil {
		sec.Checks = append(sec.Checks, Check{
			Section: SectionLiqo, Name: CheckNameNamespace, Status: StatusFail,
			Message: "kubernetes client not available",
		})
		return sec
	}

	if _, err := cs.CoreV1().Namespaces().Get(ctx, config.DefaultLiqoNamespace, metav1.GetOptions{}); err != nil {
		sec.Checks = append(sec.Checks, Check{
			Section: SectionLiqo, Name: CheckNameNamespace, Status: StatusFail,
			Message: fmt.Sprintf("liqo namespace missing: %v", err),
		})
		return sec
	}
	sec.Checks = append(sec.Checks, Check{
		Section: SectionLiqo, Name: CheckNameNamespace, Status: StatusPass,
		Message: "liqo namespace present",
	})

	if _, err := exec.LookPath(config.LiqoctlBinary); err != nil {
		sec.Checks = append(sec.Checks, Check{
			Section: SectionLiqo, Name: CheckNameHealth, Status: StatusFail,
			Message: fmt.Sprintf("%s not on PATH (run register once, or ensure ~/.nr/bin is on PATH)", config.LiqoctlBinary),
		})
		return sec
	}

	output, err := runLiqoctlJSON(ctx, v.opts.KubeconfigPath, "info", "-o", config.LiqoctlOutputJSON, "-n", config.DefaultLiqoNamespace)
	if err != nil {
		sec.Checks = append(sec.Checks, Check{
			Section: SectionLiqo, Name: CheckNameHealth, Status: StatusFail,
			Message: err.Error(),
		})
		return sec
	}
	if !liqoInfoReady(output) {
		sec.Checks = append(sec.Checks, Check{
			Section: SectionLiqo, Name: CheckNameHealth, Status: StatusFail,
			Message: "liqoctl info does not report healthy Liqo with a local cluster ID",
		})
		return sec
	}
	sec.Checks = append(sec.Checks, Check{
		Section: SectionLiqo, Name: CheckNameHealth, Status: StatusPass,
		Message: "liqoctl info reports healthy with local cluster ID",
	})
	return sec
}

func (v *Verifier) checkPeering(ctx context.Context) SectionResult {
	sec := SectionResult{Name: SectionPeering}
	if _, err := exec.LookPath(config.LiqoctlBinary); err != nil {
		sec.Checks = append(sec.Checks, Check{
			Section: SectionPeering, Name: CheckNamePeer, Status: StatusFail,
			Message: fmt.Sprintf("%s not on PATH (run register once, or ensure ~/.nr/bin is on PATH)", config.LiqoctlBinary),
		})
		return sec
	}

	output, err := runLiqoctlJSON(ctx, v.opts.KubeconfigPath, "info", "peer", "-o", config.LiqoctlOutputJSON, "-n", config.DefaultLiqoNamespace)
	if err != nil {
		sec.Checks = append(sec.Checks, Check{
			Section: SectionPeering, Name: CheckNamePeer, Status: StatusFail,
			Message: err.Error(),
		})
		return sec
	}
	if !peeringComplete(output) {
		sec.Checks = append(sec.Checks, Check{
			Section: SectionPeering, Name: CheckNamePeer, Status: StatusFail,
			Message: "no accepted resource slice or virtual node for peered cluster",
		})
		return sec
	}
	sec.Checks = append(sec.Checks, Check{
		Section: SectionPeering, Name: CheckNamePeer, Status: StatusPass,
		Message: "peering complete (accepted resource slice or virtual node)",
	})
	return sec
}

func (v *Verifier) checkOffloading(ctx context.Context) SectionResult {
	if !v.opts.ExpectOffloading {
		return skippedSection(SectionOffloading, "offloading not expected (--disable-offloading or no namespaces)")
	}
	sec := SectionResult{Name: SectionOffloading}
	if v.client == nil {
		sec.Checks = append(sec.Checks, Check{
			Section: SectionOffloading, Name: CheckNameClient, Status: StatusFail,
			Message: "kubernetes client not available",
		})
		return sec
	}

	dyn, err := dynamic.NewForConfig(v.client.GetConfig())
	if err != nil {
		sec.Checks = append(sec.Checks, Check{
			Section: SectionOffloading, Name: CheckNameClient, Status: StatusFail,
			Message: fmt.Sprintf("dynamic client: %v", err),
		})
		return sec
	}

	gvr := schema.GroupVersionResource{
		Group:    config.LiqoOffloadingAPIGroup,
		Version:  config.LiqoOffloadingAPIVersion,
		Resource: config.LiqoNamespaceOffloadingResource,
	}

	for _, ns := range v.opts.OffloadNamespaces {
		list, listErr := dyn.Resource(gvr).Namespace(ns).List(ctx, metav1.ListOptions{})
		if listErr != nil {
			if apierrors.IsNotFound(listErr) || strings.Contains(listErr.Error(), "could not find") {
				sec.Checks = append(sec.Checks, Check{
					Section: SectionOffloading, Name: ns, Status: StatusFail,
					Message: "NamespaceOffloading CRD missing or API unavailable",
				})
				continue
			}
			sec.Checks = append(sec.Checks, Check{
				Section: SectionOffloading, Name: ns, Status: StatusFail,
				Message: fmt.Sprintf("list NamespaceOffloading: %v", listErr),
			})
			continue
		}
		if len(list.Items) == 0 {
			sec.Checks = append(sec.Checks, Check{
				Section: SectionOffloading, Name: ns, Status: StatusFail,
				Message: "no NamespaceOffloading resource in namespace",
			})
			continue
		}
		sec.Checks = append(sec.Checks, Check{
			Section: SectionOffloading, Name: ns, Status: StatusPass,
			Message: fmt.Sprintf("NamespaceOffloading present (%d)", len(list.Items)),
		})
	}
	if len(sec.Checks) == 0 {
		sec.Checks = append(sec.Checks, Check{
			Section: SectionOffloading, Name: CheckNameNamespaces, Status: StatusWarn,
			Message: "no offload namespaces configured to check",
		})
	}
	return sec
}

func (v *Verifier) checkAgent(ctx context.Context) SectionResult {
	if !v.opts.ExpectAgentAPI {
		return skippedSection(SectionAgent, "agent API checks disabled (no agent ID)")
	}
	sec := SectionResult{Name: SectionAgent}
	if v.agent == nil {
		sec.Checks = append(sec.Checks, Check{
			Section: SectionAgent, Name: CheckNameAPI, Status: StatusFail,
			Message: "agent status fetcher not configured",
		})
		return sec
	}

	st, err := v.agent.FetchAgentStatus(ctx, v.opts.AgentID)
	if err != nil {
		sec.Checks = append(sec.Checks, Check{
			Section: SectionAgent, Name: CheckNameAPI, Status: StatusFail,
			Message: fmt.Sprintf("get agent: %v", err),
		})
		return sec
	}

	if st.Provisioned {
		sec.Checks = append(sec.Checks, Check{
			Section: SectionAgent, Name: CheckNameProvisioned, Status: StatusPass,
			Message: "agent marked provisioned",
		})
	} else {
		sec.Checks = append(sec.Checks, Check{
			Section: SectionAgent, Name: CheckNameProvisioned, Status: StatusFail,
			Message: "agent not provisioned yet",
		})
	}

	state := strings.ToUpper(strings.TrimSpace(st.InboundPeeringState))
	ready := state == config.InboundPeeringStateReadyProto || state == config.InboundPeeringStateReadyShort
	if ready {
		sec.Checks = append(sec.Checks, Check{
			Section: SectionAgent, Name: CheckNameInboundPeering, Status: StatusPass,
			Message: "inbound peering READY",
		})
	} else {
		msg := fmt.Sprintf("inbound peering state=%s", st.InboundPeeringState)
		if st.InboundPeeringError != "" {
			msg += ": " + st.InboundPeeringError
		}
		sec.Checks = append(sec.Checks, Check{
			Section: SectionAgent, Name: CheckNameInboundPeering, Status: StatusFail,
			Message: msg,
		})
	}

	svc := strings.ToUpper(strings.TrimSpace(st.ServiceStatus))
	switch {
	case svc == "" || svc == config.ServiceStatusUnspecified:
		sec.Checks = append(sec.Checks, Check{
			Section: SectionAgent, Name: CheckNameServiceStatus, Status: StatusWarn,
			Message: "service status not set",
		})
	case strings.Contains(svc, config.ServiceStatusOnlineToken):
		sec.Checks = append(sec.Checks, Check{
			Section: SectionAgent, Name: CheckNameServiceStatus, Status: StatusPass,
			Message: "service status ONLINE",
		})
	default:
		sec.Checks = append(sec.Checks, Check{
			Section: SectionAgent, Name: CheckNameServiceStatus, Status: StatusWarn,
			Message: fmt.Sprintf("service status=%s", st.ServiceStatus),
		})
	}
	return sec
}

func (v *Verifier) checkOperator(ctx context.Context) SectionResult {
	sec := SectionResult{Name: SectionOperator}
	cs := v.cs()
	if cs == nil {
		sec.Checks = append(sec.Checks, Check{
			Section: SectionOperator, Name: CheckNameClient, Status: StatusFail,
			Message: "kubernetes client not available",
		})
		return sec
	}

	// Always check the remote operator namespace reflected after inbound peering.
	if v.opts.ExpectOperatorRemoteNS {
		remoteNS := v.opts.operatorRemoteNamespace()
		if _, err := cs.CoreV1().Namespaces().Get(ctx, remoteNS, metav1.GetOptions{}); err != nil {
			sec.Checks = append(sec.Checks, Check{
				Section: SectionOperator, Name: CheckNameRemoteNamespace, Status: StatusFail,
				Message: fmt.Sprintf("expected reflected namespace %q missing: %v", remoteNS, err),
			})
		} else {
			sec.Checks = append(sec.Checks, Check{
				Section: SectionOperator, Name: CheckNameRemoteNamespace, Status: StatusPass,
				Message: fmt.Sprintf("namespace %q present (remote offload)", remoteNS),
			})
		}
	}

	if !v.opts.ExpectOperator {
		sec.Checks = append(sec.Checks, Check{
			Section: SectionOperator, Name: CheckNameHelmWorkloads, Status: StatusSkip,
			Message: "operator Helm install not expected (--skip-operator-install)",
		})
		return sec
	}

	helmNS := v.opts.helmNamespace()
	vncNS := v.opts.vncNamespace()

	if _, err := cs.CoreV1().Namespaces().Get(ctx, helmNS, metav1.GetOptions{}); err != nil {
		sec.Checks = append(sec.Checks, Check{
			Section: SectionOperator, Name: CheckNameHelmNamespace, Status: StatusFail,
			Message: fmt.Sprintf("namespace %s missing: %v", helmNS, err),
		})
		return sec
	}
	sec.Checks = append(sec.Checks, Check{
		Section: SectionOperator, Name: CheckNameHelmNamespace, Status: StatusPass,
		Message: fmt.Sprintf("namespace %s present", helmNS),
	})

	// CRDs installed by the hypervisor operator CRDs subchart on the provider.
	groups, err := cs.Discovery().ServerGroups()
	if err != nil {
		sec.Checks = append(sec.Checks, Check{
			Section: SectionOperator, Name: CheckNameCRDs, Status: StatusFail,
			Message: fmt.Sprintf("discovery: %v", err),
		})
	} else {
		crdGroup := v.opts.crdAPIGroup()
		found := false
		for _, g := range groups.Groups {
			if g.Name == crdGroup {
				found = true
				break
			}
		}
		if !found {
			sec.Checks = append(sec.Checks, Check{
				Section: SectionOperator, Name: CheckNameCRDs, Status: StatusFail,
				Message: fmt.Sprintf("API group %s not registered (CRDs missing)", crdGroup),
			})
		} else {
			sec.Checks = append(sec.Checks, Check{
				Section: SectionOperator, Name: CheckNameCRDs, Status: StatusPass,
				Message: fmt.Sprintf("API group %s registered", crdGroup),
			})
		}
	}

	sec.Checks = append(sec.Checks, checkDeployment(ctx, cs, SectionOperator, CheckNameOperatorDeploy,
		helmNS, v.opts.operatorDeployName())...)
	sec.Checks = append(sec.Checks, checkDaemonSetOnPhysicalNodes(ctx, cs, SectionOperator, CheckNameAlloy,
		helmNS, v.opts.alloyDaemonSetName())...)
	sec.Checks = append(sec.Checks, checkDeployment(ctx, cs, SectionOperator, CheckNameExporter,
		helmNS, v.opts.exporterDeployName())...)

	if _, err := cs.CoreV1().Namespaces().Get(ctx, vncNS, metav1.GetOptions{}); err != nil {
		sec.Checks = append(sec.Checks, Check{
			Section: SectionOperator, Name: CheckNameVNCNamespace, Status: StatusFail,
			Message: fmt.Sprintf("namespace %s missing: %v", vncNS, err),
		})
	} else {
		sec.Checks = append(sec.Checks, Check{
			Section: SectionOperator, Name: CheckNameVNCNamespace, Status: StatusPass,
			Message: fmt.Sprintf("namespace %s present", vncNS),
		})
		sec.Checks = append(sec.Checks, checkDeployment(ctx, cs, SectionOperator, CheckNameVNCGateway,
			vncNS, v.opts.vncGatewayDeployName())...)
	}

	if secret, err := cs.CoreV1().Secrets(helmNS).Get(ctx, config.DefaultMimirCredentialsSecret, metav1.GetOptions{}); err != nil {
		sec.Checks = append(sec.Checks, Check{
			Section: SectionOperator, Name: CheckNameMimirSecret, Status: StatusFail,
			Message: fmt.Sprintf("%s secret missing in %s: %v", config.DefaultMimirCredentialsSecret, helmNS, err),
		})
	} else {
		token := string(secret.Data[config.EnvMimirBearerToken])
		if token == "" {
			sec.Checks = append(sec.Checks, Check{
				Section: SectionOperator, Name: CheckNameMimirSecret, Status: StatusFail,
				Message: fmt.Sprintf("%s present but %s key is empty", config.DefaultMimirCredentialsSecret, config.EnvMimirBearerToken),
			})
		} else {
			sec.Checks = append(sec.Checks, Check{
				Section: SectionOperator, Name: CheckNameMimirSecret, Status: StatusPass,
				Message: fmt.Sprintf("%s has %s", config.DefaultMimirCredentialsSecret, config.EnvMimirBearerToken),
			})
		}
	}

	return sec
}

func checkDeployment(ctx context.Context, cs kubernetes.Interface, section, name, ns, deploy string) []Check {
	dep, err := cs.AppsV1().Deployments(ns).Get(ctx, deploy, metav1.GetOptions{})
	if err != nil {
		return []Check{{
			Section: section, Name: name, Status: StatusFail,
			Message: fmt.Sprintf("deployment %s/%s: %v", ns, deploy, err),
		}}
	}
	if !deploymentReady(dep) {
		want := DefaultDeploymentReplicas
		if dep.Spec.Replicas != nil {
			want = *dep.Spec.Replicas
		}
		return []Check{{
			Section: section, Name: name, Status: StatusFail,
			Message: fmt.Sprintf("%s/%s not ready (ready=%d want=%d)",
				ns, deploy, dep.Status.ReadyReplicas, want),
		}}
	}
	return []Check{{
		Section: section, Name: name, Status: StatusPass,
		Message: fmt.Sprintf("%s/%s ready (%d replicas)", ns, deploy, dep.Status.ReadyReplicas),
	}}
}

// checkDaemonSetOnPhysicalNodes ignores virtual-node inflation of DesiredNumberScheduled.
func checkDaemonSetOnPhysicalNodes(ctx context.Context, cs kubernetes.Interface, section, name, ns, dsName string) []Check {
	ds, err := cs.AppsV1().DaemonSets(ns).Get(ctx, dsName, metav1.GetOptions{})
	if err != nil {
		return []Check{{
			Section: section, Name: name, Status: StatusFail,
			Message: fmt.Sprintf("daemonset %s/%s: %v", ns, dsName, err),
		}}
	}
	physicalReady, virtualReady, countErr := countReadyNodesByKind(ctx, cs)
	if countErr != nil {
		return []Check{{
			Section: section, Name: name, Status: StatusFail,
			Message: fmt.Sprintf("list nodes for %s check: %v", name, countErr),
		}}
	}
	if physicalReady == 0 {
		return []Check{{
			Section: section, Name: name, Status: StatusFail,
			Message: fmt.Sprintf("no Ready physical nodes to run %s", dsName),
		}}
	}
	if int(ds.Status.NumberReady) < physicalReady {
		return []Check{{
			Section: section, Name: name, Status: StatusFail,
			Message: fmt.Sprintf("%s/%s not ready on physical nodes (ready=%d physical=%d desired=%d virtual_nodes=%d)",
				ns, dsName, ds.Status.NumberReady, physicalReady, ds.Status.DesiredNumberScheduled, virtualReady),
		}}
	}
	msg := fmt.Sprintf("%s/%s ready on %d physical node(s)", ns, dsName, physicalReady)
	if virtualReady > 0 && ds.Status.DesiredNumberScheduled > ds.Status.NumberReady {
		msg += fmt.Sprintf(" (DaemonSet also targets %d virtual node(s); ignored)", virtualReady)
	}
	return []Check{{
		Section: section, Name: name, Status: StatusPass,
		Message: msg,
	}}
}

func nodeReady(n *corev1.Node) bool {
	for _, c := range n.Status.Conditions {
		if c.Type == corev1.NodeReady && c.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func isLiqoVirtualNode(n *corev1.Node) bool {
	if n == nil {
		return false
	}
	if n.Labels[config.LiqoLabelNodeType] == config.LiqoNodeTypeVirtual {
		return true
	}
	// Older / alternate Liqo labels.
	if n.Labels[config.LiqoLabelRemoteClusterID] != "" && n.Labels[config.LiqoLabelNodeType] != "" {
		return strings.Contains(strings.ToLower(n.Labels[config.LiqoLabelNodeType]), "virtual")
	}
	return false
}

func countReadyNodesByKind(ctx context.Context, cs kubernetes.Interface) (physical, virtual int, err error) {
	nodes, err := cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return 0, 0, err
	}
	for i := range nodes.Items {
		n := &nodes.Items[i]
		if !nodeReady(n) {
			continue
		}
		if isLiqoVirtualNode(n) {
			virtual++
		} else {
			physical++
		}
	}
	return physical, virtual, nil
}

func deploymentReady(dep *appsv1.Deployment) bool {
	want := DefaultDeploymentReplicas
	if dep.Spec.Replicas != nil {
		want = *dep.Spec.Replicas
	}
	if want == 0 {
		return true
	}
	return dep.Status.ReadyReplicas >= want && dep.Status.UpdatedReplicas >= want
}

func waitForAPI(ctx context.Context, cs kubernetes.Interface, timeout time.Duration) error {
	if _, err := cs.Discovery().ServerVersion(); err == nil {
		return nil
	}
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(APIPollInterval)
	defer ticker.Stop()
	var lastErr error
	for {
		select {
		case <-ctx.Done():
			if lastErr != nil {
				return lastErr
			}
			return ctx.Err()
		case <-ticker.C:
			if time.Now().After(deadline) {
				if lastErr != nil {
					return lastErr
				}
				return fmt.Errorf("timeout waiting for Kubernetes API")
			}
			if _, lastErr = cs.Discovery().ServerVersion(); lastErr == nil {
				return nil
			}
		}
	}
}
