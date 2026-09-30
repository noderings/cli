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
)

const (
	catalogAPIGroup = config.PterodactylCRDAPIGroup
	catalogResource = "pterodactylgamecatalogs"
	vkRoleLocal     = "liqo-virtual-kubelet-local"
	vkRoleRemote    = "liqo-virtual-kubelet-remote"
)

// PrepareGameCatalogReflection installs the operator CRDs and the Liqo role
// rules a new virtual-kubelet needs to reflect PterodactylGameCatalog.
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
	return []rbacv1.PolicyRule{
		{APIGroups: []string{catalogAPIGroup}, Resources: []string{catalogResource}, Verbs: []string{"get", "list", "watch"}},
		{APIGroups: []string{catalogAPIGroup}, Resources: []string{catalogResource + "/status"}, Verbs: []string{"get", "patch", "update"}},
	}
}

func remoteCatalogRules() []rbacv1.PolicyRule {
	return []rbacv1.PolicyRule{
		{APIGroups: []string{catalogAPIGroup}, Resources: []string{catalogResource}, Verbs: []string{"create", "delete", "get", "list", "patch", "update", "watch"}},
	}
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
