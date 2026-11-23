package verify

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/noderings/cli/internal/config"
)

func TestOptionsFromRegister_full(t *testing.T) {
	opts := OptionsFromRegister(RegisterScope{
		AgentID:             "agent-1",
		DisableOffloading:   false,
		SkipOperatorInstall: false,
		VNCGatewayNamespace: "",
		OffloadNamespaces:   []string{"extra-ns"},
	})
	if !opts.ExpectOffloading {
		t.Fatal("expected offloading")
	}
	if !opts.ExpectOperator {
		t.Fatal("expected operator")
	}
	if !opts.ExpectAgentAPI {
		t.Fatal("expected agent API")
	}
	if !opts.ExpectOperatorRemoteNS {
		t.Fatal("expected operator remote ns")
	}
	if len(opts.OffloadNamespaces) < 2 {
		t.Fatalf("offload namespaces=%v want vnc + extra", opts.OffloadNamespaces)
	}
	if opts.OffloadNamespaces[0] != config.DefaultVNCGatewayNamespace {
		t.Fatalf("first offload ns=%s want %s", opts.OffloadNamespaces[0], config.DefaultVNCGatewayNamespace)
	}
}

func TestOptionsFromRegister_skipOperator(t *testing.T) {
	opts := OptionsFromRegister(RegisterScope{
		AgentID:             "a",
		SkipOperatorInstall: true,
	})
	if opts.ExpectOperator {
		t.Fatal("operator should not be expected")
	}
	if !opts.ExpectOperatorRemoteNS {
		t.Fatal("remote operator ns still expected after inbound peering")
	}
	if !opts.ExpectOffloading {
		t.Fatal("offloading still expected by default")
	}
}

func TestOptionsFromRegister_disableOffloading(t *testing.T) {
	opts := OptionsFromRegister(RegisterScope{
		AgentID:           "a",
		DisableOffloading: true,
		OffloadNamespaces: []string{"should-ignore"},
	})
	if opts.ExpectOffloading {
		t.Fatal("offloading should not be expected")
	}
	if len(opts.OffloadNamespaces) != 0 {
		t.Fatalf("offload namespaces should be empty, got %v", opts.OffloadNamespaces)
	}
}

func TestOptionsFromRegister_explicitOperatorNamespaceOffload(t *testing.T) {
	opts := OptionsFromRegister(RegisterScope{
		AgentID:           "a",
		OperatorNamespace: "custom-op",
	})
	found := false
	for _, ns := range opts.OffloadNamespaces {
		if ns == "custom-op" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected custom-op in %v", opts.OffloadNamespaces)
	}
}

func TestSectionEnabled(t *testing.T) {
	opts := Options{OnlySections: []string{"Liqo", "operator"}}
	if !opts.sectionEnabled(SectionLiqo) {
		t.Fatal("liqo should be enabled")
	}
	if !opts.sectionEnabled(SectionOperator) {
		t.Fatal("operator should be enabled")
	}
	if opts.sectionEnabled(SectionCalico) {
		t.Fatal("calico should be disabled")
	}
	all := Options{}
	if !all.sectionEnabled(SectionCalico) {
		t.Fatal("empty OnlySections enables all")
	}
}

func TestReportPassed(t *testing.T) {
	r := &Report{Sections: []SectionResult{{
		Name: "x",
		Checks: []Check{
			{Status: StatusPass},
			{Status: StatusWarn},
			{Status: StatusSkip},
		},
	}}}
	if !r.Passed() || r.FailedCount() != 0 {
		t.Fatal("warn/skip must not fail report")
	}
	r.Sections[0].Checks = append(r.Sections[0].Checks, Check{Status: StatusFail})
	if r.Passed() || r.FailedCount() != 1 {
		t.Fatal("fail must fail report")
	}
}

