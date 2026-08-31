package cli

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/noderings/cli/internal/api"
)

func TestIsAgentNotFound(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "nil error",
			err:  nil,
			want: false,
		},
		{
			name: "sentinel",
			err:  ErrAgentNotFound,
			want: true,
		},
		{
			name: "wrapped sentinel",
			err:  fmt.Errorf("lookup agent by name: %w", ErrAgentNotFound),
			want: true,
		},
		{
			name: "api error 404",
			err:  &api.APIError{StatusCode: http.StatusNotFound, Message: "Agent not found."},
			want: true,
		},
		{
			// The gateway can carry a gRPC NOT_FOUND on a non-404 status.
			name: "api error NOT_FOUND code without 404 status",
			err:  &api.APIError{StatusCode: http.StatusBadRequest, Code: "NOT_FOUND"},
			want: true,
		},
		{
			name: "wrapped api error 404",
			err:  fmt.Errorf("delete agent: %w", &api.APIError{StatusCode: http.StatusNotFound}),
			want: true,
		},
		{
			name: "api error 500",
			err:  &api.APIError{StatusCode: http.StatusInternalServerError, Message: "boom"},
			want: false,
		},
		{
			name: "unrelated error",
			err:  fmt.Errorf("connection refused"),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := isAgentNotFound(tt.err); got != tt.want {
				t.Fatalf("isAgentNotFound(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
