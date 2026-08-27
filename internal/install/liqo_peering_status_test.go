package install

import "testing"

func TestParsePeeringComplete(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		output string
		want   bool
	}{
		{
			name:   "accepted resource slice",
			output: `{"nr-mothership":{"authentication":{"status":"Healthy","resourceSlices":[{"accepted":true}]},"network":{"status":"Healthy"}}}`,
			want:   true,
		},
		{
			name:   "virtual node present",
			output: `{"nr-mothership":{"authentication":{"status":"Healthy","resourceSlices":[]},"network":{"status":"Healthy"},"offloading":{"virtualNodes":[{"name":"vn"}]}}}`,
			want:   true,
		},
		{
			name:   "stuck unaccepted",
			output: `{"nr-mothership":{"authentication":{"status":"Healthy","resourceSlices":[{"accepted":false}]},"network":{"status":"Healthy"}}}`,
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := parsePeeringComplete(tt.output); got != tt.want {
				t.Fatalf("parsePeeringComplete() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParsePeeringNeedsReset(t *testing.T) {
	t.Parallel()

	output := `{"nr-mothership":{"authentication":{"status":"Healthy","resourceSlices":[{"accepted":false}]},"network":{"status":"Healthy"}}}`
	if !parsePeeringNeedsReset(output) {
		t.Fatal("expected reset needed for unaccepted resource slice with healthy network/auth")
	}
}
