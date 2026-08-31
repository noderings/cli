package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/noderings/cli/internal/api"
	generated "github.com/noderings/cli/internal/api/generated"
	"github.com/noderings/cli/internal/config"
)

var (
	serviceAccountCmd = &cobra.Command{
		Use:     "service-account",
		Short:   "Service account management commands",
		Long:    "Manage service accounts for programmatic API access",
		Aliases: []string{"sa"},
	}

	serviceAccountCreateCmd = &cobra.Command{
		Use:   "create",
		Short: "Create a new service account",
		Long:  "Create a new service account in your organization",
		RunE:  runServiceAccountCreate,
	}

	serviceAccountListCmd = &cobra.Command{
		Use:   "list",
		Short: "List service accounts",
		Long:  "List all service accounts in your organization",
		RunE:  runServiceAccountList,
	}

	serviceAccountGetCmd = &cobra.Command{
		Use:   "get",
		Short: "Get a service account",
		Long:  "Get details of a service account by ID",
		RunE:  runServiceAccountGet,
	}

	serviceAccountUpdateCmd = &cobra.Command{
		Use:   "update",
		Short: "Update a service account",
		Long:  "Update an existing service account",
		RunE:  runServiceAccountUpdate,
	}

	serviceAccountDeleteCmd = &cobra.Command{
		Use:   "delete",
		Short: "Delete a service account",
		Long:  "Delete (soft delete) a service account. All associated tokens will be revoked.",
		RunE:  runServiceAccountDelete,
	}

	// Token subcommands
	serviceAccountTokenCmd = &cobra.Command{
		Use:   "token",
		Short: "Service account token management",
		Long:  "Manage tokens for service accounts",
	}

	serviceAccountTokenGenerateCmd = &cobra.Command{
		Use:   "generate",
		Short: "Generate a new token for a service account",
		Long:  "Generate a new JWT token for a service account. The token is only displayed once - store it securely.",
		RunE:  runServiceAccountTokenGenerate,
	}

	serviceAccountTokenListCmd = &cobra.Command{
		Use:   "list",
		Short: "List tokens for a service account",
		Long:  "List all tokens for a service account",
		RunE:  runServiceAccountTokenList,
	}

	serviceAccountTokenRevokeCmd = &cobra.Command{
		Use:   "revoke",
		Short: "Revoke a service account token",
		Long:  "Revoke a service account token. The token becomes invalid immediately.",
		RunE:  runServiceAccountTokenRevoke,
	}

	// Role subcommands
	serviceAccountRoleCmd = &cobra.Command{
		Use:   "role",
		Short: "Service account role management",
		Long:  "Manage roles for service accounts",
	}

	serviceAccountRoleAssignCmd = &cobra.Command{
		Use:   "assign",
		Short: "Assign or replace role for a service account",
		Long:  "Assign or replace a role for a service account. This replaces all existing roles for the service account. Use empty role-id to clear all roles.",
		RunE:  runServiceAccountRoleAssign,
	}

	// Group subcommands
	serviceAccountGroupCmd = &cobra.Command{
		Use:   "group",
		Short: "Service account group management",
		Long:  "Manage group memberships for service accounts",
	}

	serviceAccountGroupAddCmd = &cobra.Command{
		Use:   "add",
		Short: "Add service account(s) to a group",
		Long:  "Add one or more service accounts to a group",
		RunE:  runServiceAccountGroupAdd,
	}

	serviceAccountGroupRemoveCmd = &cobra.Command{
		Use:   "remove",
		Short: "Remove service account(s) from a group",
		Long:  "Remove one or more service accounts from a group",
		RunE:  runServiceAccountGroupRemove,
	}
)

