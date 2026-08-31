package install

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/Masterminds/semver/v3"

	"github.com/noderings/cli/internal/api"
	"github.com/noderings/cli/internal/config"
)

// ToolRequirement defines a requirement for an external tool
type ToolRequirement struct {
	Name        string
	MinVersion  string
	Optional    bool
	InstallURL  string
	VersionFlag string // Flag to get version (e.g., "--version", "version")
}

// ValidationResult represents the result of validating a tool
type ValidationResult struct {
	Tool        string
	Available   bool
	Version     string
	Valid       bool
	Error       error
	Requirement *ToolRequirement
}

// Validator validates prerequisites
type Validator struct {
	requirements map[string]*ToolRequirement
}

// CheckTool checks if a tool is available (public method for compatibility)
func (v *Validator) CheckTool(ctx context.Context, name string) error {
	req, exists := v.requirements[name]
	if !exists {
		// Tool not in requirements, assume it's optional
		return nil
	}
	result := v.checkTool(ctx, name, req)
	if !result.Available {
		return result.Error
	}
	if !result.Valid {
		return result.Error
	}
	return nil
}

// NewValidator creates a new prerequisites validator with offline-safe defaults.
// Prefer NewToolValidator so liqoctl/helm floors come from /v1/platform-versions.
func NewValidator() *Validator {
	return &Validator{
		requirements: map[string]*ToolRequirement{
			"liqoctl": {
				Name: "liqoctl",
				// NodeRings ships a Harbor fork (v0.0.0-<sha>), not upstream 0.10+.
				// Override via ApplyToolPins / NewToolValidator from platform-versions.
				MinVersion:  strings.TrimPrefix(config.DefaultLiqoVersion, "v"),
				Optional:    false,
				InstallURL:  "https://docs.liqo.io/en/stable/installation/liqoctl.html",
				VersionFlag: "version --client",
			},
			"helm": {
				Name:        "helm",
				MinVersion:  "3.12.0",
				Optional:    false,
				InstallURL:  "https://helm.sh/docs/intro/install/",
				VersionFlag: "version --short",
			},
		},
	}
}

// ApplyToolPins sets tool minimums from GetPlatformVersions pins.
// MinVersion is preferred; Version is used when MinVersion is empty (exact pin as floor).
func ApplyToolPins(v *Validator, pins *api.PlatformPins) {
	if v == nil || pins == nil {
		return
	}
	if pin, ok := pins.Get(api.ComponentLiqoctl); ok {
		min := pin.MinVersion
		if min == "" {
			min = pin.Version
		}
		v.SetMinVersion("liqoctl", min)
	}
	if pin, ok := pins.Get(api.ComponentHelm); ok {
		min := pin.MinVersion
		if min == "" {
			min = pin.Version
		}
		v.SetMinVersion("helm", min)
	}
}

// NewToolValidator builds a validator using server pins when available.
// After preparePlatformVersions, cfg.Liqo holds the same fork pin as liqoctl.
func NewToolValidator(pins *api.PlatformPins, cfg *config.Config) *Validator {
	v := NewValidator()
	ApplyToolPins(v, pins)
	if pins != nil {
		if _, ok := pins.Get(api.ComponentLiqoctl); ok {
			return v
		}
	}
	min := ""
	if cfg != nil {
		min = cfg.Liqo.MinVersion
		if min == "" {
			min = cfg.Liqo.Version
		}
	}
	if min == "" {
		min = config.DefaultLiqoVersion
	}
	v.SetMinVersion("liqoctl", min)
	return v
}

// SetMinVersion updates the minimum version for a known tool (e.g. from server pins).
func (v *Validator) SetMinVersion(name, minVersion string) {
	if v == nil || v.requirements == nil {
		return
	}
	if req, ok := v.requirements[name]; ok && minVersion != "" {
		req.MinVersion = strings.TrimPrefix(minVersion, "v")
	}
}

// ValidatePrerequisites validates all required tools
func (v *Validator) ValidatePrerequisites(ctx context.Context) ([]ValidationResult, error) {
	results := []ValidationResult{}

	for toolName, req := range v.requirements {
		result := v.checkTool(ctx, toolName, req)
		results = append(results, result)
	}

	return results, nil
}

