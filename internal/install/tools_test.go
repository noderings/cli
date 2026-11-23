package install

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noderings/cli/internal/api"
)

func TestDeriveToolDownloadURL(t *testing.T) {
	tests := []struct {
		name     string
		tool     string
		version  string
		goos     string
		goarch   string
		override string
		want     string
		wantErr  bool
	}{
		{
			name:    "helm linux amd64",
			tool:    "helm",
			version: "v3.16.4",
			goos:    "linux",
			goarch:  "amd64",
			want:    "https://get.helm.sh/helm-v3.16.4-linux-amd64.tar.gz",
		},
		{
			name:    "helm without v prefix",
			tool:    "helm",
			version: "3.16.4",
			goos:    "darwin",
			goarch:  "arm64",
			want:    "https://get.helm.sh/helm-v3.16.4-darwin-arm64.tar.gz",
		},
		{
			name:    "liqoctl",
			tool:    "liqoctl",
			version: "v0.0.0-3f1654f0",
			goos:    "linux",
			goarch:  "amd64",
			want:    "oci://harbor.noderings.com/nrings/liqoctl-linux-amd64:v0.0.0-3f1654f0",
		},
		{
			name:    "arch normalization",
			tool:    "liqoctl",
			version: "v0.0.0-3f1654f0",
			goos:    "linux",
			goarch:  "aarch64",
			want:    "oci://harbor.noderings.com/nrings/liqoctl-linux-arm64:v0.0.0-3f1654f0",
		},
		{
			name:     "override wins",
			tool:     "helm",
			version:  "v3.16.4",
			goos:     "linux",
			goarch:   "amd64",
			override: "https://cdn.example/helm.tar.gz",
			want:     "https://cdn.example/helm.tar.gz",
		},
		{
			name:    "unknown tool",
			tool:    "kubectl",
			version: "v1.0.0",
			goos:    "linux",
			goarch:  "amd64",
			wantErr: true,
		},
		{
			name:    "missing version",
			tool:    "helm",
			goos:    "linux",
			goarch:  "amd64",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DeriveToolDownloadURL(tt.tool, tt.version, tt.goos, tt.goarch, tt.override)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("got %q want %q", got, tt.want)
			}
		})
	}
}

func TestCompareSemverLooseAndSatisfies(t *testing.T) {
	cmp, err := CompareSemverLoose("v3.16.4", "3.12.0")
	if err != nil {
		t.Fatal(err)
	}
	if cmp < 0 {
		t.Fatalf("expected 3.16.4 >= 3.12.0")
	}

	if !VersionSatisfies("v3.16.4", "v3.16.4", "") {
		t.Fatal("exact version should satisfy")
	}
	if VersionSatisfies("v3.12.0", "v3.16.4", "v3.12.0") {
		t.Fatal("below required pin should not satisfy even if min is met")
	}
	if !VersionSatisfies("v3.16.4", "", "v3.12.0") {
		t.Fatal("min version alone should apply when required empty")
	}
	if VersionSatisfies("", "v1.0.0", "") {
		t.Fatal("empty current should not satisfy")
	}
}

func TestVerifySHA256File(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bin")
	if err := os.WriteFile(path, []byte("hello"), 0600); err != nil {
		t.Fatal(err)
	}
	// echo -n hello | shasum -a 256
	const sum = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	if err := VerifySHA256File(path, sum); err != nil {
		t.Fatalf("expected checksum match: %v", err)
	}
	if err := VerifySHA256File(path, "deadbeef"); err == nil {
		t.Fatal("expected checksum mismatch")
	}
}

// discardLogger satisfies Logger without producing test output.
type discardLogger struct{}

func (discardLogger) Info(...any)           {}
func (discardLogger) Infof(string, ...any)  {}
func (discardLogger) Warn(...any)           {}
func (discardLogger) Warnf(string, ...any)  {}
func (discardLogger) Error(...any)          {}
func (discardLogger) Errorf(string, ...any) {}
func (discardLogger) Debug(...any)          {}
func (discardLogger) Debugf(string, ...any) {}

func TestUpstreamChecksumURL(t *testing.T) {
	cases := []struct {
		name string
		tool string
		url  string
		want string
	}{
		{
			name: "official helm host has a sidecar",
			tool: api.ComponentHelm,
			url:  "https://get.helm.sh/helm-v3.16.4-linux-arm64.tar.gz",
			want: "https://get.helm.sh/helm-v3.16.4-linux-arm64.tar.gz.sha256sum",
		},
		{
			name: "helm mirror is not assumed to publish one",
			tool: api.ComponentHelm,
			url:  "https://mirror.internal/helm-v3.16.4-linux-arm64.tar.gz",
			want: "",
		},
		{
			name: "oci liqoctl has none",
			tool: api.ComponentLiqoctl,
			url:  "oci://harbor.noderings.com/nrings/liqoctl-linux-arm64:v1.1.1",
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := upstreamChecksumURL(tc.tool, tc.url); got != tc.want {
				t.Fatalf("upstreamChecksumURL(%q,%q)=%q want %q", tc.tool, tc.url, got, tc.want)
			}
		})
	}
}

