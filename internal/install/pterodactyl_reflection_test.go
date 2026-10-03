package install

import (
	"testing"

	rbacv1 "k8s.io/api/rbac/v1"
)

func TestMergeCatalogRulesSkipsCoveredVerbs(t *testing.T) {
	local := localCatalogRules()
	have := []rbacv1.PolicyRule{local[0]}
	next, changed := mergeCatalogRules(have, []rbacv1.PolicyRule{local[0]})
	if changed || len(next) != 1 {
		t.Fatalf("covered rule was added: changed=%v len=%d", changed, len(next))
	}
	remote := remoteCatalogRules()
	next, changed = mergeCatalogRules(have, remote)
	if !changed || len(next) != 1+len(remote) {
		t.Fatalf("missing verbs were not added: changed=%v len=%d", changed, len(next))
	}
	if !catalogRuleCovered(next, rbacv1.PolicyRule{
		APIGroups: []string{catalogAPIGroup},
		Resources: []string{"pterodactylnodes"},
		Verbs:     []string{"get", "list", "watch"},
	}) {
		t.Fatal("node list was not granted")
	}
}
