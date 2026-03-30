//go:build windows

package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	internalnotify "openclaw-guard-kit/internal/notify"
	"openclaw-guard-kit/internal/protocol"
	"openclaw-guard-kit/notify"
)

type ProbeStatus int

const (
	ProbeUnknown ProbeStatus = iota
	ProbeOnline
	ProbeOffline
)

type DetectorState int

const (
	DetStateUnknown DetectorState = iota
	DetStateStarting
	DetStateOnline
	DetStateTransition
	DetStateOfflineConfirmed
	DetStateOffline
)

type DetectorConfig struct {
	OpenClawPath           string
	RootDir                string
	AgentID                string
	ManualOverridePath     string
	ProbeIntervalSeconds   int
	StartupProtectSeconds  int
	OfflineGraceSeconds    int
	RestartCooldownSeconds int
	HealthyConfirmCount    int
	UnhealthyConfirmCount  int
	LogLevel               string
	GatewayHost            string
	GatewayPort            int
}

type Detector struct {
	cfg            DetectorConfig
	state          DetectorState
	logger         *log.Logger
	guardExePath   string
	guardUIExePath string
	notifier       *internalnotify.MultiNotifier

	lastProbe       ProbeStatus
	healthyCount    int
	unhealthyCount  int
	transitionSince time.Time

	guardAttached bool
	uiAttached    bool
	guardPID      int
	uiPID         int

	lastLifecycleKey  string
	lastAnomalyKey    string
	lastAnomalyAt     time.Time
	lastNotifyType    string
	lastNotifyMessage string
	lastNotifyAt      time.Time
}

type detectorStatusFile struct {
	DetectorState     string    `json:"detectorState"`
	GatewayStatus     string    `json:"gatewayStatus"`
	GuardAttached     bool      `json:"guardAttached"`
	UIAttached        bool      `json:"uiAttached"`
	GuardPID          int       `json:"guardPid,omitempty"`
	UIPID             int       `json:"uiPid,omitempty"`
	LastNotifyType    string    `json:"lastNotifyType,omitempty"`
	LastNotifyMessage string    `json:"lastNotifyMessage,omitempty"`
	LastNotifyAt      time.Time `json:"lastNotifyAt,omitempty"`
	UpdatedAt         time.Time `json:"updatedAt"`
}

type startupProtectFile struct {
	Until time.Time `json:"until"`
}

func NewDetector(logger *log.Logger, cfg DetectorConfig, guardExePath, guardUIExePath string) *Detector {
	notify.SetRootDir(cfg.RootDir)
	notify.InitCredentialsStore(cfg.RootDir)

	lifecycleNotifier := internalnotify.NewMultiNotifier(
		nil,
		notify.TelegramNotifier{},
		notify.FeishuNotifier{},
		notify.WeComNotifier{},
	)

	return &Detector{
		cfg:            cfg,
		state:          DetStateUnknown,
		logger:         logger,
		guardExePath:   guardExePath,
		guardUIExePath: guardUIExePath,
		notifier:       lifecycleNotifier,
	}
}

func (d *Detector) Run(ctx context.Context) error {
	d.state = DetStateStarting
	d.tick(ctx)

	ticker := time.NewTicker(time.Duration(d.probeIntervalSeconds()) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			d.stopUIIfNeeded(shutdownCtx)
			d.stopGuardIfNeeded(shutdownCtx)
			cancel()
			d.refreshAttachmentFlags(context.Background())
			d.writeStatusFile()
			return ctx.Err()
		case <-ticker.C:
			d.tick(ctx)
		}
	}
}

