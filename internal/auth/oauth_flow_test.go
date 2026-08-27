package auth

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestGeneratePKCE(t *testing.T) {
	first, err := GeneratePKCE()
	if err != nil {
		t.Fatal(err)
	}
	second, err := GeneratePKCE()
	if err != nil {
		t.Fatal(err)
	}
	if first.Method != "S256" {
		t.Fatalf("method=%q", first.Method)
	}
	if first.CodeVerifier == second.CodeVerifier {
		t.Fatal("verifiers should be random")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(first.CodeVerifier)
	if err != nil || len(decoded) != 32 {
		t.Fatalf("verifier decoded length=%d err=%v", len(decoded), err)
	}
	hash := sha256.Sum256([]byte(first.CodeVerifier))
	want := base64.RawURLEncoding.EncodeToString(hash[:])
	if first.CodeChallenge != want {
		t.Fatalf("challenge=%q want %q", first.CodeChallenge, want)
	}
}

func TestGenerateState(t *testing.T) {
	state, err := GenerateState()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(state)
	if err != nil || len(decoded) != 16 {
		t.Fatalf("state decoded length=%d err=%v", len(decoded), err)
	}
}

func TestInitAndStartAuthFlow(t *testing.T) {
	client := NewOAuthClient(&OAuthConfig{
		ClientID:    "nr-cli",
		AuthURL:     "https://auth.example/authorize?existing=1",
		RedirectURL: "http://127.0.0.1/callback",
		Scopes:      []string{"openid", "profile"},
	})
	if err := client.InitAuthFlow(); err != nil {
		t.Fatal(err)
	}
	if client.GetState() == "" || client.verifier == "" || client.challenge == "" {
		t.Fatal("auth flow values were not initialized")
	}

	raw, err := client.StartAuthFlow(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	wants := map[string]string{
		"existing":              "1",
		"client_id":             "nr-cli",
		"response_type":         "code",
		"redirect_uri":          "http://127.0.0.1/callback",
		"code_challenge":        client.challenge,
		"code_challenge_method": "S256",
		"state":                 client.state,
		"scope":                 "openid profile",
	}
	for key, want := range wants {
		if got := query.Get(key); got != want {
			t.Fatalf("%s=%q want %q", key, got, want)
		}
	}
}

func TestNewOAuthClientTLSInsecure(t *testing.T) {
	client := NewOAuthClient(&OAuthConfig{TLSInsecure: true})
	transport, ok := client.httpClient.Transport.(*http.Transport)
	if !ok || transport.TLSClientConfig == nil || !transport.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("expected explicit insecure TLS transport")
	}
	if transport.TLSClientConfig.MinVersion != 0 && transport.TLSClientConfig.MinVersion < tls.VersionTLS12 {
		t.Fatalf("unexpected TLS minimum: %d", transport.TLSClientConfig.MinVersion)
	}
}

func TestExchangeCode(t *testing.T) {
	var form url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method=%s", r.Method)
		}
		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Errorf("Accept=%q", got)
		}
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse form: %v", err)
		}
		form = r.Form
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "access",
			"refresh_token": "refresh",
			"expires_in":    3600,
			"token_type":    "Bearer",
			"scope":         "openid",
		})
	}))
	defer server.Close()

	client := NewOAuthClient(&OAuthConfig{
		ClientID: "nr-cli", TokenURL: server.URL, RedirectURL: "http://127.0.0.1/callback",
	})
	client.verifier = "verifier"
	before := time.Now()
	token, err := client.ExchangeCode(context.Background(), "code-1")
	if err != nil {
		t.Fatal(err)
	}
	if token.AccessToken != "access" || token.RefreshToken != "refresh" || token.TokenType != "Bearer" || token.Scope != "openid" {
		t.Fatalf("token=%#v", token)
	}
	if token.ExpiresAt.Before(before.Add(59*time.Minute)) || token.ExpiresAt.After(before.Add(61*time.Minute)) {
		t.Fatalf("expiresAt=%s", token.ExpiresAt)
	}
	wants := map[string]string{
		"grant_type": "authorization_code", "code": "code-1", "client_id": "nr-cli",
		"redirect_uri": "http://127.0.0.1/callback", "code_verifier": "verifier",
	}
	for key, want := range wants {
		if got := form.Get(key); got != want {
			t.Fatalf("%s=%q want %q", key, got, want)
		}
	}
}

