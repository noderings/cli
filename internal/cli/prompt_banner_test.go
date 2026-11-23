package cli

import (
	"strings"
	"testing"
)

func TestCredentialBannerPlain(t *testing.T) {
	got := formatCredentialBanner("Configure Proxmox API access", false)
	if strings.Contains(got, "\033") {
		t.Fatalf("plain banner included color: %q", got)
	}
	if !strings.Contains(got, "========\nConfigure Proxmox API access\n========\n") {
		t.Fatalf("banner shape: %q", got)
	}
	if !strings.Contains(got, "Waiting for your input") {
		t.Fatalf("missing wait line: %q", got)
	}
}

func TestCredentialBannerColor(t *testing.T) {
	got := formatCredentialBanner("Configure Pterodactyl panel access", true)
	if !strings.Contains(got, "\033[1;36m========\033[0m") {
		t.Fatalf("missing rule color: %q", got)
	}
	if !strings.Contains(got, "\033[1;33mConfigure Pterodactyl panel access\033[0m") {
		t.Fatalf("missing title color: %q", got)
	}
}

func TestWithKubeconfig(t *testing.T) {
	plain := withKubeconfig("", "apply", "-f", "-")
	if len(plain) != 3 || plain[0] != "apply" {
		t.Fatalf("empty kubeconfig: %#v", plain)
	}
	got := withKubeconfig("/etc/rancher/k3s/k3s.yaml", "upgrade", "--install")
	if got[0] != "--kubeconfig" || got[1] != "/etc/rancher/k3s/k3s.yaml" || got[2] != "upgrade" {
		t.Fatalf("kubeconfig args: %#v", got)
	}
}
