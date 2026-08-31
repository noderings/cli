package cli

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/noderings/cli/internal/api"
)

func newDeleteAgentResponse(t *testing.T, status int, body string) *http.Response {
	t.Helper()
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestDeleteAgentResponseErr(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		status  int
		body    string
		wantErr bool
	}{
		{
			name:   "success",
			status: http.StatusOK,
			body:   `{"success":true}`,
		},
		{
			name:   "not found is treated as already deleted",
			status: http.StatusNotFound,
			body:   `{"message":"Cluster was not found.","details":[{"code":"NOT_FOUND"}]}`,
		},
		{
			name:    "server error surfaces",
			status:  http.StatusInternalServerError,
			body:    `{"message":"Something went wrong."}`,
			wantErr: true,
		},
		{
			name:    "permission denied surfaces",
			status:  http.StatusForbidden,
			body:    `{"message":"denied","details":[{"code":"PERMISSION_DENIED"}]}`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := deleteAgentResponseErr(newDeleteAgentResponse(t, tt.status, tt.body))
			if tt.wantErr && err == nil {
				t.Fatal("expected error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

// DoWithAutoRefresh maps a 404 onto an error and never returns the response, so the
// already-deleted case must be recognized on the error path. Testing deleteAgentResponseErr
// with a bare 404 response alone would pass while the real deregister still fails.
func TestDeleteAgentNotFoundArrivesOnErrorPath(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"Agent not found.","details":[{"code":"NOT_FOUND"}]}`))
	}))
	defer srv.Close()

	client, err := api.NewClient(&api.Config{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	ctx := context.Background()
	resp, err := client.DoWithAutoRefresh(ctx, deleteAgentRetries, func() (*http.Response, error) {
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodDelete, srv.URL, nil)
		if reqErr != nil {
			return nil, reqErr
		}
		return http.DefaultClient.Do(req)
	})
	if resp != nil {
		_ = resp.Body.Close()
		t.Fatal("expected no response for 404")
	}
	if err == nil {
		t.Fatal("expected an error for 404")
	}
	if !isAgentNotFound(err) {
		t.Fatalf("isAgentNotFound(%v) = false, want true", err)
	}
}

func TestDeleteAgentResponseErrPreservesAPIError(t *testing.T) {
	t.Parallel()

	err := deleteAgentResponseErr(newDeleteAgentResponse(
		t, http.StatusInternalServerError, `{"message":"boom"}`,
	))
	if err == nil {
		t.Fatal("expected error")
	}
	var apiErr *api.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *api.APIError, got %T", err)
	}
	if apiErr.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", apiErr.StatusCode, http.StatusInternalServerError)
	}
}
