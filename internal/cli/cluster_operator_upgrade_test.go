package cli

import (
	"testing"

	"github.com/noderings/cli/internal/config"
)

func TestHypervisorCredsProvidedFile(t *testing.T) {
	t.Parallel()
	ok, err := hypervisorCredsProvided(config.HypervisorDriverProxmox, clusterRegisterOpts{
		proxmoxInstancesFile: "/tmp/pve.yaml",
	})
	if err != nil || !ok {
		t.Fatalf("file should count as credentials provided: ok=%v err=%v", ok, err)
	}
}

func TestHypervisorCredsProvidedNone(t *testing.T) {
	t.Setenv("PROXMOX_URL", "")
	t.Setenv("PROXMOX_USERNAME", "")
	t.Setenv("PROXMOX_TOKEN_ID", "")
	t.Setenv("PROXMOX_TOKEN_SECRET", "")
	t.Setenv(config.EnvProxmoxInstancesFile, "")
	ok, err := hypervisorCredsProvided(config.HypervisorDriverProxmox, clusterRegisterOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected no credentials")
	}
}
