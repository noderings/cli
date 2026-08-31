package verify

import (
	"context"
	"errors"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	k8stesting "k8s.io/client-go/testing"

	"github.com/noderings/cli/internal/config"
)

type fakeKubeClient struct {
	clientset kubernetes.Interface
	config    *rest.Config
}

func (f fakeKubeClient) GetClientset() kubernetes.Interface { return f.clientset }
func (f fakeKubeClient) GetConfig() *rest.Config            { return f.config }

type fakeAgentFetcher struct {
	status *AgentStatus
	err    error
}

func (f fakeAgentFetcher) FetchAgentStatus(context.Context, string) (*AgentStatus, error) {
	return f.status, f.err
}

func readyNode(name string, virtual bool) *corev1.Node {
	labels := map[string]string{}
	if virtual {
		labels[config.LiqoLabelNodeType] = config.LiqoNodeTypeVirtual
	}
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels},
		Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{{
			Type: corev1.NodeReady, Status: corev1.ConditionTrue,
		}}},
	}
}

func findCheck(t *testing.T, sec SectionResult, name string) Check {
	t.Helper()
	for _, check := range sec.Checks {
		if check.Name == name {
			return check
		}
	}
	t.Fatalf("check %q not found in %#v", name, sec.Checks)
	return Check{}
}

func TestVerifierRunHonorsSectionFilter(t *testing.T) {
	v := newVerifier(Options{
		AgentID:        "agent-1",
		OnlySections:   []string{SectionAgent, SectionOffloading},
		ExpectAgentAPI: false,
	}, nil, nil)

	report, err := v.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.AgentID != "agent-1" {
		t.Fatalf("agent ID=%q", report.AgentID)
	}
	if len(report.Sections) != 2 {
		t.Fatalf("sections=%v", report.Sections)
	}
	for _, section := range report.Sections {
		if findCheck(t, section, CheckNameScope).Status != StatusSkip {
			t.Fatalf("section %s should be skipped", section.Name)
		}
	}
}

func TestCheckKubernetesCountsOnlyReadyPhysicalNodes(t *testing.T) {
	notReady := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "not-ready"}}
	clientset := fake.NewClientset(
		readyNode("provider", false),
		readyNode("virtual", true),
		notReady,
	)
	v := newVerifier(Options{}, fakeKubeClient{clientset: clientset}, nil)

	section := v.checkKubernetes(context.Background())
	if got := findCheck(t, section, CheckNameAPI).Status; got != StatusPass {
		t.Fatalf("API status=%s", got)
	}
	nodes := findCheck(t, section, CheckNameNodes)
	if nodes.Status != StatusPass {
		t.Fatalf("nodes=%#v", nodes)
	}
	if !strings.Contains(nodes.Message, "1 physical") || !strings.Contains(nodes.Message, "+1 virtual") {
		t.Fatalf("unexpected node message %q", nodes.Message)
	}
}

func TestCheckKubernetesWithoutClientFails(t *testing.T) {
	section := newVerifier(Options{}, nil, nil).checkKubernetes(context.Background())
	if got := findCheck(t, section, CheckNameAPI).Status; got != StatusFail {
		t.Fatalf("status=%s", got)
	}
}

func TestCheckCalicoIgnoresVirtualNodeDaemonSetInflation(t *testing.T) {
	clientset := fake.NewClientset(
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: config.DefaultCalicoSystemNamespace}},
		readyNode("provider", false),
		readyNode("virtual", true),
		&appsv1.DaemonSet{
			ObjectMeta: metav1.ObjectMeta{
				Name:      config.DefaultCalicoNodeDaemonSet,
				Namespace: config.DefaultCalicoSystemNamespace,
			},
			Status: appsv1.DaemonSetStatus{
				DesiredNumberScheduled: 2,
				NumberReady:            1,
			},
		},
	)
	v := newVerifier(Options{}, fakeKubeClient{clientset: clientset}, nil)

	section := v.checkCalico(context.Background())
	check := findCheck(t, section, CheckNameCalicoNode)
	if check.Status != StatusPass {
		t.Fatalf("calico check=%#v", check)
	}
	if !strings.Contains(check.Message, "ignored") {
		t.Fatalf("expected virtual-node explanation, got %q", check.Message)
	}
}

