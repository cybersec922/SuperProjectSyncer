//go:build !windows

package service

import (
	"context"
	"fmt"
)

func installWindows(exePath, configPath string) error {
	return fmt.Errorf("installWindows called on non-windows")
}

func uninstallWindows() error {
	return fmt.Errorf("uninstallWindows called on non-windows")
}

// RunIfService is a no-op on non-Windows platforms.
func RunIfService(runFn func(ctx context.Context) error) bool {
	return false
}
