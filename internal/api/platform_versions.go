package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/avast/retry-go/v5"

	generated "github.com/noderings/cli/internal/api/generated"
)

// Component names returned by MetadataService.GetPlatformVersions.
const (
	ComponentK3s     = "k3s"
	ComponentK3d     = "k3d"
	ComponentCalico  = "calico"
	ComponentLiqo    = "liqo"
	ComponentLiqoctl = "liqoctl"
	ComponentHelm    = "helm"
)

// ComponentPin is a normalized platform component version pin.
type ComponentPin struct {
	Name           string
	Version        string
	MinVersion     string
	DownloadURL    string
	ChecksumSHA256 string
}

// PlatformPins is the install matrix keyed by component name.
type PlatformPins struct {
	SchemaVersion string
	Components    map[string]ComponentPin
}

// Get returns a component pin by name.
func (p *PlatformPins) Get(name string) (ComponentPin, bool) {
	if p == nil || p.Components == nil {
		return ComponentPin{}, false
	}
	pin, ok := p.Components[strings.ToLower(name)]
	return pin, ok
}

// VersionOr returns the pinned version for name, or fallback if missing/empty.
func (p *PlatformPins) VersionOr(name, fallback string) string {
	if pin, ok := p.Get(name); ok && pin.Version != "" {
		return pin.Version
	}
	return fallback
}

// ParsePlatformVersions converts a generated V1PlatformVersions into PlatformPins.
func ParsePlatformVersions(pv *generated.V1PlatformVersions) (*PlatformPins, error) {
	if pv == nil {
		return nil, fmt.Errorf("platform versions response is empty")
	}

	pins := &PlatformPins{
		Components: make(map[string]ComponentPin),
	}
	if pv.SchemaVersion != nil {
		pins.SchemaVersion = *pv.SchemaVersion
	}
	if pv.Components == nil {
		return pins, nil
	}

	for _, c := range *pv.Components {
		pin := ComponentPin{}
		if c.Name != nil {
			pin.Name = strings.ToLower(strings.TrimSpace(*c.Name))
		}
		if pin.Name == "" {
			continue
		}
		if c.Version != nil {
			pin.Version = strings.TrimSpace(*c.Version)
		}
		if c.MinVersion != nil {
			pin.MinVersion = strings.TrimSpace(*c.MinVersion)
		}
		if c.DownloadUrl != nil {
			pin.DownloadURL = strings.TrimSpace(*c.DownloadUrl)
		}
		if c.ChecksumSha256 != nil {
			pin.ChecksumSHA256 = strings.TrimSpace(*c.ChecksumSha256)
		}
		pins.Components[pin.Name] = pin
	}

	return pins, nil
}

// GetPlatformVersions fetches the server-pinned install matrix.
// This endpoint is anonymous; no auth token is required.
func (c *Client) GetPlatformVersions(ctx context.Context) (*PlatformPins, error) {
	if c == nil || c.generated == nil {
		return nil, fmt.Errorf("api client is not initialized")
	}

	resp, err := c.generated.MetadataServiceGetPlatformVersions(ctx)
	if err != nil {
		return nil, fmt.Errorf("get platform versions: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		return nil, ParseError(resp)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read platform versions response: %w", err)
	}

	var pv generated.V1PlatformVersions
	if err := json.Unmarshal(body, &pv); err != nil {
		return nil, fmt.Errorf("parse platform versions: %w", err)
	}

	return ParsePlatformVersions(&pv)
}

// GetPlatformVersionsWithRetry fetches platform versions with retry on transient failures.
func (c *Client) GetPlatformVersionsWithRetry(ctx context.Context, maxRetries uint) (*PlatformPins, error) {
	var pins *PlatformPins
	err := c.WithRetry(ctx, maxRetries, func() error {
		resp, callErr := c.generated.MetadataServiceGetPlatformVersions(ctx)
		if callErr != nil {
			return callErr
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode >= 400 {
			apiErr := ParseError(resp)
			if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
				return retry.Unrecoverable(apiErr)
			}
			if isRetryableError(apiErr) {
				return apiErr
			}
			return retry.Unrecoverable(apiErr)
		}

		body, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return readErr
		}

		var pv generated.V1PlatformVersions
		if unmarshalErr := json.Unmarshal(body, &pv); unmarshalErr != nil {
			return retry.Unrecoverable(fmt.Errorf("parse platform versions: %w", unmarshalErr))
		}

		parsed, parseErr := ParsePlatformVersions(&pv)
		if parseErr != nil {
			return retry.Unrecoverable(parseErr)
		}
		pins = parsed
		return nil
	})
	if err != nil {
		return nil, err
	}
	return pins, nil
}
