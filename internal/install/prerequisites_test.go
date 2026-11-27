package install

import (
	"testing"

	"github.com/noderings/cli/internal/api"
	"github.com/noderings/cli/internal/config"
)

func TestNewToolValidatorUsesPlatformPins(t *testing.T) {
	pins := &api.PlatformPins{
		Components: map[string]api.ComponentPin{
			api.ComponentLiqoctl: {
				Name:       api.ComponentLiqoctl,
				Version:    "v0.0.0-50a39cf9",
				MinVersion: "v0.0.0-50a39cf9",
			},
			api.ComponentHelm: {
				Name:       api.ComponentHelm,
				Version:    "v3.16.4",
				MinVersion: "v3.12.0",
			},
		},
	}

	v := NewToolValidator(pins, nil)
	if got := v.requirements["liqoctl"].MinVersion; got != "0.0.0-50a39cf9" {
		t.Fatalf("liqoctl min = %q, want 0.0.0-50a39cf9", got)
	}
	if got := v.requirements["helm"].MinVersion; got != "3.12.0" {
		t.Fatalf("helm min = %q, want 3.12.0", got)
	}
}

func TestNewToolValidatorFallsBackToConfigLiqoPin(t *testing.T) {
	cfg := &config.Config{}
	cfg.Liqo.Version = "v0.0.0-deadbeef"
	cfg.Liqo.MinVersion = "v0.0.0-deadbeef"

	v := NewToolValidator(nil, cfg)
	if got := v.requirements["liqoctl"].MinVersion; got != "0.0.0-deadbeef" {
		t.Fatalf("liqoctl min = %q, want 0.0.0-deadbeef", got)
	}
}

func TestValidateVersionForkPin(t *testing.T) {
	v := NewValidator()
	ok, err := v.validateVersion("Client version: v0.0.0-50a39cf9", "0.0.0-50a39cf9")
	if err != nil {
		t.Fatalf("validateVersion: %v", err)
	}
	if !ok {
		t.Fatal("expected fork pin to satisfy matching min version")
	}
}
