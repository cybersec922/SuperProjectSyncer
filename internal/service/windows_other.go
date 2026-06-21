//go:build !windows

package service

import (
	"context"
	"fmt"
)

func RunAsService(name string, runFn func(ctx context.Context) error) error {
	return fmt.Errorf("RunAsService is only supported on Windows")
}
