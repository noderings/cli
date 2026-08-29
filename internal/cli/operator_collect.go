package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/noderings/cli/internal/install"
)

// collectOperatorInstallInputs gathers Proxmox instances. Mimir tokens are issued via API
// during register (or taken from MIMIR_BEARER_TOKEN if already set).
func collectOperatorInstallInputs(
	cfg *install.ProxmoxOperatorConfig,
	instancesFile string,
	nonInteractive bool,
	log interface {
		Infof(format string, args ...interface{})
		Info(args ...interface{})
	},
) error {
	if cfg == nil {
		return fmt.Errorf("operator config is required")
	}

	var instances []install.ProxmoxInstance
	switch {
	case strings.TrimSpace(instancesFile) != "":
		loaded, err := install.LoadProxmoxInstancesFile(instancesFile)
		if err != nil {
			return err
		}
		instances = loaded
		log.Infof("Loaded %d Proxmox instance(s) from %s", len(instances), instancesFile)
	default:
		fromEnv, err := install.ProxmoxInstanceFromEnv()
		if err != nil {
			return err
		}
		if fromEnv != nil {
			instances = []install.ProxmoxInstance{*fromEnv}
			log.Info("Using Proxmox credentials from PROXMOX_* environment variables")
		}
	}

	if len(instances) == 0 {
		if nonInteractive {
			return fmt.Errorf("proxmox credentials required: set PROXMOX_* env, --proxmox-instances-file, or run interactively (without --yes)")
		}
		log.Info("Configure Proxmox API access for the operator (supports multiple instances)")
		for i := 0; ; i++ {
			defaultID := fmt.Sprintf("proxmox-%d", i+1)
			id, err := promptString("Proxmox instance ID (local to this agent, not the platform name)", defaultID)
			if err != nil {
				return err
			}
			url, err := promptString("Proxmox URL (e.g. https://pve.example:8006)", "")
			if err != nil {
				return err
			}
			url = install.NormalizeHypervisorAPIURL(url)
			user, err := promptString("Proxmox username", "kopfoperator@pve")
			if err != nil {
				return err
			}
			tokenID, err := promptString("Proxmox token ID", "")
			if err != nil {
				return err
			}
			tokenSecret, err := promptSecret("Proxmox token secret", false)
			if err != nil {
				return err
			}
			inst := install.ProxmoxInstance{
				ID:          id,
				URL:         url,
				Username:    user,
				TokenID:     tokenID,
				TokenSecret: tokenSecret,
			}
			if err := inst.Validate(); err != nil {
				return err
			}
			instances = append(instances, inst)

			more, err := confirmYesNo("Add another Proxmox instance?", "set --proxmox-instances-file for non-interactive install")
			if err != nil {
				return err
			}
			if !more {
				break
			}
		}
	}

	if len(instances) == 0 {
		return fmt.Errorf("at least one Proxmox instance is required")
	}
	cfg.Instances = instances
	return nil
}

