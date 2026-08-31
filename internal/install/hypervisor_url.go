package install

import "strings"

// NormalizeHypervisorAPIURL trims whitespace and trailing slashes so
// https://192.168.1.103/ and https://192.168.1.103 store as the same origin.
// Operators append their API path (VirtFusion/SolusVM /api/v1, Proxmox /api2/json).
func NormalizeHypervisorAPIURL(raw string) string {
	return strings.TrimRight(strings.TrimSpace(raw), "/")
}
