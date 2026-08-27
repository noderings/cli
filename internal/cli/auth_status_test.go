package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/noderings/cli/internal/api"
	generated "github.com/noderings/cli/internal/api/generated"
)

func unauthorizedErr() error {
	return &api.APIError{StatusCode: http.StatusUnauthorized, Message: "Authentication required."}
}

// captureStdout runs fn with stdout redirected and returns what it printed.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	fn()
	_ = w.Close()

	var sb strings.Builder
	buf := make([]byte, 4096)
	for {
		n, readErr := r.Read(buf)
		sb.Write(buf[:n])
		if readErr != nil {
			break
		}
	}
	return sb.String()
}

// A locally parseable token is not the same as a token the server accepts. Reporting
// "Authenticated" for a revoked or foreign token sends users hunting the wrong problem.
func TestPrintAuthHeadline(t *testing.T) {
	cases := []struct {
		name        string
		offline     bool
		verified    bool
		verifyErr   error
		wantContain []string
		wantAbsent  []string
	}{
		{
			name:        "verified with server",
			verified:    true,
			wantContain: []string{"Authenticated", "verified with server"},
			wantAbsent:  []string{"rejected", "not verified"},
		},
		{
			name:        "rejected is not reported as authenticated",
			verifyErr:   unauthorizedErr(),
			wantContain: []string{"rejected by server"},
			wantAbsent:  []string{"verified with server"},
		},
		{
			// An unreachable mothership must not be reported as a bad token, or operators
			// regenerate credentials to fix a network problem.
			name:        "unreachable server is unknown, not rejected",
			verifyErr:   errors.New("dial tcp 192.168.252.12:20000: connect: connection refused"),
			wantContain: []string{"Could not verify"},
			wantAbsent:  []string{"rejected by server", "verified with server"},
		},
		{
			name:        "server error is unknown, not rejected",
			verifyErr:   &api.APIError{StatusCode: http.StatusBadGateway, Message: "Bad Gateway"},
			wantContain: []string{"Could not verify"},
			wantAbsent:  []string{"rejected by server"},
		},
		{
			name:        "offline does not claim authentication",
			offline:     true,
			wantContain: []string{"not verified", "offline"},
			wantAbsent:  []string{"verified with server", "rejected"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := captureStdout(t, func() {
				printAuthHeadline(tc.offline, tc.verified, tc.verifyErr)
			})
			for _, want := range tc.wantContain {
				if !strings.Contains(out, want) {
					t.Fatalf("output missing %q:\n%s", want, out)
				}
			}
			for _, absent := range tc.wantAbsent {
				if strings.Contains(out, absent) {
					t.Fatalf("output should not contain %q:\n%s", absent, out)
				}
			}
		})
	}
}

// A rejection is only actionable if it names the cause and how to get a working credential.
// Remediation is credential-specific: telling an OAuth user to mint a service account token
// sends them somewhere that cannot fix their session.
func TestPrintAuthVerifyFailure(t *testing.T) {
	cases := []struct {
		name        string
		verifyErr   error
		oauth       bool
		wantContain []string
		wantAbsent  []string
	}{
		{
			name:        "service account rejection points at the token",
			verifyErr:   unauthorizedErr(),
			wantContain: []string{"Authentication required.", "revoked or deleted", "Access Control", "nr auth set-token"},
			wantAbsent:  []string{"nr auth refresh"},
		},
		{
			name:        "oauth rejection points at the session",
			verifyErr:   unauthorizedErr(),
			oauth:       true,
			wantContain: []string{"Authentication required.", "nr auth refresh", "nr auth login"},
			wantAbsent:  []string{"nr auth set-token", "Access Control"},
		},
		{
			name:        "unreachable server does not blame the token",
			verifyErr:   errors.New("dial tcp 192.168.252.12:20000: connect: connection refused"),
			wantContain: []string{"connection refused", "reachable", "may still be"},
			wantAbsent:  []string{"nr auth set-token", "revoked or deleted"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := captureStdout(t, func() {
				printAuthVerifyFailure(tc.verifyErr, tc.oauth)
			})
			for _, want := range tc.wantContain {
				if !strings.Contains(out, want) {
					t.Fatalf("output missing %q:\n%s", want, out)
				}
			}
			for _, absent := range tc.wantAbsent {
				if strings.Contains(out, absent) {
					t.Fatalf("output should not contain %q:\n%s", absent, out)
				}
			}
		})
	}
}