func (d *Detector) tick(ctx context.Context) {
	prevState := d.state
	prevGuardAttached := d.guardAttached
	prevUIAttached := d.uiAttached

	if d.isManualOverrideActive() || d.isOfflineFlagActive() {
		d.lastProbe = ProbeOffline
		d.healthyCount = 0
		d.unhealthyCount = 0
		d.transitionSince = time.Time{}
		d.state = DetStateOfflineConfirmed
		d.stopUIIfNeeded(ctx)
		d.stopGuardIfNeeded(ctx)
		d.refreshAttachmentFlags(ctx)
		d.state = DetStateOffline
		d.publishLifecycleEvents(ctx, prevState, prevGuardAttached, prevUIAttached)
		d.writeStatusFile()
		return
	}

	probe := d.probeOpenClaw(ctx)
	d.lastProbe = probe

	switch probe {
	case ProbeOnline:
		d.handleOnline(ctx)
	case ProbeOffline:
		d.handleOffline(ctx)
	default:
		d.handleUnknown()
	}

	d.refreshAttachmentFlags(ctx)
	d.publishLifecycleEvents(ctx, prevState, prevGuardAttached, prevUIAttached)
	d.writeStatusFile()
}

func (d *Detector) handleOnline(ctx context.Context) {
	d.unhealthyCount = 0
	d.healthyCount++
	d.transitionSince = time.Time{}

	if d.state != DetStateOnline {
		d.state = DetStateStarting
	}

	d.ensureGuardRunning(ctx)
	d.ensureUIRunning(ctx)

	if d.healthyCount >= d.healthyConfirmCount() {
		d.state = DetStateOnline
	}
}

func (d *Detector) handleOffline(ctx context.Context) {
	d.healthyCount = 0
	d.unhealthyCount++

	now := time.Now().UTC()
	if d.transitionSince.IsZero() {
		d.transitionSince = now
		d.state = DetStateTransition
		return
	}

	elapsed := time.Since(d.transitionSince)
	offlineGrace := time.Duration(d.offlineGraceSeconds()) * time.Second
	offlineStopDelay := time.Duration(d.offlineStopDelaySeconds()) * time.Second

	if elapsed >= offlineStopDelay {
		d.state = DetStateOfflineConfirmed
		d.stopUIIfNeeded(ctx)
		d.stopGuardIfNeeded(ctx)
		d.state = DetStateOffline
		return
	}

	if elapsed >= offlineGrace {
		d.state = DetStateOfflineConfirmed
		return
	}

	if elapsed >= time.Duration(d.transitionProtectSeconds())*time.Second {
		d.state = DetStateOfflineConfirmed
		return
	}

	d.state = DetStateTransition
}
func (d *Detector) handleUnknown() {
	if d.state == DetStateUnknown {
		d.state = DetStateStarting
	}
}
func (d *Detector) offlineStopDelaySeconds() int {
	return d.offlineGraceSeconds()
}

func (d *Detector) ensureGuardRunning(ctx context.Context) {
	if strings.TrimSpace(d.guardExePath) == "" {
		return
	}
	if _, err := os.Stat(d.guardExePath); err != nil {
		return
	}

	if d.guardIsRunning() {
		d.guardAttached = true
		if d.guardPID == 0 {
			d.guardPID = findProcessPIDByImage(filepath.Base(d.guardExePath))
		}
		return
	}

	args := []string{
		"watch",
		"--root", d.cfg.RootDir,
		"--agent", d.cfg.AgentID,
		"--interval", strconv.Itoa(d.probeIntervalSeconds()),
	}

	cmd := exec.CommandContext(ctx, d.guardExePath, args...)
	if err := cmd.Start(); err != nil {
		d.logger.Printf("failed to start guard: %v", err)
		d.notifyAnomaly(ctx, "guard_start_failed", fmt.Sprintf("守护程序异常：guard 启动失败（%v）", err))
		return
	}

	d.guardPID = cmd.Process.Pid

	for i := 0; i < 8; i++ {
		time.Sleep(500 * time.Millisecond)
		if d.guardIsRunning() {
			d.guardAttached = true
			d.armStartupProtectionWindow()
			return
		}
	}

	d.notifyAnomaly(ctx, "guard_not_running_after_start", "守护程序异常：guard 启动后未进入运行状态。")
}