func init() {
	// Add service account command to root
	rootCmd.AddCommand(serviceAccountCmd)

	// Add service account subcommands
	serviceAccountCmd.AddCommand(serviceAccountCreateCmd)
	serviceAccountCmd.AddCommand(serviceAccountListCmd)
	serviceAccountCmd.AddCommand(serviceAccountGetCmd)
	serviceAccountCmd.AddCommand(serviceAccountUpdateCmd)
	serviceAccountCmd.AddCommand(serviceAccountDeleteCmd)
	serviceAccountCmd.AddCommand(serviceAccountTokenCmd)

	// Add token subcommands
	serviceAccountTokenCmd.AddCommand(serviceAccountTokenGenerateCmd)
	serviceAccountTokenCmd.AddCommand(serviceAccountTokenListCmd)
	serviceAccountTokenCmd.AddCommand(serviceAccountTokenRevokeCmd)

	// Add role subcommands
	serviceAccountCmd.AddCommand(serviceAccountRoleCmd)
	serviceAccountRoleCmd.AddCommand(serviceAccountRoleAssignCmd)

	// Add group subcommands
	serviceAccountCmd.AddCommand(serviceAccountGroupCmd)
	serviceAccountGroupCmd.AddCommand(serviceAccountGroupAddCmd)
	serviceAccountGroupCmd.AddCommand(serviceAccountGroupRemoveCmd)

	// Create flags
	serviceAccountCreateCmd.Flags().String("name", "", "Service account name (required)")
	serviceAccountCreateCmd.Flags().String("description", "", "Service account description")
	serviceAccountCreateCmd.Flags().StringSlice("allowed-ips", []string{}, "Comma-separated list of allowed IP addresses or CIDR ranges (e.g., 192.168.1.0/24,10.0.0.0/8)")
	serviceAccountCreateCmd.Flags().String("output", config.OutputFormatText, "Output format: text|json")

	serviceAccountListCmd.Flags().Bool("include-inactive", false, "Include inactive service accounts")
	serviceAccountListCmd.Flags().Int32("page", 0, "Page number (0-indexed)")
	serviceAccountListCmd.Flags().Int32("page-size", 10, "Items per page (use -1 for all)")
	serviceAccountListCmd.Flags().String("output", config.OutputFormatText, "Output format: text|json")

	serviceAccountGetCmd.Flags().String("id", "", "Service account ID (required)")
	serviceAccountGetCmd.Flags().String("output", config.OutputFormatText, "Output format: text|json")

	serviceAccountUpdateCmd.Flags().String("id", "", "Service account ID (required)")
	serviceAccountUpdateCmd.Flags().String("name", "", "Update service account name")
	serviceAccountUpdateCmd.Flags().String("description", "", "Update service account description")
	serviceAccountUpdateCmd.Flags().Bool("is-active", true, "Set service account active status")
	serviceAccountUpdateCmd.Flags().StringSlice("allowed-ips", []string{}, "Update allowed IP addresses or CIDR ranges")
	serviceAccountUpdateCmd.Flags().String("output", config.OutputFormatText, "Output format: text|json")

	serviceAccountDeleteCmd.Flags().String("id", "", "Service account ID (required)")
	serviceAccountDeleteCmd.Flags().Bool("force", false, "Skip confirmation prompt")

	serviceAccountTokenGenerateCmd.Flags().String("service-account-id", "", "Service account ID (required)")
	serviceAccountTokenGenerateCmd.Flags().String("name", "", "Token name (required)")
	serviceAccountTokenGenerateCmd.Flags().Int32("expires-in-days", 90, "Token expiration in days (default: 90)")

	serviceAccountTokenListCmd.Flags().String("service-account-id", "", "Service account ID (required)")
	serviceAccountTokenListCmd.Flags().Bool("include-inactive", false, "Include inactive/revoked tokens")
	serviceAccountTokenListCmd.Flags().Int32("page", 0, "Page number (0-indexed)")
	serviceAccountTokenListCmd.Flags().Int32("page-size", 10, "Items per page (use -1 for all)")
	serviceAccountTokenListCmd.Flags().String("output", config.OutputFormatText, "Output format: text|json")

	serviceAccountTokenRevokeCmd.Flags().String("token-id", "", "Token ID to revoke (required)")

	serviceAccountRoleAssignCmd.Flags().String("service-account-id", "", "Service account ID (required)")
	serviceAccountRoleAssignCmd.Flags().String("role-id", "", "Role ID to assign (optional - leave empty to clear all roles)")

	serviceAccountGroupAddCmd.Flags().String("group-id", "", "Group ID (required)")
	serviceAccountGroupAddCmd.Flags().StringSlice("service-account-ids", []string{}, "Service account IDs to add (required, comma-separated)")

	serviceAccountGroupRemoveCmd.Flags().String("group-id", "", "Group ID (required)")
	serviceAccountGroupRemoveCmd.Flags().StringSlice("service-account-ids", []string{}, "Service account IDs to remove (required, comma-separated)")
}

