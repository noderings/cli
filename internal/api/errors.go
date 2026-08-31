package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// ProviderReviewPendingMessage is the backend PermissionDenied copy for an
// unverified provider organization. CLI commands that hit blocked provider APIs
// must surface this string to the user (not a generic 403).
const ProviderReviewPendingMessage = "Provider organization review is pending."

// APIError represents an API error from the backend API
type APIError struct {
	StatusCode int
	Message    string
	Details    string
	Code       string // gRPC error code (e.g., "NOT_FOUND", "ALREADY_EXISTS")
}

func (e *APIError) Error() string {
	if msg := e.UserMessage(); msg != "" {
		return msg
	}
	if e == nil {
		return "API error"
	}
	if e.Code != "" && e.StatusCode != 0 {
		return fmt.Sprintf("API error (%d, %s)", e.StatusCode, e.Code)
	}
	if e.StatusCode != 0 {
		return fmt.Sprintf("API error (%d)", e.StatusCode)
	}
	if e.Code != "" {
		return e.Code
	}
	return "API error"
}

// UserMessage returns the backend error text when it is more specific than an HTTP status label.
func (e *APIError) UserMessage() string {
	if e == nil {
		return ""
	}
	msg := strings.TrimSpace(e.Message)
	if msg == "" || isHTTPStatusLabel(msg, e.StatusCode) {
		return ""
	}
	return msg
}

// IsNotFound checks if error is 404
func (e *APIError) IsNotFound() bool {
	return e.StatusCode == http.StatusNotFound || e.Code == "NOT_FOUND"
}

// IsUnauthorized checks if error is 401
func (e *APIError) IsUnauthorized() bool {
	return e.StatusCode == http.StatusUnauthorized || e.Code == "UNAUTHENTICATED"
}

// IsAlreadyExists checks if error is 409 or ALREADY_EXISTS
func (e *APIError) IsAlreadyExists() bool {
	return e.StatusCode == http.StatusConflict || e.Code == "ALREADY_EXISTS"
}

// IsQuotaExceeded reports plan/entitlement limits (HTTP 429 + quota message).
// Distinct from transient rate limiting so callers can avoid retrying forever.
func (e *APIError) IsQuotaExceeded() bool {
	if e.StatusCode != http.StatusTooManyRequests && e.Code != "RESOURCE_EXHAUSTED" {
		return false
	}
	msg := strings.ToLower(e.Message + " " + e.Details + " " + e.Code)
	return strings.Contains(msg, "plan limit") ||
		strings.Contains(msg, "quota") ||
		strings.Contains(msg, "subscription") ||
		strings.Contains(msg, "limit_key") ||
		strings.Contains(msg, "quota_limit")
}

// IsPermissionDenied checks if error is 403 or PERMISSION_DENIED
func (e *APIError) IsPermissionDenied() bool {
	return e.StatusCode == http.StatusForbidden || e.Code == "PERMISSION_DENIED"
}

// IsProviderReviewPending reports the unverified-provider marketplace lock.
func IsProviderReviewPending(err error) bool {
	if err == nil {
		return false
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return isProviderReviewPendingText(apiErr.Message) || isProviderReviewPendingText(apiErr.Details)
	}
	return isProviderReviewPendingText(err.Error())
}

func isProviderReviewPendingText(s string) bool {
	return strings.Contains(strings.ToLower(s), "provider organization review is pending")
}

// ParseError parses an HTTP error response from the backend API.
// The gateway emits google.rpc.Status JSON (code/message/details) and sometimes a
// nested camelCase error object ({error:{message,status}}).
func ParseError(resp *http.Response) error {
	apiErr := &APIError{
		StatusCode: resp.StatusCode,
		Message:    resp.Status,
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		applyStatusHeaders(apiErr, resp.Header)
		return apiErr
	}

	applyJSONError(apiErr, body)
	applyStatusHeaders(apiErr, resp.Header)
	return apiErr
}

func applyJSONError(apiErr *APIError, body []byte) {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return
	}

	var top map[string]json.RawMessage
	if err := json.Unmarshal(body, &top); err != nil {
		if !strings.HasPrefix(trimmed, "{") && !strings.HasPrefix(trimmed, "[") {
			apiErr.Message = trimmed
		}
		return
	}
	fillAPIErrorFromObject(apiErr, top)
}