func (d *Detector) ensureUIRunning(ctx context.Context) {
	if strings.TrimSpace(d.guardUIExePath) == "" {
		return
	}
	if _, err := os.Stat(d.guardUIExePath); err != nil {
		return
	}
	if !d.guardIsRunning() {
		return
	}

	if d.uiIsRunning() {
		d.uiAttached = true
		if d.uiPID == 0 {
			d.uiPID = findProcessPIDByImage(filepath.Base(d.guardUIExePath))
		}
		return
	}

	args := []string{
		"--start-hidden",
		"--guard-exe", d.guardExePath,
		"--root", d.cfg.RootDir,
		"--agent", d.cfg.AgentID,
	}

	cmd := exec.CommandContext(ctx, d.guardUIExePath, args...)
	if err := cmd.Start(); err != nil {
		d.logger.Printf("failed to start guard-ui: %v", err)
		d.notifyAnomaly(ctx, "guard_ui_start_failed", fmt.Sprintf("守护程序异常：guard-ui 启动失败（%v）", err))
		return
	}

	d.uiPID = cmd.Process.Pid
	time.Sleep(500 * time.Millisecond)
	d.uiAttached = d.uiIsRunning()
	if !d.uiAttached {
		d.notifyAnomaly(ctx, "guard_ui_not_running_after_start", "守护程序异常：guard-ui 启动后未进入运行状态。")
	}
}

func (d *Detector) stopGuardIfNeeded(ctx context.Context) {
	d.clearStartupProtectionWindow()

	if !d.guardIsRunning() && d.guardPID == 0 {
		d.guardAttached = false
		return
	}

	stopCtx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	_, err := d.runGuard(stopCtx, "stop")
	cancel()
	if err != nil {
		d.logger.Printf("guard stop command failed, fallback to taskkill: %v", err)
	}

	for i := 0; i < 10; i++ {
		time.Sleep(500 * time.Millisecond)
		if !d.guardIsRunning() {
			d.guardAttached = false
			d.guardPID = 0
			return
		}
	}

	if d.guardPID > 0 {
		_ = exec.CommandContext(ctx, "taskkill", "/PID", strconv.Itoa(d.guardPID), "/F").Run()
	}
	if name := filepath.Base(d.guardExePath); name != "" {
		_ = exec.CommandContext(ctx, "taskkill", "/IM", name, "/F").Run()
	}

	for i := 0; i < 6; i++ {
		time.Sleep(300 * time.Millisecond)
		if !d.guardIsRunning() {
			d.guardAttached = false
			d.guardPID = 0
			return
		}
	}

	d.notifyAnomaly(ctx, "guard_stop_failed", "守护程序异常：guard 停止失败。")
	d.guardAttached = false
	d.guardPID = 0
}

func (d *Detector) stopUIIfNeeded(ctx context.Context) {
	if !d.uiIsRunning() && d.uiPID == 0 {
		d.uiAttached = false
		return
	}

	if d.uiPID > 0 {
		_ = exec.CommandContext(ctx, "taskkill", "/PID", strconv.Itoa(d.uiPID), "/F").Run()
	}
	if name := filepath.Base(d.guardUIExePath); name != "" {
		_ = exec.CommandContext(ctx, "taskkill", "/IM", name, "/F").Run()
	}

	for i := 0; i < 6; i++ {
		time.Sleep(300 * time.Millisecond)
		if !d.uiIsRunning() {
			d.uiAttached = false
			d.uiPID = 0
			return
		}
	}

	d.notifyAnomaly(ctx, "guard_ui_stop_failed", "守护程序异常：guard-ui 停止失败。")
	d.uiAttached = false
	d.uiPID = 0
}

