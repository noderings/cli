package install

import (
	"testing"

	rbacv1 "k8s.io/api/rbac/v1"
)

func TestMergeCatalogRulesSkipsCoveredVerbs(t *testing.T) {
	have := []rbacv1.PolicyRule{{
		APIGroups: []string{catalogAPIGroup},
		Resources: []string{catalogResource},
		Verbs:     []string{"get", "list", "watch"},
	}}
	next, changed := mergeCatalogRules(have, localCatalogRules()[:1])
	if changed || len(next) != 1 {
		t.Fatalf("covered rule was added: changed=%v len=%d", changed, len(next))
	}
	next, changed = mergeCatalogRules(have, remoteCatalogRules())
	if !changed || len(next) != 2 {
		t.Fatalf("missing verbs were not added: changed=%v len=%d", changed, len(next))
	}
}
