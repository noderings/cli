package cli

import (
	"testing"

	generated "github.com/noderings/cli/internal/api/generated"
)

func TestParseAgentGatewayRegion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    generated.V1AgentGatewayRegion
		wantErr bool
	}{
		{name: "AMS01 uppercase", input: "AMS01", want: generated.V1AgentGatewayRegionAGENTGATEWAYREGIONAMS01},
		{name: "ams01 lowercase", input: "ams01", want: generated.V1AgentGatewayRegionAGENTGATEWAYREGIONAMS01},
		{name: "proto enum", input: "AGENT_GATEWAY_REGION_AMS01", want: generated.V1AgentGatewayRegionAGENTGATEWAYREGIONAMS01},
		{name: "legacy FRA01 rejected", input: "FRA01", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseAgentGatewayRegion(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got region %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}
