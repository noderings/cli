package install

import (
	"os"
	"testing"

	"github.com/noderings/cli/internal/config"
)

func TestMimirTLSEnabledDefaultsSecure(t *testing.T) {
	_ = os.Unsetenv("MIMIR_TLS_ENABLED")

	if !mimirTLSEnabled() {
		t.Fatal("expected TLS on by default for bearer remote_write")
	}
}

func TestMimirTLSEnabledEnvOverride(t *testing.T) {
	t.Setenv("MIMIR_TLS_ENABLED", "true")
	if !mimirTLSEnabled() {
		t.Fatal("MIMIR_TLS_ENABLED=true should force TLS on")
	}

	t.Setenv("MIMIR_TLS_ENABLED", "false")
	if mimirTLSEnabled() {
		t.Fatal("MIMIR_TLS_ENABLED=false should force TLS off")
	}
}

func TestMimirServiceEndpointDefaultsToProduction(t *testing.T) {
	if config.DefaultMimirServiceEndpoint != "metrics.noderings.com" {
		t.Fatalf("production default=%q", config.DefaultMimirServiceEndpoint)
	}

	t.Setenv(config.EnvMimirServiceEndpoint, "")
	_ = os.Unsetenv(config.EnvMimirTLSEnabled)

	px := BaseConfigFromEnv("")
	if px.MimirServiceEndpoint != config.DefaultMimirServiceEndpoint {
		t.Fatalf("proxmox endpoint=%q want production default", px.MimirServiceEndpoint)
	}
	if !px.MimirTLSEnabled {
		t.Fatal("proxmox TLS must default on")
	}

	vf := VirtFusionBaseConfigFromEnv("")
	if vf.MimirServiceEndpoint != config.DefaultMimirServiceEndpoint {
		t.Fatalf("virtfusion endpoint=%q want production default", vf.MimirServiceEndpoint)
	}
	if !vf.MimirTLSEnabled {
		t.Fatal("virtfusion TLS must default on")
	}

	svm := SolusVMBaseConfigFromEnv("")
	if svm.MimirServiceEndpoint != config.DefaultMimirServiceEndpoint {
		t.Fatalf("solusvm endpoint=%q want production default", svm.MimirServiceEndpoint)
	}
	if !svm.MimirTLSEnabled {
		t.Fatal("solusvm TLS must default on")
	}
}

func TestMimirServiceEndpointLabEnvOverride(t *testing.T) {
	t.Setenv(config.EnvMimirServiceEndpoint, "127.0.0.1:8082")
	t.Setenv(config.EnvMimirTLSEnabled, "false")

	px := BaseConfigFromEnv("")
	if px.MimirServiceEndpoint != "127.0.0.1:8082" || px.MimirTLSEnabled {
		t.Fatalf("proxmox lab override endpoint=%q tls=%v", px.MimirServiceEndpoint, px.MimirTLSEnabled)
	}
	vf := VirtFusionBaseConfigFromEnv("")
	if vf.MimirServiceEndpoint != "127.0.0.1:8082" || vf.MimirTLSEnabled {
		t.Fatalf("virtfusion lab override endpoint=%q tls=%v", vf.MimirServiceEndpoint, vf.MimirTLSEnabled)
	}
	svm := SolusVMBaseConfigFromEnv("")
	if svm.MimirServiceEndpoint != "127.0.0.1:8082" || svm.MimirTLSEnabled {
		t.Fatalf("solusvm lab override endpoint=%q tls=%v", svm.MimirServiceEndpoint, svm.MimirTLSEnabled)
	}
}
