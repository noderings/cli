package install

import (
	"fmt"
	"net/url"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// SolusVMInstance holds one SolusVM 2 management-node API endpoint + bearer token.
type SolusVMInstance struct {
	ID    string `yaml:"id" json:"id"`
	URL   string `yaml:"url" json:"url"`
	Token string `yaml:"token" json:"token"`
}

// Validate checks required fields for one SolusVM 2 instance.
func (v SolusVMInstance) Validate() error {
	missing := []string{}
	if strings.TrimSpace(v.ID) == "" {
		missing = append(missing, "id")
	}
	if strings.TrimSpace(v.URL) == "" {
		missing = append(missing, "url")
	}
	if strings.TrimSpace(v.Token) == "" {
		missing = append(missing, "token")
	}
	if len(missing) > 0 {
		return fmt.Errorf("solusvm instance %q missing: %s", v.ID, strings.Join(missing, ", "))
	}
	parsed, err := url.Parse(strings.TrimSpace(v.URL))
	if err != nil {
		return fmt.Errorf("solusvm instance %q has an invalid url %q: %w", v.ID, v.URL, err)
	}
	if parsed.Scheme != "https" {
		return fmt.Errorf("solusvm instance %q url must use https:// (got %q)", v.ID, v.URL)
	}
	host := strings.ToLower(parsed.Host)
	path := strings.ToLower(parsed.Path)
	if parsed.Port() == "5656" || strings.Contains(host, ":5656") || strings.Contains(strings.ToLower(v.URL), ":5656") {
		return fmt.Errorf("solusvm instance %q url looks like SolusVM 1 (:5656); use the SolusVM 2 management node on port 443", v.ID)
	}
	if strings.Contains(path, "/api/admin") {
		return fmt.Errorf("solusvm instance %q url looks like SolusVM 1 (/api/admin); use the SolusVM 2 management node origin only", v.ID)
	}
	return nil
}

// SolusVMCredentialsSecretName matches the chart fullname:
// {release}-solusvm-operator-solusvm-credentials-{id}.
func SolusVMCredentialsSecretName(helmRelease, instanceID string) string {
	return fmt.Sprintf("%s-solusvm-operator-solusvm-credentials-%s", helmRelease, instanceID)
}

type solusvmInstancesFile struct {
	Instances []SolusVMInstance `yaml:"instances"`
}

// LoadSolusVMInstancesFile reads a YAML file with either:
//
//	instances: [ { id, url, token }, ... ]
//
// or a bare list of instances. The file must be mode 0600.
func LoadSolusVMInstancesFile(path string) ([]SolusVMInstance, error) {
	if info, err := os.Stat(path); err == nil && info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("solusvm instances file %s is readable by group/other (mode %#o); run: chmod 600 %s",
			path, info.Mode().Perm(), path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read solusvm instances file: %w", err)
	}
	var wrapped solusvmInstancesFile
	if err := yaml.Unmarshal(data, &wrapped); err == nil && len(wrapped.Instances) > 0 {
		return normalizeSolusVMInstances(wrapped.Instances)
	}
	var list []SolusVMInstance
	if err := yaml.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("parse solusvm instances file %s: expected instances: [] or a list: %w", path, err)
	}
	return normalizeSolusVMInstances(list)
}

func normalizeSolusVMInstances(in []SolusVMInstance) ([]SolusVMInstance, error) {
	if len(in) == 0 {
		return nil, fmt.Errorf("no solusvm instances provided")
	}
	out := make([]SolusVMInstance, 0, len(in))
	seen := map[string]struct{}{}
	for i, inst := range in {
		inst.ID = strings.TrimSpace(inst.ID)
		inst.URL = NormalizeHypervisorAPIURL(inst.URL)
		inst.Token = strings.TrimSpace(inst.Token)
		if inst.ID == "" {
			inst.ID = fmt.Sprintf("svm-%d", i+1)
		}
		if _, ok := seen[inst.ID]; ok {
			return nil, fmt.Errorf("duplicate solusvm instance id %q", inst.ID)
		}
		seen[inst.ID] = struct{}{}
		if err := inst.Validate(); err != nil {
			return nil, err
		}
		out = append(out, inst)
	}
	return out, nil
}

// SolusVMInstanceFromEnv builds a single instance from SOLUSVM_* env vars.
// Returns nil, nil when none of the required vars are set.
func SolusVMInstanceFromEnv() (*SolusVMInstance, error) {
	apiURL := NormalizeHypervisorAPIURL(os.Getenv("SOLUSVM_URL"))
	token := strings.TrimSpace(os.Getenv("SOLUSVM_TOKEN"))
	if apiURL == "" && token == "" {
		return nil, nil
	}
	id := strings.TrimSpace(os.Getenv("SOLUSVM_INSTANCE_ID"))
	if id == "" {
		id = "svm-1"
	}
	inst := &SolusVMInstance{
		ID:    id,
		URL:   apiURL,
		Token: token,
	}
	if err := inst.Validate(); err != nil {
		return nil, fmt.Errorf("SOLUSVM_* env incomplete: %w", err)
	}
	return inst, nil
}
