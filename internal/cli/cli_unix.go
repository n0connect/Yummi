//go:build unix || darwin || linux

package cli

import "syscall"

// diskFree returns available disk space in bytes for the given path.
// This implementation uses Unix syscall.Statfs for POSIX systems.
func diskFree(path string) (uint64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, err
	}
	return stat.Bavail * uint64(stat.Bsize), nil
}
