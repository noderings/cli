package install

import (
	"testing"
)

func TestParsePeeredClusterIDsFromInfoPeerOutput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		output         string
		localClusterID string
		want           []string
		wantErr        bool
	}{
		{
			name: "top level peer keys",
			output: `{
				"nr-mothership": {"network": {"podCIDR": "10.100.0.0/16"}},
				"local": {"clusterID": "agent-123"}
			}`,
			localClusterID: "agent-123",
			want:           []string{"nr-mothership"},
		},
		{
			name: "peers wrapper",
			output: `{
				"peers": {
					"nr-mothership": {"network": {}},
					"agent-123": {"network": {}}
				}
			}`,
			localClusterID: "agent-123",
			want:           []string{"nr-mothership"},
		},
		{
			name:           "empty output",
			output:         "",
			localClusterID: "agent-123",
			wantErr:        true,
		},
		{
			name:           "invalid json",
			output:         "{",
			localClusterID: "agent-123",
			wantErr:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := parsePeeredClusterIDsFromInfoPeerOutput(tt.output, tt.localClusterID)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got IDs %v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parsePeeredClusterIDsFromInfoPeerOutput() error = %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Fatalf("got %v, want %v", got, tt.want)
				}
			}
		})
	}
}
