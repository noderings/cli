package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/noderings/cli/internal/config"
)

// parseOutputFormat validates --output values (text|json). Empty means text.
func parseOutputFormat(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", config.OutputFormatText:
		return config.OutputFormatText, nil
	case config.OutputFormatJSON:
		return config.OutputFormatJSON, nil
	default:
		return "", UsageErrorf("--output must be %s or %s", config.OutputFormatText, config.OutputFormatJSON)
	}
}

// resolveOutputFlag reads --output, optionally falling back to --format when present.
func resolveOutputFlag(cmd *cobra.Command) (string, error) {
	raw, _ := cmd.Flags().GetString("output")
	if cmd.Flags().Lookup("format") != nil {
		if strings.EqualFold(strings.TrimSpace(raw), "") || strings.EqualFold(strings.TrimSpace(raw), config.OutputFormatText) {
			if format, _ := cmd.Flags().GetString("format"); strings.TrimSpace(format) != "" &&
				!strings.EqualFold(strings.TrimSpace(format), config.OutputFormatText) {
				raw = format
			}
		}
	}
	return parseOutputFormat(raw)
}

// writeJSON writes a single JSON document to stdout (pipe-safe; logs stay on stderr).
func writeJSON(v any) error {
	return writeJSONTo(os.Stdout, v)
}

func writeJSONTo(w io.Writer, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(data))
	return err
}

// Glyph helpers: Unicode on a TTY, ASCII when stdout is piped/redirected.
func markPass() string {
	if isStdoutTerminal() {
		return "✓"
	}
	return "[OK]"
}

func markFail() string {
	if isStdoutTerminal() {
		return "✗"
	}
	return "[FAIL]"
}

func markWarn() string {
	if isStdoutTerminal() {
		return "⚠"
	}
	return "[WARN]"
}