func (d *Detector) refreshAttachmentFlags(ctx context.Context) {
	d.guardAttached = d.guardIsRunning()
	if d.guardAttached && d.guardPID == 0 {
		d.guardPID = findProcessPIDByImage(filepath.Base(d.guardExePath))
	}
	if !d.guardAttached {
		d.guardPID = 0
		d.uiAttached = false
		d.uiPID = 0
		return
	}

	d.uiAttached = d.uiIsRunning()
	if d.uiAttached && d.uiPID == 0 {
		d.uiPID = findProcessPIDByImage(filepath.Base(d.guardUIExePath))
	}
	if !d.uiAttached {
		d.uiPID = 0
	}

	_ = ctx
}

func (d *Detector) publishLifecycleEvents(ctx context.Context, prevState DetectorState, prevGuardAttached, prevUIAttached bool) {
	switch {
	case prevState == DetStateTransition && d.state == DetStateOnline:
		d.notifyLifecycle(ctx, protocol.EventOpenClawRecovered, "OpenClaw 已恢复。")
	case (prevState == DetStateUnknown || prevState == DetStateOffline || prevState == DetStateStarting) && d.state == DetStateOnline:
		d.notifyLifecycle(ctx, protocol.EventOpenClawOnline, "OpenClaw 已上线。")
	case prevState == DetStateOnline && d.state == DetStateTransition:
		d.notifyLifecycle(ctx, protocol.EventOpenClawTransition, "OpenClaw 关闭中或重启中，正在等待确认。")
	}

	if d.state == DetStateOffline &&
		!d.guardAttached &&
		!d.uiAttached &&
		(prevState == DetStateOnline || prevState == DetStateTransition || prevState == DetStateOfflineConfirmed || prevGuardAttached || prevUIAttached) {
		d.notifyLifecycle(ctx, protocol.EventOpenClawOfflineConfirmed, "OpenClaw 已确认关闭，守护程序已退出。")
	}
}

func (d *Detector) notifyLifecycle(ctx context.Context, eventType, message string) {
	key := eventType + "|" + strings.TrimSpace(message)
	if key == d.lastLifecycleKey {
		return
	}
	d.lastLifecycleKey = key
	d.dispatchEvent(ctx, eventType, message, nil)
}

func (d *Detector) notifyAnomaly(ctx context.Context, key, message string) {
	key = strings.TrimSpace(key)
	if key == "" {
		key = strings.TrimSpace(message)
	}
	if key == d.lastAnomalyKey && time.Since(d.lastAnomalyAt) < 5*time.Minute {
		return
	}
	d.lastAnomalyKey = key
	d.lastAnomalyAt = time.Now().UTC()
	d.dispatchEvent(ctx, protocol.EventGuardAnomaly, message, map[string]string{
		"component": key,
	})
}

func (d *Detector) dispatchEvent(ctx context.Context, eventType, message string, data map[string]string) {
	if d.logger != nil {
		d.logger.Printf("detector notify | type=%s message=%s", eventType, message)
	}

	now := time.Now().UTC()
	d.lastNotifyType = strings.TrimSpace(eventType)
	d.lastNotifyMessage = strings.TrimSpace(message)
	d.lastNotifyAt = now

	if d.notifier == nil {
		return
	}

	if strings.TrimSpace(d.cfg.RootDir) != "" {
		notify.SetRootDir(d.cfg.RootDir)
		notify.InitCredentialsStore(d.cfg.RootDir)
	}

	event := protocol.Event{
		Type:    eventType,
		AgentID: strings.TrimSpace(d.cfg.AgentID),
		Message: strings.TrimSpace(message),
		At:      now,
		Data:    data,
	}

	if err := d.notifier.Notify(ctx, event); err != nil && d.logger != nil {
		d.logger.Printf("detector notify failed | type=%s error=%v", eventType, err)
	}
}
func (d *Detector) guardIsRunning() bool {
	if strings.TrimSpace(d.guardExePath) == "" {
		return false
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	out, err := d.runGuard(ctx, "status")
	if err != nil {
		lowerErr := strings.ToLower(err.Error())
		return strings.Contains(lowerErr, "guard is running")
	}

	return strings.Contains(strings.ToLower(out), "guard is running")
}

func (d *Detector) uiIsRunning() bool {
	name := filepath.Base(strings.TrimSpace(d.guardUIExePath))
	if name == "" {
		return false
	}
	return findProcessPIDByImage(name) > 0
}

func (d *Detector) runGuard(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, d.guardExePath, args...)
	output, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(output))
	if err != nil {
		if text != "" {
			return text, execError(text)
		}
		return text, err
	}
	return text, nil
}
func (d *Detector) runOpenClaw(ctx context.Context, args ...string) (string, error) {
	cmdName := strings.TrimSpace(d.cfg.OpenClawPath)
	if cmdName == "" {
		cmdName = "openclaw"
	}

	cmd := exec.CommandContext(ctx, cmdName, args...)
	output, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(output))
	if err != nil {
		if text != "" {
			return text, execError(text)
		}
		return text, err
	}
	return text, nil
}

