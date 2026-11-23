package cli

import (
	"testing"

	"github.com/spf13/cobra"

	"github.com/noderings/cli/internal/config"
)

func testCmdWithDev(t *testing.T, dev bool) *cobra.Command {
	t.Helper()

	parent := &cobra.Command{Use: "nr"}
	parent.PersistentFlags().Bool("dev", false, "")
	child := &cobra.Command{Use: "register"}
	parent.AddCommand(child)

	args := []string{}
	if dev {
		args = append(args, "--dev")
	}
	if err := parent.ParseFlags(args); err != nil {
		t.Fatalf("parse flags: %v", err)
	}
	return child
}

func TestGetAPIURLPrefersMothershipConfigOverDevLocalhost(t *testing.T) {
	t.Parallel()

	cmd := testCmdWithDev(t, true)
	got := GetAPIURL(cmd, "https://192.168.2.1:20000")
	if got != "https://192.168.2.1:20000" {
		t.Fatalf("GetAPIURL() = %q, want mothership LAN URL when configured", got)
	}
}

func TestGetAPIURLUsesDevLocalhostWithoutMothershipConfig(t *testing.T) {
	t.Parallel()

	cmd := testCmdWithDev(t, true)
	got := GetAPIURL(cmd, config.DefaultAPIBaseURL)
	if got != config.DevAPIURL() {
		t.Fatalf("GetAPIURL() = %q, want %q", got, config.DevAPIURL())
	}
}
