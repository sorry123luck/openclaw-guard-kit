//go:build !windows

package main

import "fmt"

func runWindowsService(_ []string) error {
	return fmt.Errorf("run-service is only supported on Windows")
}

func handleServiceCommand(_ []string) error {
	return fmt.Errorf("service command is only supported on Windows")
}
