package install

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/noderings/cli/internal/config"
)

func TestVNCGatewayImageRefDefaults(t *testing.T) {
	got := vncGatewayImageRef("", "")
	want := config.DefaultVNCGatewayImageRepository + ":" + config.DefaultVNCGatewayImageTagRFB
	if got != want {
		t.Fatalf("got %s want %s", got, want)
	}
	got = vncGatewayImageRef("ghcr.io/noderings/vnc-gateway", "dev")
	if got != "ghcr.io/noderings/vnc-gateway:dev" {
		t.Fatalf("got %s", got)
	}
}

func TestUsesLocalRFBImage(t *testing.T) {
	if usesLocalRFBImage(config.DefaultVNCGatewayImageTagProxmox) {
		t.Fatal("Harbor main must not load local-rfb")
	}
	if usesLocalRFBImage("") {
		t.Fatal("empty tag must not load local-rfb")
	}
	if !usesLocalRFBImage(config.DefaultVNCGatewayImageTagRFB) {
		t.Fatal("rfb tag should load local-rfb")
	}
}

func TestFindVNCGatewaySourceEnv(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VNC_GATEWAY_SOURCE", dir)
	if got := findVNCGatewaySource(); got != dir {
		t.Fatalf("got %s want %s", got, dir)
	}
}
