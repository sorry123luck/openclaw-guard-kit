//go:build windows

package main

import (
	"context"
	"time"

	// reuse stdlib log for minimal skeleton; a real logger would be wired later
	"log"
)

// Minimal entry to initialize and run the detector skeleton
func main() {
	cfg := DetectorConfig{
		OpenClawPath:           "C:\\Program Files\\OpenClaw\\openclaw.exe",
		RootDir:                "C:\\OpenClaw",
		AgentID:                "main",
		ProbeIntervalSeconds:   10,
		OfflineGraceSeconds:    1800,
		RestartCooldownSeconds: 60,
		HealthyConfirmCount:    3,
		UnhealthyConfirmCount:  2,
		LogLevel:               "info",
	}

	detector := NewDetector(cfg)
	// Run with a cancellable context; in real deployment, wire to OS signals
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Stop after a long-running period in this skeleton for demonstration
	go func() {
		time.Sleep(5 * time.Minute)
		cancel()
	}()

	if err := detector.Run(ctx); err != nil {
		log.Printf("detector exited: %v", err)
	}
}