func TestLiqoInfoReady(t *testing.T) {
	if liqoInfoReady(`{"health":{"healthy":true},"local":{"clusterID":""}}`) {
		t.Fatal("empty cluster ID must fail")
	}
	if !liqoInfoReady(`{"health":{"healthy":true},"local":{"clusterID":"abc"}}`) {
		t.Fatal("healthy with cluster ID must pass")
	}
	if liqoInfoReady(`{"health":{"healthy":false},"local":{"clusterID":"abc"}}`) {
		t.Fatal("unhealthy must fail")
	}
}

func TestPeeringComplete(t *testing.T) {
	ok := `{"peer-a":{"authentication":{"status":"Healthy","resourceSlices":[{"accepted":true}]},"network":{"status":"Healthy"},"offloading":{"virtualNodes":[]}}}`
	if !peeringComplete(ok) {
		t.Fatal("accepted slice should complete peering")
	}
	vn := `{"peer-a":{"authentication":{"status":"Healthy","resourceSlices":[]},"network":{"status":"Healthy"},"offloading":{"virtualNodes":[{"name":"vn"}]}}}`
	if !peeringComplete(vn) {
		t.Fatal("virtual node should complete peering")
	}
	bad := `{"peer-a":{"authentication":{"status":"Healthy","resourceSlices":[{"accepted":false}]},"network":{"status":"Healthy"},"offloading":{"virtualNodes":[]}}}`
	if peeringComplete(bad) {
		t.Fatal("unaccepted slice without VN must not complete")
	}
}

func TestIsLiqoVirtualNode(t *testing.T) {
	physical := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "nr-provider"}}
	if isLiqoVirtualNode(physical) {
		t.Fatal("physical node must not be virtual")
	}

	virtual := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name:   "remote-cluster",
		Labels: map[string]string{config.LiqoLabelNodeType: config.LiqoNodeTypeVirtual},
	}}
	if !isLiqoVirtualNode(virtual) {
		t.Fatal("liqo.io/type=virtual-node must be detected")
	}
}

func TestSummaryLine(t *testing.T) {
	r := &Report{Sections: []SectionResult{{
		Checks: []Check{{Status: StatusPass}, {Status: StatusSkip}},
	}}}
	if !strings.Contains(SummaryLine(r), "verify OK") {
		t.Fatalf("got %q", SummaryLine(r))
	}
	r.Sections[0].Checks = append(r.Sections[0].Checks, Check{Status: StatusFail})
	if !strings.Contains(SummaryLine(r), "verify FAILED") {
		t.Fatalf("got %q", SummaryLine(r))
	}
}