// checkTool checks if a tool is available and meets version requirements
func (v *Validator) checkTool(ctx context.Context, name string, req *ToolRequirement) ValidationResult {
	result := ValidationResult{
		Tool:        name,
		Requirement: req,
	}

	// Check if tool exists in PATH
	_, err := exec.LookPath(name)
	if err != nil {
		result.Available = false
		result.Error = fmt.Errorf("%s not found in PATH", name)
		return result
	}

	result.Available = true

	// Get version - split VersionFlag to support multiple arguments (e.g., "version --client")
	versionArgs := strings.Fields(req.VersionFlag)
	cmd := exec.CommandContext(ctx, name, versionArgs...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		result.Error = fmt.Errorf("failed to get version: %w", err)
		return result
	}

	versionStr := strings.TrimSpace(string(output))
	result.Version = versionStr

	// Parse and validate version if required
	if req.MinVersion != "" {
		valid, err := v.validateVersion(versionStr, req.MinVersion)
		if err != nil {
			result.Error = err
			return result
		}
		result.Valid = valid
		if !valid {
			result.Error = fmt.Errorf("version %s is below minimum required %s", versionStr, req.MinVersion)
		}
	} else {
		result.Valid = true // No version requirement
	}

	return result
}

// validateVersion validates that the current version meets the minimum requirement
func (v *Validator) validateVersion(current, minimum string) (bool, error) {
	// Extract version number from output (handles various formats)
	currentVer := extractVersion(current)
	if currentVer == "" {
		return false, fmt.Errorf("could not parse version from: %s", current)
	}

	minVer, err := semver.NewVersion(minimum)
	if err != nil {
		return false, fmt.Errorf("invalid minimum version: %w", err)
	}

	curVer, err := semver.NewVersion(currentVer)
	if err != nil {
		return false, fmt.Errorf("could not parse current version: %w", err)
	}

	return curVer.Compare(minVer) >= 0, nil
}

// PrintResults prints validation results
func (v *Validator) PrintResults(results []ValidationResult) error {
	hasErrors := false

	fmt.Println("\nPrerequisite Check Results:")
	fmt.Println("============================")

	for _, r := range results {
		status := "✓"
		if !r.Available || !r.Valid {
			status = "✗"
			hasErrors = true
		}

		msg := fmt.Sprintf("%s %s", status, r.Tool)
		if r.Available {
			msg += fmt.Sprintf(" (version: %s)", r.Version)
			if !r.Valid && r.Error != nil {
				msg += fmt.Sprintf(" - %s", r.Error)
			}
		} else if r.Error != nil {
			msg += fmt.Sprintf(" - %s", r.Error)
		}

		if hasErrors {
			fmt.Println(msg)
		} else {
			fmt.Println(msg)
		}
	}

	if hasErrors {
		fmt.Println("\nInstallation Instructions:")
		fmt.Println("=========================")
		for _, r := range results {
			if !r.Available || !r.Valid {
				if r.Requirement != nil && r.Requirement.InstallURL != "" {
					fmt.Printf("%s: %s\n", r.Tool, r.Requirement.InstallURL)
				}
			}
		}
		return fmt.Errorf("prerequisite validation failed")
	}

	fmt.Println("\n✓ All prerequisites satisfied")
	return nil
}

// extractVersion extracts a semantic version from tool output
func extractVersion(versionOutput string) string {
	// Simple version extraction (can be enhanced)
	// Looks for patterns like v1.2.3 or 1.2.3
	words := strings.Fields(versionOutput)
	for _, word := range words {
		// Remove common prefixes
		word = strings.TrimPrefix(word, "v")
		word = strings.TrimPrefix(word, "V")
		word = strings.TrimPrefix(word, "version")
		word = strings.TrimPrefix(word, "Version")
		word = strings.TrimSpace(word)

		// Try to parse as semver
		if _, err := semver.NewVersion(word); err == nil {
			return word
		}

		// Try to extract version from strings like "liqoctl version 1.2.3"
		parts := strings.Split(word, ":")
		if len(parts) > 1 {
			ver := strings.TrimSpace(parts[len(parts)-1])
			if _, err := semver.NewVersion(ver); err == nil {
				return ver
			}
		}
	}
	return ""
}
