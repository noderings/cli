package utils

import (
	"fmt"
	"os"
	"path/filepath"
)

// EnsureDir ensures a directory exists
func EnsureDir(path string) error {
	//nolint:gosec // G301: general-purpose directory creation uses standard 0755
	return os.MkdirAll(path, 0755)
}

// FileExists checks if a file exists
func FileExists(path string) bool {
	_, err := os.Stat(path)
	return !os.IsNotExist(err)
}

// ExpandPath expands ~ and environment variables in path
func ExpandPath(path string) (string, error) {
	if len(path) == 0 {
		return path, nil
	}

	if path[0] == '~' {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("get home dir: %w", err)
		}
		path = filepath.Join(home, path[1:])
	}

	return os.ExpandEnv(path), nil
}
