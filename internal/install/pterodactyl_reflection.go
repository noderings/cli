package install

import (
	"context"
	"fmt"
	"slices"

	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/noderings/cli/internal/config"
	"github.com/noderings/cli/internal/k8s"
)

const (
	catalogAPIGroup = config.PterodactylCRDAPIGroup
	vkRoleLocal     = "liqo-virtual-kubelet-local"
	vkRoleRemote    = "liqo-virtual-kubelet-remote"
)

// pterodactylReflectedResources is every gs.pterodactyl.com type the virtual
// kubelet is told to reflect. A namespace reflector does not become ready
// until each of those lists succeeds, so a missing rule stalls the game
// catalog the same way it would stall a VirtFusion node.
var pterodactylReflectedResources = []string{
	"pterodactylservers",
	"pterodactylactions",
	"pterodactylgamecatalogs",
	"pterodactylcontrols",
	"pterodactylnodes",
	"pterodactylwebsocketrequests",
}

// PrepareGameCatalogReflection installs the operator CRDs and the Liqo role
// rules a new virtual-kubelet needs to reflect Pterodactyl resources.
// Already-installed Liqo does not re-apply its values, so this runs on every
// install, including the skip path.
func (l *LiqoManager) PrepareGameCatalogReflection(ctx context.Context) error {
	if l.k8sClient == nil || l.k8sClient.GetClientset() == nil {
		return fmt.Errorf("kubernetes client is not connected")
	}
	if err := EnsureHypervisorCRDs(ctx, l.getKubeconfigPath(), l.logger); err != nil {
		return err
	}
	return ensureCatalogClusterRoles(ctx, l.k8sClient.GetClientset())
}

// EnsurePterodactylLiqoRBAC merges the Pterodactyl reflection rules into the
// Liqo virtual-kubelet roles. A Proxmox agent that later runs install-pterodactyl
// already has Liqo, and that command does not re-run liqoctl, so this is what
// updates the roles. When Liqo is not installed yet the roles are absent and
// the later register path adds them.
func EnsurePterodactylLiqoRBAC(ctx context.Context, kubeconfig string) error {
	client, err := k8s.NewClient(kubeconfig)
	if err != nil {
		return fmt.Errorf("kubernetes client: %w", err)
	}
	cs := client.GetClientset()
	if cs == nil {
		return fmt.Errorf("kubernetes client is not connected")
	}
	_, err = cs.RbacV1().ClusterRoles().Get(ctx, vkRoleLocal, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get ClusterRole %s: %w", vkRoleLocal, err)
	}
	return ensureCatalogClusterRoles(ctx, cs)
}

func ensureCatalogClusterRoles(ctx context.Context, cs kubernetes.Interface) error {
	if err := ensureCatalogRole(ctx, cs, vkRoleLocal, localCatalogRules()); err != nil {
		return err
	}
	return ensureCatalogRole(ctx, cs, vkRoleRemote, remoteCatalogRules())
}

func ensureCatalogRole(ctx context.Context, cs kubernetes.Interface, name string, want []rbacv1.PolicyRule) error {
	role, err := cs.RbacV1().ClusterRoles().Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf("ClusterRole %s is missing; Liqo is not ready to reflect the game catalog", name)
		}
		return fmt.Errorf("get ClusterRole %s: %w", name, err)
	}
	next, changed := mergeCatalogRules(role.Rules, want)
	if !changed {
		return nil
	}
	role.Rules = next
	if _, err := cs.RbacV1().ClusterRoles().Update(ctx, role, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("update ClusterRole %s: %w", name, err)
	}
	return nil
}

func localCatalogRules() []rbacv1.PolicyRule {
	rules := make([]rbacv1.PolicyRule, 0, len(pterodactylReflectedResources)*2)
	for _, resource := range pterodactylReflectedResources {
		rules = append(rules,
			rbacv1.PolicyRule{APIGroups: []string{catalogAPIGroup}, Resources: []string{resource}, Verbs: []string{"get", "list", "watch"}},
			rbacv1.PolicyRule{APIGroups: []string{catalogAPIGroup}, Resources: []string{resource + "/status"}, Verbs: []string{"get", "patch", "update"}},
		)
	}
	return rules
}

func remoteCatalogRules() []rbacv1.PolicyRule {
	rules := make([]rbacv1.PolicyRule, 0, len(pterodactylReflectedResources))
	for _, resource := range pterodactylReflectedResources {
		rules = append(rules, rbacv1.PolicyRule{
			APIGroups: []string{catalogAPIGroup},
			Resources: []string{resource},
			Verbs:     []string{"create", "delete", "get", "list", "patch", "update", "watch"},
		})
	}
	return rules
}

func mergeCatalogRules(have, want []rbacv1.PolicyRule) ([]rbacv1.PolicyRule, bool) {
	changed := false
	for _, rule := range want {
		if catalogRuleCovered(have, rule) {
			continue
		}
		have = append(have, rule)
		changed = true
	}
	return have, changed
}

func catalogRuleCovered(have []rbacv1.PolicyRule, want rbacv1.PolicyRule) bool {
	for _, rule := range have {
		if !sameCatalogStrings(rule.APIGroups, want.APIGroups) || !sameCatalogStrings(rule.Resources, want.Resources) {
			continue
		}
		if catalogVerbsCover(rule.Verbs, want.Verbs) {
			return true
		}
	}
	return false
}

func catalogVerbsCover(have, want []string) bool {
	for _, verb := range want {
		if !slices.Contains(have, verb) && !slices.Contains(have, "*") {
			return false
		}
	}
	return true
}

func sameCatalogStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	left := append([]string(nil), a...)
	right := append([]string(nil), b...)
	slices.Sort(left)
	slices.Sort(right)
	return slices.Equal(left, right)
}
