package cli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/noderings/cli/internal/api"
)

func TestRejectUnverifiedProvider(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		body      string
		status    int
		wantPend  bool
		wantOther bool
	}{
		{
			name:     "unverified provider",
			status:   http.StatusOK,
			body:     `{"organizations":[{"type":"ORGANIZATION_TYPE_PROVIDER","isVerified":false,"name":"acme"}]}`,
			wantPend: true,
		},
		{
			name:     "verified provider",
			status:   http.StatusOK,
			body:     `{"organizations":[{"type":"ORGANIZATION_TYPE_PROVIDER","isVerified":true}]}`,
			wantPend: false,
		},
		{
			name:     "blocked list still surfaces pending copy",
			status:   http.StatusForbidden,
			body:     `{"code":7,"message":"Provider organization review is pending."}`,
			wantPend: true,
		},
		{
			name:      "generic forbidden",
			status:    http.StatusForbidden,
			body:      `{"code":7,"message":"You are not allowed to perform this action."}`,
			wantOther: true,
		},
		{
			name:   "service account falls back to get organization",
			status: http.StatusUnauthorized,
			body:   `{"code":16,"message":"Your session is invalid. Please sign in again."}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/v1/organization" && tt.status == http.StatusUnauthorized {
					w.WriteHeader(http.StatusOK)
					_, _ = w.Write([]byte(`{"organization":{"type":"ORGANIZATION_TYPE_PROVIDER","isVerified":true}}`))
					return
				}
				if r.URL.Path != "/v1/organizations" {
					t.Errorf("path = %s", r.URL.Path)
				}
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			t.Cleanup(srv.Close)

			client, err := api.NewClient(&api.Config{BaseURL: srv.URL, Timeout: 2 * time.Second})
			if err != nil {
				t.Fatal(err)
			}
			err = rejectUnverifiedProvider(context.Background(), client)
			pending := api.IsProviderReviewPending(err)
			if pending != tt.wantPend {
				t.Fatalf("pending=%v want %v err=%v", pending, tt.wantPend, err)
			}
			if tt.wantPend && FormatUserError(err) != api.ProviderReviewPendingMessage {
				t.Fatalf("FormatUserError=%q", FormatUserError(err))
			}
			if tt.wantOther && (err == nil || pending) {
				t.Fatalf("expected generic error, got %v", err)
			}
			if !tt.wantPend && !tt.wantOther && err != nil {
				t.Fatalf("verified provider: %v", err)
			}
		})
	}
}
