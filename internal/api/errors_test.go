package api

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestAPIError_IsQuotaExceeded(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  *APIError
		want bool
	}{
		{
			name: "plan limit message",
			err: &APIError{
				StatusCode: http.StatusTooManyRequests,
				Message:    "Adding 1 more K8s agents would exceed your plan limit of 1. Current count: 1.",
			},
			want: true,
		},
		{
			name: "transient rate limit without quota wording",
			err: &APIError{
				StatusCode: http.StatusTooManyRequests,
				Message:    "Too Many Requests",
			},
			want: false,
		},
		{
			name: "conflict is not quota",
			err: &APIError{
				StatusCode: http.StatusConflict,
				Message:    "Agent name already exists in organization.",
				Code:       "ALREADY_EXISTS",
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.err.IsQuotaExceeded(); got != tt.want {
				t.Fatalf("IsQuotaExceeded() = %v, want %v", got, tt.want)
			}
			if tt.want && isRetryableError(tt.err) {
				t.Fatalf("quota errors must not be retryable")
			}
		})
	}
}

func TestParseErrorGatewayShapes(t *testing.T) {
	t.Parallel()

	const pending = ProviderReviewPendingMessage
	tests := []struct {
		name        string
		status      int
		body        string
		header      http.Header
		wantMsg     string
		wantCode    string
		wantPending bool
	}{
		{
			name:        "grpc-gateway google.rpc.Status",
			status:      http.StatusForbidden,
			body:        `{"code":7,"message":"Provider organization review is pending.","details":[]}`,
			wantMsg:     pending,
			wantCode:    "PERMISSION_DENIED",
			wantPending: true,
		},
		{
			name:        "camelCase nested error object",
			status:      http.StatusForbidden,
			body:        `{"error":{"code":403,"message":"Provider organization review is pending.","status":"PERMISSION_DENIED"}}`,
			wantMsg:     pending,
			wantCode:    "PERMISSION_DENIED",
			wantPending: true,
		},
		{
			name:        "string grpc code",
			status:      http.StatusForbidden,
			body:        `{"code":"PERMISSION_DENIED","message":"Provider organization review is pending."}`,
			wantMsg:     pending,
			wantCode:    "PERMISSION_DENIED",
			wantPending: true,
		},
		{
			name:     "generic permission denied is not review pending",
			status:   http.StatusForbidden,
			body:     `{"code":7,"message":"You are not allowed to perform this action."}`,
			wantMsg:  "You are not allowed to perform this action.",
			wantCode: "PERMISSION_DENIED",
		},
		{
			name:        "grpc-message header fallback",
			status:      http.StatusForbidden,
			header:      http.Header{"Grpc-Status": []string{"7"}, "Grpc-Message": []string{"Provider%20organization%20review%20is%20pending."}},
			wantMsg:     pending,
			wantCode:    "PERMISSION_DENIED",
			wantPending: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			resp := &http.Response{
				StatusCode: tt.status,
				Status:     fmt.Sprintf("%d %s", tt.status, http.StatusText(tt.status)),
				Header:     tt.header,
				Body:       io.NopCloser(strings.NewReader(tt.body)),
			}
			err := ParseError(resp)
			apiErr, ok := err.(*APIError)
			if !ok {
				t.Fatalf("ParseError type %T", err)
			}
			if apiErr.UserMessage() != tt.wantMsg {
				t.Fatalf("UserMessage=%q want %q (raw %q)", apiErr.UserMessage(), tt.wantMsg, apiErr.Message)
			}
			if apiErr.Code != tt.wantCode {
				t.Fatalf("Code=%q want %q", apiErr.Code, tt.wantCode)
			}
			if apiErr.Error() != tt.wantMsg {
				t.Fatalf("Error()=%q want user-facing %q", apiErr.Error(), tt.wantMsg)
			}
			if got := IsProviderReviewPending(err); got != tt.wantPending {
				t.Fatalf("IsProviderReviewPending=%v want %v", got, tt.wantPending)
			}
			if !apiErr.IsPermissionDenied() {
				t.Fatal("expected IsPermissionDenied")
			}
		})
	}
}

func TestIsProviderReviewPendingWrapped(t *testing.T) {
	t.Parallel()
	err := fmt.Errorf("lookup agent by name: %w", &APIError{
		StatusCode: http.StatusForbidden,
		Code:       "PERMISSION_DENIED",
		Message:    ProviderReviewPendingMessage,
	})
	if !IsProviderReviewPending(err) {
		t.Fatal("expected wrapped pending review to match")
	}
	if IsProviderReviewPending(fmt.Errorf("create agent: %w", &APIError{
		StatusCode: http.StatusForbidden,
		Message:    "You are not allowed to perform this action.",
	})) {
		t.Fatal("generic 403 must not be treated as marketplace review")
	}
}
