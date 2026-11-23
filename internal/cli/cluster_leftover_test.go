package cli

import (
	"strings"
	"testing"
)

func TestLeftoverLocalClusterError(t *testing.T) {
	t.Parallel()

	if err := leftoverLocalClusterError("old", "new", false); err != nil {
		t.Fatalf("no k3s: %v", err)
	}
	if err := leftoverLocalClusterError("", "new", true); err != nil {
		t.Fatalf("no previous id: %v", err)
	}
	if err := leftoverLocalClusterError("same", "same", true); err != nil {
		t.Fatalf("same agent: %v", err)
	}
	err := leftoverLocalClusterError("844edc3d", "375027c4", true)
	if err == nil {
		t.Fatal("expected leftover k3s error")
	}
	if !strings.Contains(err.Error(), "nr cluster deregister") {
		t.Fatalf("want deregister hint, got %v", err)
	}
	if !strings.Contains(err.Error(), "--resume") {
		t.Fatalf("want resume hint, got %v", err)
	}
}

func TestAgentDeleteLocalClusterGuard(t *testing.T) {
	t.Parallel()

	if err := agentDeleteLocalClusterGuard(false, false); err != nil {
		t.Fatalf("no k3s: %v", err)
	}
	if err := agentDeleteLocalClusterGuard(true, true); err != nil {
		t.Fatalf("keep-cluster: %v", err)
	}
	err := agentDeleteLocalClusterGuard(true, false)
	if err == nil {
		t.Fatal("expected refuse")
	}
	if !strings.Contains(err.Error(), "deregister") {
		t.Fatalf("got %v", err)
	}
}

func TestOperatorCredentialRecoveryHint(t *testing.T) {
	t.Parallel()
	got := operatorCredentialRecoveryHint("agent01")
	if !strings.Contains(got, "--resume --name agent01") {
		t.Fatalf("got %q", got)
	}
	if !strings.Contains(got, "deregister --name agent01") {
		t.Fatalf("got %q", got)
	}
}
