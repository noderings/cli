package cli

import (
	"encoding/json"
	"strings"
	"testing"

	generated "github.com/noderings/cli/internal/api/generated"
)

func TestProviderOrgsFromListJSONIgnoresClientOrgs(t *testing.T) {
	t.Parallel()

	provider := generated.ORGANIZATIONTYPEPROVIDER
	client := generated.ORGANIZATIONTYPECLIENT
	pid := "526ad533-9fef-47ee-9cd6-354c9b9e5cc5"
	cid := "d307243c-c8e3-450e-b226-df4636795c5c"
	pname := "Virt VM Inc"
	cname := "My Organization"
	body, err := json.Marshal(generated.V1ListOrganizationsResponse{
		Organizations: &[]generated.V1Organization{
			{Id: &pid, Name: &pname, Type: &provider},
			{Id: &cid, Name: &cname, Type: &client},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	got := providerOrgsFromListJSON(body)
	if len(got) != 1 || got[0].id != pid || got[0].name != pname {
		t.Fatalf("got %#v, want only provider %s", got, pid)
	}
}

func TestUniqueProviderOrganizationSingle(t *testing.T) {
	t.Parallel()
	id, err := uniqueProviderOrganization([]providerOrg{{id: "abc", name: "One"}})
	if err != nil {
		t.Fatal(err)
	}
	if id != "abc" {
		t.Fatalf("id=%q", id)
	}
}

func TestUniqueProviderOrganizationEmpty(t *testing.T) {
	t.Parallel()
	_, err := uniqueProviderOrganization(nil)
	if err == nil || !strings.Contains(err.Error(), "no provider organization") {
		t.Fatalf("err=%v", err)
	}
}

func TestUniqueProviderOrganizationMultiple(t *testing.T) {
	t.Parallel()
	_, err := uniqueProviderOrganization([]providerOrg{
		{id: "a", name: "A"},
		{id: "b", name: "B"},
	})
	if err == nil || !strings.Contains(err.Error(), "only have one") {
		t.Fatalf("err=%v", err)
	}
}

func TestCanonicalAPIPlatformDriver(t *testing.T) {
	t.Parallel()
	vf := generated.PLATFORMDRIVERVIRTFUSION
	svm := generated.PLATFORMDRIVERSOLUSVM
	pve := generated.PLATFORMDRIVERPROXMOX
	unspec := generated.PLATFORMDRIVERUNSPECIFIED
	if got := canonicalAPIPlatformDriver(&vf); got != "virtfusion" {
		t.Fatalf("vf got %q", got)
	}
	if got := canonicalAPIPlatformDriver(&svm); got != "solusvm" {
		t.Fatalf("svm got %q", got)
	}
	if got := canonicalAPIPlatformDriver(&pve); got != "proxmox" {
		t.Fatalf("pve got %q", got)
	}
	if got := canonicalAPIPlatformDriver(&unspec); got != "" {
		t.Fatalf("unspec got %q", got)
	}
	if got := canonicalAPIPlatformDriver(nil); got != "" {
		t.Fatalf("nil got %q", got)
	}
}
