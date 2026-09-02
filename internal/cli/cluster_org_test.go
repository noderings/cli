package cli

import (
	"strings"
	"testing"

	generated "github.com/noderings/cli/internal/api/generated"
)

func TestParseOrganizationID(t *testing.T) {
	t.Parallel()
	id := "11111111-2222-4333-8444-555555555555"
	got, err := parseOrganizationID("  " + id + "  ")
	if err != nil {
		t.Fatal(err)
	}
	if got != id {
		t.Fatalf("got %q", got)
	}
	if _, err := parseOrganizationID(""); err == nil || !strings.Contains(err.Error(), "--org-id is required") {
		t.Fatalf("empty err=%v", err)
	}
	if _, err := parseOrganizationID("not-a-uuid"); err == nil || !strings.Contains(err.Error(), "must be a UUID") {
		t.Fatalf("invalid err=%v", err)
	}
}

func TestCanonicalAPIPlatformDriver(t *testing.T) {
	t.Parallel()
	vf := generated.PLATFORMDRIVERVIRTFUSION
	svm := generated.PLATFORMDRIVERSOLUSVM
	pve := generated.PLATFORMDRIVERPROXMOX
	unspec := generated.PLATFORMDRIVERUNSPECIFIED
	if got := canonicalAPIPlatformDriver(&vf); got != "virtfusion" {
		t.Fatalf("vf got %q", got)
	}
	if got := canonicalAPIPlatformDriver(&svm); got != "solusvm" {
		t.Fatalf("svm got %q", got)
	}
	if got := canonicalAPIPlatformDriver(&pve); got != "proxmox" {
		t.Fatalf("pve got %q", got)
	}
	if got := canonicalAPIPlatformDriver(&unspec); got != "" {
		t.Fatalf("unspec got %q", got)
	}
	if got := canonicalAPIPlatformDriver(nil); got != "" {
		t.Fatalf("nil got %q", got)
	}
}
