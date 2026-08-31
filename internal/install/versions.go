package install

import (
	"fmt"
	"strings"

	"github.com/Masterminds/semver/v3"
)

// VersionChecker checks and compares tool versions
type VersionChecker struct{}

// NewVersionChecker creates a new version checker
func NewVersionChecker() *VersionChecker {
	return &VersionChecker{}
}

// CompareVersions compares two semantic versions
// Returns: -1 if current < minimum, 0 if equal, 1 if current >= minimum
func (v *VersionChecker) CompareVersions(current, minimum string) (int, error) {
	currentVer, err := semver.NewVersion(current)
	if err != nil {
		return 0, fmt.Errorf("invalid current version format: %w", err)
	}

	minVer, err := semver.NewVersion(minimum)
	if err != nil {
		return 0, fmt.Errorf("invalid minimum version format: %w", err)
	}

	return currentVer.Compare(minVer), nil
}

// MeetsMinimum checks if current version meets minimum requirement
func (v *VersionChecker) MeetsMinimum(current, minimum string) (bool, error) {
	compare, err := v.CompareVersions(current, minimum)
	if err != nil {
		return false, err
	}
	return compare >= 0, nil
}

// ExtractVersion extracts a semantic version from tool output
// Handles various output formats like:
// - "v1.2.3"
// - "1.2.3"
// - "tool version 1.2.3"
// - "tool 1.2.3 (build abc123)"
func (v *VersionChecker) ExtractVersion(versionOutput string) (string, error) {
	// Remove common prefixes and suffixes
	cleaned := strings.TrimSpace(versionOutput)

	// Split by whitespace
	words := strings.Fields(cleaned)

	for _, word := range words {
		// Remove common prefixes
		word = strings.TrimPrefix(word, "v")
		word = strings.TrimPrefix(word, "V")
		word = strings.TrimPrefix(word, "version")
		word = strings.TrimPrefix(word, "Version")
		word = strings.TrimSpace(word)

		// Remove common suffixes (build info, etc.)
		if idx := strings.IndexAny(word, "+-"); idx > 0 {
			word = word[:idx]
		}

		// Try to parse as semver
		if _, err := semver.NewVersion(word); err == nil {
			return word, nil
		}

		// Try to extract from patterns like "1.2.3" or "v1.2.3"
		if strings.Contains(word, ".") {
			parts := strings.Split(word, ".")
			if len(parts) >= 2 {
				// Try to build a version string
				versionStr := strings.Join(parts[:min(3, len(parts))], ".")
				if _, err := semver.NewVersion(versionStr); err == nil {
					return versionStr, nil
				}
			}
		}
	}

	return "", fmt.Errorf("could not extract version from: %s", versionOutput)
}

// min returns the minimum of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
