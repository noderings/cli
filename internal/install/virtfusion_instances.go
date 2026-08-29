package install

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// VirtFusionInstance holds one VirtFusion control-plane endpoint plus Global and User API credentials.
type VirtFusionInstance struct {
	ID           string `yaml:"id" json:"id"`
	URL          string `yaml:"url" json:"url"`
	Token        string `yaml:"token" json:"token"`
	UserAPIToken string `yaml:"userApiToken" json:"userApiToken"`
	UserID       int    `yaml:"userId" json:"userId"`
	UserName     string `yaml:"userName" json:"userName"`
}

// Validate checks required fields for one VirtFusion instance.
func (v VirtFusionInstance) Validate() error {
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
	if strings.TrimSpace(v.UserAPIToken) == "" {
		missing = append(missing, "userApiToken")
	}
	if v.UserID < 1 {
		missing = append(missing, "userId")
	}
	if strings.TrimSpace(v.UserName) == "" {
		missing = append(missing, "userName")
	}
	if len(missing) > 0 {
		return fmt.Errorf("virtfusion instance %q missing: %s", v.ID, strings.Join(missing, ", "))
	}
	parsed, err := url.Parse(strings.TrimSpace(v.URL))
	if err != nil {
		return fmt.Errorf("virtfusion instance %q has an invalid url %q: %w", v.ID, v.URL, err)
	}
	if parsed.Scheme != "https" {
		return fmt.Errorf("virtfusion instance %q url must use https:// (got %q)", v.ID, v.URL)
	}
	return nil
}

// VirtFusionCredentialsSecretName matches the chart fullname:
// {release}-virtfusion-operator-virtfusion-credentials-{id}.
func VirtFusionCredentialsSecretName(helmRelease, instanceID string) string {
	return fmt.Sprintf("%s-virtfusion-operator-virtfusion-credentials-%s", helmRelease, instanceID)
}

type virtfusionInstancesFile struct {
	Instances []VirtFusionInstance `yaml:"instances"`
}

// LoadVirtFusionInstancesFile reads a YAML file with either:
//
//	instances: [ { id, url, token, userApiToken, userId, userName }, ... ]
//
// or a bare list of instances. The file must be mode 0600.
func LoadVirtFusionInstancesFile(path string) ([]VirtFusionInstance, error) {
	if info, err := os.Stat(path); err == nil && info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("virtfusion instances file %s is readable by group/other (mode %#o); run: chmod 600 %s",
			path, info.Mode().Perm(), path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read virtfusion instances file: %w", err)
	}
	var wrapped virtfusionInstancesFile
	if err := yaml.Unmarshal(data, &wrapped); err == nil && len(wrapped.Instances) > 0 {
		return normalizeVirtFusionInstances(wrapped.Instances)
	}
	var list []VirtFusionInstance
	if err := yaml.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("parse virtfusion instances file %s: expected instances: [] or a list: %w", path, err)
	}
	return normalizeVirtFusionInstances(list)
}

func normalizeVirtFusionInstances(in []VirtFusionInstance) ([]VirtFusionInstance, error) {
	if len(in) == 0 {
		return nil, fmt.Errorf("no virtfusion instances provided")
	}
	out := make([]VirtFusionInstance, 0, len(in))
	seen := map[string]struct{}{}
	for i, inst := range in {
		inst.ID = strings.TrimSpace(inst.ID)
		inst.URL = NormalizeHypervisorAPIURL(inst.URL)
		inst.Token = strings.TrimSpace(inst.Token)
		inst.UserAPIToken = strings.TrimSpace(inst.UserAPIToken)
		inst.UserName = strings.TrimSpace(inst.UserName)
		if inst.ID == "" {
			inst.ID = fmt.Sprintf("vf-%d", i+1)
		}
		if _, ok := seen[inst.ID]; ok {
			return nil, fmt.Errorf("duplicate virtfusion instance id %q", inst.ID)
		}
		seen[inst.ID] = struct{}{}
		if err := inst.Validate(); err != nil {
			return nil, err
		}
		out = append(out, inst)
	}
	return out, nil
}

// VirtFusionInstanceFromEnv builds a single instance from VIRTFUSION_* env vars.
// Returns nil, nil when none of the required vars are set.
func VirtFusionInstanceFromEnv() (*VirtFusionInstance, error) {
	apiURL := NormalizeHypervisorAPIURL(os.Getenv("VIRTFUSION_URL"))
	token := strings.TrimSpace(os.Getenv("VIRTFUSION_TOKEN"))
	userAPI := strings.TrimSpace(os.Getenv("VIRTFUSION_USER_API_TOKEN"))
	userName := strings.TrimSpace(os.Getenv("VIRTFUSION_USER_NAME"))
	rawUserID := strings.TrimSpace(os.Getenv("VIRTFUSION_USER_ID"))
	if apiURL == "" && token == "" && userAPI == "" && userName == "" && rawUserID == "" {
		return nil, nil
	}
	id := strings.TrimSpace(os.Getenv("VIRTFUSION_INSTANCE_ID"))
	if id == "" {
		id = "vf-1"
	}
	userID := 0
	if rawUserID != "" {
		n, err := strconv.Atoi(rawUserID)
		if err != nil {
			return nil, fmt.Errorf("VIRTFUSION_USER_ID must be a positive integer")
		}
		userID = n
	}
	inst := &VirtFusionInstance{
		ID:           id,
		URL:          apiURL,
		Token:        token,
		UserAPIToken: userAPI,
		UserID:       userID,
		UserName:     userName,
	}
	if err := inst.Validate(); err != nil {
		return nil, fmt.Errorf("VIRTFUSION_* env incomplete: %w", err)
	}
	return inst, nil
}
