package install

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/user"
	"runtime"
	"strings"
	"time"
)

// SystemValidator performs pre-flight system checks
type SystemValidator struct {
	logger Logger
}

// NewSystemValidator creates a new system validator
func NewSystemValidator(logger Logger) *SystemValidator {
	return &SystemValidator{
		logger: logger,
	}
}

// SystemCheckResult represents the result of a system check
type SystemCheckResult struct {
	Check   string
	Status  string // "pass" | "fail" | "warn"
	Message string
}

// ValidateSystem checks system requirements
func (v *SystemValidator) ValidateSystem(ctx context.Context) ([]SystemCheckResult, error) {
	var results []SystemCheckResult

	// Check OS (Ubuntu recommended)
	osCheck := v.checkOS()
	results = append(results, osCheck)

	// Check root/sudo access
	rootCheck := v.checkRootAccess()
	results = append(results, rootCheck)

	// Check network connectivity
	networkCheck := v.checkNetworkConnectivity(ctx)
	results = append(results, networkCheck)

	// Check DNS resolution
	dnsCheck := v.checkDNS(ctx)
	results = append(results, dnsCheck)

	// Check required ports
	portsCheck := v.checkPorts()
	results = append(results, portsCheck)

	// Check disk space
	diskCheck := v.checkDiskSpace()
	results = append(results, diskCheck)

	// Check memory
	memoryCheck := v.checkMemory()
	results = append(results, memoryCheck)

	return results, nil
}

// checkOS checks if running on a supported OS
func (v *SystemValidator) checkOS() SystemCheckResult {
	osName := runtime.GOOS
	if osName != "linux" {
		return SystemCheckResult{
			Check:   "OS",
			Status:  "warn",
			Message: fmt.Sprintf("Running on %s (Ubuntu Linux recommended)", osName),
		}
	}

	// Try to detect Ubuntu
	if _, err := os.Stat("/etc/os-release"); err == nil {
		data, err := os.ReadFile("/etc/os-release")
		if err == nil {
			content := string(data)
			if strings.Contains(strings.ToLower(content), "ubuntu") {
				return SystemCheckResult{
					Check:   "OS",
					Status:  "pass",
					Message: "Ubuntu detected",
				}
			}
		}
	}

	return SystemCheckResult{
		Check:   "OS",
		Status:  "warn",
		Message: "Linux detected (Ubuntu recommended)",
	}
}

// checkRootAccess checks if running as root or has sudo access
func (v *SystemValidator) checkRootAccess() SystemCheckResult {
	currentUser, err := user.Current()
	if err != nil {
		return SystemCheckResult{
			Check:   "Root/Sudo Access",
			Status:  "fail",
			Message: fmt.Sprintf("Cannot determine user: %v", err),
		}
	}

	if currentUser.Uid == "0" {
		return SystemCheckResult{
			Check:   "Root/Sudo Access",
			Status:  "pass",
			Message: "Running as root",
		}
	}

	// Check if sudo is available
	if _, err := exec.LookPath("sudo"); err != nil {
		return SystemCheckResult{
			Check:   "Root/Sudo Access",
			Status:  "fail",
			Message: "Not running as root and sudo not available",
		}
	}

	// Test sudo access
	cmd := exec.Command("sudo", "-n", "true")
	if err := cmd.Run(); err == nil {
		return SystemCheckResult{
			Check:   "Root/Sudo Access",
			Status:  "pass",
			Message: "Sudo access available (passwordless)",
		}
	}

	return SystemCheckResult{
		Check:   "Root/Sudo Access",
		Status:  "warn",
		Message: "Sudo available but may require password",
	}
}

// checkNetworkConnectivity checks internet connectivity
func (v *SystemValidator) checkNetworkConnectivity(ctx context.Context) SystemCheckResult {
	// Try to connect to a well-known host
	timeout := 5 * time.Second
	conn, err := net.DialTimeout("tcp", "8.8.8.8:53", timeout)
	if err != nil {
		return SystemCheckResult{
			Check:   "Network Connectivity",
			Status:  "fail",
			Message: "Cannot reach internet (8.8.8.8:53)",
		}
	}
	_ = conn.Close()

	return SystemCheckResult{
		Check:   "Network Connectivity",
		Status:  "pass",
		Message: "Internet connectivity OK",
	}
}