func TestExchangeCodeErrors(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		want       string
	}{
		{name: "http error", statusCode: http.StatusUnauthorized, body: `{"error":"invalid_grant"}`, want: "status 401"},
		{name: "invalid JSON", statusCode: http.StatusOK, body: `{`, want: "parse token response"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.statusCode)
				_, _ = io.WriteString(w, tt.body)
			}))
			defer server.Close()
			client := NewOAuthClient(&OAuthConfig{TokenURL: server.URL})
			_, err := client.ExchangeCode(context.Background(), "code")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err=%v want %q", err, tt.want)
			}
		})
	}
}

func TestRefreshToken(t *testing.T) {
	var form url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		form = r.Form
		_, _ = io.WriteString(w, `{"access_token":"new","refresh_token":"rotated","expires_in":120,"token_type":"Bearer"}`)
	}))
	defer server.Close()
	client := NewOAuthClient(&OAuthConfig{ClientID: "nr-cli", TokenURL: server.URL})

	token, err := client.RefreshToken(context.Background(), "old-refresh")
	if err != nil {
		t.Fatal(err)
	}
	if token.AccessToken != "new" || token.RefreshToken != "rotated" {
		t.Fatalf("token=%#v", token)
	}
	if form.Get("grant_type") != "refresh_token" || form.Get("refresh_token") != "old-refresh" ||
		form.Get("client_id") != "nr-cli" {
		t.Fatalf("form=%v", form)
	}
}

func TestRefreshTokenErrors(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "invalid grant", body: `{"error":"invalid_grant"}`, want: "re-authenticate"},
		{name: "description", body: `{"error":"server_error","error_description":"try later"}`, want: "try later"},
		{name: "unstructured", body: `not-json`, want: "status 400"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, tt.body)
			}))
			defer server.Close()
			client := NewOAuthClient(&OAuthConfig{TokenURL: server.URL})
			_, err := client.RefreshToken(context.Background(), "refresh")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err=%v want %q", err, tt.want)
			}
		})
	}
}

func TestRevokeToken(t *testing.T) {
	var path string
	var form url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_ = r.ParseForm()
		form = r.Form
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	client := NewOAuthClient(&OAuthConfig{ClientID: "nr-cli", TokenURL: server.URL + "/token"})

	if err := client.RevokeToken(context.Background(), "secret", "refresh_token"); err != nil {
		t.Fatal(err)
	}
	if path != "/revoke" {
		t.Fatalf("path=%q", path)
	}
	if form.Get("token") != "secret" || form.Get("token_type_hint") != "refresh_token" ||
		form.Get("client_id") != "nr-cli" {
		t.Fatalf("form=%v", form)
	}
}

func TestRevokeTokenError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "no", http.StatusUnauthorized)
	}))
	defer server.Close()
	client := NewOAuthClient(&OAuthConfig{TokenURL: server.URL + "/token"})
	err := client.RevokeToken(context.Background(), "token", "")
	if err == nil || !strings.Contains(err.Error(), "status 401") {
		t.Fatalf("err=%v", err)
	}
}

func TestWriteCallbackPageEscapesInput(t *testing.T) {
	recorder := httptest.NewRecorder()
	writeCallbackPage(recorder, http.StatusBadRequest, "<title>", "<headline>", "<message>", "error")
	body := recorder.Body.String()
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("code=%d headers=%v", recorder.Code, recorder.Header())
	}
	for _, escaped := range []string{"&lt;title&gt;", "&lt;headline&gt;", "&lt;message&gt;"} {
		if !strings.Contains(body, escaped) {
			t.Fatalf("body missing %q", escaped)
		}
	}
}
