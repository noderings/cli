package install

import "testing"

func TestRemapWithinCIDRs(t *testing.T) {
	tests := []struct {
		name    string
		address string
		spec    []string
		status  []string
		want    string
		wantErr bool
	}{
		{
			name:    "remapped external cidr",
			address: "10.72.0.1",
			spec:    []string{"10.72.0.0/16"},
			status:  []string{"10.81.0.0/16"},
			want:    "10.81.0.1",
		},
		{
			name:    "identical cidrs are returned untouched",
			address: "10.72.0.1",
			spec:    []string{"10.72.0.0/16"},
			status:  []string{"10.72.0.0/16"},
			want:    "10.72.0.1",
		},
		{
			name:    "host bits preserved across a narrower mask",
			address: "10.72.1.5",
			spec:    []string{"10.72.0.0/16"},
			status:  []string{"10.81.0.0/16"},
			want:    "10.81.1.5",
		},
		{
			name:    "second cidr matches",
			address: "10.90.0.7",
			spec:    []string{"10.72.0.0/16", "10.90.0.0/16"},
			status:  []string{"10.81.0.0/16", "10.91.0.0/16"},
			want:    "10.91.0.7",
		},
		{
			name:    "address outside every cidr",
			address: "192.168.1.161",
			spec:    []string{"10.72.0.0/16"},
			status:  []string{"10.81.0.0/16"},
			wantErr: true,
		},
		{
			name:    "invalid address",
			address: "not-an-ip",
			spec:    []string{"10.72.0.0/16"},
			status:  []string{"10.81.0.0/16"},
			wantErr: true,
		},
		{
			name:    "ipv6 remap",
			address: "fd00:72::1",
			spec:    []string{"fd00:72::/32"},
			status:  []string{"fd00:81::/32"},
			want:    "fd00:81::1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := remapWithinCIDRs(tt.address, tt.spec, tt.status)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAPIServerNeedsProxy(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want bool
	}{
		{name: "rfc1918 address", url: "https://192.168.1.161:6443", want: true},
		{name: "private 10/8", url: "https://10.0.0.5:6443", want: true},
		{name: "carrier grade nat", url: "https://100.64.3.9:6443", want: true},
		{name: "loopback", url: "https://127.0.0.1:6443", want: true},
		{name: "link local", url: "https://169.254.1.1:6443", want: true},
		{name: "public address", url: "https://95.179.200.10:6443", want: false},
		{name: "hostname is assumed routable", url: "https://api.example.com:6443", want: false},
		{name: "no scheme", url: "192.168.1.161:6443", want: true},
		{name: "empty", url: "", want: false},
		{name: "public ipv6", url: "https://[2001:db8::1]:6443", want: false},
		{name: "unique local ipv6", url: "https://[fd00::1]:6443", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := APIServerNeedsProxy(tt.url); got != tt.want {
				t.Errorf("APIServerNeedsProxy(%q) = %v, want %v", tt.url, got, tt.want)
			}
		})
	}
}