func TestValidateOnlySections(t *testing.T) {
	if err := ValidateOnlySections(nil); err != nil {
		t.Fatal(err)
	}
	if err := ValidateOnlySections([]string{"kubernetes", "liqo"}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateOnlySections([]string{"nope"}); err == nil {
		t.Fatal("expected unknown section error")
	}
}

func TestOptionsFromRegister_helmEnv(t *testing.T) {
	t.Setenv(config.EnvHelmNamespace, "custom-helm")
	t.Setenv(config.EnvHelmRelease, "custom-rel")
	t.Setenv(config.EnvVNCGatewayNamespace, "custom-vnc")
	opts := OptionsFromRegister(RegisterScope{AgentID: "a"})
	if opts.OperatorNamespace != "custom-helm" {
		t.Fatalf("helm ns=%s", opts.OperatorNamespace)
	}
	if opts.OperatorRelease != "custom-rel" {
		t.Fatalf("helm release=%s", opts.OperatorRelease)
	}
	if opts.VNCGatewayNamespace != "custom-vnc" {
		t.Fatalf("vnc ns=%s", opts.VNCGatewayNamespace)
	}
	// Explicit scope wins over env for VNC.
	opts = OptionsFromRegister(RegisterScope{AgentID: "a", VNCGatewayNamespace: "flag-vnc"})
	if opts.VNCGatewayNamespace != "flag-vnc" {
		t.Fatalf("flag should win, got %s", opts.VNCGatewayNamespace)
	}
}

func TestOptionsFromRegister_virtfusion(t *testing.T) {
	t.Setenv(config.EnvHelmNamespace, "")
	opts := OptionsFromRegister(RegisterScope{
		AgentID:          "a",
		HypervisorDriver: config.HypervisorDriverVirtFusion,
	})
	if opts.HypervisorDriver != config.HypervisorDriverVirtFusion {
		t.Fatalf("driver=%s", opts.HypervisorDriver)
	}
	if opts.OperatorNamespace != config.DefaultVirtFusionOperatorHelmNamespace {
		t.Fatalf("helm ns=%s want %s", opts.OperatorNamespace, config.DefaultVirtFusionOperatorHelmNamespace)
	}
	if opts.helmNamespace() != config.DefaultVirtFusionOperatorHelmNamespace {
		t.Fatalf("helmNamespace()=%s", opts.helmNamespace())
	}
	if opts.operatorDeployName() != config.HelmVirtFusionOperatorDeployName(opts.OperatorRelease) {
		t.Fatalf("operator deploy=%s", opts.operatorDeployName())
	}
	if opts.exporterDeployName() != config.HelmVirtFusionExporterDeployName(opts.OperatorRelease) {
		t.Fatalf("exporter deploy=%s", opts.exporterDeployName())
	}
	if opts.vncGatewayDeployName() != config.HelmVirtFusionVNCGatewayDeployName(opts.OperatorRelease) {
		t.Fatalf("vnc deploy=%s", opts.vncGatewayDeployName())
	}
	if opts.crdAPIGroup() != config.VirtFusionCRDAPIGroup {
		t.Fatalf("crd group=%s", opts.crdAPIGroup())
	}

	t.Setenv(config.EnvHelmNamespace, "custom-vf-helm")
	opts = OptionsFromRegister(RegisterScope{
		AgentID:          "a",
		HypervisorDriver: config.HypervisorDriverVirtFusion,
	})
	if opts.OperatorNamespace != "custom-vf-helm" {
		t.Fatalf("explicit HELM_NAMESPACE should win, got %s", opts.OperatorNamespace)
	}
}

func TestOptionsFromRegister_solusvm(t *testing.T) {
	t.Setenv(config.EnvHelmNamespace, "")
	opts := OptionsFromRegister(RegisterScope{
		AgentID:          "a",
		HypervisorDriver: config.HypervisorDriverSolusVM,
	})
	if opts.HypervisorDriver != config.HypervisorDriverSolusVM {
		t.Fatalf("driver=%s", opts.HypervisorDriver)
	}
	if opts.OperatorNamespace != config.DefaultSolusVMOperatorHelmNamespace {
		t.Fatalf("helm ns=%s want %s", opts.OperatorNamespace, config.DefaultSolusVMOperatorHelmNamespace)
	}
	if opts.helmNamespace() != config.DefaultSolusVMOperatorHelmNamespace {
		t.Fatalf("helmNamespace()=%s", opts.helmNamespace())
	}
	if opts.operatorDeployName() != config.HelmSolusVMOperatorDeployName(opts.OperatorRelease) {
		t.Fatalf("operator deploy=%s", opts.operatorDeployName())
	}
	if opts.exporterDeployName() != config.HelmSolusVMExporterDeployName(opts.OperatorRelease) {
		t.Fatalf("exporter deploy=%s", opts.exporterDeployName())
	}
	if opts.vncGatewayDeployName() != config.HelmSolusVMVNCGatewayDeployName(opts.OperatorRelease) {
		t.Fatalf("vnc deploy=%s", opts.vncGatewayDeployName())
	}
	if opts.crdAPIGroup() != config.SolusVMCRDAPIGroup {
		t.Fatalf("crd group=%s", opts.crdAPIGroup())
	}
}
