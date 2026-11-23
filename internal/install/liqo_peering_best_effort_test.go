package install

import "testing"

func TestIsBenignUnpeerLogLine(t *testing.T) {
	t.Parallel()

	cases := []struct {
		line string
		want bool
	}{
		{
			line: `ERRO: error: an error occurred while checking bidirectional peering: Error from server (NotFound): foreignclusters.core.liqo.io "foreign cluster with ID nr-mothership" not found`,
			want: true,
		},
		{
			line: `INFO: Network configuration correctly retrieved`,
			want: false,
		},
		{
			line: `ERRO: error: an error occurred while retrieving cluster id: Error from server (NotFound): configmaps "configmaps" not found`,
			want: false,
		},
	}

	for _, tc := range cases {
		if got := isBenignUnpeerLogLine(tc.line); got != tc.want {
			t.Fatalf("isBenignUnpeerLogLine(%q)=%v, want %v", tc.line, got, tc.want)
		}
	}
}
