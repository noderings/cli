package config

import "testing"

func TestDefaultHarborRegistryIsNoderings(t *testing.T) {
	if DefaultHarborRegistry != "harbor.noderings.com" {
		t.Fatalf("DefaultHarborRegistry=%s want harbor.noderings.com (nrings.io is retired)", DefaultHarborRegistry)
	}
	if DefaultHarborPublicProject != "nrings" {
		t.Fatalf("DefaultHarborPublicProject=%s want nrings (public provider images)", DefaultHarborPublicProject)
	}
	if DefaultVNCGatewayImageRepository != "harbor.noderings.com/nrings/vnc-gateway" {
		t.Fatalf("DefaultVNCGatewayImageRepository=%s want harbor.noderings.com/nrings/vnc-gateway", DefaultVNCGatewayImageRepository)
	}
	if DefaultLiqoChartOCI != "oci://harbor.noderings.com/nrings/liqo" {
		t.Fatalf("DefaultLiqoChartOCI=%s want oci://harbor.noderings.com/nrings/liqo", DefaultLiqoChartOCI)
	}
}

func TestResolveVNCGatewayImageTagDefaults(t *testing.T) {
	t.Setenv(EnvVNCGatewayImageTag, "")
	t.Setenv(EnvProxmoxVNCGatewayImageTag, "")
	t.Setenv(EnvVirtFusionVNCGatewayImageTag, "")
	t.Setenv(EnvSolusVMVNCGatewayImageTag, "")

	if got := ResolveVNCGatewayImageTag(HypervisorDriverProxmox); got != DefaultVNCGatewayImageTagProxmox {
		t.Fatalf("proxmox default=%s want %s", got, DefaultVNCGatewayImageTagProxmox)
	}
	if got := ResolveVNCGatewayImageTag(HypervisorDriverVirtFusion); got != DefaultVNCGatewayImageTagRFB {
		t.Fatalf("virtfusion default=%s want %s", got, DefaultVNCGatewayImageTagRFB)
	}
	if got := ResolveVNCGatewayImageTag(HypervisorDriverSolusVM); got != DefaultVNCGatewayImageTagRFB {
		t.Fatalf("solusvm default=%s want %s", got, DefaultVNCGatewayImageTagRFB)
	}
}

func TestResolveVNCGatewayImageTagDriverEnvWinsOverGeneric(t *testing.T) {
	t.Setenv(EnvVNCGatewayImageTag, "generic")
	t.Setenv(EnvProxmoxVNCGatewayImageTag, "pve-custom")
	t.Setenv(EnvVirtFusionVNCGatewayImageTag, "vf-custom")
	t.Setenv(EnvSolusVMVNCGatewayImageTag, "svm-custom")

	if got := ResolveVNCGatewayImageTag(HypervisorDriverProxmox); got != "pve-custom" {
		t.Fatalf("proxmox=%s", got)
	}
	if got := ResolveVNCGatewayImageTag(HypervisorDriverVirtFusion); got != "vf-custom" {
		t.Fatalf("virtfusion=%s", got)
	}
	if got := ResolveVNCGatewayImageTag(HypervisorDriverSolusVM); got != "svm-custom" {
		t.Fatalf("solusvm=%s", got)
	}
}

func TestResolveVNCGatewayImageTagGenericDoesNotOverrideProxmox(t *testing.T) {
	t.Setenv(EnvVNCGatewayImageTag, DefaultVNCGatewayImageTagRFB)
	t.Setenv(EnvProxmoxVNCGatewayImageTag, "")
	t.Setenv(EnvVirtFusionVNCGatewayImageTag, "")
	t.Setenv(EnvSolusVMVNCGatewayImageTag, "")

	if got := ResolveVNCGatewayImageTag(HypervisorDriverProxmox); got != DefaultVNCGatewayImageTagProxmox {
		t.Fatalf("proxmox must stay Harbor main, got %s", got)
	}
	if got := ResolveVNCGatewayImageTag(HypervisorDriverVirtFusion); got != DefaultVNCGatewayImageTagRFB {
		t.Fatalf("virtfusion generic=%s", got)
	}
}
