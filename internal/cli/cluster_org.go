package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/noderings/cli/internal/api"
	generated "github.com/noderings/cli/internal/api/generated"
	"github.com/noderings/cli/internal/config"
)

const envOrganizationID = "NR_ORGANIZATION_ID"

// organizationIDFromCmd returns --org-id, then NR_ORGANIZATION_ID. Empty if unset.
// The value is not trusted: the API still authorizes the token against that tenant.
func organizationIDFromCmd(cmd *cobra.Command) string {
	if cmd != nil {
		if id, err := cmd.Flags().GetString("org-id"); err == nil {
			if v := strings.TrimSpace(id); v != "" {
				return v
			}
		}
	}
	return strings.TrimSpace(os.Getenv(envOrganizationID))
}

func parseOrganizationID(raw string) (string, error) {
	id := strings.TrimSpace(raw)
	if id == "" {
		return "", fmt.Errorf("--org-id is required (copy it from the console, or set %s)", envOrganizationID)
	}
	if !uuidPattern.MatchString(id) {
		return "", fmt.Errorf("--org-id must be a UUID, got %q", id)
	}
	return id, nil
}

// requireOrganizationID is the tenant for nr cluster register. The CLI does not
// pick a provider org via ListOrganizations (users can belong to client and
// provider orgs). Auth remains the service-account or session token.
func requireOrganizationID(cmd *cobra.Command) (string, error) {
	return parseOrganizationID(organizationIDFromCmd(cmd))
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
