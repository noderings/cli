package install

import (
	"fmt"
	"net"
	"sort"
	"strings"
)

const agentIPCheckName = "Agent IP"

type ifaceAddr struct {
	name     string
	ip       net.IP
	up       bool
	loopback bool
}

// listLocalAddrs is swapped in tests.
var listLocalAddrs = listLocalInterfaceAddrs

func listLocalInterfaceAddrs() ([]ifaceAddr, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	out := make([]ifaceAddr, 0, len(ifaces))
	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		up := iface.Flags&net.FlagUp != 0
		loopbackIface := iface.Flags&net.FlagLoopback != 0
		for _, a := range addrs {
			ip := ipFromAddr(a)
			if ip == nil || ip.IsUnspecified() {
				continue
			}
			out = append(out, ifaceAddr{
				name:     iface.Name,
				ip:       ip,
				up:       up,
				loopback: loopbackIface || ip.IsLoopback(),
			})
		}
	}
	return out, nil
}

func ipFromAddr(a net.Addr) net.IP {
	switch v := a.(type) {
	case *net.IPNet:
		return v.IP
	case *net.IPAddr:
		return v.IP
	default:
		return nil
	}
}

func (v *SystemValidator) checkAgentIP(agentIP string) SystemCheckResult {
	addrs, err := listLocalAddrs()
	if err != nil {
		return SystemCheckResult{
			Check:   agentIPCheckName,
			Status:  "fail",
			Message: fmt.Sprintf("Cannot list network interfaces: %v", err),
		}
	}
	return checkAgentIPResult(agentIP, addrs)
}

func checkAgentIPResult(agentIP string, addrs []ifaceAddr) SystemCheckResult {
	want := strings.TrimSpace(agentIP)
	if want == "" {
		return SystemCheckResult{
			Check:   agentIPCheckName,
			Status:  "fail",
			Message: "--agent-ip is required and must be an address assigned to a local interface",
		}
	}
	parsed := net.ParseIP(want)
	if parsed == nil {
		return SystemCheckResult{
			Check:   agentIPCheckName,
			Status:  "fail",
			Message: fmt.Sprintf("%q is not a valid IP address", want),
		}
	}

	var matched *ifaceAddr
	for i := range addrs {
		a := &addrs[i]
		if a.ip.Equal(parsed) {
			matched = a
			break
		}
	}
	if matched == nil {
		return SystemCheckResult{
			Check:   agentIPCheckName,
			Status:  "fail",
			Message: fmt.Sprintf("%s is not assigned to any local interface%s. k3s uses --node-ip from --agent-ip; use an address from `ip -4 addr` on this VM (not a placeholder like 1.1.1.1)", want, formatLocalAddrHint(addrs)),
		}
	}
	if matched.loopback {
		return SystemCheckResult{
			Check:   agentIPCheckName,
			Status:  "fail",
			Message: fmt.Sprintf("%s is on loopback %s; pass a non-loopback address assigned to this VM", want, matched.name),
		}
	}
	if !matched.up {
		return SystemCheckResult{
			Check:   agentIPCheckName,
			Status:  "fail",
			Message: fmt.Sprintf("%s is assigned to %s but that interface is down", want, matched.name),
		}
	}
	return SystemCheckResult{
		Check:   agentIPCheckName,
		Status:  "pass",
		Message: fmt.Sprintf("%s is assigned to %s", want, matched.name),
	}
}

func formatLocalAddrHint(addrs []ifaceAddr) string {
	seen := map[string]struct{}{}
	var parts []string
	for _, a := range addrs {
		if a.loopback || a.ip.IsLinkLocalUnicast() {
			continue
		}
		ip := a.ip.String()
		if _, ok := seen[ip]; ok {
			continue
		}
		seen[ip] = struct{}{}
		parts = append(parts, fmt.Sprintf("%s=%s", a.name, ip))
	}
	if len(parts) == 0 {
		return " (no non-loopback addresses found)"
	}
	sort.Strings(parts)
	const max = 8
	if len(parts) > max {
		parts = append(parts[:max], "...")
	}
	return " (this VM has " + strings.Join(parts, ", ") + ")"
}
