package verify

import (
	"fmt"
	"os"
	"strings"

	"github.com/noderings/cli/internal/config"
)

// Options controls which verify subsections run and what they expect.
// Built from `nr cluster register` flags so skipped phases are not required.
type Options struct {
	AgentID        string
	KubeconfigPath string

	// OnlySections, when non-empty, restricts execution to these section names.
	OnlySections []string

	// ExpectOffloading requires NamespaceOffloading for OffloadNamespaces.
	ExpectOffloading  bool
	OffloadNamespaces []string

	// ExpectOperator requires hypervisor operator chart workloads and CRDs.
	ExpectOperator      bool
	HypervisorDriver    string
	OperatorNamespace   string // Helm install namespace (default: proxmox-system or virtfusion-system)
	OperatorRelease     string
	VNCGatewayNamespace string

	// ExpectOperatorRemoteNS requires the remote namespace reflected after inbound peering.
	ExpectOperatorRemoteNS bool
	OperatorRemoteNS       string

	// ExpectAgentAPI polls the platform API for provisioned / inbound peering status.
	ExpectAgentAPI bool
}

// RegisterScope captures the register-flag subset that affects verify.
type RegisterScope struct {
	AgentID             string
	DisableOffloading   bool
	SkipOperatorInstall bool
	HypervisorDriver    string
	OperatorNamespace   string // optional extra namespace to offload (not the Helm namespace)
	VNCGatewayNamespace string
	OffloadNamespaces   []string
}

// OptionsFromRegister builds verify Options matching what register installed.
func OptionsFromRegister(scope RegisterScope) Options {
	helmNSFallback := config.DefaultProxmoxOperatorHelmNamespace
	if config.IsSolusVMHypervisor(scope.HypervisorDriver) {
		helmNSFallback = config.DefaultSolusVMOperatorHelmNamespace
	} else if config.IsVirtFusionHypervisor(scope.HypervisorDriver) {
		helmNSFallback = config.DefaultVirtFusionOperatorHelmNamespace
	}
	opts := Options{
		AgentID:                strings.TrimSpace(scope.AgentID),
		HypervisorDriver:       strings.TrimSpace(scope.HypervisorDriver),
		ExpectAgentAPI:         strings.TrimSpace(scope.AgentID) != "",
		ExpectOperatorRemoteNS: true,
		OperatorRemoteNS:       config.DefaultOperatorRemoteNamespace,
		OperatorNamespace:      getenvOr(helmNSFallback, config.EnvHelmNamespace),
		OperatorRelease:        getenvOr(config.DefaultProxmoxOperatorHelmRelease, config.EnvHelmRelease),
		VNCGatewayNamespace:    config.DefaultVNCGatewayNamespace,
		ExpectOperator:         !scope.SkipOperatorInstall,
		ExpectOffloading:       !scope.DisableOffloading,
	}

	if ns := strings.TrimSpace(scope.VNCGatewayNamespace); ns != "" {
		opts.VNCGatewayNamespace = ns
	} else if env := strings.TrimSpace(os.Getenv(config.EnvVNCGatewayNamespace)); env != "" {
		opts.VNCGatewayNamespace = env
	}

	if scope.DisableOffloading {
		opts.OffloadNamespaces = nil
		opts.ExpectOffloading = false
	} else {
		opts.OffloadNamespaces = collectOffloadNamespaces(scope)
		if len(opts.OffloadNamespaces) == 0 {
			opts.ExpectOffloading = false
		}
	}

	return opts
}

