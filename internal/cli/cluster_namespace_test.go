package cli

import (
	"strings"
	"testing"

	"github.com/noderings/cli/internal/config"
)

func TestBuildRemoteNamespaceName(t *testing.T) {
	t.Parallel()

	agentID := "a1b2c3d4-e5f6-7890-abcd-ef1234567890"

	tests := []struct {
		name            string
		agentID         string
		localNamespace  string
		want            string
		wantErr         bool
		wantErrContains string
	}{
		{
			name:           "vnc gateway production remote name",
			agentID:        agentID,
			localNamespace: config.DefaultVNCGatewayNamespace,
			want:           agentID + config.SuffixVNCGatewayNamespace,
		},
		{
			name:           "uppercase agent id is lowercased",
			agentID:        strings.ToUpper(agentID),
			localNamespace: config.DefaultVNCGatewayNamespace,
			want:           agentID + config.SuffixVNCGatewayNamespace,
		},
		{
			name:            "missing agent id",
			agentID:         " ",
			localNamespace:  config.DefaultVNCGatewayNamespace,
			wantErr:         true,
			wantErrContains: "agent ID is required",
		},
		{
			name:            "missing local namespace",
			agentID:         agentID,
			localNamespace:  "",
			wantErr:         true,
			wantErrContains: "local namespace is required",
		},
		{
			name:            "too long namespace",
			agentID:         agentID,
			localNamespace:  strings.Repeat("a", config.MaxK8sNamespaceLen),
			wantErr:         true,
			wantErrContains: "exceeds maximum length",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := buildRemoteNamespaceName(tt.agentID, tt.localNamespace)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}
				if tt.wantErrContains != "" && !strings.Contains(err.Error(), tt.wantErrContains) {
					t.Fatalf("error %q does not contain %q", err.Error(), tt.wantErrContains)
				}
				return
			}
			if err != nil {
				t.Fatalf("buildRemoteNamespaceName() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
			if len(got) > config.MaxK8sNamespaceLen {
				t.Fatalf("remote namespace length %d exceeds %d", len(got), config.MaxK8sNamespaceLen)
			}
		})
	}
}
