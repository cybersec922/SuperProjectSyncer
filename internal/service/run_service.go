//go:build windows

package service

import (
	"context"
	"log"

	"golang.org/x/sys/windows/svc"
)

// RunIfService runs runFn inside the Windows Service Control Manager when
// the process was started as a service. Returns true if it handled execution.
func RunIfService(runFn func(ctx context.Context) error) bool {
	isService, err := svc.IsWindowsService()
	if err != nil {
		log.Printf("service detect: %v", err)
		return false
	}
	if !isService {
		return false
	}
	if err := RunAsService(serviceName, runFn); err != nil {
		log.Fatal(err)
	}
	return true
}
