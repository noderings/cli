package cli

import (
	"fmt"
	"strings"
	"testing"

	"github.com/noderings/cli/internal/config"
)

func TestBuildInstallLiqoConfigDevVsProd(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Mothership: config.MothershipConfig{Host: "203.0.113.10"}, // TEST-NET-3; not a real host IP
		Liqo: config.LiqoConfig{
			Version:                 config.DefaultLiqoVersion,
			Timeout:                 "10m",
			GWServiceType:           config.DefaultGWServiceType,
			GWServerServiceLocation: config.DefaultGWServerServiceLocation,
			PodOffloadingStrategy:   "Remote",
			PodCIDR:                 "10.200.0.0/16",
			ServiceCIDR:             "10.201.0.0/16",
			ChartOCI:                config.DefaultLiqoChartOCI,
			ChartVersion:            config.DefaultLiqoChartVersion,
		},
	}

	prod := buildInstallLiqoConfig(cfg, "agent-id", "203.0.113.10", "/kube/config", false)
	if prod.GWServiceType != config.DefaultGWServiceType {
		t.Fatalf("prod gw type = %q, want %s", prod.GWServiceType, config.DefaultGWServiceType)
	}
	if prod.DisableAPIServerSanityCheck {
		t.Fatal("prod should not disable API server sanity check")
	}
	if prod.Version != config.DefaultLiqoVersion {
		t.Fatalf("prod version = %q", prod.Version)
	}
	if prod.ChartOCI != config.DefaultLiqoChartOCI {
		t.Fatalf("prod chart oci = %q", prod.ChartOCI)
	}
	if prod.GWServerServiceLocation != config.DefaultGWServerServiceLocation {
		t.Fatalf("prod gw location = %q", prod.GWServerServiceLocation)
	}
	if prod.PodOffloadingStrategy != "Remote" {
		t.Fatalf("prod pod strategy = %q", prod.PodOffloadingStrategy)
	}

	dev := buildInstallLiqoConfig(cfg, "agent-id", "192.168.2.19", "/kube/config", true)
	if dev.GWServiceType != config.DevGWServiceType {
		t.Fatalf("dev gw type = %q, want %s", dev.GWServiceType, config.DevGWServiceType)
	}
	if !dev.DisableAPIServerSanityCheck {
		t.Fatal("dev should disable API server sanity check")
	}
	if dev.GWServerServiceLocation != config.DefaultGWServerServiceLocation {
		t.Fatalf("dev gw location = %q, want %s", dev.GWServerServiceLocation, config.DefaultGWServerServiceLocation)
	}
	if dev.GWClientAddress != "" {
		t.Fatalf("dev must not set gw client address, got %q", dev.GWClientAddress)
	}

	peerDev := buildPeerLiqoConfig(cfg, "/kube/config", "192.168.2.19", true)
	if peerDev.GWServiceType != config.DevGWServiceType {
		t.Fatalf("dev peer gw type = %q, want %s", peerDev.GWServiceType, config.DevGWServiceType)
	}
	if peerDev.GWClientAddress != "" {
		t.Fatalf("dev peer must not set gw client address, got %q", peerDev.GWClientAddress)
	}

	// --dev ignores explicit gw client address/port from config.
	cfg.Liqo.GWClientAddress = "198.51.100.1"
	cfg.Liqo.GWClientPort = "31001"
	devIgnoresClient := buildInstallLiqoConfig(cfg, "agent-id", "192.168.2.19", "/kube/config", true)
	if devIgnoresClient.GWClientAddress != "" || devIgnoresClient.GWClientPort != "" {
		t.Fatalf("dev should clear gw client overrides, got address=%q port=%q",
			devIgnoresClient.GWClientAddress, devIgnoresClient.GWClientPort)
	}
	peerDevIgnores := buildPeerLiqoConfig(cfg, "/kube/config", "192.168.2.19", true)
	if peerDevIgnores.GWClientAddress != "" || peerDevIgnores.GWClientPort != "" {
		t.Fatalf("dev peer should clear gw client overrides, got address=%q port=%q",
			peerDevIgnores.GWClientAddress, peerDevIgnores.GWClientPort)
	}

	peerProd := buildPeerLiqoConfig(cfg, "/kube/config", "203.0.113.10", false)
	if peerProd.GWClientAddress != "198.51.100.1" {
		t.Fatalf("prod peer should keep explicit address, got %q", peerProd.GWClientAddress)
	}
	if peerProd.GWClientPort != "31001" {
		t.Fatalf("prod peer should keep explicit port, got %q", peerProd.GWClientPort)
	}
	cfg.Liqo.GWClientAddress = ""
	cfg.Liqo.GWClientPort = ""
	peerProdNoOverride := buildPeerLiqoConfig(cfg, "/kube/config", "203.0.113.10", false)
	if peerProdNoOverride.GWClientAddress != "" {
		t.Fatalf("prod peer must not invent gw client address, got %q", peerProdNoOverride.GWClientAddress)
	}
}

