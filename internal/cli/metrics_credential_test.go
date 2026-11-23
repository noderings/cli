package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"

	"github.com/noderings/cli/internal/api"
	"github.com/noderings/cli/internal/logger"
)

func testLogger(t *testing.T) *logger.Logger {
	t.Helper()
	l := logrus.New()
	l.SetOutput(io.Discard)
	return &logger.Logger{Logger: l}
}

func TestIssueMetricsWriteCredentialNilClient(t *testing.T) {
	t.Parallel()
	_, err := issueMetricsWriteCredential(context.Background(), nil, "agent", testLogger(t))
	if err == nil || !strings.Contains(err.Error(), "api client is required") {
		t.Fatalf("err = %v, want api client required", err)
	}
}

func TestIssueMetricsWriteCredentialCreatesWhenNoneExist(t *testing.T) {
	t.Parallel()

	agentID := "d9234658-6186-4776-8f01-58f435533df0"
	var sawCreate bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/metrics-write-credentials"):
			if got := r.URL.Query().Get("agentId"); got != agentID {
				t.Errorf("list agentId = %q, want %q", got, agentID)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"credentials":[],"totalCount":0}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/metrics-write-credentials":
			sawCreate = true
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode create body: %v", err)
			}
			if body["agentId"] != agentID {
				t.Errorf("create agentId = %v, want %q", body["agentId"], agentID)
			}
			if body["name"] != metricsCredNamePrefix+agentID {
				t.Errorf("create name = %v, want %q", body["name"], metricsCredNamePrefix+agentID)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"token":"secret-token","credential":{"id":"cred-1"}}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	client, err := api.NewClient(&api.Config{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	client.SetToken("tok")

	token, err := issueMetricsWriteCredential(context.Background(), client, agentID, testLogger(t))
	if err != nil {
		t.Fatalf("issueMetricsWriteCredential: %v", err)
	}
	if token != "secret-token" {
		t.Fatalf("token = %q, want secret-token", token)
	}
	if !sawCreate {
		t.Fatal("expected create call")
	}
}

func TestIssueMetricsWriteCredentialRotatesExisting(t *testing.T) {
	t.Parallel()

	agentID := "d9234658-6186-4776-8f01-58f435533df0"
	credID := "cred-existing"
	var sawRotate bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/metrics-write-credentials"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"credentials":[{"id":%q,"name":%q,"agentId":%q}],"totalCount":1}`,
				credID, metricsCredNamePrefix+agentID, agentID)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/metrics-write-credentials/"+credID+":rotate":
			sawRotate = true
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"token":"rotated-token"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/metrics-write-credentials":
			t.Error("unexpected create when active credential exists")
			w.WriteHeader(http.StatusInternalServerError)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	client, err := api.NewClient(&api.Config{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	client.SetToken("tok")

	token, err := issueMetricsWriteCredential(context.Background(), client, agentID, testLogger(t))
	if err != nil {
		t.Fatalf("issueMetricsWriteCredential: %v", err)
	}
	if token != "rotated-token" {
		t.Fatalf("token = %q, want rotated-token", token)
	}
	if !sawRotate {
		t.Fatal("expected rotate call")
	}
}

func TestIssueMetricsWriteCredentialEmptyToken(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"credentials":[]}`))
		case http.MethodPost:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"token":"  "}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	client, err := api.NewClient(&api.Config{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	client.SetToken("tok")

	_, err = issueMetricsWriteCredential(context.Background(), client, "", testLogger(t))
	if err == nil || !strings.Contains(err.Error(), "empty token") {
		t.Fatalf("err = %v, want empty token", err)
	}
}

func TestIssueMetricsWriteCredentialHTTPError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"credentials":[]}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"forbidden"}`))
	}))
	defer srv.Close()

	client, err := api.NewClient(&api.Config{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	client.SetToken("tok")

	_, err = issueMetricsWriteCredential(context.Background(), client, "agent", testLogger(t))
	if err == nil {
		t.Fatal("expected error")
	}
}
