package api

import (
	"testing"

	generated "github.com/noderings/cli/internal/api/generated"
)

func strPtr(s string) *string { return &s }

func TestParsePlatformVersions(t *testing.T) {
	pv := &generated.V1PlatformVersions{
		SchemaVersion: strPtr("1"),
		Components: &[]generated.V1ComponentVersion{
			{
				Name:           strPtr("Helm"),
				Version:        strPtr("v3.16.4"),
				MinVersion:     strPtr("v3.12.0"),
				DownloadUrl:    strPtr("https://example.com/helm.tgz"),
				ChecksumSha256: strPtr("abc"),
			},
			{
				Name:    strPtr("k3s"),
				Version: strPtr("v1.31.5+k3s1"),
			},
			{
				Name: strPtr(""), // skipped
			},
		},
	}

	pins, err := ParsePlatformVersions(pv)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pins.SchemaVersion != "1" {
		t.Fatalf("schema version: got %q", pins.SchemaVersion)
	}
	helm, ok := pins.Get("helm")
	if !ok {
		t.Fatal("expected helm pin")
	}
	if helm.Version != "v3.16.4" || helm.DownloadURL == "" || helm.ChecksumSHA256 != "abc" {
		t.Fatalf("unexpected helm pin: %+v", helm)
	}
	if pins.VersionOr("k3s", "fallback") != "v1.31.5+k3s1" {
		t.Fatalf("k3s version mismatch")
	}
	if pins.VersionOr("missing", "fallback") != "fallback" {
		t.Fatalf("expected fallback")
	}
}

func TestParsePlatformVersionsNil(t *testing.T) {
	if _, err := ParsePlatformVersions(nil); err == nil {
		t.Fatal("expected error for nil input")
	}
}
