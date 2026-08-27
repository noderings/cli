package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/noderings/cli/internal/api"
)

// Standard process exit codes (sysexits-aligned for CLI misuse).
const (
	ExitOK     = 0
	ExitError  = 1 // runtime / operational failure
	ExitMisuse = 2 // invalid usage, unknown flags/commands, bad arguments
)

// UsageError marks invalid CLI invocation (flags, args, unknown commands).
type UsageError struct {
	Err error
}

func (e *UsageError) Error() string {
	if e == nil || e.Err == nil {
		return "invalid usage"
	}
	return e.Err.Error()
}

func (e *UsageError) Unwrap() error { return e.Err }

// NewUsageError wraps err as invalid CLI usage.
func NewUsageError(err error) error {
	if err == nil {
		return nil
	}
	var ue *UsageError
	if errors.As(err, &ue) {
		return err
	}
	return &UsageError{Err: err}
}

// UsageErrorf formats a usage error.
func UsageErrorf(format string, args ...any) error {
	return NewUsageError(fmt.Errorf(format, args...))
}

// RequiredFlag reports a missing mandatory flag as invalid usage (exit 2).
func RequiredFlag(name string) error {
	return UsageErrorf("--%s is required", name)
}

// RequiredFlagf reports a missing mandatory flag together with a hint on how to supply it.
func RequiredFlagf(name, hintFormat string, args ...any) error {
	return UsageErrorf("--%s is required (%s)", name, fmt.Sprintf(hintFormat, args...))
}

// RequiredOneOfFlags reports that at least one of several alternative flags must be set.
func RequiredOneOfFlags(names ...string) error {
	switch len(names) {
	case 0:
		return UsageErrorf("a required flag is missing")
	case 1:
		return RequiredFlag(names[0])
	}
	dashed := make([]string, 0, len(names))
	for _, n := range names {
		dashed = append(dashed, "--"+n)
	}
	last := len(dashed) - 1
	return UsageErrorf("%s or %s is required", strings.Join(dashed[:last], ", "), dashed[last])
}

// ExitCode maps an Execute error to a process exit status.
func ExitCode(err error) int {
	if err == nil {
		return ExitOK
	}
	if errors.Is(err, pflag.ErrHelp) {
		return ExitOK
	}
	var ue *UsageError
	if errors.As(err, &ue) {
		return ExitMisuse
	}
	// Cobra / pflag usage failures that were not wrapped.
	if isUsageMessage(err.Error()) {
		return ExitMisuse
	}
	return ExitError
}

// FormatUserError is the stderr text for a failed command. Blocked provider APIs
// (CreateAgent, peering, cluster register, and similar) all print the same backend copy.
func FormatUserError(err error) string {
	if err == nil {
		return ""
	}
	if api.IsProviderReviewPending(err) {
		return api.ProviderReviewPendingMessage
	}
	return err.Error()
}

// isUsageMessage matches the argument and flag errors that cobra and pflag generate
// themselves, which reach Execute unwrapped. CLI code must not depend on it: return a
// UsageError instead (see RequiredFlag), since matching on prose also catches runtime
// failures that merely read like usage text.
func isUsageMessage(msg string) bool {
	lower := strings.ToLower(msg)
	patterns := []string{
		"unknown flag",
		"unknown command",
		"unknown shorthand flag",
		"flag needs an argument",
		"bad flag syntax",
		"invalid argument",
		"required flag",
		"accepts ",          // "accepts 1 arg(s), received 2"
		"requires at least", // "requires at least 1 arg(s), only received 0"
	}
	for _, p := range patterns {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

func initFlagErrorHandling(cmd *cobra.Command) {
	cmd.SetFlagErrorFunc(func(c *cobra.Command, err error) error {
		if err == nil {
			return nil
		}
		// pflag already produces clear messages; mark as misuse for exit 2.
		if errors.Is(err, pflag.ErrHelp) {
			return err
		}
		return NewUsageError(fmt.Errorf("%w\nSee '%s --help' for available flags", err, c.CommandPath()))
	})
}
