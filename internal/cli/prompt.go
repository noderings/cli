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

	fmt.Fprintf(os.Stderr, "%s [y/N]: ", colorPromptLabel(question))
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

func isStderrTerminal() bool {
	return term.IsTerminal(int(os.Stderr.Fd()))
}

func promptColorEnabled() bool {
	return isStderrTerminal() && os.Getenv("NO_COLOR") == ""
}

// beginCredentialPrompt marks the start of an interactive Proxmox, VirtFusion,
// SolusVM, or Pterodactyl question block. The rule and title stay plain when
// stderr is not a terminal.
func beginCredentialPrompt(title string) {
	fmt.Fprintln(os.Stderr)
	fmt.Fprint(os.Stderr, formatCredentialBanner(title, promptColorEnabled()))
}

func formatCredentialBanner(title string, color bool) string {
	line := "========"
	waiting := "Waiting for your input. What you type is shown."
	if !color {
		return line + "\n" + title + "\n" + line + "\n" + waiting + "\n"
	}
	return "\033[1;36m" + line + "\033[0m\n" +
		"\033[1;33m" + title + "\033[0m\n" +
		"\033[1;36m" + line + "\033[0m\n" +
		"\033[2m" + waiting + "\033[0m\n"
}

func colorPromptLabel(label string) string {
	if !promptColorEnabled() {
		return label
	}
	return "\033[1;33m" + label + "\033[0m"
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
		fmt.Fprintf(os.Stderr, "%s [%s]: ", colorPromptLabel(label), defaultValue)
	} else {
		fmt.Fprintf(os.Stderr, "%s: ", colorPromptLabel(label))
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
