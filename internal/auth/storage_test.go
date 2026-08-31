package auth

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/noderings/cli/internal/config"
)

func fallbackStorage(t *testing.T) *Storage {
	t.Helper()
	return &Storage{
		keyringAvailable: false,
		fallbackPath:     filepath.Join(t.TempDir(), "nested", "tokens.json"),
	}
}

func TestStorageFileRoundTripAndDelete(t *testing.T) {
	storage := fallbackStorage(t)
	want := &Token{
		AccessToken: "access", RefreshToken: "refresh", ExpiresAt: time.Now().Add(time.Hour).UTC(),
		TokenType: "Bearer", Scope: "openid profile",
	}
	if err := storage.SaveToken(want); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(storage.fallbackPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0600 {
		t.Fatalf("mode=%#o want 0600", got)
	}

	got, err := storage.LoadToken()
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessToken != want.AccessToken || got.RefreshToken != want.RefreshToken ||
		got.TokenType != want.TokenType || got.Scope != want.Scope || !got.ExpiresAt.Equal(want.ExpiresAt) {
		t.Fatalf("got=%#v want=%#v", got, want)
	}

	if err := storage.DeleteToken(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(storage.fallbackPath); !os.IsNotExist(err) {
		t.Fatalf("token file still exists: %v", err)
	}
	if err := storage.DeleteToken(); err != nil {
		t.Fatalf("delete should be idempotent: %v", err)
	}
}

func TestStorageLoadErrors(t *testing.T) {
	storage := fallbackStorage(t)
	if _, err := storage.LoadToken(); err == nil || !strings.Contains(err.Error(), "token not found") {
		t.Fatalf("missing err=%v", err)
	}
	if err := os.MkdirAll(filepath.Dir(storage.fallbackPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(storage.fallbackPath, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := storage.LoadToken(); err == nil || !strings.Contains(err.Error(), "unmarshal token") {
		t.Fatalf("malformed err=%v", err)
	}
}

func TestTokenExpiry(t *testing.T) {
	expired := &Token{ExpiresAt: time.Now().Add(-time.Second)}
	if !expired.IsExpired() {
		t.Fatal("past token should be expired")
	}
	fresh := &Token{ExpiresAt: time.Now().Add(time.Hour)}
	if fresh.IsExpired() {
		t.Fatal("future token should not be expired")
	}
	if !fresh.IsExpiringSoon(2 * time.Hour) {
		t.Fatal("token should expire within two hours")
	}
	if fresh.IsExpiringSoon(10 * time.Minute) {
		t.Fatal("token should not expire within ten minutes")
	}
}

func clearTokenEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{"NR_API_TOKEN", "NODERINGS_API_TOKEN", "NR_TOKEN", "NODERINGS_TOKEN"} {
		t.Setenv(key, "")
	}
}

func TestGetTokenFromEnvPriorityAndWhitespace(t *testing.T) {
	clearTokenEnv(t)
	t.Setenv("NODERINGS_API_TOKEN", " second ")
	t.Setenv("NR_TOKEN", "third")
	if got, ok := GetTokenFromEnv(); !ok || got != "second" {
		t.Fatalf("token=%q ok=%v", got, ok)
	}

	t.Setenv("NR_API_TOKEN", " first ")
	if got, ok := GetTokenFromEnv(); !ok || got != "first" {
		t.Fatalf("token=%q ok=%v", got, ok)
	}

	t.Setenv("NR_API_TOKEN", "   ")
	if got, ok := GetTokenFromEnv(); !ok || got != "second" {
		t.Fatalf("whitespace should be ignored; token=%q ok=%v", got, ok)
	}
}

func TestGetTokenFromConfig(t *testing.T) {
	cfg := &config.Config{Auth: config.AuthConfig{Token: " config-token "}}
	if got, ok := GetTokenFromConfig(cfg); !ok || got != "config-token" {
		t.Fatalf("token=%q ok=%v", got, ok)
	}
	cfg.Auth.Token = " "
	if _, ok := GetTokenFromConfig(cfg); ok {
		t.Fatal("blank config token should not be found")
	}
}

func TestLoadTokenInfoPriority(t *testing.T) {
	clearTokenEnv(t)
	cfg := &config.Config{Auth: config.AuthConfig{Token: "config-token"}}

	info, err := LoadTokenInfo(cfg, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if info.Token != "config-token" || info.Source != TokenSourceConfigFile || info.IsOAuthToken {
		t.Fatalf("info=%#v", info)
	}

	t.Setenv("NR_API_TOKEN", "env-token")
	info, err = LoadTokenInfo(cfg, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if info.Token != "env-token" || info.Source != TokenSourceEnvVar {
		t.Fatalf("info=%#v", info)
	}
}
