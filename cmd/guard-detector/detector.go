//go:build windows

package main

import (
	"context"
	"log"
	"time"
)

// DetectorState defines minimal runtime states (skeleton for Stage-2)
type DetectorState int

const (
	DetStateUnknown DetectorState = iota
	DetStateOnline
	DetStateOffline
	DetStateStarting
)

// DetectorConfig holds the minimal config for the detector skeleton
type DetectorConfig struct {
	OpenClawPath           string
	RootDir                string
	AgentID                string
	ProbeIntervalSeconds   int
	OfflineGraceSeconds    int
	RestartCooldownSeconds int
	HealthyConfirmCount    int
	UnhealthyConfirmCount  int
	LogFile                string
	LogLevel               string
	ManualOverridePath     string
}

// Detector is a lightweight skeleton for Stage-2
type Detector struct {
	cfg    DetectorConfig
	state  DetectorState
	logger *log.Logger
}

// NewDetector creates a new detector instance (minimal)
func NewDetector(cfg DetectorConfig) *Detector {
	return &Detector{cfg: cfg, state: DetStateUnknown, logger: log.Default()}
}

// Run starts a minimal event loop representing a state machine skeleton
func (d *Detector) Run(ctx context.Context) error {
	// Initialize state to Online after a tiny delay to simulate startup
	d.state = DetStateStarting
	time.Sleep(100 * time.Millisecond)
	d.state = DetStateOnline

	ticker := time.NewTicker(time.Duration(maxInt(1, d.cfg.ProbeIntervalSeconds)) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			// In a full implementation we would perform health checks here
		}
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
