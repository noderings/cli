package config

import "testing"

func TestHarborOperatorImageTag(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"0.1.3", "v0.1.3"},
		{"0.1.9", "v0.1.9"},
		{"0.1.4", "v0.1.4"},
		{"0.1.8", "v0.1.8"},
		{"v0.1.3", "v0.1.3"},
		{"V0.1.3", "v0.1.3"},
		{"  0.1.3  ", "v0.1.3"},
	}
	for _, tc := range cases {
		if got := HarborOperatorImageTag(tc.in); got != tc.want {
			t.Errorf("HarborOperatorImageTag(%q)=%q want %q", tc.in, got, tc.want)
		}
	}
}

func TestResolveOperatorImageTagEnvWins(t *testing.T) {
	t.Setenv(EnvProxmoxOperatorImageTag, "v9.9.9")
	got := ResolveOperatorImageTag(HypervisorDriverProxmox, DefaultProxmoxOperatorChartVersion)
	if got != "v9.9.9" {
		t.Fatalf("got %q", got)
	}
}

func TestResolveOperatorImageTagDefaultsToHarborPrefix(t *testing.T) {
	t.Setenv(EnvProxmoxOperatorImageTag, "")
	t.Setenv(EnvVirtFusionOperatorImageTag, "")
	t.Setenv(EnvSolusVMOperatorImageTag, "")
	cases := []struct {
		driver, chart string
	}{
		{HypervisorDriverProxmox, DefaultProxmoxOperatorChartVersion},
		{HypervisorDriverVirtFusion, DefaultVirtFusionOperatorChartVersion},
		{HypervisorDriverSolusVM, DefaultSolusVMOperatorChartVersion},
	}
	for _, tc := range cases {
		if got := ResolveOperatorImageTag(tc.driver, tc.chart); got != HarborOperatorImageTag(tc.chart) {
			t.Fatalf("%s %q", tc.driver, got)
		}
	}
}
