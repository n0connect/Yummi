package main

import (
	"fmt"
	"os"
	"runtime"
)

// requireSandboxRuntime prevents accidental execution of sandbox tools on a
// developer's computer or in an unrelated builder container. There is no
// environment-variable bypass. This is an execution-placement guard, not
// attestation against an attacker controlling the container or its root user.
func requireSandboxRuntime() error {
	if runtime.GOOS != "linux" || os.Geteuid() != 1000 {
		return fmt.Errorf("sandbox runtime required: Linux container, UID 1000")
	}
	if info, err := os.Lstat("/.dockerenv"); err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("sandbox runtime required: Docker container marker missing")
	}
	info, err := os.Lstat("/usr/local/bin/yummi-sandbox-mcp")
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 {
		return fmt.Errorf("sandbox runtime required: installed Yummi sandbox binary missing or writable")
	}
	return nil
}