func runServiceAccountCreate(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	// Get authenticated API client
	apiClient, err := getAuthenticatedAPIClient(cmd)
	if err != nil {
		return err
	}

	genClient := apiClient.GetGeneratedClient()

	// Get flags
	name, _ := cmd.Flags().GetString("name")
	if name == "" {
		return RequiredFlag("name")
	}

	description, _ := cmd.Flags().GetString("description")
	allowedIPs, _ := cmd.Flags().GetStringSlice("allowed-ips")

	// Create request
	req := generated.V1CreateServiceAccountRequest{
		Name:       &name,
		AllowedIps: &allowedIPs,
	}
	if description != "" {
		req.Description = &description
	}

	// Make API call
	resp, err := apiClient.DoWithAutoRefresh(ctx, 3, func() (*http.Response, error) {
		return genClient.ServiceAccountServiceCreateServiceAccount(ctx, req)
	})
	if err != nil {
		return fmt.Errorf("create service account: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return api.ParseError(resp)
	}

	var serviceAccount generated.V1ServiceAccountResponse
	if err := json.NewDecoder(resp.Body).Decode(&serviceAccount); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	output, err := resolveOutputFlag(cmd)
	if err != nil {
		return err
	}
	if output == config.OutputFormatJSON {
		return writeJSON(serviceAccount)
	}

	fmt.Printf("%s Service account created successfully!\n", markPass())
	fmt.Printf("ID: %s\n", *serviceAccount.Id)
	fmt.Printf("Name: %s\n", *serviceAccount.Name)
	if serviceAccount.Description != nil {
		fmt.Printf("Description: %s\n", *serviceAccount.Description)
	}
	fmt.Printf("Active: %v\n", *serviceAccount.IsActive)
	if serviceAccount.AllowedIps != nil && len(*serviceAccount.AllowedIps) > 0 {
		fmt.Printf("Allowed IPs: %s\n", strings.Join(*serviceAccount.AllowedIps, ", "))
	}

	return nil
}

func runServiceAccountList(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	// Get authenticated API client
	apiClient, err := getAuthenticatedAPIClient(cmd)
	if err != nil {
		return err
	}

	genClient := apiClient.GetGeneratedClient()

	// Get flags
	includeInactive, _ := cmd.Flags().GetBool("include-inactive")
	page, _ := cmd.Flags().GetInt32("page")
	pageSize, _ := cmd.Flags().GetInt32("page-size")

	// Create request params
	params := &generated.ServiceAccountServiceListServiceAccountsParams{
		IncludeInactive: &includeInactive,
		Page:            &page,
		PageSize:        &pageSize,
	}

	// Make API call
	resp, err := apiClient.DoWithAutoRefresh(ctx, 3, func() (*http.Response, error) {
		return genClient.ServiceAccountServiceListServiceAccounts(ctx, params)
	})
	if err != nil {
		return fmt.Errorf("list service accounts: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return api.ParseError(resp)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	var listResp generated.V1ListServiceAccountsResponse
	if err := json.Unmarshal(body, &listResp); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	output, err := resolveOutputFlag(cmd)
	if err != nil {
		return err
	}
	if output == config.OutputFormatJSON {
		return writeJSON(listResp)
	}

	// Print results
	if listResp.ServiceAccounts == nil || len(*listResp.ServiceAccounts) == 0 {
		fmt.Println("No service accounts found.")
		return nil
	}

	fmt.Printf("Service Accounts (Total: %d)\n\n", *listResp.TotalCount)
	for _, sa := range *listResp.ServiceAccounts {
		status := "Active"
		if sa.IsActive != nil && !*sa.IsActive {
			status = "Inactive"
		}
		fmt.Printf("ID: %s\n", *sa.Id)
		fmt.Printf("  Name: %s\n", *sa.Name)
		fmt.Printf("  Status: %s\n", status)
		if sa.ActiveTokenCount != nil {
			fmt.Printf("  Active Tokens: %d\n", *sa.ActiveTokenCount)
		}
		if sa.LastUsedAt != nil {
			fmt.Printf("  Last Used: %s\n", sa.LastUsedAt.Format(time.RFC3339))
		}
		fmt.Println()
	}

	return nil
}

func runServiceAccountGet(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	// Get authenticated API client
	apiClient, err := getAuthenticatedAPIClient(cmd)
	if err != nil {
		return err
	}

	genClient := apiClient.GetGeneratedClient()

	// Get flags
	id, _ := cmd.Flags().GetString("id")
	if id == "" {
		return RequiredFlag("id")
	}

	// Make API call
	resp, err := apiClient.DoWithAutoRefresh(ctx, 3, func() (*http.Response, error) {
		return genClient.ServiceAccountServiceGetServiceAccount(ctx, id)
	})
	if err != nil {
		return fmt.Errorf("get service account: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return api.ParseError(resp)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	var serviceAccount generated.V1ServiceAccountResponse
	if err := json.Unmarshal(body, &serviceAccount); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	output, err := resolveOutputFlag(cmd)
	if err != nil {
		return err
	}
	if output == config.OutputFormatJSON {
		return writeJSON(serviceAccount)
	}

	// Print result
	fmt.Printf("Service Account Details\n")
	fmt.Printf("======================\n\n")
	fmt.Printf("ID: %s\n", *serviceAccount.Id)
	fmt.Printf("Name: %s\n", *serviceAccount.Name)
	if serviceAccount.Description != nil {
		fmt.Printf("Description: %s\n", *serviceAccount.Description)
	}
	fmt.Printf("Active: %v\n", *serviceAccount.IsActive)
	if serviceAccount.OrganizationId != nil {
		fmt.Printf("Organization ID: %s\n", *serviceAccount.OrganizationId)
	}
	if serviceAccount.CreatedByUserId != nil {
		fmt.Printf("Created By: %s\n", *serviceAccount.CreatedByUserId)
	}
	if serviceAccount.CreatedAt != nil {
		fmt.Printf("Created At: %s\n", serviceAccount.CreatedAt.Format(time.RFC3339))
	}
	if serviceAccount.UpdatedAt != nil {
		fmt.Printf("Updated At: %s\n", serviceAccount.UpdatedAt.Format(time.RFC3339))
	}
	if serviceAccount.LastUsedAt != nil {
		fmt.Printf("Last Used At: %s\n", serviceAccount.LastUsedAt.Format(time.RFC3339))
	}
	if serviceAccount.ActiveTokenCount != nil {
		fmt.Printf("Active Tokens: %d\n", *serviceAccount.ActiveTokenCount)
	}
	if serviceAccount.AllowedIps != nil && len(*serviceAccount.AllowedIps) > 0 {
		fmt.Printf("Allowed IPs: %s\n", strings.Join(*serviceAccount.AllowedIps, ", "))
	}
	if serviceAccount.RoleIds != nil && len(*serviceAccount.RoleIds) > 0 {
		fmt.Printf("Roles: %s\n", strings.Join(*serviceAccount.RoleIds, ", "))
	}
	if serviceAccount.GroupIds != nil && len(*serviceAccount.GroupIds) > 0 {
		fmt.Printf("Groups: %s\n", strings.Join(*serviceAccount.GroupIds, ", "))
	}

	return nil
}

func runServiceAccountUpdate(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	// Get authenticated API client
	apiClient, err := getAuthenticatedAPIClient(cmd)
	if err != nil {
		return err
	}

	genClient := apiClient.GetGeneratedClient()

	// Get flags
	id, _ := cmd.Flags().GetString("id")
	if id == "" {
		return RequiredFlag("id")
	}

	// Build update request (only include fields that were provided)
	updateReq := generated.ServiceAccountServiceUpdateServiceAccountBody{}

	if cmd.Flags().Changed("name") {
		name, _ := cmd.Flags().GetString("name")
		updateReq.Name = &name
	}

	if cmd.Flags().Changed("description") {
		description, _ := cmd.Flags().GetString("description")
		updateReq.Description = &description
	}

	if cmd.Flags().Changed("is-active") {
		isActive, _ := cmd.Flags().GetBool("is-active")
		updateReq.IsActive = &isActive
	}

	if cmd.Flags().Changed("allowed-ips") {
		allowedIPs, _ := cmd.Flags().GetStringSlice("allowed-ips")
		updateReq.AllowedIps = &allowedIPs
	}

	// Make API call
	resp, err := apiClient.DoWithAutoRefresh(ctx, 3, func() (*http.Response, error) {
		return genClient.ServiceAccountServiceUpdateServiceAccount(ctx, id, updateReq)
	})
	if err != nil {
		return fmt.Errorf("update service account: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return api.ParseError(resp)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	var serviceAccount generated.V1ServiceAccountResponse
	if err := json.Unmarshal(body, &serviceAccount); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	output, err := resolveOutputFlag(cmd)
	if err != nil {
		return err
	}
	if output == config.OutputFormatJSON {
		return writeJSON(serviceAccount)
	}

	fmt.Printf("%s Service account updated successfully!\n", markPass())
	fmt.Printf("ID: %s\n", *serviceAccount.Id)
	fmt.Printf("Name: %s\n", *serviceAccount.Name)
	if serviceAccount.Description != nil {
		fmt.Printf("Description: %s\n", *serviceAccount.Description)
	}
	fmt.Printf("Active: %v\n", *serviceAccount.IsActive)

	return nil
}

func runServiceAccountDelete(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	// Get authenticated API client
	apiClient, err := getAuthenticatedAPIClient(cmd)
	if err != nil {
		return err
	}

	genClient := apiClient.GetGeneratedClient()

	// Get flags
	id, _ := cmd.Flags().GetString("id")
	if id == "" {
		return RequiredFlag("id")
	}

	force, _ := cmd.Flags().GetBool("force")
	if !force {
		ok, confirmErr := confirmYesNo(
			fmt.Sprintf("Delete service account %s? All associated tokens will be revoked", id),
			"re-run with --force to skip confirmation",
		)
		if confirmErr != nil {
			return confirmErr
		}
		if !ok {
			fmt.Fprintln(os.Stderr, "Canceled.")
			return nil
		}
	}

	// Make API call
	resp, err := apiClient.DoWithAutoRefresh(ctx, 3, func() (*http.Response, error) {
		return genClient.ServiceAccountServiceDeleteServiceAccount(ctx, id)
	})
	if err != nil {
		return fmt.Errorf("delete service account: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Accept both 200 OK and 204 No Content as success
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return api.ParseError(resp)
	}

	fmt.Println("✓ Service account deleted successfully!")
	fmt.Println("All associated tokens have been revoked.")

	return nil
}

func runServiceAccountTokenGenerate(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	// Get authenticated API client
	apiClient, err := getAuthenticatedAPIClient(cmd)
	if err != nil {
		return err
	}

	genClient := apiClient.GetGeneratedClient()

	// Get flags
	serviceAccountID, _ := cmd.Flags().GetString("service-account-id")
	if serviceAccountID == "" {
		return RequiredFlag("service-account-id")
	}

	tokenName, _ := cmd.Flags().GetString("name")
	if tokenName == "" {
		return RequiredFlag("name")
	}

	expiresInDays, _ := cmd.Flags().GetInt32("expires-in-days")

	// Create request
	req := generated.ServiceAccountServiceGenerateTokenBody{
		Name:          &tokenName,
		ExpiresInDays: &expiresInDays,
	}

	// Make API call
	resp, err := apiClient.DoWithAutoRefresh(ctx, 3, func() (*http.Response, error) {
		return genClient.ServiceAccountServiceGenerateToken(ctx, serviceAccountID, req)
	})
	if err != nil {
		return fmt.Errorf("generate token: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return api.ParseError(resp)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	var tokenResp generated.V1GenerateServiceAccountTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	// Print result with warning
	if tokenResp.Warning != nil {
		fmt.Printf("⚠️  %s\n\n", *tokenResp.Warning)
	}

	fmt.Println("✓ Token generated successfully!")
	fmt.Printf("Token ID: %s\n", *tokenResp.TokenId)
	if tokenResp.ExpiresAt != nil {
		fmt.Printf("Expires At: %s\n", tokenResp.ExpiresAt.Format(time.RFC3339))
	}
	if tokenResp.CreatedAt != nil {
		fmt.Printf("Created At: %s\n", tokenResp.CreatedAt.Format(time.RFC3339))
	}
	fmt.Println()
	fmt.Println("Token (store this securely - it will only be shown once):")
	fmt.Println("==========================================================")
	if tokenResp.Token != nil {
		fmt.Println(*tokenResp.Token)
	}

	return nil
}

func runServiceAccountTokenList(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	// Get authenticated API client
	apiClient, err := getAuthenticatedAPIClient(cmd)
	if err != nil {
		return err
	}

	genClient := apiClient.GetGeneratedClient()

	// Get flags
	serviceAccountID, _ := cmd.Flags().GetString("service-account-id")
	if serviceAccountID == "" {
		return RequiredFlag("service-account-id")
	}

	includeInactive, _ := cmd.Flags().GetBool("include-inactive")
	page, _ := cmd.Flags().GetInt32("page")
	pageSize, _ := cmd.Flags().GetInt32("page-size")

	// Create request params
	params := &generated.ServiceAccountServiceListTokensParams{
		IncludeInactive: &includeInactive,
		Page:            &page,
		PageSize:        &pageSize,
	}

	// Make API call
	resp, err := apiClient.DoWithAutoRefresh(ctx, 3, func() (*http.Response, error) {
		return genClient.ServiceAccountServiceListTokens(ctx, serviceAccountID, params)
	})
	if err != nil {
		return fmt.Errorf("list tokens: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return api.ParseError(resp)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	var listResp generated.V1ListServiceAccountTokensResponse
	if err := json.Unmarshal(body, &listResp); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	output, err := resolveOutputFlag(cmd)
	if err != nil {
		return err
	}
	if output == config.OutputFormatJSON {
		return writeJSON(listResp)
	}

	// Print results
	if listResp.Tokens == nil || len(*listResp.Tokens) == 0 {
		fmt.Println("No tokens found.")
		return nil
	}

	fmt.Printf("Tokens (Total: %d)\n\n", *listResp.TotalCount)
	for _, token := range *listResp.Tokens {
		fmt.Printf("Token ID: %s\n", *token.TokenId)
		if token.Name != nil {
			fmt.Printf("  Name: %s\n", *token.Name)
		}
		if token.Status != nil {
			fmt.Printf("  Status: %s\n", *token.Status)
		}
		if token.CreatedAt != nil {
			fmt.Printf("  Created At: %s\n", token.CreatedAt.Format(time.RFC3339))
		}
		if token.ExpiresAt != nil {
			fmt.Printf("  Expires At: %s\n", token.ExpiresAt.Format(time.RFC3339))
		}
		if token.LastUsedAt != nil {
			fmt.Printf("  Last Used At: %s\n", token.LastUsedAt.Format(time.RFC3339))
		}
		fmt.Println()
	}

	return nil
}

func runServiceAccountTokenRevoke(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	// Get authenticated API client
	apiClient, err := getAuthenticatedAPIClient(cmd)
	if err != nil {
		return err
	}

	genClient := apiClient.GetGeneratedClient()

	// Get flags
	tokenID, _ := cmd.Flags().GetString("token-id")
	if tokenID == "" {
		return RequiredFlag("token-id")
	}

	// Make API call
	resp, err := apiClient.DoWithAutoRefresh(ctx, 3, func() (*http.Response, error) {
		return genClient.ServiceAccountServiceRevokeToken(ctx, tokenID)
	})
	if err != nil {
		return fmt.Errorf("revoke token: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Accept both 200 OK and 204 No Content as success
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return api.ParseError(resp)
	}

	fmt.Println("✓ Token revoked successfully!")
	fmt.Println("The token is now invalid and cannot be used for API requests.")

	return nil
}

func runServiceAccountRoleAssign(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	// Get authenticated API client
	apiClient, err := getAuthenticatedAPIClient(cmd)
	if err != nil {
		return err
	}

	genClient := apiClient.GetGeneratedClient()

	// Get flags
	serviceAccountID, _ := cmd.Flags().GetString("service-account-id")
	if serviceAccountID == "" {
		return RequiredFlag("service-account-id")
	}

	roleID, _ := cmd.Flags().GetString("role-id")

	// Create request body
	reqBody := generated.V1ReplaceRoleForServiceAccountRequest{
		ServiceAccountId: &serviceAccountID,
	}
	if roleID != "" {
		reqBody.RoleId = &roleID
	}

	// Make API call
	resp, err := apiClient.DoWithAutoRefresh(ctx, 3, func() (*http.Response, error) {
		return genClient.RoleServiceReplaceRoleForServiceAccount(ctx, reqBody)
	})
	if err != nil {
		return fmt.Errorf("assign role: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Accept both 200 OK and 204 No Content as success
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return api.ParseError(resp)
	}

	if roleID == "" {
		fmt.Println("✓ All roles cleared successfully!")
		fmt.Println("The service account now has no roles assigned.")
	} else {
		fmt.Println("✓ Role assigned successfully!")
		fmt.Printf("Service account %s now has role %s assigned.\n", serviceAccountID, roleID)
	}

	return nil
}

func runServiceAccountGroupAdd(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	// Get authenticated API client
	apiClient, err := getAuthenticatedAPIClient(cmd)
	if err != nil {
		return err
	}

	genClient := apiClient.GetGeneratedClient()

	// Get flags
	groupID, _ := cmd.Flags().GetString("group-id")
	if groupID == "" {
		return RequiredFlag("group-id")
	}

	serviceAccountIDs, _ := cmd.Flags().GetStringSlice("service-account-ids")
	if len(serviceAccountIDs) == 0 {
		return RequiredFlagf("service-account-ids", "at least one service account ID")
	}

	// Create request body
	reqBody := generated.V1AddServiceAccountToGroupRequest{
		GroupId:           &groupID,
		ServiceAccountIds: &serviceAccountIDs,
	}

	// Make API call
	resp, err := apiClient.DoWithAutoRefresh(ctx, 3, func() (*http.Response, error) {
		return genClient.GroupServiceAddServiceAccountToGroup(ctx, reqBody)
	})
	if err != nil {
		return fmt.Errorf("add service account to group: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Accept both 200 OK and 204 No Content as success
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return api.ParseError(resp)
	}

	fmt.Printf("✓ Successfully added %d service account(s) to group %s\n", len(serviceAccountIDs), groupID)
	for _, saID := range serviceAccountIDs {
		fmt.Printf("  - %s\n", saID)
	}

	return nil
}

func runServiceAccountGroupRemove(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	// Get authenticated API client
	apiClient, err := getAuthenticatedAPIClient(cmd)
	if err != nil {
		return err
	}

	genClient := apiClient.GetGeneratedClient()

	// Get flags
	groupID, _ := cmd.Flags().GetString("group-id")
	if groupID == "" {
		return RequiredFlag("group-id")
	}

	serviceAccountIDs, _ := cmd.Flags().GetStringSlice("service-account-ids")
	if len(serviceAccountIDs) == 0 {
		return RequiredFlagf("service-account-ids", "at least one service account ID")
	}

	// Create request params (this endpoint uses query parameters)
	params := &generated.GroupServiceRemoveServiceAccountFromGroupParams{
		GroupId:           &groupID,
		ServiceAccountIds: &serviceAccountIDs,
	}

	// Make API call
	resp, err := apiClient.DoWithAutoRefresh(ctx, 3, func() (*http.Response, error) {
		return genClient.GroupServiceRemoveServiceAccountFromGroup(ctx, params)
	})
	if err != nil {
		return fmt.Errorf("remove service account from group: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Accept both 200 OK and 204 No Content as success
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return api.ParseError(resp)
	}

	fmt.Printf("✓ Successfully removed %d service account(s) from group %s\n", len(serviceAccountIDs), groupID)
	for _, saID := range serviceAccountIDs {
		fmt.Printf("  - %s\n", saID)
	}

	return nil
}