type execError string

func (e execError) Error() string { return string(e) }

func findProcessPIDByImage(imageName string) int {
	imageName = strings.TrimSpace(imageName)
	if imageName == "" {
		return 0
	}

	out, err := exec.Command("tasklist", "/FI", "IMAGENAME eq "+imageName, "/FO", "CSV", "/NH").Output()
	if err != nil {
		return 0
	}

	reader := csv.NewReader(strings.NewReader(string(out)))
	records, err := reader.ReadAll()
	if err != nil {
		return 0
	}

	for _, record := range records {
		if len(record) < 2 {
			continue
		}
		if strings.Contains(strings.ToLower(record[0]), "no tasks are running") {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(record[0]), imageName) {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSpace(record[1]))
		if err == nil && pid > 0 {
			return pid
		}
	}

	return 0
}

func (d *Detector) probeOpenClaw(ctx context.Context) ProbeStatus {
	if d.cfg.GatewayPort > 0 {
		return d.probeGatewayTCP(ctx)
	}
	return d.probeGatewayFallback(ctx)
}

func (d *Detector) probeGatewayTCP(_ context.Context) ProbeStatus {
	host := strings.TrimSpace(d.cfg.GatewayHost)
	if host == "" {
		host = "127.0.0.1"
	}

	addr := net.JoinHostPort(host, strconv.Itoa(d.cfg.GatewayPort))
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return ProbeOffline
	}
	_ = conn.Close()
	return ProbeOnline
}

func (d *Detector) probeGatewayFallback(ctx context.Context) ProbeStatus {
	out, err := d.runOpenClaw(ctx, "gateway", "status", "--require-rpc")
	if err == nil {
		return ProbeOnline
	}

	joined := strings.ToLower(strings.TrimSpace(strings.TrimSpace(out) + " " + strings.TrimSpace(err.Error())))
	if d.logger != nil && joined != "" {
		d.logger.Printf("gateway fallback probe failed: %s", joined)
	}

	switch {
	case joined == "":
		return ProbeUnknown
	case strings.Contains(joined, "not recognized as an internal") ||
		strings.Contains(joined, "executable file not found") ||
		strings.Contains(joined, "no such file") ||
		(strings.Contains(joined, "file not found") &&
			!strings.Contains(joined, "rpc") &&
			!strings.Contains(joined, "connect") &&
			!strings.Contains(joined, "gateway")):
		return ProbeUnknown
	default:
		return ProbeOffline
	}
}

func (d *Detector) isManualOverrideActive() bool {
	if strings.TrimSpace(d.cfg.ManualOverridePath) == "" {
		return false
	}
	fi, err := os.Stat(d.cfg.ManualOverridePath)
	return err == nil && !fi.IsDir()
}

func (d *Detector) isOfflineFlagActive() bool {
	if strings.TrimSpace(d.cfg.RootDir) == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(d.cfg.RootDir, ".offline"))
	return err == nil
}

