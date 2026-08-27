package api

import (
	"context"
	"net/http"
	"testing"
)

func TestSetOrganizationIDSetsHeader(t *testing.T) {
	c, err := NewClient(&Config{BaseURL: "https://example.invalid"})
	if err != nil {
		t.Fatal(err)
	}
	c.SetToken("tok")
	c.SetOrganizationID("526ad533-9fef-47ee-9cd6-354c9b9e5cc5")

	req, err := http.NewRequest(http.MethodGet, "https://example.invalid/v1/agents", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, editor := range c.GetGeneratedClient().RequestEditors {
		if err := editor(context.Background(), req); err != nil {
			t.Fatal(err)
		}
	}
	if got := req.Header.Get("Authorization"); got != "Bearer tok" {
		t.Fatalf("Authorization=%q", got)
	}
	if got := req.Header.Get(OrganizationIDHeader); got != "526ad533-9fef-47ee-9cd6-354c9b9e5cc5" {
		t.Fatalf("%s=%q", OrganizationIDHeader, got)
	}

	c.SetOrganizationID("")
	req, err = http.NewRequest(http.MethodGet, "https://example.invalid/v1/agents", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, editor := range c.GetGeneratedClient().RequestEditors {
		if err := editor(context.Background(), req); err != nil {
			t.Fatal(err)
		}
	}
	if req.Header.Get(OrganizationIDHeader) != "" {
		t.Fatalf("empty org id should omit %s, got %q", OrganizationIDHeader, req.Header.Get(OrganizationIDHeader))
	}
}
