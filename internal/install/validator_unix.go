//go:build !windows

package install

import (
	"fmt"
	"syscall"
)

// checkDiskSpace checks available disk space (Unix implementation)
func (v *SystemValidator) checkDiskSpace() SystemCheckResult {
	var stat syscall.Statfs_t
	if err := syscall.Statfs("/", &stat); err != nil {
		return SystemCheckResult{
			Check:   "Disk Space",
			Status:  "warn",
			Message: "Cannot check disk space",
		}
	}

	// Calculate available space in GB
	availableBytes := stat.Bavail * uint64(stat.Bsize)
	availableGB := float64(availableBytes) / (1024 * 1024 * 1024)

	// Require at least 10GB free
	if availableGB < 10 {
		return SystemCheckResult{
			Check:   "Disk Space",
			Status:  "fail",
			Message: fmt.Sprintf("Insufficient disk space: %.2f GB available (need at least 10 GB)", availableGB),
		}
	}

	return SystemCheckResult{
		Check:   "Disk Space",
		Status:  "pass",
		Message: fmt.Sprintf("Disk space OK: %.2f GB available", availableGB),
	}
}