func fillAPIErrorFromObject(apiErr *APIError, obj map[string]json.RawMessage) {
	if raw, ok := obj["error"]; ok {
		var nested map[string]json.RawMessage
		if json.Unmarshal(raw, &nested) == nil {
			fillAPIErrorFromObject(apiErr, nested)
		} else {
			var s string
			if json.Unmarshal(raw, &s) == nil {
				s = strings.TrimSpace(s)
				if s != "" && !strings.EqualFold(s, "error") {
					apiErr.Message = s
				}
			}
		}
	}

	for _, key := range []string{"message", "msg", "detail"} {
		raw, ok := obj[key]
		if !ok {
			continue
		}
		var s string
		if json.Unmarshal(raw, &s) == nil && strings.TrimSpace(s) != "" {
			apiErr.Message = strings.TrimSpace(s)
			break
		}
	}

	if raw, ok := obj["status"]; ok {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			if code := normalizeRPCCode(s); code != "" {
				apiErr.Code = code
			}
		}
	}

	if raw, ok := obj["code"]; ok {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			if code := normalizeRPCCode(s); code != "" {
				apiErr.Code = code
			}
		} else {
			var n int
			if json.Unmarshal(raw, &n) == nil && n >= 1 && n <= 16 {
				if name, ok := grpcCodeName[n]; ok {
					apiErr.Code = name
				}
			}
		}
	}

	raw, ok := obj["details"]
	if !ok {
		return
	}
	var details []map[string]json.RawMessage
	if json.Unmarshal(raw, &details) != nil || len(details) == 0 {
		return
	}
	d := details[0]
	if raw, ok := d["code"]; ok {
		var s string
		if json.Unmarshal(raw, &s) == nil && apiErr.Code == "" {
			apiErr.Code = normalizeRPCCode(s)
		}
	}
	if raw, ok := d["error"]; ok {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			apiErr.Details = strings.TrimSpace(s)
		}
	}
}

func applyStatusHeaders(apiErr *APIError, header http.Header) {
	if header == nil {
		return
	}
	if apiErr.Code == "" {
		for _, key := range []string{"Grpc-Status", "Grpc-Metadata-Grpc-Status"} {
			if code := normalizeRPCCode(header.Get(key)); code != "" {
				apiErr.Code = code
				break
			}
		}
	}
	if apiErr.UserMessage() != "" {
		return
	}
	if msg := header.Get("Grpc-Message"); msg != "" {
		if decoded, err := url.QueryUnescape(msg); err == nil {
			apiErr.Message = strings.TrimSpace(decoded)
		} else {
			apiErr.Message = strings.TrimSpace(msg)
		}
	}
}

func normalizeRPCCode(s string) string {
	s = strings.TrimSpace(strings.ToUpper(s))
	s = strings.ReplaceAll(s, " ", "_")
	switch s {
	case "NOT_FOUND", "ALREADY_EXISTS", "PERMISSION_DENIED", "RESOURCE_EXHAUSTED",
		"UNAUTHENTICATED", "INVALID_ARGUMENT", "FAILED_PRECONDITION", "UNAVAILABLE", "INTERNAL":
		return s
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return ""
	}
	return grpcCodeName[n]
}

func isHTTPStatusLabel(msg string, statusCode int) bool {
	if statusCode == 0 {
		return false
	}
	text := http.StatusText(statusCode)
	if strings.EqualFold(msg, text) {
		return true
	}
	want := fmt.Sprintf("%d %s", statusCode, text)
	return strings.EqualFold(msg, want)
}

// grpcCodeName maps google.rpc.Code numbers used in gateway JSON.
var grpcCodeName = map[int]string{
	3:  "INVALID_ARGUMENT",
	5:  "NOT_FOUND",
	6:  "ALREADY_EXISTS",
	7:  "PERMISSION_DENIED",
	8:  "RESOURCE_EXHAUSTED",
	9:  "FAILED_PRECONDITION",
	14: "UNAVAILABLE",
	13: "INTERNAL",
	16: "UNAUTHENTICATED",
}
