package install

import "testing"

func TestNormalizeHypervisorAPIURL(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"https://192.168.1.103/", "https://192.168.1.103"},
		{"https://192.168.1.103", "https://192.168.1.103"},
		{"  https://192.168.1.103///  ", "https://192.168.1.103"},
		{"https://pve.example:8006/", "https://pve.example:8006"},
		{"https://cp.example.com/path/", "https://cp.example.com/path"},
		{"", ""},
	}
	for _, tc := range tests {
		if got := NormalizeHypervisorAPIURL(tc.in); got != tc.want {
			t.Errorf("NormalizeHypervisorAPIURL(%q)=%q want %q", tc.in, got, tc.want)
		}
	}
}