// checkDNS checks DNS resolution
func (v *SystemValidator) checkDNS(ctx context.Context) SystemCheckResult {
	resolver := net.DefaultResolver
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	_, err := resolver.LookupHost(ctx, "google.com")
	if err != nil {
		return SystemCheckResult{
			Check:   "DNS Resolution",
			Status:  "fail",
			Message: fmt.Sprintf("DNS resolution failed: %v", err),
		}
	}

	return SystemCheckResult{
		Check:   "DNS Resolution",
		Status:  "pass",
		Message: "DNS resolution OK",
	}
}

// checkPorts checks if required ports are available
func (v *SystemValidator) checkPorts() SystemCheckResult {
	// Required ports for k3s: 6443, 10250, 8472 (UDP), 51820 (UDP), 51821 (UDP)
	requiredPorts := []struct {
		port  int
		proto string
	}{
		{6443, "tcp"},
		{10250, "tcp"},
		{8472, "udp"},
		{51820, "udp"},
		{51821, "udp"},
	}

	var failedPorts []string
	for _, p := range requiredPorts {
		if !v.isPortAvailable(p.port, p.proto) {
			failedPorts = append(failedPorts, fmt.Sprintf("%d/%s", p.port, p.proto))
		}
	}

	if len(failedPorts) > 0 {
		return SystemCheckResult{
			Check:   "Required Ports",
			Status:  "warn",
			Message: fmt.Sprintf("Some ports may be in use: %s", strings.Join(failedPorts, ", ")),
		}
	}

	return SystemCheckResult{
		Check:   "Required Ports",
		Status:  "pass",
		Message: "Required ports available",
	}
}

// isPortAvailable checks if a port is available
func (v *SystemValidator) isPortAvailable(port int, proto string) bool {
	var network string
	if proto == "tcp" {
		network = "tcp"
	} else {
		network = "udp"
	}

	addr := fmt.Sprintf(":%d", port)
	listener, err := net.Listen(network, addr)
	if err != nil {
		return false
	}
	_ = listener.Close()
	return true
}

// checkMemory checks available memory
func (v *SystemValidator) checkMemory() SystemCheckResult {
	// Read /proc/meminfo on Linux
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return SystemCheckResult{
			Check:   "Memory",
			Status:  "warn",
			Message: "Cannot check memory",
		}
	}

	// Parse MemAvailable (preferred) or MemFree
	lines := strings.Split(string(data), "\n")
	var memAvailableKB, memFreeKB int64
	for _, line := range lines {
		if strings.HasPrefix(line, "MemAvailable:") {
			_, _ = fmt.Sscanf(line, "MemAvailable: %d kB", &memAvailableKB)
		}
		if strings.HasPrefix(line, "MemFree:") {
			_, _ = fmt.Sscanf(line, "MemFree: %d kB", &memFreeKB)
		}
	}

	memGB := float64(memAvailableKB) / (1024 * 1024)
	if memGB == 0 {
		memGB = float64(memFreeKB) / (1024 * 1024)
	}

	// Require at least 2GB
	if memGB < 2 {
		return SystemCheckResult{
			Check:   "Memory",
			Status:  "fail",
			Message: fmt.Sprintf("Insufficient memory: %.2f GB available (need at least 2 GB)", memGB),
		}
	}

	return SystemCheckResult{
		Check:   "Memory",
		Status:  "pass",
		Message: fmt.Sprintf("Memory OK: %.2f GB available", memGB),
	}
}

// ValidateTempDir checks if temp directory is writable
func (v *SystemValidator) ValidateTempDir(tempDir string) error {
	// Check if directory exists or can be created
	//nolint:gosec // G301: temp dir permissions match os.TempDir defaults
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		return fmt.Errorf("cannot create temp dir: %w", err)
	}

	// Check if writable
	testFile := fmt.Sprintf("%s/.test", tempDir)
	//nolint:gosec // G306: ephemeral writability probe file
	if err := os.WriteFile(testFile, []byte("test"), 0644); err != nil {
		return fmt.Errorf("temp dir not writable: %w", err)
	}
	_ = os.Remove(testFile)

	return nil
}