func getenvOr(fallback, key string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

// ValidateOnlySections returns an error if OnlySections contains unknown names.
func ValidateOnlySections(sections []string) error {
	if len(sections) == 0 {
		return nil
	}
	known := map[string]struct{}{}
	for _, s := range AllSections {
		known[s] = struct{}{}
	}
	var unknown []string
	for _, raw := range sections {
		name := strings.ToLower(strings.TrimSpace(raw))
		if name == "" {
			continue
		}
		if _, ok := known[name]; !ok {
			unknown = append(unknown, raw)
		}
	}
	if len(unknown) > 0 {
		return fmt.Errorf("unknown verify section(s) %v; valid: %s", unknown, strings.Join(AllSections, ", "))
	}
	return nil
}

func collectOffloadNamespaces(scope RegisterScope) []string {
	seen := map[string]struct{}{}
	var out []string
	add := func(ns string) {
		ns = strings.TrimSpace(ns)
		if ns == "" {
			return
		}
		if _, ok := seen[ns]; ok {
			return
		}
		seen[ns] = struct{}{}
		out = append(out, ns)
	}

	vnc := strings.TrimSpace(scope.VNCGatewayNamespace)
	if vnc == "" {
		if env := strings.TrimSpace(os.Getenv(config.EnvVNCGatewayNamespace)); env != "" {
			vnc = env
		} else {
			vnc = config.DefaultVNCGatewayNamespace
		}
	}
	add(vnc)
	add(scope.OperatorNamespace)
	for _, ns := range scope.OffloadNamespaces {
		add(ns)
	}
	return out
}

// sectionEnabled reports whether a section should run given OnlySections.
func (o Options) sectionEnabled(name string) bool {
	if len(o.OnlySections) == 0 {
		return true
	}
	for _, s := range o.OnlySections {
		if strings.EqualFold(strings.TrimSpace(s), name) {
			return true
		}
	}
	return false
}

func (o Options) operatorDeployName() string {
	if config.IsSolusVMHypervisor(o.HypervisorDriver) {
		return config.HelmSolusVMOperatorDeployName(o.OperatorRelease)
	}
	if config.IsVirtFusionHypervisor(o.HypervisorDriver) {
		return config.HelmVirtFusionOperatorDeployName(o.OperatorRelease)
	}
	return config.HelmProxmoxOperatorDeployName(o.OperatorRelease)
}

func (o Options) alloyDaemonSetName() string {
	return config.HelmAlloyDaemonSetName(o.OperatorRelease)
}

func (o Options) exporterDeployName() string {
	if config.IsSolusVMHypervisor(o.HypervisorDriver) {
		return config.HelmSolusVMExporterDeployName(o.OperatorRelease)
	}
	if config.IsVirtFusionHypervisor(o.HypervisorDriver) {
		return config.HelmVirtFusionExporterDeployName(o.OperatorRelease)
	}
	return config.HelmExporterDeployName(o.OperatorRelease)
}

func (o Options) vncGatewayDeployName() string {
	if config.IsSolusVMHypervisor(o.HypervisorDriver) {
		return config.HelmSolusVMVNCGatewayDeployName(o.OperatorRelease)
	}
	if config.IsVirtFusionHypervisor(o.HypervisorDriver) {
		return config.HelmVirtFusionVNCGatewayDeployName(o.OperatorRelease)
	}
	return config.HelmVNCGatewayDeployName(o.OperatorRelease)
}

func (o Options) helmNamespace() string {
	if o.OperatorNamespace != "" {
		return o.OperatorNamespace
	}
	if config.IsSolusVMHypervisor(o.HypervisorDriver) {
		return config.DefaultSolusVMOperatorHelmNamespace
	}
	if config.IsVirtFusionHypervisor(o.HypervisorDriver) {
		return config.DefaultVirtFusionOperatorHelmNamespace
	}
	return config.DefaultProxmoxOperatorHelmNamespace
}

func (o Options) crdAPIGroup() string {
	if config.IsSolusVMHypervisor(o.HypervisorDriver) {
		return config.SolusVMCRDAPIGroup
	}
	if config.IsVirtFusionHypervisor(o.HypervisorDriver) {
		return config.VirtFusionCRDAPIGroup
	}
	return config.ProxmoxCRDAPIGroup
}

func (o Options) vncNamespace() string {
	if o.VNCGatewayNamespace != "" {
		return o.VNCGatewayNamespace
	}
	return config.DefaultVNCGatewayNamespace
}

func (o Options) operatorRemoteNamespace() string {
	if o.OperatorRemoteNS != "" {
		return o.OperatorRemoteNS
	}
	return config.DefaultOperatorRemoteNamespace
}
