package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	generated "github.com/noderings/cli/internal/api/generated"
)

func TestOrganizationIDFromCmdFlagBeatsEnv(t *testing.T) {
	t.Setenv("NR_ORGANIZATION_ID", "from-env")
	cmd := &cobra.Command{Use: "register"}
	cmd.Flags().String("organization-id", "", "")
	if err := cmd.Flags().Set("organization-id", "from-flag"); err != nil {
		t.Fatal(err)
	}
	if got := organizationIDFromCmd(cmd); got != "from-flag" {
		t.Fatalf("got %q", got)
	}
}

func TestOrganizationIDFromCmdEnv(t *testing.T) {
	t.Setenv("NR_ORGANIZATION_ID", " from-env ")
	cmd := &cobra.Command{Use: "register"}
	cmd.Flags().String("organization-id", "", "")
	if got := organizationIDFromCmd(cmd); got != "from-env" {
		t.Fatalf("got %q", got)
	}
}

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

func TestChooseProviderOrganizationSingleNonInteractive(t *testing.T) {
	t.Parallel()
	id, err := chooseProviderOrganization([]providerOrg{{id: "abc", name: "One"}}, true)
	if err != nil {
		t.Fatal(err)
	}
	if id != "abc" {
		t.Fatalf("id=%q", id)
	}
}

func TestChooseProviderOrganizationEmpty(t *testing.T) {
	t.Parallel()
	_, err := chooseProviderOrganization(nil, true)
	if err == nil || !strings.Contains(err.Error(), "no provider organization") {
		t.Fatalf("err=%v", err)
	}
}

func TestChooseProviderOrganizationMultipleNeedsFlagWhenNotTTY(t *testing.T) {
	t.Parallel()
	_, err := chooseProviderOrganization([]providerOrg{
		{id: "a", name: "A"},
		{id: "b", name: "B"},
	}, false)
	if err == nil || !strings.Contains(err.Error(), "--organization-id") {
		t.Fatalf("err=%v", err)
	}
}