// Only a refusal from the server disproves the token; anything else leaves it unknown.
func TestIsAuthRejection(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil is not a rejection", nil, false},
		{"401 is a rejection", unauthorizedErr(), true},
		{"UNAUTHENTICATED code is a rejection", &api.APIError{Code: "UNAUTHENTICATED"}, true},
		{"403 is not a rejection", &api.APIError{StatusCode: http.StatusForbidden}, false},
		{"502 is not a rejection", &api.APIError{StatusCode: http.StatusBadGateway}, false},
		{"transport failure is not a rejection", errors.New("connection refused"), false},
		{"wrapped 401 is a rejection", fmt.Errorf("verify: %w", unauthorizedErr()), true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isAuthRejection(tc.err); got != tc.want {
				t.Fatalf("isAuthRejection(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// Offline must stay opt-in: the default has to reach the server, otherwise the command
// regresses to reporting only that a token parses.
func TestAuthStatusOfflineFlagDefaultsToVerifying(t *testing.T) {
	flag := authStatusCmd.Flags().Lookup("offline")
	if flag == nil {
		t.Fatal("auth status must expose an --offline flag")
	}
	if flag.DefValue != "false" {
		t.Fatalf("offline should default to false, got %q", flag.DefValue)
	}
}

func TestProviderReviewPendingFromListOrgs(t *testing.T) {
	t.Parallel()

	verified := true
	unverified := false
	provider := generated.ORGANIZATIONTYPEPROVIDER
	client := generated.ORGANIZATIONTYPECLIENT

	if providerReviewPendingFromListOrgs([]byte(`{"organizations":[]}`)) {
		t.Fatal("empty list is not pending")
	}
	body, err := json.Marshal(generated.V1ListOrganizationsResponse{
		Organizations: &[]generated.V1Organization{
			{Type: &provider, IsVerified: &unverified, Name: ptr("acme")},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !providerReviewPendingFromListOrgs(body) {
		t.Fatal("unverified provider org should be pending")
	}

	body, err = json.Marshal(generated.V1ListOrganizationsResponse{
		Organizations: &[]generated.V1Organization{
			{Type: &provider, IsVerified: &verified},
			{Type: &client, IsVerified: &unverified},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if providerReviewPendingFromListOrgs(body) {
		t.Fatal("verified provider plus unverified client is not pending")
	}

	body, err = json.Marshal(generated.V1ListOrganizationsResponse{
		Organizations: &[]generated.V1Organization{
			{Type: &provider, IsVerified: &verified},
			{Type: &provider, IsVerified: &unverified},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if providerReviewPendingFromListOrgs(body) {
		t.Fatal("a verified provider org should not fail-fast because another provider is still in review")
	}

	// omitempty: unverified providers may omit isVerified=false
	body, err = json.Marshal(generated.V1ListOrganizationsResponse{
		Organizations: &[]generated.V1Organization{
			{Type: &provider},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !providerReviewPendingFromListOrgs(body) {
		t.Fatal("provider org without isVerified should be treated as pending")
	}
}

func ptr[T any](v T) *T { return &v }

func TestPrintProviderReviewPendingNote(t *testing.T) {
	out := captureStdout(t, printProviderReviewPendingNote)
	if !strings.Contains(out, api.ProviderReviewPendingMessage) {
		t.Fatalf("missing pending message:\n%s", out)
	}
}
