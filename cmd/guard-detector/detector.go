//go:build windows

package main

import (
	"context"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// DetectorState defines minimal runtime states (Stage-2)
type DetectorState int

const (
	DetStateUnknown DetectorState = iota
	DetStateStarting
	DetStateOnline
	DetStateOffline
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

// Detector is a lightweight skeleton for Stage-2 with actual attach/detach hooks
type Detector struct {
	cfg            DetectorConfig
	state          DetectorState
	logger         *log.Logger
	guardExePath   string
	guardUIExePath string
	offlineSince   time.Time
	cooldownUntil  time.Time
	guardAttached  bool
	uiAttached     bool
}

func NewDetector(logger *log.Logger, cfg DetectorConfig, guardExePath, guardUIExePath string) *Detector {
	return &Detector{cfg: cfg, state: DetStateUnknown, logger: logger, guardExePath: guardExePath, guardUIExePath: guardUIExePath}
}

// Run starts a minimal event loop representing a state machine skeleton
func (d *Detector) Run(ctx context.Context) error {
	d.state = DetStateStarting
	time.Sleep(100 * time.Millisecond)
	d.state = DetStateOnline

	ticker := time.NewTicker(time.Duration(maxInt(1, d.cfg.ProbeIntervalSeconds)) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			d.detachGuardIfNeeded()
			d.detachUIIfNeeded()
			return ctx.Err()
		case <-ticker.C:
			d.tick(ctx)
		}
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (d *Detector) tick(ctx context.Context) {
	// minimal: prefer manual override, then attach if online, otherwise detach
	if d.isManualOverrideActive() {
		d.detachGuardIfNeeded()
		d.detachUIIfNeeded()
		d.state = DetStateOffline
		return
	}
	// Online/offline detection via OpenClaw health checks
	if d.isOpenClawOnline(ctx) {
		// OpenClaw online -> attach if not already
		d.offlineSince = time.Time{}
		d.cooldownUntil = time.Time{}
		d.ensureAttachments(ctx)
		d.state = DetStateOnline
		return
	}

	// OpenClaw offline: start/grace timer
	if d.offlineSince.IsZero() {
		d.offlineSince = time.Now().UTC()
	}
	if time.Since(d.offlineSince) >= time.Duration(d.cfg.OfflineGraceSeconds)*time.Second {
		d.detachGuardIfNeeded()
		d.detachUIIfNeeded()
		d.state = DetStateOffline
		d.cooldownUntil = time.Now().Add(time.Duration(d.cfg.RestartCooldownSeconds) * time.Second)
	}
}

// ensureAttachments attaches guard/UI when online and not already attached
func (d *Detector) ensureAttachments(ctx context.Context) {
	if !d.guardAttached {
		d.attachGuardIfNeeded(ctx)
	}
	if !d.uiAttached {
		d.attachUIIfNeeded(ctx)
	}
}

// isOpenClawOnline checks the external OpenClaw running state via official CLI if available
func (d *Detector) isOpenClawOnline(ctx context.Context) bool {
	if d.cfg.OpenClawPath == "" {
		return true
	}
	cmd := exec.CommandContext(ctx, d.cfg.OpenClawPath, "gateway", "status", "--require-rpc")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return false
	}
	s := strings.ToLower(strings.TrimSpace(string(out)))
	if strings.Contains(s, "running") || strings.Contains(s, "start-pending") {
		return true
	}
	return false
}

func (d *Detector) isManualOverrideActive() bool {
	if d.cfg.ManualOverridePath == "" {
		return false
	}
	if fi, err := os.Stat(d.cfg.ManualOverridePath); err == nil && !fi.IsDir() {
		return true
	}
	return false
}

func (d *Detector) isOfflineFlagActive() bool {
	if d.cfg.RootDir == "" {
		return false
	}
	offlinePath := filepath.Join(d.cfg.RootDir, ".offline")
	_, err := os.Stat(offlinePath)
	return err == nil
}

func (d *Detector) offlineGraceSeconds() int {
	if d.cfg.OfflineGraceSeconds <= 0 {
		return 1800
	}
	return d.cfg.OfflineGraceSeconds
}

func (d *Detector) attachGuardIfNeeded(ctx context.Context) {
	if d.guardAttached || d.guardExePath == "" {
		return
	}
	args := []string{"watch", "--root", d.cfg.RootDir, "--agent", d.cfg.AgentID, "--poll", strconv.Itoa(d.cfg.ProbeIntervalSeconds)}
	cmd := exec.CommandContext(ctx, d.guardExePath, args...)
	if err := cmd.Start(); err != nil {
		if d.logger != nil {
			d.logger.Printf("failed to start guard: %v", err)
		}
		return
	}
	d.guardAttached = true
	if d.logger != nil {
		d.logger.Printf("guard attached: %s", d.guardExePath)
	}
}

func (d *Detector) detachGuardIfNeeded() {
	if !d.guardAttached {
		return
	}
	name := filepath.Base(d.guardExePath)
	exec.Command("taskkill", "/IM", name, "/F").Run()
	d.guardAttached = false
	if d.logger != nil {
		d.logger.Printf("guard detached: %s", d.guardExePath)
	}
}

func (d *Detector) attachUIIfNeeded(ctx context.Context) {
	if d.uiAttached || d.guardUIExePath == "" {
		return
	}
	cmd := exec.CommandContext(ctx, d.guardUIExePath, "--start-hidden", "--guard-exe", d.guardExePath, "--root", d.cfg.RootDir, "--agent", d.cfg.AgentID)
	if err := cmd.Start(); err != nil {
		if d.logger != nil {
			d.logger.Printf("failed to start guard UI: %v", err)
		}
		return
	}
	d.uiAttached = true
	if d.logger != nil {
		d.logger.Printf("guard UI attached: %s", d.guardUIExePath)
	}
}

func (d *Detector) detachUIIfNeeded() {
	if !d.uiAttached {
		return
	}
	name := filepath.Base(d.guardUIExePath)
	exec.Command("taskkill", "/IM", name, "/F").Run()
	d.uiAttached = false
	if d.logger != nil {
		d.logger.Printf("guard UI detached: %s", d.guardUIExePath)
	}
}