func TestResolveRemoteClusterIDNoDevFallback(t *testing.T) {
	t.Parallel()

	opts := clusterRegisterOpts{dev: true}
	_, err := resolveRemoteClusterID(nil, opts, "", fmt.Errorf("detect failed"))
	if err == nil {
		t.Fatal("expected error when detection fails; --dev must not invent a cluster id")
	}

	got, err := resolveRemoteClusterID(nil, opts, "nr-mothership", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "nr-mothership" {
		t.Fatalf("got %q, want detected id", got)
	}
}

func TestCollectOffloadNamespaces(t *testing.T) {
	t.Parallel()

	got := collectOffloadNamespaces(clusterRegisterOpts{
		vncGatewayNamespace: config.DefaultVNCGatewayNamespace,
		offloadNamespaces:   []string{"extra", config.DefaultVNCGatewayNamespace},
	})
	want := []string{config.DefaultVNCGatewayNamespace, "extra"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}

	withOperator := collectOffloadNamespaces(clusterRegisterOpts{
		vncGatewayNamespace: config.DefaultVNCGatewayNamespace,
		operatorNamespace:   "custom-operator",
	})
	wantWithOp := []string{config.DefaultVNCGatewayNamespace, "custom-operator"}
	if len(withOperator) != len(wantWithOp) {
		t.Fatalf("got %v, want %v", withOperator, wantWithOp)
	}
}

func TestParseHypervisorDriver(t *testing.T) {
	t.Parallel()

	got, err := parseHypervisorDriver("")
	if err != nil || got != config.HypervisorDriverProxmox {
		t.Fatalf("empty driver got %q err=%v", got, err)
	}
	got, err = parseHypervisorDriver("VirtFusion")
	if err != nil || got != config.HypervisorDriverVirtFusion {
		t.Fatalf("virtfusion got %q err=%v", got, err)
	}
	got, err = parseHypervisorDriver("PLATFORM_DRIVER_SOLUSVM")
	if err != nil || got != config.HypervisorDriverSolusVM {
		t.Fatalf("proto solusvm got %q err=%v", got, err)
	}
	got, err = parseHypervisorDriver("solusvm")
	if err != nil || got != config.HypervisorDriverSolusVM {
		t.Fatalf("solusvm got %q err=%v", got, err)
	}
	if _, err := parseHypervisorDriver("xen"); err == nil {
		t.Fatal("expected error for unknown driver")
	}
}

func TestResolveHypervisorDriver(t *testing.T) {
	t.Parallel()

	if _, err := resolveHypervisorDriver("", false, ""); err == nil {
		t.Fatal("expected error when org has no driver")
	} else if !strings.Contains(err.Error(), "--hypervisor-driver") {
		t.Fatalf("empty-org error should mention --hypervisor-driver, got %v", err)
	}
	got, err := resolveHypervisorDriver("", false, config.HypervisorDriverVirtFusion)
	if err != nil || got != config.HypervisorDriverVirtFusion {
		t.Fatalf("inherit org got %q err=%v", got, err)
	}
	got, err = resolveHypervisorDriver("solusvm", true, config.HypervisorDriverSolusVM)
	if err != nil || got != config.HypervisorDriverSolusVM {
		t.Fatalf("matching flag got %q err=%v", got, err)
	}
	if _, err := resolveHypervisorDriver("solusvm", true, config.HypervisorDriverVirtFusion); err == nil {
		t.Fatal("expected mismatch error")
	}
	got, err = resolveHypervisorDriver("virtfusion", true, "")
	if err != nil || got != config.HypervisorDriverVirtFusion {
		t.Fatalf("explicit flag with empty org got %q err=%v", got, err)
	}
}

func TestValidateRegisterHypervisorOpts(t *testing.T) {
	t.Parallel()

	if err := validateRegisterHypervisorOpts(clusterRegisterOpts{
		hypervisorDriver:     config.HypervisorDriverVirtFusion,
		proxmoxInstancesFile: "pve.yaml",
	}); err == nil {
		t.Fatal("expected error when proxmox instances file is set for virtfusion")
	}
	if err := validateRegisterHypervisorOpts(clusterRegisterOpts{
		hypervisorDriver:        config.HypervisorDriverProxmox,
		virtfusionInstancesFile: "vf.yaml",
	}); err == nil {
		t.Fatal("expected error when virtfusion instances file is set for proxmox")
	}
	if err := validateRegisterHypervisorOpts(clusterRegisterOpts{
		hypervisorDriver:        config.HypervisorDriverVirtFusion,
		virtfusionInstancesFile: "vf.yaml",
	}); err != nil {
		t.Fatal(err)
	}
	if err := validateRegisterHypervisorOpts(clusterRegisterOpts{
		hypervisorDriver:     config.HypervisorDriverSolusVM,
		proxmoxInstancesFile: "pve.yaml",
	}); err == nil {
		t.Fatal("expected error when proxmox instances file is set for solusvm")
	}
	if err := validateRegisterHypervisorOpts(clusterRegisterOpts{
		hypervisorDriver:     config.HypervisorDriverSolusVM,
		solusvmInstancesFile: "svm.yaml",
	}); err != nil {
		t.Fatal(err)
	}
	if err := validateRegisterHypervisorOpts(clusterRegisterOpts{
		hypervisorDriver:     config.HypervisorDriverProxmox,
		solusvmInstancesFile: "svm.yaml",
	}); err == nil {
		t.Fatal("expected error when solusvm instances file is set for proxmox")
	}
}

func TestOperatorHelmName(t *testing.T) {
	t.Parallel()
	if got := operatorHelmName(config.HypervisorDriverVirtFusion); got != "virtfusion-operator" {
		t.Fatalf("got %q", got)
	}
	if got := operatorHelmName(config.HypervisorDriverSolusVM); got != "solusvm-operator" {
		t.Fatalf("got %q", got)
	}
	if got := operatorHelmName(""); got != "proxmox-operator" {
		t.Fatalf("got %q", got)
	}
}

func TestBuildInstallLiqoConfigDefaultPodOffloadingStrategy(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Liqo: config.LiqoConfig{
			Version: config.DefaultLiqoVersion,
			Timeout: "10m",
		},
	}
	got := buildInstallLiqoConfig(cfg, "agent-id", "203.0.113.10", "/kube/config", false)
	if got.PodOffloadingStrategy != config.DefaultPodOffloadingStrategy {
		t.Fatalf("got %q, want %q", got.PodOffloadingStrategy, config.DefaultPodOffloadingStrategy)
	}
	if got.GWServerServiceLocation != config.DefaultGWServerServiceLocation {
		t.Fatalf("got %q, want %q", got.GWServerServiceLocation, config.DefaultGWServerServiceLocation)
	}
}
