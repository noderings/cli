package auth

import (
	"fmt"
	"os/exec"
	"runtime"
)

// OpenBrowser opens the authorization URL in the default browser.
// On failure it returns a short error without embedding the URL; callers print the URL once.
func OpenBrowser(url string) error {
	cmd, err := browserCommand(runtime.GOOS, url)
	if err != nil {
		return err
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("open browser: %w", err)
	}

	return nil
}

// browserCommand builds the platform command that opens url.
// The URL is always passed as a single argument — never through a shell — because OAuth
// URLs contain "&", which cmd.exe would treat as a command separator and truncate.
func browserCommand(goos, url string) (*exec.Cmd, error) {
	switch goos {
	case "linux":
		browsers := []string{"xdg-open", "x-www-browser", "www-browser", "firefox", "google-chrome", "chromium"}
		for _, browser := range browsers {
			if _, err := exec.LookPath(browser); err == nil {
				return exec.Command(browser, url), nil
			}
		}
		return nil, fmt.Errorf("no browser found")
	case "darwin":
		return exec.Command("open", url), nil
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url), nil
	default:
		return nil, fmt.Errorf("unsupported platform %q", goos)
	}
}
