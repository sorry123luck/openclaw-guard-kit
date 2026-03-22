//go:build windows

package main

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"time"
)

// Minimal entry to initialize and run the detector skeleton
func main() {
	// discover root dir and guard binaries
	exePath, _ := os.Executable()
	exeDir := filepath.Dir(exePath)
	rootDir := filepath.Clean(filepath.Join(exeDir, ".."))
	guardExe := filepath.Join(rootDir, "guard.exe")
	guardUI := filepath.Join(rootDir, "guard-ui.exe")

	cfg := DetectorConfig{
		OpenClawPath:           filepath.Join(rootDir, "openclaw.exe"),
		RootDir:                rootDir,
		AgentID:                "main",
		ProbeIntervalSeconds:   10,
		OfflineGraceSeconds:    1800,
		RestartCooldownSeconds: 60,
		HealthyConfirmCount:    3,
		UnhealthyConfirmCount:  2,
		LogLevel:               "info",
	}

	logger := log.New(os.Stdout, "detector ", log.LstdFlags)
	detector := NewDetector(logger, cfg, guardExe, guardUI)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// keep alive for a while
	go func() {
		time.Sleep(5 * time.Minute)
		cancel()
	}()

	if err := detector.Run(ctx); err != nil {
		logger.Printf("detector exited: %v", err)
	}
}