func TestCheckAgentStatusBranches(t *testing.T) {
	tests := []struct {
		name      string
		fetcher   AgentStatusFetcher
		want      map[string]Status
		messageIn string
	}{
		{
			name: "ready",
			fetcher: fakeAgentFetcher{status: &AgentStatus{
				Provisioned: true, InboundPeeringState: "ready", ServiceStatus: "service_status_online",
			}},
			want: map[string]Status{
				CheckNameProvisioned: StatusPass, CheckNameInboundPeering: StatusPass, CheckNameServiceStatus: StatusPass,
			},
		},
		{
			name: "not ready",
			fetcher: fakeAgentFetcher{status: &AgentStatus{
				InboundPeeringState: "ERROR", InboundPeeringError: "peer failed", ServiceStatus: "OFFLINE",
			}},
			want: map[string]Status{
				CheckNameProvisioned: StatusFail, CheckNameInboundPeering: StatusFail, CheckNameServiceStatus: StatusWarn,
			},
			messageIn: "peer failed",
		},
		{
			name:      "fetch error",
			fetcher:   fakeAgentFetcher{err: errors.New("API down")},
			want:      map[string]Status{CheckNameAPI: StatusFail},
			messageIn: "API down",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := newVerifier(Options{AgentID: "agent-1", ExpectAgentAPI: true}, nil, tt.fetcher)
			section := v.checkAgent(context.Background())
			for name, want := range tt.want {
				check := findCheck(t, section, name)
				if check.Status != want {
					t.Fatalf("%s status=%s want %s", name, check.Status, want)
				}
				if tt.messageIn != "" && !strings.Contains(check.Message, tt.messageIn) &&
					name != CheckNameProvisioned && name != CheckNameServiceStatus {
					t.Fatalf("%s message=%q, want %q", name, check.Message, tt.messageIn)
				}
			}
		})
	}
}

func TestCheckAgentDisabledAndMissingFetcher(t *testing.T) {
	disabled := newVerifier(Options{}, nil, nil).checkAgent(context.Background())
	if findCheck(t, disabled, CheckNameScope).Status != StatusSkip {
		t.Fatal("disabled agent check should skip")
	}

	missing := newVerifier(Options{ExpectAgentAPI: true}, nil, nil).checkAgent(context.Background())
	if findCheck(t, missing, CheckNameAPI).Status != StatusFail {
		t.Fatal("missing fetcher should fail")
	}
}

func TestCheckDeployment(t *testing.T) {
	replicas := int32(2)
	ready := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "ready", Namespace: "ns"},
		Spec:       appsv1.DeploymentSpec{Replicas: &replicas},
		Status:     appsv1.DeploymentStatus{ReadyReplicas: 2, UpdatedReplicas: 2},
	}
	unready := ready.DeepCopy()
	unready.Name = "unready"
	unready.Status.ReadyReplicas = 1
	clientset := fake.NewClientset(ready, unready)

	tests := []struct {
		name string
		want Status
	}{
		{name: "ready", want: StatusPass},
		{name: "unready", want: StatusFail},
		{name: "missing", want: StatusFail},
	}
	for _, tt := range tests {
		checks := checkDeployment(context.Background(), clientset, SectionOperator, "deployment", "ns", tt.name)
		if len(checks) != 1 || checks[0].Status != tt.want {
			t.Fatalf("%s checks=%#v", tt.name, checks)
		}
	}
}

func TestCheckDaemonSetOnPhysicalNodes(t *testing.T) {
	clientset := fake.NewClientset(
		readyNode("provider", false),
		readyNode("virtual", true),
		&appsv1.DaemonSet{
			ObjectMeta: metav1.ObjectMeta{Name: "alloy", Namespace: "ns"},
			Status: appsv1.DaemonSetStatus{
				DesiredNumberScheduled: 2,
				NumberReady:            1,
			},
		},
	)
	checks := checkDaemonSetOnPhysicalNodes(context.Background(), clientset, SectionOperator, "alloy", "ns", "alloy")
	if len(checks) != 1 || checks[0].Status != StatusPass {
		t.Fatalf("checks=%#v", checks)
	}
}

func TestCheckOperatorSkipStillChecksRemoteNamespace(t *testing.T) {
	clientset := fake.NewClientset(&corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: config.DefaultOperatorRemoteNamespace},
	})
	v := newVerifier(Options{
		ExpectOperatorRemoteNS: true,
		ExpectOperator:         false,
	}, fakeKubeClient{clientset: clientset}, nil)

	section := v.checkOperator(context.Background())
	if findCheck(t, section, CheckNameRemoteNamespace).Status != StatusPass {
		t.Fatal("remote namespace should pass")
	}
	if findCheck(t, section, CheckNameHelmWorkloads).Status != StatusSkip {
		t.Fatal("Helm workloads should skip")
	}
}

func TestCountReadyNodesByKindAndDeploymentReady(t *testing.T) {
	clientset := fake.NewClientset(readyNode("provider", false), readyNode("virtual", true))
	physical, virtual, err := countReadyNodesByKind(context.Background(), clientset)
	if err != nil || physical != 1 || virtual != 1 {
		t.Fatalf("physical=%d virtual=%d err=%v", physical, virtual, err)
	}

	zero := int32(0)
	if !deploymentReady(&appsv1.Deployment{Spec: appsv1.DeploymentSpec{Replicas: &zero}}) {
		t.Fatal("zero-replica deployment should be ready")
	}
}

func TestCheckHelpersHandleListErrors(t *testing.T) {
	clientset := fake.NewClientset()
	clientset.PrependReactor("list", "nodes", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("list failed")
	})

	_, _, err := countReadyNodesByKind(context.Background(), clientset)
	if err == nil || !strings.Contains(err.Error(), "list failed") {
		t.Fatalf("err=%v", err)
	}
}
