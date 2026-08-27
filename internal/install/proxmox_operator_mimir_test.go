package install

import (
	"os"
	"testing"
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
