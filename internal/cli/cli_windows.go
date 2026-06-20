//go:build windows

package cli

import (
	"errors"
	"syscall"
	"unsafe"
)

var kernel32 = syscall.NewLazyDLL("kernel32.dll")
var getDiskFreeSpaceEx = kernel32.NewProc("GetDiskFreeSpaceExW")

// diskFree returns available disk space in bytes for the given path.
// This implementation uses Windows GetDiskFreeSpaceEx API.
func diskFree(path string) (uint64, error) {
	if getDiskFreeSpaceEx.Find() != nil {
		// Fallback if API not available
		return 0, errors.New("GetDiskFreeSpaceEx not available")
	}

	pathPtr, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}

	var freeBytesAvailable uint64
	var totalBytes uint64
	var totalFreeBytes uint64

	ret, _, callErr := getDiskFreeSpaceEx.Call(
		uintptr(unsafe.Pointer(pathPtr)),
		uintptr(unsafe.Pointer(&freeBytesAvailable)),
		uintptr(unsafe.Pointer(&totalBytes)),
		uintptr(unsafe.Pointer(&totalFreeBytes)),
	)

	if ret == 0 {
		if callErr != nil {
			return 0, callErr
		}
		return 0, errors.New("GetDiskFreeSpaceEx failed")
	}

	return freeBytesAvailable, nil
}
