package config

import (
	"os"
	"testing"
)

func TestApplyMothershipProfile(t *testing.T) {
	c := &Config{
		Mothership: MothershipConfig{
			Host:         "10.0.0.5",
			APIPort:      20000,
			FrontendPort: 3000,
			TLSInsecure:  true,
		},
		API: APIConfig{
			BaseURL:     "https://api.noderings.com",
			TLSInsecure: false,
		},
		Frontend: FrontendConfig{BaseURL: "http://localhost:3000"},
	}

	t.Cleanup(func() {
		_ = os.Unsetenv("NR_API_URL")
		_ = os.Unsetenv("NR_FRONTEND_URL")
		_ = os.Unsetenv("NR_API_TLS_INSECURE")
	})

	applyMothershipProfile(c)

	if got := c.API.BaseURL; got != "https://10.0.0.5:20000" {
		t.Fatalf("API.BaseURL = %q, want https://10.0.0.5:20000", got)
	}
	if got := c.Frontend.BaseURL; got != "http://10.0.0.5:3000" {
		t.Fatalf("Frontend.BaseURL = %q, want http://10.0.0.5:3000", got)
	}
	if !c.API.TLSInsecure {
		t.Fatal("expected API.TLSInsecure true from mothership")
	}
}

func TestApplyMothershipProfileRespectsNRAPIURL(t *testing.T) {
	t.Setenv("NR_API_URL", "https://custom.example:9999")

	// Simulate viper having applied NR_API_URL into API.BaseURL before applyMothershipProfile.
	c := &Config{
		Mothership: MothershipConfig{Host: "10.0.0.5", APIPort: 20000, FrontendPort: 3000, TLSInsecure: true},
		API:        APIConfig{BaseURL: "https://custom.example:9999"},
		Frontend:   FrontendConfig{BaseURL: "http://localhost:3000"},
	}

	applyMothershipProfile(c)

	if got := c.API.BaseURL; got != "https://custom.example:9999" {
		t.Fatalf("API.BaseURL = %q, want NR_API_URL value preserved (not overwritten by mothership)", got)
	}
	if got := c.Frontend.BaseURL; got != "http://10.0.0.5:3000" {
		t.Fatalf("Frontend.BaseURL = %q, want mothership-derived frontend", got)
	}
}