func (d *Detector) detectorStateString() string {
	switch d.state {
	case DetStateStarting:
		return "starting"
	case DetStateOnline:
		return "online"
	case DetStateTransition:
		return "transition"
	case DetStateOfflineConfirmed:
		return "offline_confirmed"
	case DetStateOffline:
		return "offline"
	default:
		return "unknown"
	}
}
func (d *Detector) logf(format string, args ...interface{}) {
	if d.logger == nil {
		return
	}
	d.logger.Printf(format, args...)
}
func (d *Detector) gatewayStateString() string {
	switch d.lastProbe {
	case ProbeOnline:
		return "online"
	case ProbeOffline:
		return "offline"
	default:
		return "unknown"
	}
}

func (d *Detector) statusFilePath() string {
	if strings.TrimSpace(d.cfg.RootDir) == "" {
		return ""
	}
	return filepath.Join(d.cfg.RootDir, ".guard-state", "detector-status.json")
}

func (d *Detector) writeStatusFile() {
	path := d.statusFilePath()
	if path == "" {
		return
	}

	_ = os.MkdirAll(filepath.Dir(path), 0o755)

	payload := detectorStatusFile{
		DetectorState:     d.detectorStateString(),
		GatewayStatus:     d.gatewayStateString(),
		GuardAttached:     d.guardAttached,
		UIAttached:        d.uiAttached,
		GuardPID:          d.guardPID,
		UIPID:             d.uiPID,
		LastNotifyType:    d.lastNotifyType,
		LastNotifyMessage: d.lastNotifyMessage,
		LastNotifyAt:      d.lastNotifyAt,
		UpdatedAt:         time.Now().UTC(),
	}

	raw, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return
	}
	_ = os.Rename(tmp, path)
}
func (d *Detector) startupProtectFilePath() string {
	if strings.TrimSpace(d.cfg.RootDir) == "" {
		return ""
	}
	return filepath.Join(d.cfg.RootDir, ".guard-state", "startup-protect.json")
}

func (d *Detector) startupProtectSeconds() int {
	if d.cfg.StartupProtectSeconds <= 0 {
		return 20
	}
	return d.cfg.StartupProtectSeconds
}

func (d *Detector) armStartupProtectionWindow() {
	path := d.startupProtectFilePath()
	if path == "" {
		return
	}

	until := time.Now().UTC().Add(time.Duration(d.startupProtectSeconds()) * time.Second)
	payload := startupProtectFile{Until: until}

	raw, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return
	}

	_ = os.MkdirAll(filepath.Dir(path), 0o755)

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return
	}
	_ = os.Rename(tmp, path)

	if d.logger != nil {
		d.logger.Printf("startup protection armed until %s", until.Format(time.RFC3339))
	}
}

func (d *Detector) clearStartupProtectionWindow() {
	path := d.startupProtectFilePath()
	if path == "" {
		return
	}
	_ = os.Remove(path)
}

func (d *Detector) readStartupProtectionUntil() time.Time {
	path := d.startupProtectFilePath()
	if path == "" {
		return time.Time{}
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return time.Time{}
	}

	var payload startupProtectFile
	if err := json.Unmarshal(raw, &payload); err != nil {
		return time.Time{}
	}
	return payload.Until.UTC()
}

func (d *Detector) probeIntervalSeconds() int {
	if d.cfg.ProbeIntervalSeconds <= 0 {
		return 5
	}
	return d.cfg.ProbeIntervalSeconds
}

func (d *Detector) healthyConfirmCount() int {
	if d.cfg.HealthyConfirmCount <= 0 {
		return 2
	}
	return d.cfg.HealthyConfirmCount
}

func (d *Detector) offlineGraceSeconds() int {
	if d.cfg.OfflineGraceSeconds <= 0 {
		return 90
	}
	return d.cfg.OfflineGraceSeconds
}

func (d *Detector) transitionProtectSeconds() int {
	if d.cfg.RestartCooldownSeconds <= 0 {
		return 45
	}
	return d.cfg.RestartCooldownSeconds
}
