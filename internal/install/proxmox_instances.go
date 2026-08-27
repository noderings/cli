package install

import (
	"fmt"
	"net/url"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// ProxmoxInstance holds one Proxmox API endpoint + token (user-supplied secrets).
type ProxmoxInstance struct {
	ID          string `yaml:"id" json:"id"`
	URL         string `yaml:"url" json:"url"`
	Username    string `yaml:"username" json:"username"`
	TokenID     string `yaml:"tokenId" json:"tokenId"`
	TokenSecret string `yaml:"tokenSecret" json:"tokenSecret"`
}

// Validate checks required fields for one instance.
func (p ProxmoxInstance) Validate() error {
	missing := []string{}
	if strings.TrimSpace(p.ID) == "" {
		missing = append(missing, "id")
	}
	if strings.TrimSpace(p.URL) == "" {
		missing = append(missing, "url")
	}
	if strings.TrimSpace(p.Username) == "" {
		missing = append(missing, "username")
	}
	if strings.TrimSpace(p.TokenID) == "" {
		missing = append(missing, "tokenId")
	}
	if strings.TrimSpace(p.TokenSecret) == "" {
		missing = append(missing, "tokenSecret")
	}
	if len(missing) > 0 {
		return fmt.Errorf("proxmox instance %q missing: %s", p.ID, strings.Join(missing, ", "))
	}
	// The API token below is sent on every request, so refuse plaintext endpoints.
	parsed, err := url.Parse(strings.TrimSpace(p.URL))
	if err != nil {
		return fmt.Errorf("proxmox instance %q has an invalid url %q: %w", p.ID, p.URL, err)
	}
	if parsed.Scheme != "https" {
		return fmt.Errorf("proxmox instance %q url must use https:// (got %q)", p.ID, p.URL)
	}
	return nil
}

// CredentialsSecretName matches the chart fullname: {release}-proxmox-operator-proxmox-credentials-{id}.
func CredentialsSecretName(helmRelease, instanceID string) string {
	return fmt.Sprintf("%s-proxmox-operator-proxmox-credentials-%s", helmRelease, instanceID)
}

type proxmoxInstancesFile struct {
	Instances []ProxmoxInstance `yaml:"instances"`
}

// LoadProxmoxInstancesFile reads a YAML file with either:
//
//	instances: [ { id, url, username, tokenId, tokenSecret }, ... ]
//
// or a bare list of instances.
func LoadProxmoxInstancesFile(path string) ([]ProxmoxInstance, error) {
	// The file holds plaintext API tokens; refuse group/world-readable modes.
	if info, err := os.Stat(path); err == nil && info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("proxmox instances file %s is readable by group/other (mode %#o); run: chmod 600 %s",
			path, info.Mode().Perm(), path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read proxmox instances file: %w", err)
	}
	var wrapped proxmoxInstancesFile
	if err := yaml.Unmarshal(data, &wrapped); err == nil && len(wrapped.Instances) > 0 {
		return normalizeInstances(wrapped.Instances)
	}
	var list []ProxmoxInstance
	if err := yaml.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("parse proxmox instances file %s: expected instances: [] or a list: %w", path, err)
	}
	return normalizeInstances(list)
}

func normalizeInstances(in []ProxmoxInstance) ([]ProxmoxInstance, error) {
	if len(in) == 0 {
		return nil, fmt.Errorf("no proxmox instances provided")
	}
	out := make([]ProxmoxInstance, 0, len(in))
	seen := map[string]struct{}{}
	for i, inst := range in {
		inst.ID = strings.TrimSpace(inst.ID)
		inst.URL = NormalizeHypervisorAPIURL(inst.URL)
		inst.Username = strings.TrimSpace(inst.Username)
		inst.TokenID = strings.TrimSpace(inst.TokenID)
		inst.TokenSecret = strings.TrimSpace(inst.TokenSecret)
		if inst.ID == "" {
			inst.ID = fmt.Sprintf("proxmox-%d", i+1)
		}
		if _, ok := seen[inst.ID]; ok {
			return nil, fmt.Errorf("duplicate proxmox instance id %q", inst.ID)
		}
		seen[inst.ID] = struct{}{}
		if err := inst.Validate(); err != nil {
			return nil, err
		}
		out = append(out, inst)
	}
	return out, nil
}

// ProxmoxInstanceFromEnv builds a single instance from PROXMOX_* env vars (test-k3d compat).
// Returns nil, nil when none of the required vars are set.
func ProxmoxInstanceFromEnv() (*ProxmoxInstance, error) {
	url := NormalizeHypervisorAPIURL(os.Getenv("PROXMOX_URL"))
	user := strings.TrimSpace(os.Getenv("PROXMOX_USERNAME"))
	tokenID := strings.TrimSpace(os.Getenv("PROXMOX_TOKEN_ID"))
	tokenSecret := strings.TrimSpace(os.Getenv("PROXMOX_TOKEN_SECRET"))
	if url == "" && user == "" && tokenID == "" && tokenSecret == "" {
		return nil, nil
	}
	id := strings.TrimSpace(os.Getenv("PROXMOX_INSTANCE_ID"))
	if id == "" {
		id = "proxmox-1"
	}
	inst := &ProxmoxInstance{
		ID:          id,
		URL:         url,
		Username:    user,
		TokenID:     tokenID,
		TokenSecret: tokenSecret,
	}
	if err := inst.Validate(); err != nil {
		return nil, fmt.Errorf("PROXMOX_* env incomplete: %w", err)
	}
	return inst, nil
}
