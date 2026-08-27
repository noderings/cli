package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/noderings/cli/internal/api"
	generated "github.com/noderings/cli/internal/api/generated"
)

type providerOrg struct {
	id   string
	name string
}

func organizationIDFromCmd(cmd *cobra.Command) string {
	if cmd != nil {
		if f := cmd.Flag("organization-id"); f != nil {
			if v := strings.TrimSpace(f.Value.String()); v != "" {
				return v
			}
		}
	}
	for _, key := range []string{"NR_ORGANIZATION_ID", "NODERINGS_ORGANIZATION_ID"} {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			return v
		}
	}
	return ""
}

// ensureProviderOrganization sets X-Organization-ID so provider APIs (ListAgents,
// CreateAgent) run against the provider tenant instead of the OAuth home org.
func ensureProviderOrganization(ctx context.Context, client *api.Client, yes bool) error {
	if client == nil {
		return nil
	}
	if id := strings.TrimSpace(client.GetOrganizationID()); id != "" {
		return nil
	}

	orgs, err := listProviderOrganizations(ctx, client)
	if err != nil {
		return fmt.Errorf("list organizations: %w\nPass --organization-id from the UI install command", err)
	}
	id, err := chooseProviderOrganization(orgs, yes)
	if err != nil {
		return err
	}
	client.SetOrganizationID(id)
	fmt.Fprintf(os.Stderr, "Using provider organization %s (%s)\n", orgNameByID(orgs, id), id)
	return nil
}

func listProviderOrganizations(ctx context.Context, client *api.Client) ([]providerOrg, error) {
	pageSize := int32(-1)
	resp, err := client.DoWithAutoRefresh(ctx, 3, func() (*http.Response, error) {
		return client.GetGeneratedClient().OrganizationServiceListOrganizations(ctx,
			&generated.OrganizationServiceListOrganizationsParams{PageSize: &pageSize})
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		return nil, api.ParseError(resp)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return providerOrgsFromListJSON(body), nil
}

func providerOrgsFromListJSON(body []byte) []providerOrg {
	var list generated.V1ListOrganizationsResponse
	if err := json.Unmarshal(body, &list); err != nil || list.Organizations == nil {
		return nil
	}
	out := make([]providerOrg, 0, len(*list.Organizations))
	for _, org := range *list.Organizations {
		if org.Type == nil || *org.Type != generated.ORGANIZATIONTYPEPROVIDER {
			continue
		}
		id := ""
		if org.Id != nil {
			id = strings.TrimSpace(*org.Id)
		}
		if id == "" {
			continue
		}
		name := id
		if org.Name != nil && strings.TrimSpace(*org.Name) != "" {
			name = strings.TrimSpace(*org.Name)
		}
		out = append(out, providerOrg{id: id, name: name})
	}
	return out
}

func chooseProviderOrganization(orgs []providerOrg, yes bool) (string, error) {
	if len(orgs) == 0 {
		return "", fmt.Errorf("no provider organization found for this account. Pass --organization-id from the UI install command")
	}
	if len(orgs) == 1 && (yes || !isStdinTerminal()) {
		return orgs[0].id, nil
	}
	if !isStdinTerminal() {
		return "", fmt.Errorf("pass --organization-id <uuid> (provider organization). The UI install command includes this flag")
	}

	fmt.Fprintln(os.Stderr, "Provider organizations:")
	for i, org := range orgs {
		fmt.Fprintf(os.Stderr, "  %d) %s (%s)\n", i+1, org.name, org.id)
	}
	raw, err := promptString("Select a provider organization", "1")
	if err != nil {
		return "", err
	}
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < 1 || n > len(orgs) {
		return "", fmt.Errorf("invalid selection %q (enter 1-%d)", raw, len(orgs))
	}
	return orgs[n-1].id, nil
}

func orgNameByID(orgs []providerOrg, id string) string {
	for _, org := range orgs {
		if org.id == id {
			return org.name
		}
	}
	return id
}
