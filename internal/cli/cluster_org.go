package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/noderings/cli/internal/api"
	generated "github.com/noderings/cli/internal/api/generated"
	"github.com/noderings/cli/internal/config"
)

type providerOrg struct {
	id   string
	name string
}

// ensureProviderOrganization lists organizations and sets X-Organization-ID to
// the account's provider tenant. OAuth otherwise binds to the home (often
// client) org, which cannot create or list agents. A user has one provider org.
func ensureProviderOrganization(ctx context.Context, client *api.Client) error {
	if client == nil {
		return nil
	}
	if id := strings.TrimSpace(client.GetOrganizationID()); id != "" {
		return nil
	}

	orgs, err := listProviderOrganizations(ctx, client)
	if err != nil {
		var apiErr *api.APIError
		if errors.As(err, &apiErr) && apiErr.IsUnauthorized() {
			// Service-account tokens cannot list orgs; they are already tenant-scoped.
			return nil
		}
		return fmt.Errorf("list organizations: %w", err)
	}
	id, err := uniqueProviderOrganization(orgs)
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

// fetchOrganizationHypervisorDriver returns the canonical org driver derived
// from registered agents, or empty if none have a hypervisor driver yet.
func fetchOrganizationHypervisorDriver(ctx context.Context, client *api.Client) string {
	if client == nil {
		return ""
	}
	resp, err := client.DoWithAutoRefresh(ctx, 3, func() (*http.Response, error) {
		return client.GetGeneratedClient().OrganizationServiceGetOrganization(ctx)
	})
	if err != nil {
		return ""
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		return ""
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return ""
	}
	var parsed generated.V1GetOrganizationResponse
	if err := json.Unmarshal(body, &parsed); err != nil || parsed.Organization == nil {
		return ""
	}
	return canonicalAPIPlatformDriver(parsed.Organization.HypervisorDriver)
}

func canonicalAPIPlatformDriver(d *generated.V1PlatformDriver) string {
	if d == nil {
		return ""
	}
	switch *d {
	case generated.PLATFORMDRIVERVIRTFUSION:
		return config.HypervisorDriverVirtFusion
	case generated.PLATFORMDRIVERSOLUSVM:
		return config.HypervisorDriverSolusVM
	case generated.PLATFORMDRIVERPROXMOX:
		return config.HypervisorDriverProxmox
	default:
		return ""
	}
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

func uniqueProviderOrganization(orgs []providerOrg) (string, error) {
	if len(orgs) == 0 {
		return "", fmt.Errorf("no provider organization found for this account")
	}
	if len(orgs) > 1 {
		return "", fmt.Errorf("this account has %d provider organizations; a user can only have one", len(orgs))
	}
	return orgs[0].id, nil
}

func orgNameByID(orgs []providerOrg, id string) string {
	for _, org := range orgs {
		if org.id == id {
			return org.name
		}
	}
	return id
}
