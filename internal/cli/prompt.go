package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"
)

// confirmYesNo asks a yes/no question on the terminal. Default is no.
// Returns an error when stdin is not a TTY; nonInteractiveHint explains how to skip the prompt.
func confirmYesNo(question, nonInteractiveHint string) (bool, error) {
	if !isStdinTerminal() {
		if strings.TrimSpace(nonInteractiveHint) == "" {
			nonInteractiveHint = "re-run with --yes or --force for non-interactive use"
		}
		return false, fmt.Errorf("stdin is not a terminal; %s", nonInteractiveHint)
	}

	fmt.Fprintf(os.Stderr, "%s [y/N]: ", question)
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return false, fmt.Errorf("read confirmation: %w", err)
	}
	answer := strings.TrimSpace(strings.ToLower(line))
	return answer == "y" || answer == "yes", nil
}

func isStdinTerminal() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}

func isStdoutTerminal() bool {
	return term.IsTerminal(int(os.Stdout.Fd()))
}

// promptString asks for a non-secret value. Empty input keeps defaultValue.
func promptString(label, defaultValue string) (string, error) {
	if !isStdinTerminal() {
		return "", fmt.Errorf("stdin is not a terminal; set env/flags or --proxmox-instances-file / --virtfusion-instances-file for non-interactive install")
	}
	if defaultValue != "" {
		fmt.Fprintf(os.Stderr, "%s [%s]: ", label, defaultValue)
	} else {
		fmt.Fprintf(os.Stderr, "%s: ", label)
	}
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("read %s: %w", label, err)
	}
	value := strings.TrimSpace(line)
	if value == "" {
		return defaultValue, nil
	}
	return value, nil
}

// promptSecret asks for a secret with no echo. Empty input is allowed when allowEmpty is true.
func promptSecret(label string, allowEmpty bool) (string, error) {
	if !isStdinTerminal() {
		return "", fmt.Errorf("stdin is not a terminal; set env/flags for non-interactive install")
	}
	fmt.Fprintf(os.Stderr, "%s: ", label)
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", label, err)
	}
	value := strings.TrimSpace(string(b))
	if value == "" && !allowEmpty {
		return "", fmt.Errorf("%s is required", label)
	}
	return value, nil
}
