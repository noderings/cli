package install

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"golang.org/x/term"
)

// SudoManager handles sudo authentication and command execution
type SudoManager struct {
	needsPassword bool
	password      string
	logger        Logger
}

// NewSudoManager creates a new sudo manager and authenticates once
func NewSudoManager(logger Logger) (*SudoManager, error) {
	sm := &SudoManager{
		logger: logger,
	}

	// Check if running as root
	if os.Geteuid() == 0 {
		sm.needsPassword = false
		return sm, nil
	}

	// Check if passwordless sudo is available
	testCmd := exec.Command("sudo", "-n", "true")
	if err := testCmd.Run(); err == nil {
		// Passwordless sudo available
		sm.needsPassword = false
		return sm, nil
	}

	// Need password, prompt for it
	password, err := sm.promptSudoPassword()
	if err != nil {
		return nil, fmt.Errorf("failed to get sudo password: %w", err)
	}

	// Verify password works and refresh sudo timestamp
	// This extends the sudo session so subsequent commands won't need password
	if err := sm.verifyAndRefreshSudo(password); err != nil {
		return nil, fmt.Errorf("sudo password verification failed: %w", err)
	}

	sm.needsPassword = true
	sm.password = password
	return sm, nil
}

// promptSudoPassword prompts for sudo with echo on. Hidden input looks like
// paste failed; nr never disables terminal echo for any prompt.
func (sm *SudoManager) promptSudoPassword() (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return "", fmt.Errorf("stdin is not a terminal, cannot prompt for password")
	}

	fmt.Fprint(os.Stderr, "Enter sudo password: ")
	_ = os.Stderr.Sync()

	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("failed to read password: %w", err)
	}
	password := strings.TrimSpace(line)
	if password == "" {
		return "", fmt.Errorf("password cannot be empty")
	}
	return password, nil
}

// verifyAndRefreshSudo verifies the password and refreshes sudo timestamp
// This extends the sudo session so subsequent commands can use sudo -n (non-interactive)
func (sm *SudoManager) verifyAndRefreshSudo(password string) error {
	// First verify password works
	verifyCmd := exec.Command("sudo", "-S", "true")
	verifyCmd.Stdin = bytes.NewReader([]byte(password + "\n"))
	verifyCmd.Stderr = nil
	if err := verifyCmd.Run(); err != nil {
		return err
	}

	// Then refresh sudo timestamp to extend the session
	// sudo -v validates and extends the sudo timestamp
	refreshCmd := exec.Command("sudo", "-S", "-v")
	refreshCmd.Stdin = bytes.NewReader([]byte(password + "\n"))
	refreshCmd.Stderr = nil
	if err := refreshCmd.Run(); err != nil {
		return fmt.Errorf("failed to refresh sudo timestamp: %w", err)
	}

	return nil
}

// RunCommand runs a command with sudo if needed
// Uses sudo -n -E (non-interactive, preserve environment) after initial authentication
func (sm *SudoManager) RunCommand(ctx context.Context, args ...string) *exec.Cmd {
	if os.Geteuid() == 0 {
		// Running as root, no sudo needed
		return exec.CommandContext(ctx, args[0], args[1:]...)
	}

	// Use sudo -n -E for all commands after initial authentication
	// -n: non-interactive (won't prompt, uses cached credentials)
	// -E: preserve environment variables
	sudoArgs := append([]string{"sudo", "-n", "-E"}, args...)
	return exec.CommandContext(ctx, sudoArgs[0], sudoArgs[1:]...)
}

// PrepareStdin prepares stdin for a command
// After initial authentication, sudo -n doesn't need password input
func (sm *SudoManager) PrepareStdin(additionalData []byte) io.Reader {
	if additionalData == nil {
		return nil
	}
	return bytes.NewReader(additionalData)
}
