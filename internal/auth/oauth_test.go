package auth

import "testing"

func TestLocalCallbackRedirectURLForcesHTTPLoopback(t *testing.T) {
	redirectURL, err := localCallbackRedirectURL("https://example.com/callback?state=keep", 22222)
	if err != nil {
		t.Fatalf("localCallbackRedirectURL returned error: %v", err)
	}

	want := "http://127.0.0.1:22222/callback?state=keep"
	if redirectURL != want {
		t.Fatalf("redirect URL = %q, want %q", redirectURL, want)
	}
}

func TestLocalCallbackRedirectURLDefaultsPath(t *testing.T) {
	redirectURL, err := localCallbackRedirectURL("https://example.com", 22222)
	if err != nil {
		t.Fatalf("localCallbackRedirectURL returned error: %v", err)
	}

	want := "http://127.0.0.1:22222/callback"
	if redirectURL != want {
		t.Fatalf("redirect URL = %q, want %q", redirectURL, want)
	}
}
