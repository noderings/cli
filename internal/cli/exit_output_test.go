package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/spf13/pflag"

	"github.com/noderings/cli/internal/api"
	"github.com/noderings/cli/internal/config"
)

func TestExitCode(t *testing.T) {
	t.Parallel()
	if got := ExitCode(nil); got != ExitOK {
		t.Fatalf("nil: got %d want %d", got, ExitOK)
	}
	if got := ExitCode(pflag.ErrHelp); got != ExitOK {
		t.Fatalf("ErrHelp: got %d want %d", got, ExitOK)
	}
	if got := ExitCode(UsageErrorf("--output must be text or json")); got != ExitMisuse {
		t.Fatalf("UsageError: got %d want %d", got, ExitMisuse)
	}
	if got := ExitCode(errors.New("unknown flag: --nope")); got != ExitMisuse {
		t.Fatalf("unknown flag: got %d want %d", got, ExitMisuse)
	}
	if got := ExitCode(RequiredFlag("name")); got != ExitMisuse {
		t.Fatalf("missing required flag: got %d want %d", got, ExitMisuse)
	}
	if got := ExitCode(RequiredFlagf("token", "or use --from-env")); got != ExitMisuse {
		t.Fatalf("missing required flag with hint: got %d want %d", got, ExitMisuse)
	}
	if got := ExitCode(RequiredOneOfFlags("name", "agent-id")); got != ExitMisuse {
		t.Fatalf("missing one-of flag: got %d want %d", got, ExitMisuse)
	}
	// Wrapping must not lose the misuse classification.
	if got := ExitCode(fmt.Errorf("resolve agent: %w", RequiredFlag("name"))); got != ExitMisuse {
		t.Fatalf("wrapped required flag: got %d want %d", got, ExitMisuse)
	}
	if got := ExitCode(fmt.Errorf("provider verification failed (2 check(s))")); got != ExitError {
		t.Fatalf("runtime: got %d want %d", got, ExitError)
	}
	// Runtime failures that merely read like usage text must stay exit 1.
	for _, msg := range []string{
		"--dev requires configuration under mothership.host",
		"agent ID is required to build a namespace",
		"gateway region must be one of eu, us",
	} {
		if got := ExitCode(fmt.Errorf("%s", msg)); got != ExitError {
			t.Fatalf("runtime %q: got %d want %d", msg, got, ExitError)
		}
	}
}

func TestRequiredFlagMessages(t *testing.T) {
	t.Parallel()
	cases := []struct {
		err  error
		want string
	}{
		{RequiredFlag("agent-ip"), "--agent-ip is required"},
		{RequiredFlagf("name", "or pass --agent-id from the install command"),
			"--name is required (or pass --agent-id from the install command)"},
		{RequiredOneOfFlags("name", "agent-id"), "--name or --agent-id is required"},
		{RequiredOneOfFlags("a", "b", "c"), "--a, --b or --c is required"},
		{RequiredOneOfFlags("only"), "--only is required"},
	}
	for _, tc := range cases {
		if got := tc.err.Error(); got != tc.want {
			t.Errorf("got %q, want %q", got, tc.want)
		}
	}
}

func TestParseOutputFormat(t *testing.T) {
	t.Parallel()
	got, err := parseOutputFormat("")
	if err != nil || got != config.OutputFormatText {
		t.Fatalf("empty: got %q err %v", got, err)
	}
	got, err = parseOutputFormat("JSON")
	if err != nil || got != config.OutputFormatJSON {
		t.Fatalf("json: got %q err %v", got, err)
	}
	_, err = parseOutputFormat("yaml")
	if err == nil {
		t.Fatal("expected error for yaml")
	}
	if ExitCode(err) != ExitMisuse {
		t.Fatalf("yaml exit: got %d want %d", ExitCode(err), ExitMisuse)
	}
}

func TestWriteJSONTo(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	payload := map[string]string{"ok": "yes"}
	if err := writeJSONTo(&buf, payload); err != nil {
		t.Fatal(err)
	}
	var decoded map[string]string
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("invalid JSON on stdout buffer: %v\n%s", err, buf.String())
	}
	if decoded["ok"] != "yes" {
		t.Fatalf("decoded=%v", decoded)
	}
}

func TestFormatUserErrorProviderReviewPending(t *testing.T) {
	t.Parallel()
	pending := &api.APIError{
		StatusCode: http.StatusForbidden,
		Code:       "PERMISSION_DENIED",
		Message:    api.ProviderReviewPendingMessage,
	}
	if got := FormatUserError(fmt.Errorf("lookup agent by name: list agents: %w", pending)); got != api.ProviderReviewPendingMessage {
		t.Fatalf("got %q", got)
	}
	generic := FormatUserError(fmt.Errorf("create agent: %w", &api.APIError{
		StatusCode: http.StatusForbidden,
		Message:    "You are not allowed to perform this action.",
	}))
	if generic == api.ProviderReviewPendingMessage {
		t.Fatal("generic 403 must stay distinct from marketplace review")
	}
	if !strings.Contains(generic, "You are not allowed to perform this action.") {
		t.Fatalf("generic 403 lost backend copy: %q", generic)
	}
}
