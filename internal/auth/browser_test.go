package auth

import (
	"strings"
	"testing"
)

// OAuth URLs contain "&"; the URL must always be a single argv entry so no shell
// can split it into separate commands.
func TestBrowserCommandPassesFullURLAsSingleArgument(t *testing.T) {
	const authURL = "https://example.com/authorize?response_type=code&client_id=nr&scope=openid%20offline_access&state=abc"

	for _, goos := range []string{"darwin", "windows"} {
		t.Run(goos, func(t *testing.T) {
			cmd, err := browserCommand(goos, authURL)
			if err != nil {
				t.Fatalf("browserCommand(%s): %v", goos, err)
			}

			var found bool
			for _, arg := range cmd.Args {
				if arg == authURL {
					found = true
					continue
				}
				if strings.Contains(arg, "client_id") {
					t.Fatalf("URL was split across arguments: %v", cmd.Args)
				}
			}
			if !found {
				t.Fatalf("full URL missing from args: %v", cmd.Args)
			}
			for _, arg := range cmd.Args {
				if arg == "/c" || arg == "-c" {
					t.Fatalf("%s command routes the URL through a shell: %v", goos, cmd.Args)
				}
			}
		})
	}
}

// The linux branch resolves a browser via LookPath, so it can only be asserted on hosts
// that actually have one installed.
func TestBrowserCommandLinuxPassesFullURLAsSingleArgument(t *testing.T) {
	const authURL = "https://example.com/authorize?response_type=code&client_id=nr&state=abc"

	cmd, err := browserCommand("linux", authURL)
	if err != nil {
		t.Skipf("no browser available on this host: %v", err)
	}

	if len(cmd.Args) != 2 {
		t.Fatalf("expected browser plus URL, got %v", cmd.Args)
	}
	if cmd.Args[1] != authURL {
		t.Fatalf("URL argument = %q, want %q", cmd.Args[1], authURL)
	}
}

func TestBrowserCommandRejectsUnsupportedPlatform(t *testing.T) {
	if _, err := browserCommand("plan9", "https://example.com"); err == nil {
		t.Fatal("expected error for unsupported platform")
	}
}
