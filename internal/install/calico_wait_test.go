package install

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/noderings/cli/internal/config"
)

func TestCalicoReadyIgnoringVirtualNodes(t *testing.T) {
	t.Parallel()

	if calicoReadyIgnoringVirtualNodes(1, 0) {
		t.Fatal("no physical nodes must not be ready")
	}
	if !calicoReadyIgnoringVirtualNodes(1, 1) {
		t.Fatal("1 ready on 1 physical node (leftover virtual-node Desired=2 is ignored)")
	}
	if calicoReadyIgnoringVirtualNodes(1, 2) {
		t.Fatal("1 ready on 2 physical nodes must wait")
	}
}

func TestCountReadyPhysicalNodesIgnoresLiqoVirtual(t *testing.T) {
	t.Parallel()

	physical := corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "nr-agent-pve"},
		Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{{
			Type: corev1.NodeReady, Status: corev1.ConditionTrue,
		}}},
	}
	virtual := corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "nr-mothership",
			Labels: map[string]string{
				config.LiqoLabelNodeType: config.LiqoNodeTypeVirtual,
			},
		},
		Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{{
			Type: corev1.NodeReady, Status: corev1.ConditionFalse,
		}}},
	}
	if got := countReadyPhysicalNodes([]corev1.Node{physical, virtual}); got != 1 {
		t.Fatalf("physical=%d want 1", got)
	}
	if isLiqoVirtualNode(&physical) {
		t.Fatal("physical node must not be virtual")
	}
	if !isLiqoVirtualNode(&virtual) {
		t.Fatal("liqo.io/type=virtual-node must be detected")
	}
}
