package cli

import (
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/noderings/cli/internal/api"
	generated "github.com/noderings/cli/internal/api/generated"
)

// rejectUnverifiedProvider fails fast when the session tenant is a provider org that
// has not passed marketplace review. Login and org-list still work; CreateAgent,
// cluster register, and peering do not.
func rejectUnverifiedProvider(ctx context.Context, apiClient *api.Client) error {
	if apiClient == nil {
		return nil
	}
	// When --org-id is set, check that tenant only. Listing memberships must not
	// choose an organization for the caller.
	if apiClient.GetOrganizationID() != "" {
		return rejectUnverifiedProviderFromGet(ctx, apiClient)
	}
	pageSize := int32(-1)
	resp, err := apiClient.GetGeneratedClient().OrganizationServiceListOrganizations(ctx,
		&generated.OrganizationServiceListOrganizationsParams{PageSize: &pageSize})
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusUnauthorized {
		// Service-account JWTs cannot list orgs. Tenant is already on the token.
		return rejectUnverifiedProviderFromGet(ctx, apiClient)
	}
	if resp.StatusCode >= 400 {
		return api.ParseError(resp)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if providerReviewPendingFromListOrgs(body) {
		return &api.APIError{
			StatusCode: http.StatusForbidden,
			Code:       "PERMISSION_DENIED",
			Message:    api.ProviderReviewPendingMessage,
		}
	}
	return nil
}

func providerReviewPendingFromListOrgs(body []byte) bool {
	var list generated.V1ListOrganizationsResponse
	if err := json.Unmarshal(body, &list); err != nil || list.Organizations == nil {
		return false
	}
	hasUnverifiedProvider := false
	hasVerifiedProvider := false
	for _, org := range *list.Organizations {
		if org.Type == nil || *org.Type != generated.ORGANIZATIONTYPEPROVIDER {
			continue
		}
		if org.IsVerified != nil && *org.IsVerified {
			hasVerifiedProvider = true
			continue
		}
		hasUnverifiedProvider = true
	}
	return hasUnverifiedProvider && !hasVerifiedProvider
}

func rejectUnverifiedProviderFromGet(ctx context.Context, apiClient *api.Client) error {
	resp, err := apiClient.GetGeneratedClient().OrganizationServiceGetOrganization(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		return api.ParseError(resp)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	var parsed generated.V1GetOrganizationResponse
	if err := json.Unmarshal(body, &parsed); err != nil || parsed.Organization == nil {
		return nil
	}
	org := parsed.Organization
	if org.Type == nil || *org.Type != generated.ORGANIZATIONTYPEPROVIDER {
		return nil
	}
	if org.IsVerified != nil && *org.IsVerified {
		return nil
	}
	return &api.APIError{
		StatusCode: http.StatusForbidden,
		Code:       "PERMISSION_DENIED",
		Message:    api.ProviderReviewPendingMessage,
	}
}