func TestFetchReleaseSHA256Formats(t *testing.T) {
	const digest = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"

	cases := []struct {
		name    string
		body    string
		asset   string
		want    string
		wantErr bool
	}{
		{
			name:  "checksums list",
			body:  "deadbeef  other.tar.gz\n" + digest + "  helm-v3.16.4-linux-arm64.tar.gz\n",
			asset: "helm-v3.16.4-linux-arm64.tar.gz",
			want:  digest,
		},
		{
			name:  "per-artifact sidecar with filename",
			body:  digest + "  helm-v3.16.4-linux-arm64.tar.gz\n",
			asset: "helm-v3.16.4-linux-arm64.tar.gz",
			want:  digest,
		},
		{
			name:  "bare digest sidecar",
			body:  digest + "\n",
			asset: "helm-v3.16.4-linux-arm64.tar.gz",
			want:  digest,
		},
		{
			name:    "asset absent from list",
			body:    "deadbeef  other.tar.gz\n",
			asset:   "helm-v3.16.4-linux-arm64.tar.gz",
			wantErr: true,
		},
		{
			name:    "single field that is not a digest",
			body:    "not-a-checksum\n",
			asset:   "helm-v3.16.4-linux-arm64.tar.gz",
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, tc.body)
			}))
			defer srv.Close()

			boot := &ToolBootstrapper{logger: discardLogger{}, httpClient: srv.Client()}
			got, err := boot.fetchReleaseSHA256(context.Background(), srv.URL, tc.asset)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestVerifyDownload(t *testing.T) {
	// echo -n hello | shasum -a 256
	const helloSum = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"

	newArtifact := func(t *testing.T) string {
		t.Helper()
		p := filepath.Join(t.TempDir(), "artifact")
		if err := os.WriteFile(p, []byte("hello"), 0600); err != nil {
			t.Fatal(err)
		}
		return p
	}

	t.Run("server pin is used when present", func(t *testing.T) {
		boot := &ToolBootstrapper{logger: discardLogger{}, httpClient: http.DefaultClient}
		pin := api.ComponentPin{Name: api.ComponentHelm, ChecksumSHA256: helloSum}
		err := boot.verifyDownload(context.Background(), pin, helmDownloadBase+"helm.tar.gz", newArtifact(t))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("server pin mismatch fails", func(t *testing.T) {
		boot := &ToolBootstrapper{logger: discardLogger{}, httpClient: http.DefaultClient}
		pin := api.ComponentPin{Name: api.ComponentHelm, ChecksumSHA256: strings.Repeat("a", 64)}
		err := boot.verifyDownload(context.Background(), pin, helmDownloadBase+"helm.tar.gz", newArtifact(t))
		if err == nil {
			t.Fatal("expected checksum mismatch")
		}
	})

	// Regression: the control plane pins no digests, which used to abort every registration.
	t.Run("oci pull without a pin is accepted", func(t *testing.T) {
		boot := &ToolBootstrapper{logger: discardLogger{}, httpClient: http.DefaultClient}
		pin := api.ComponentPin{Name: api.ComponentLiqoctl}
		err := boot.verifyDownload(context.Background(), pin,
			"oci://harbor.noderings.com/nrings/liqoctl-linux-arm64:v1.1.1", newArtifact(t))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("no pin and no sidecar still fails with actionable message", func(t *testing.T) {
		boot := &ToolBootstrapper{logger: discardLogger{}, httpClient: http.DefaultClient}
		pin := api.ComponentPin{Name: api.ComponentHelm}
		err := boot.verifyDownload(context.Background(), pin,
			"https://mirror.internal/helm-v3.16.4-linux-arm64.tar.gz", newArtifact(t))
		if err == nil {
			t.Fatal("expected error for unverifiable download")
		}
		if !strings.Contains(err.Error(), "verify_checksums=false") {
			t.Fatalf("error should name the escape hatch, got: %v", err)
		}
	})
}

func TestVersionSatisfiesTable(t *testing.T) {
	cases := []struct {
		current, required, min string
		want                   bool
	}{
		{"v1.1.1", "v1.1.1", "", true},
		{"v1.2.0", "v1.1.1", "", true},
		{"v1.0.0", "v1.1.1", "v1.0.0", false},
		{"v1.0.0", "", "v1.0.0", true},
		{"v0.9.0", "v1.1.1", "v1.0.0", false},
		{"", "v1.0.0", "", false},
		{"Client version: v0.0.0-3f1654f0", "v0.0.0-3f1654f0", "", true},
		{"Client version: v1.1.1", "v0.0.0-3f1654f0", "", false},
		{"v1.1.1", "v0.0.0-3f1654f0", "", false},
	}
	for _, tc := range cases {
		if got := VersionSatisfies(tc.current, tc.required, tc.min); got != tc.want {
			t.Fatalf("VersionSatisfies(%q,%q,%q)=%v want %v", tc.current, tc.required, tc.min, got, tc.want)
		}
	}
}
