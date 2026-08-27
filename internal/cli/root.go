package cli

import (
	"fmt"
	"os"
	"sync"

	"github.com/spf13/cobra"

	"github.com/noderings/cli/internal/config"
	"github.com/noderings/cli/internal/logger"
)

// IsDevMode checks if development mode is enabled via the root --dev flag.
func IsDevMode(cmd *cobra.Command) bool {
	if cmd == nil {
		return false
	}
	root := cmd
	for root.Parent() != nil {
		root = root.Parent()
	}
	if root.PersistentFlags().Lookup("dev") == nil {
		return false
	}
	dev, _ := root.PersistentFlags().GetBool("dev")
	return dev
}

// GetDevSettings returns development settings based on --dev flag
// If --dev is set, it enables debug, verbose, and TLS insecure mode
func GetDevSettings(cmd *cobra.Command) (debug, verbose, tlsInsecure bool) {
	return IsDevMode(cmd), IsDevMode(cmd), IsDevMode(cmd)
}

const insecureTLSWarning = "TLS certificate verification is DISABLED (tls_insecure / --dev). Do not use in production."

var insecureTLSWarnOnce sync.Once

// warnInsecureTLSOnce emits the insecure-TLS warning at most once per process. Command
// setup and API client construction each detect the condition independently, so without the
// guard a single command prints the same warning several times. A nil logger falls back to
// stderr for callers that build a client before logging is configured.
func warnInsecureTLSOnce(log *logger.Logger) {
	insecureTLSWarnOnce.Do(func() {
		if log != nil {
			log.Warn("SECURITY: " + insecureTLSWarning)
			return
		}
		fmt.Fprintln(os.Stderr, "WARNING: "+insecureTLSWarning)
	})
}

// GetDevAPIURL returns the development API URL if --dev is enabled, otherwise returns empty string
func GetDevAPIURL(cmd *cobra.Command) string {
	if IsDevMode(cmd) {
		return config.DevAPIURL()
	}
	return ""
}

// GetDevFrontendURL returns the development frontend URL if --dev is enabled, otherwise returns empty string
func GetDevFrontendURL(cmd *cobra.Command) string {
	if IsDevMode(cmd) {
		return config.DevFrontendURL()
	}
	return ""
}

// GetAPIURL returns the API URL with the following priority:
// 1. --api-url flag (if set)
// 2. Config-derived base URL (e.g. mothership.host profile for a LAN control plane)
// 3. Dev API URL (if --dev flag is set; localhost when no control-plane host is configured)
// 4. Config file API base URL
func GetAPIURL(cmd *cobra.Command, cfgBaseURL string) string {
	if apiURL, _ := cmd.Flags().GetString("api-url"); apiURL != "" {
		return apiURL
	}
	// Custom / local control-plane config wins over the localhost --dev default.
	if cfgBaseURL != "" && cfgBaseURL != config.DefaultAPIBaseURL {
		return cfgBaseURL
	}
	if devAPIURL := GetDevAPIURL(cmd); devAPIURL != "" {
		return devAPIURL
	}
	return cfgBaseURL
}

var (
	// These will be set by build flags
	version   string
	commit    string
	buildDate string
)

func init() {
	if version == "" {
		version = "dev"
	}
	if commit == "" {
		commit = "unknown"
	}
	if buildDate == "" {
		buildDate = "unknown"
	}
}

var rootCmd = &cobra.Command{
	Use:   "nr",
	Short: "NodeRings CLI - Manage VM clusters with k3s, Calico, and Liqo",
	Long: `NodeRings CLI (nr) is a command-line tool for managing VM clusters
with k3s, Calico, and Liqo. It provides commands for authentication,
cluster registration, and troubleshooting.`,
	Version:       fmt.Sprintf("%s (commit: %s, built: %s)", version, commit, buildDate),
	SilenceUsage:  true, // don't dump flag help on runtime errors (timeouts, API failures)
	SilenceErrors: true, // main prints the error once to stderr
}

// Execute runs the CLI
func Execute() error {
	return rootCmd.Execute()
}

func init() {
	initFlagErrorHandling(rootCmd)

	// Add global flags
	rootCmd.PersistentFlags().String("api-url", "", "Override API base URL (highest precedence; see also NR_API_URL)")
	rootCmd.PersistentFlags().String("config-dir", "", "Override default config directory (default: ~/.nr)")

	// Hidden dev flag - enables all development features (debug, verbose, TLS insecure)
	_ = rootCmd.PersistentFlags().Bool("dev", false, "Enable development mode")
	_ = rootCmd.PersistentFlags().MarkHidden("dev")

	// Add subcommands
	rootCmd.AddCommand(authCmd)
	rootCmd.AddCommand(agentCmd)
	rootCmd.AddCommand(clusterCmd)
	rootCmd.AddCommand(versionCmd)
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the nr version",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println(rootCmd.Version)
	},
}