// collectVirtFusionOperatorInstallInputs gathers VirtFusion instances. Mimir tokens are
// issued via API during register (or taken from MIMIR_BEARER_TOKEN if already set).
func collectVirtFusionOperatorInstallInputs(
	cfg *install.VirtFusionOperatorConfig,
	instancesFile string,
	nonInteractive bool,
	log interface {
		Infof(format string, args ...interface{})
		Info(args ...interface{})
	},
) error {
	if cfg == nil {
		return fmt.Errorf("operator config is required")
	}

	var instances []install.VirtFusionInstance
	switch {
	case strings.TrimSpace(instancesFile) != "":
		loaded, err := install.LoadVirtFusionInstancesFile(instancesFile)
		if err != nil {
			return err
		}
		instances = loaded
		log.Infof("Loaded %d VirtFusion instance(s) from %s", len(instances), instancesFile)
	default:
		fromEnv, err := install.VirtFusionInstanceFromEnv()
		if err != nil {
			return err
		}
		if fromEnv != nil {
			instances = []install.VirtFusionInstance{*fromEnv}
			log.Info("Using VirtFusion credentials from VIRTFUSION_* environment variables")
		}
	}

	if len(instances) == 0 {
		if nonInteractive {
			return fmt.Errorf("virtfusion credentials required: set VIRTFUSION_URL, VIRTFUSION_TOKEN, VIRTFUSION_USER_API_TOKEN, VIRTFUSION_USER_ID, VIRTFUSION_USER_NAME, --virtfusion-instances-file, or run interactively (without --yes)")
		}
		log.Info("Configure VirtFusion API access for the operator (supports multiple instances)")
		log.Info("Create a normal VirtFusion client user (not an admin). Copy the numeric user ID. Log in as that user and generate a User API token under Account → API.")
		for i := 0; ; i++ {
			defaultID := fmt.Sprintf("vf-%d", i+1)
			id, err := promptString("VirtFusion instance ID (local to this agent, not the platform name)", defaultID)
			if err != nil {
				return err
			}
			url, err := promptString("VirtFusion URL (e.g. https://cp.example.com)", "")
			if err != nil {
				return err
			}
			url = install.NormalizeHypervisorAPIURL(url)
			// Echo on paste: VF tokens are long; hidden prompts look like the paste failed.
			token, err := promptString("VirtFusion Global API token", "")
			if err != nil {
				return err
			}
			if strings.TrimSpace(token) == "" {
				return fmt.Errorf("VirtFusion Global API token is required")
			}
			userName, err := promptString("VirtFusion client username (the panel user you created for NodeRings)", "")
			if err != nil {
				return err
			}
			userIDStr, err := promptString("VirtFusion client user ID (numeric, from Users list)", "")
			if err != nil {
				return err
			}
			userID, err := strconv.Atoi(strings.TrimSpace(userIDStr))
			if err != nil || userID < 1 {
				return fmt.Errorf("VirtFusion client user ID must be a positive integer")
			}
			userAPIToken, err := promptString("VirtFusion User API token (Account → API while logged in as that user)", "")
			if err != nil {
				return err
			}
			if strings.TrimSpace(userAPIToken) == "" {
				return fmt.Errorf("VirtFusion User API token is required")
			}
			inst := install.VirtFusionInstance{
				ID:           id,
				URL:          url,
				Token:        token,
				UserAPIToken: userAPIToken,
				UserID:       userID,
				UserName:     userName,
			}
			if err := inst.Validate(); err != nil {
				return err
			}
			instances = append(instances, inst)

			more, err := confirmYesNo("Add another VirtFusion instance?", "set --virtfusion-instances-file for non-interactive install")
			if err != nil {
				return err
			}
			if !more {
				break
			}
		}
	}

	if len(instances) == 0 {
		return fmt.Errorf("at least one VirtFusion instance is required")
	}
	cfg.Instances = instances
	return nil
}

// collectSolusVMOperatorInstallInputs gathers SolusVM 2 instances. Mimir tokens are
// issued via API during register (or taken from MIMIR_BEARER_TOKEN if already set).
func collectSolusVMOperatorInstallInputs(
	cfg *install.SolusVMOperatorConfig,
	instancesFile string,
	nonInteractive bool,
	log interface {
		Infof(format string, args ...interface{})
		Info(args ...interface{})
	},
) error {
	if cfg == nil {
		return fmt.Errorf("operator config is required")
	}

	var instances []install.SolusVMInstance
	switch {
	case strings.TrimSpace(instancesFile) != "":
		loaded, err := install.LoadSolusVMInstancesFile(instancesFile)
		if err != nil {
			return err
		}
		instances = loaded
		log.Infof("Loaded %d SolusVM instance(s) from %s", len(instances), instancesFile)
	default:
		fromEnv, err := install.SolusVMInstanceFromEnv()
		if err != nil {
			return err
		}
		if fromEnv != nil {
			instances = []install.SolusVMInstance{*fromEnv}
			log.Info("Using SolusVM credentials from SOLUSVM_* environment variables")
		}
	}

	if len(instances) == 0 {
		if nonInteractive {
			return fmt.Errorf("solusvm credentials required: set SOLUSVM_URL and SOLUSVM_TOKEN, --solusvm-instances-file, or run interactively (without --yes)")
		}
		log.Info("Configure SolusVM 2 API access for the operator (supports multiple instances)")
		for i := 0; ; i++ {
			defaultID := fmt.Sprintf("svm-%d", i+1)
			id, err := promptString("SolusVM instance ID (local to this agent, not the platform name)", defaultID)
			if err != nil {
				return err
			}
			url, err := promptString("SolusVM 2 management node URL (e.g. https://mn.example.com)", "")
			if err != nil {
				return err
			}
			url = install.NormalizeHypervisorAPIURL(url)
			token, err := promptSecret("SolusVM API token", false)
			if err != nil {
				return err
			}
			inst := install.SolusVMInstance{
				ID:    id,
				URL:   url,
				Token: token,
			}
			if err := inst.Validate(); err != nil {
				return err
			}
			instances = append(instances, inst)

			more, err := confirmYesNo("Add another SolusVM instance?", "set --solusvm-instances-file for non-interactive install")
			if err != nil {
				return err
			}
			if !more {
				break
			}
		}
	}

	if len(instances) == 0 {
		return fmt.Errorf("at least one SolusVM instance is required")
	}
	cfg.Instances = instances
	return nil
}
