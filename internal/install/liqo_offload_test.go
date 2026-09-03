package install

import (
	"fmt"
	"testing"
)

func TestRemoteNamespaceNameFromUnstructured(t *testing.T) {
	t.Parallel()

	if got := remoteNamespaceNameFromUnstructured(nil); got != "" {
		t.Fatalf("nil obj: got %q", got)
	}
	if got := remoteNamespaceNameFromUnstructured(map[string]any{
		"spec": map[string]any{"remoteNamespaceName": "bd0cb8d3-vnc-gateway"},
	}); got != "bd0cb8d3-vnc-gateway" {
		t.Fatalf("spec: got %q", got)
	}
	if got := remoteNamespaceNameFromUnstructured(map[string]any{
		"status": map[string]any{"remoteNamespaceName": "from-status"},
	}); got != "from-status" {
		t.Fatalf("status fallback: got %q", got)
	}
	if got := remoteNamespaceNameFromUnstructured(map[string]any{
		"spec":   map[string]any{"remoteNamespaceName": "from-spec"},
		"status": map[string]any{"remoteNamespaceName": "from-status"},
	}); got != "from-spec" {
		t.Fatalf("spec wins: got %q", got)
	}
}

func TestOffloadNeedsRemap(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		currentRemote string
		wantedRemote  string
		localNS       string
		want          bool
	}{
		{name: "no existing offload", currentRemote: "", wantedRemote: "new-id-vnc-gateway", localNS: "vnc-gateway"},
		{name: "same remote name", currentRemote: "new-id-vnc-gateway", wantedRemote: "new-id-vnc-gateway", localNS: "vnc-gateway"},
		{name: "empty wanted matches local default", currentRemote: "vnc-gateway", wantedRemote: "", localNS: "vnc-gateway"},
		{
			name:          "previous agent id leftover",
			currentRemote: "bd0cb8d3-2cd8-4d05-beab-38be5bedca19-vnc-gateway",
			wantedRemote:  "b3f90ef5-be65-411f-bc0a-1fbdb8f92429-vnc-gateway",
			localNS:       "vnc-gateway",
			want:          true,
		},
		{
			name:          "empty wanted but leftover remote name",
			currentRemote: "old-id-vnc-gateway",
			wantedRemote:  "",
			localNS:       "vnc-gateway",
			want:          true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := offloadNeedsRemap(tt.currentRemote, tt.wantedRemote, tt.localNS)
			if got != tt.want {
				t.Fatalf("offloadNeedsRemap(%q, %q, %q) = %v, want %v",
					tt.currentRemote, tt.wantedRemote, tt.localNS, got, tt.want)
			}
		})
	}
}

func TestIsMissingAPI(t *testing.T) {
	t.Parallel()
	if isMissingAPI(nil) {
		t.Fatal("nil is not a missing API")
	}
	if !isMissingAPI(fmt.Errorf("the server could not find the requested resource")) {
		t.Fatal("want missing API for CRD-not-installed error")
	}
	if isMissingAPI(fmt.Errorf("connection refused")) {
		t.Fatal("connection errors are not missing API")
	}
}
