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

// promptVisibleToken asks for an API token with echo on. Do not add a
// promptSecret / term.ReadPassword path: hidden paste looks like it failed.
func promptVisibleToken(label string) (string, error) {
	value, err := promptString(label, "")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("%s is required", label)
	}
	return value, nil
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
