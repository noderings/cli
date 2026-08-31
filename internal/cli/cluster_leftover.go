package cli

import (
	"context"
	"fmt"

	"github.com/noderings/cli/internal/install"
)

// leftoverLocalClusterError is the delete-then-register trap: API agent is gone
// (or a new UUID was created) while this host still runs k3s/Liqo from the
// previous agent. Re-register then hangs on Calico because leftover virtual
// nodes inflate DaemonSet DesiredNumberScheduled.
func leftoverLocalClusterError(previousAgentID, newAgentID string, k3sPresent bool) error {
	if !k3sPresent || previousAgentID == "" || newAgentID == "" || previousAgentID == newAgentID {
		return nil
	}
	return fmt.Errorf(
		"this host still has k3s from agent %s, but this register is using %s.\n"+
			"nr agent delete only removes the API record; leftover Liqo virtual nodes make Calico wait forever.\n"+
			"Resume the original install (if that agent still exists):\n"+
			"  nr cluster register --resume --name <name> --yes\n"+
			"Or wipe local k3s then register:\n"+
			"  nr cluster deregister --name <name> --yes\n"+
			"  nr cluster register --name <name> --agent-ip <ip> --gateway-region <region> --yes\n"+
			"To replace k3s in place: nr cluster register --force --yes ...",
		previousAgentID, newAgentID,
	)
}

func rejectStaleLocalCluster(ctx context.Context, previousAgentID, newAgentID string, force bool) error {
	if force {
		return nil
	}
	k3s, _, _, _ := install.DetectInstalledComponents(ctx)
	return leftoverLocalClusterError(previousAgentID, newAgentID, k3s)
}

// agentDeleteLocalClusterGuard blocks API-only delete while k3s is still on
// this host. That is what turned an aborted operator prompt into a wedged
// second register. --keep-cluster opts into the old API-only behavior.
func agentDeleteLocalClusterGuard(k3sPresent, keepCluster bool) error {
	if !k3sPresent || keepCluster {
		return nil
	}
	return fmt.Errorf(
		"k3s is still installed on this host. Deleting the API agent leaves a half-installed cluster;\n" +
			"the next nr cluster register then hangs (Calico on leftover Liqo virtual nodes).\n" +
			"Finish credentials on the existing install:\n" +
			"  nr cluster register --resume --name <name> --yes\n" +
			"Wipe API agent and local k3s together:\n" +
			"  nr cluster deregister --name <name> --yes\n" +
			"API-only delete (not recommended): nr agent delete --name <name> --keep-cluster",
	)
}

func operatorCredentialRecoveryHint(agentName string) string {
	if agentName == "" {
		agentName = "<name>"
	}
	return fmt.Sprintf(
		"\nThe cluster is already peered; do not run nr agent delete. Resume with credentials:\n"+
			"  nr cluster register --resume --name %s --yes\n"+
			"  (pass --proxmox-instances-file / --virtfusion-instances-file / --solusvm-instances-file, or PROXMOX_* / VIRTFUSION_* / SOLUSVM_* env)\n"+
			"To wipe and start over:\n"+
			"  nr cluster deregister --name %s --yes",
		agentName, agentName,
	)
}
