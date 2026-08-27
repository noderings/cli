package install

import (
	"os"
	"path/filepath"
	"testing"
)

func TestKubeconfigCandidatesPrefersExplicit(t *testing.T) {
	t.Parallel()
	got := kubeconfigCandidates("/custom/kubeconfig")
	if got[0] != "/custom/kubeconfig" {
		t.Fatalf("expected preferred first, got %v", got)
	}
	foundK3s := false
	for _, c := range got {
		if c == k3sDefaultKubeconfig {
			foundK3s = true
		}
	}
	if !foundK3s {
		t.Fatalf("expected k3s default in candidates: %v", got)
	}
}

func TestIsReadableFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	readable := filepath.Join(dir, "readable")
	if err := os.WriteFile(readable, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if !isReadableFile(readable) {
		t.Fatal("expected readable file")
	}
	missing := filepath.Join(dir, "missing")
	if isReadableFile(missing) {
		t.Fatal("expected missing file to be unreadable")
	}
}
