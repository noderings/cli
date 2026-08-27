//go:build windows

package install

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

var (
	kernel32           = syscall.NewLazyDLL("kernel32.dll")
	getDiskFreeSpaceEx = kernel32.NewProc("GetDiskFreeSpaceExW")
)

// checkDiskSpace checks available disk space (Windows implementation)
func (v *SystemValidator) checkDiskSpace() SystemCheckResult {
	// Get the current working directory's drive
	wd, err := os.Getwd()
	if err != nil {
		return SystemCheckResult{
			Check:   "Disk Space",
			Status:  "warn",
			Message: "Cannot determine current directory for disk space check",
		}
	}

	// Extract drive letter (e.g., "C:")
	var drive string
	if len(wd) >= 2 && wd[1] == ':' {
		drive = wd[0:2] + "\\"
	} else {
		drive = "C:\\"
	}

	// Call GetDiskFreeSpaceExW
	var freeBytes, totalBytes, availBytes int64
	drivePtr, _ := syscall.UTF16PtrFromString(drive)
	ret, _, _ := getDiskFreeSpaceEx.Call(
		uintptr(unsafe.Pointer(drivePtr)),
		uintptr(unsafe.Pointer(&freeBytes)),
		uintptr(unsafe.Pointer(&totalBytes)),
		uintptr(unsafe.Pointer(&availBytes)),
	)

	if ret == 0 {
		return SystemCheckResult{
			Check:   "Disk Space",
			Status:  "warn",
			Message: "Cannot check disk space on Windows",
		}
	}

	// Calculate available space in GB
	availableGB := float64(availBytes) / (1024 * 1024 * 1024)

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
