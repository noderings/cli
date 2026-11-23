package install

import (
	"strings"

	corev1 "k8s.io/api/core/v1"

	"github.com/noderings/cli/internal/config"
)

func nodeReady(n corev1.Node) bool {
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
	if n.Labels[config.LiqoLabelRemoteClusterID] != "" && n.Labels[config.LiqoLabelNodeType] != "" {
		return strings.Contains(strings.ToLower(n.Labels[config.LiqoLabelNodeType]), "virtual")
	}
	return false
}

func countReadyPhysicalNodes(nodes []corev1.Node) int {
	n := 0
	for i := range nodes {
		node := &nodes[i]
		if !nodeReady(*node) || isLiqoVirtualNode(node) {
			continue
		}
		n++
	}
	return n
}

// calicoReadyIgnoringVirtualNodes is true when Calico is Ready on every physical
// node. Liqo virtual nodes inflate DaemonSet DesiredNumberScheduled and never
// run calico-node, so requiring NumberReady == DesiredNumberScheduled hangs
// re-register after a leftover peering.
func calicoReadyIgnoringVirtualNodes(numberReady, physicalReady int) bool {
	return physicalReady > 0 && numberReady >= physicalReady
}
