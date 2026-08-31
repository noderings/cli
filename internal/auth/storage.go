package auth

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/zalando/go-keyring"

	"github.com/noderings/cli/internal/config"
)

// Storage handles secure token storage
type Storage struct {
	keyringAvailable bool
	fallbackPath     string
}

// NewStorage creates a new token storage
func NewStorage(configDir string) (*Storage, error) {
	storage := &Storage{
		fallbackPath: filepath.Join(configDir, "tokens.json"),
	}

	// Try to set a test value to check if keyring is available
	err := keyring.Set(config.DefaultKeyringService, "test", "test")
	if err != nil {
		// Keyring not available, use file-based storage
		storage.keyringAvailable = false
		// Clean up test value if it was set
		_ = keyring.Delete(config.DefaultKeyringService, "test")
	} else {
		storage.keyringAvailable = true
		// Clean up test value
		_ = keyring.Delete(config.DefaultKeyringService, "test")
	}

	return storage, nil
}

// SaveToken saves a token securely
func (s *Storage) SaveToken(token *Token) error {
	// Serialize token to JSON for keyring/file storage (secrets are stored encrypted by the OS keyring or in a restricted file)
	//nolint:gosec // G117: intentionally marshal OAuth tokens for secure local storage
	data, err := json.Marshal(token)
	if err != nil {
		return fmt.Errorf("marshal token: %w", err)
	}

	if s.keyringAvailable {
		// Use OS keyring
		if err := keyring.Set(config.DefaultKeyringService, config.DefaultKeyringTokenKey, string(data)); err != nil {
			// Fallback to file if keyring fails
			return s.saveToFile(data)
		}
		return nil
	}

	// Use file storage fallback (JSON with mode 0600; not encrypted)
	return s.saveToFile(data)
}

// saveToFile saves the token as JSON with restricted permissions (0600).
// This is not encryption — prefer OS keyring when available.
func (s *Storage) saveToFile(data []byte) error {
	// Ensure directory exists
	dir := filepath.Dir(s.fallbackPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}

	// Write file with restricted permissions (600 = owner read/write only)
	if err := os.WriteFile(s.fallbackPath, data, 0600); err != nil {
		return fmt.Errorf("write token file: %w", err)
	}

	return nil
}

// LoadToken loads a token from secure storage
func (s *Storage) LoadToken() (*Token, error) {
	var data []byte
	var err error

	if s.keyringAvailable {
		// Try keyring first
		keyringData, keyringErr := keyring.Get(config.DefaultKeyringService, config.DefaultKeyringTokenKey)
		if keyringErr == nil {
			data = []byte(keyringData)
		} else {
			// Fallback to file
			data, err = s.loadFromFile()
			if err != nil {
				return nil, fmt.Errorf("load token: keyring error: %v, file error: %w", keyringErr, err)
			}
		}
	} else {
		// Use file storage
		data, err = s.loadFromFile()
		if err != nil {
			return nil, fmt.Errorf("load token: %w", err)
		}
	}

	// Deserialize token
	var token Token
	if err := json.Unmarshal(data, &token); err != nil {
		return nil, fmt.Errorf("unmarshal token: %w", err)
	}

	return &token, nil
}

// loadFromFile loads token from file
func (s *Storage) loadFromFile() ([]byte, error) {
	data, err := os.ReadFile(s.fallbackPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("token not found")
		}
		return nil, fmt.Errorf("read token file: %w", err)
	}
	return data, nil
}

// DeleteToken removes a token from storage
func (s *Storage) DeleteToken() error {
	if s.keyringAvailable {
		// Try keyring first
		if err := keyring.Delete(config.DefaultKeyringService, config.DefaultKeyringTokenKey); err != nil {
			// If keyring delete fails, try file
			return s.deleteFromFile()
		}
		// Also try to delete file if it exists (cleanup)
		_ = s.deleteFromFile()
		return nil
	}

	return s.deleteFromFile()
}

// deleteFromFile deletes token file
func (s *Storage) deleteFromFile() error {
	if err := os.Remove(s.fallbackPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete token file: %w", err)
	}
	return nil
}
