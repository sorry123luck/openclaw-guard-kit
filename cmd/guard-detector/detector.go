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
	"sort"
	"strconv"
	"strings"
	"time"

	"openclaw-guard-kit/backup"
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
	OpenClawPath            string
	RootDir                 string
	AgentID                 string
	ManualOverridePath      string
	ProbeIntervalSeconds    int
	CandidateStableSeconds  int
	HealthCheckIntervalSec  int
	HealthCommandTimeoutSec int
	DoctorCommandTimeoutSec int
	DoctorDeep              bool
	StartupProtectSeconds   int
	OfflineGraceSeconds     int
	RestartCooldownSeconds  int
	HealthyConfirmCount     int
	UnhealthyConfirmCount   int
	LogLevel                string
	GatewayHost             string
	GatewayPort             int
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

	candidateBatchKey      string
	candidateTargets       []string
	candidateStatus        string
	candidateObservedSince time.Time
	lastHealthCheckAt      time.Time
	healthStatus           string
	healthMessage          string
	diagnosisStatus        string
	diagnosisSummary       string
	diagnosisAt            time.Time
	doctorLogPath          string
	rollbackStatus         string
	rollbackMessage        string
	rollbackAt             time.Time
}

type detectorStatusFile struct {
	DetectorState     string    `json:"detectorState"`
	GatewayStatus     string    `json:"gatewayStatus"`
	CandidateStatus   string    `json:"candidateStatus,omitempty"`
	CandidateTargets  []string  `json:"candidateTargets,omitempty"`
	CandidateSince    time.Time `json:"candidateSince,omitempty"`
	HealthStatus      string    `json:"healthStatus,omitempty"`
	HealthMessage     string    `json:"healthMessage,omitempty"`
	LastHealthCheckAt time.Time `json:"lastHealthCheckAt,omitempty"`
	DiagnosisStatus   string    `json:"diagnosisStatus,omitempty"`
	DiagnosisSummary  string    `json:"diagnosisSummary,omitempty"`
	DiagnosisAt       time.Time `json:"diagnosisAt,omitempty"`
	DoctorLogPath     string    `json:"doctorLogPath,omitempty"`
	RollbackStatus    string    `json:"rollbackStatus,omitempty"`
	RollbackMessage   string    `json:"rollbackMessage,omitempty"`
	RollbackAt        time.Time `json:"rollbackAt,omitempty"`
	GuardAttached     bool      `json:"guardAttached"`
	UIAttached        bool      `json:"uiAttached"`
	GuardPID          int       `json:"guardPid,omitempty"`
	UIPID             int       `json:"uiPid,omitempty"`
	LastNotifyType    string    `json:"lastNotifyType,omitempty"`
	LastNotifyMessage string    `json:"lastNotifyMessage,omitempty"`
	LastNotifyAt      time.Time `json:"lastNotifyAt,omitempty"`
	UpdatedAt         time.Time `json:"updatedAt"`
}
type DoctorResult struct {
	Command  string
	Output   string
	TimedOut bool
	Err      error
}

type DoctorSummary struct {
	Category    string
	UserMessage string
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
		d.resetCandidateVerification("pending", "监控暂停或离线标记生效，候选验证已暂停。")
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

	if d.state == DetStateOnline {
		d.finalizeStartupProtection(ctx)
		d.handleCandidateVerification(ctx)
	} else if probe != ProbeOnline {
		d.resetCandidateVerification("pending", "OpenClaw 未稳定在线，候选验证等待恢复。")
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
func (d *Detector) handleCandidateVerification(ctx context.Context) {
	manifest, err := d.loadManifest()
	if err != nil {
		d.clearCandidateVerification()
		return
	}

	batchKey, targets := candidateBatchInfo(manifest.CandidateTargets)
	if batchKey == "" || len(targets) == 0 {
		d.clearCandidateVerification()
		return
	}

	if batchKey != d.candidateBatchKey {
		d.candidateBatchKey = batchKey
		d.candidateTargets = targets
		d.candidateStatus = "pending"
		d.candidateObservedSince = time.Time{}
		d.lastHealthCheckAt = time.Time{}
		d.healthStatus = "pending"
		d.healthMessage = "发现待验证候选快照，等待健康检查。"
		d.clearFailureOutcome()
	}

	if !d.shouldRunHealthCheck() {
		return
	}

	now := time.Now().UTC()
	d.lastHealthCheckAt = now

	ok, message := d.verifyOpenClawHealth(ctx, targets)
	if !ok {
		d.handleCandidateFailure(ctx, batchKey, targets, message)
		return
	}

	if d.candidateObservedSince.IsZero() {
		d.candidateObservedSince = now
	}
	d.candidateStatus = "verifying"
	d.healthStatus = "ok"
	d.healthMessage = message

	if time.Since(d.candidateObservedSince) < time.Duration(d.candidateStableSeconds())*time.Second {
		return
	}

	ready, readyMessage := d.verifyCandidatePromotionReady(ctx)
	if !ready {
		d.handleCandidateFailure(ctx, batchKey, targets, readyMessage)
		return
	}

	if err := d.promoteCandidateTargets(targets); err != nil {
		d.candidateStatus = "blocked"
		d.healthStatus = "failed"
		d.healthMessage = "候选快照升格失败：" + err.Error()
		d.notifyAnomaly(ctx, "candidate_promote_failed|"+batchKey, "候选配置验证通过，但升格 trusted 失败："+err.Error())
		return
	}

	d.notifyLifecycle(ctx, protocol.EventCandidatePromoted, "候选配置已验证通过，已升格为可信恢复点。")
	d.clearFailureOutcome()
	d.candidateStatus = "promoted"
	d.healthStatus = "ok"
	d.healthMessage = "候选配置已通过验证并升格为 trusted。"
	d.candidateBatchKey = ""
	d.candidateTargets = nil
	d.candidateObservedSince = time.Time{}
}
func (d *Detector) handleCandidateFailure(ctx context.Context, batchKey string, targets []string, verifyMessage string) {
	now := time.Now().UTC()
	d.candidateStatus = "diagnosing"
	d.candidateObservedSince = time.Time{}
	d.healthStatus = "failed"
	d.healthMessage = strings.TrimSpace(verifyMessage)
	d.diagnosisStatus = "running"
	d.diagnosisSummary = "正在调用 OpenClaw doctor 进行诊断。"
	d.diagnosisAt = now
	d.rollbackStatus = "pending"
	d.rollbackMessage = "等待诊断完成后回退 trusted。"
	d.rollbackAt = time.Time{}
	d.writeStatusFile()

	doctorResult, doctorErr := d.runDoctorDiagnosis(ctx)
	logPath, logErr := d.writeDoctorLog(doctorResult)
	if logErr == nil {
		d.doctorLogPath = logPath
	}

	summary := d.summarizeDoctorOutput(doctorResult, verifyMessage)
	d.diagnosisStatus = "done"
	d.diagnosisSummary = summary.UserMessage
	d.diagnosisAt = time.Now().UTC()
	if doctorErr != nil && strings.TrimSpace(doctorResult.Output) == "" {
		d.diagnosisStatus = "failed"
		d.diagnosisSummary = "OpenClaw doctor 运行失败，未获取到有效诊断输出：" + strings.TrimSpace(doctorErr.Error())
	}
	if logErr != nil {
		if d.diagnosisSummary == "" {
			d.diagnosisSummary = "OpenClaw doctor 已执行，但写入诊断日志失败。"
		}
		d.diagnosisSummary = strings.TrimSpace(d.diagnosisSummary) + "；诊断日志写入失败：" + strings.TrimSpace(logErr.Error())
	}

	if !shouldRollbackCandidate(summary, verifyMessage) {
		d.candidateStatus = "self_heal"
		d.rollbackStatus = "skipped"
		d.rollbackMessage = "doctor 未识别到应自动回滚的硬故障，暂不回滚，等待后续重试或人工确认。"
		d.rollbackAt = time.Now().UTC()
		d.notifyAnomaly(ctx, "candidate_self_heal|"+batchKey, "候选配置验证未通过，但 doctor 未识别到应自动回滚的硬故障，暂不回滚："+d.diagnosisSummary)
		d.ensureGuardRunning(ctx)
		d.ensureUIRunning(ctx)
		return
	}

	archivedPaths, archiveErr := d.archiveBadCandidateTargets(targets)
	archiveSummary := summarizeArchivedPaths(archivedPaths)
	if archiveErr != nil {
		d.logf("archive bad candidate failed: %v", archiveErr)
	}

	if err := d.rollbackCandidateTargets(targets); err != nil {
		d.rollbackStatus = "failed"
		d.rollbackMessage = "恢复 trusted 失败：" + strings.TrimSpace(err.Error())
		if archiveSummary != "" {
			d.rollbackMessage += "；异常版本已归档：" + archiveSummary
		}
		if archiveErr != nil {
			d.rollbackMessage += "；异常版本归档部分失败：" + strings.TrimSpace(archiveErr.Error())
		}
		d.rollbackAt = time.Now().UTC()
		d.candidateStatus = "failed"
		d.notifyAnomaly(ctx, "candidate_failure_recovery_failed|"+batchKey, "候选配置验证失败，doctor 已完成诊断，但恢复 trusted 失败："+strings.TrimSpace(err.Error()))
		return
	}

	d.rollbackStatus = "done"
	d.rollbackMessage = "已恢复到最近 trusted 稳定版本。"
	if archiveSummary != "" {
		d.rollbackMessage += "；异常版本已归档：" + archiveSummary
	}
	if archiveErr != nil {
		d.rollbackMessage += "；异常版本归档部分失败：" + strings.TrimSpace(archiveErr.Error())
	}
	d.rollbackAt = time.Now().UTC()
	d.notifyAnomaly(ctx, "candidate_health_failed|"+batchKey, "候选配置验证失败，已执行 doctor 诊断并恢复 trusted："+d.diagnosisSummary)
	d.clearCandidateVerification()
}

func (d *Detector) runDoctorDiagnosis(ctx context.Context) (DoctorResult, error) {
	doctorCtx, cancel := context.WithTimeout(ctx, time.Duration(d.doctorCommandTimeoutSeconds())*time.Second)
	defer cancel()

	cmdName := strings.TrimSpace(d.cfg.OpenClawPath)
	if cmdName == "" {
		cmdName = "openclaw"
	}

	args := []string{"doctor", "--non-interactive"}
	if d.cfg.DoctorDeep {
		args = append(args, "--deep")
	}

	cmd := exec.CommandContext(doctorCtx, cmdName, args...)
	if strings.TrimSpace(d.cfg.RootDir) != "" {
		cmd.Dir = d.cfg.RootDir
	}
	output, err := cmd.CombinedOutput()
	result := DoctorResult{
		Command: strings.Join(append([]string{cmdName}, args...), " "),
		Output:  strings.TrimSpace(string(output)),
	}
	if doctorCtx.Err() == context.DeadlineExceeded {
		result.TimedOut = true
	}
	result.Err = err
	return result, err
}

func (d *Detector) summarizeDoctorOutput(result DoctorResult, verifyMessage string) DoctorSummary {
	raw := strings.TrimSpace(result.Output)
	lower := strings.ToLower(raw)

	if raw == "" && result.TimedOut {
		return DoctorSummary{
			Category:    "timeout",
			UserMessage: "OpenClaw doctor 诊断超时，未返回有效结果。",
		}
	}

	if containsAny(lower,
		"invalid config",
		"unrecognized key",
		"unknown key",
		"malformed type",
		"invalid type",
		"invalid value",
		"validation failed",
		"hard error",
		"plugin hard error",
		"plugin failed to load",
	) {
		return DoctorSummary{
			Category:    "rollback",
			UserMessage: "检测到 OpenClaw 配置硬故障，候选配置应视为不可用。",
		}
	}

	if containsAny(lower,
		"service not running",
		"gateway unhealthy",
		"port collision",
		"address already in use",
		"extra gateway",
		"legacy gateway",
		"auth mismatch",
		"token mismatch",
	) {
		return DoctorSummary{
			Category:    "self_heal",
			UserMessage: "检测到 OpenClaw 运行层异常，优先按守护程序自愈流程处理。",
		}
	}
	if containsAny(lower,
		"doctor warnings",
		"state integrity",
		"security",
		"skills status",
		"memory search provider is set to",
		"gateway memory probe",
		"orphan transcript",
		"plugins.allow is empty",
		"grouppolicy",
	) {
		return DoctorSummary{
			Category:    "ignored",
			UserMessage: "OpenClaw doctor 未识别到配置硬故障；当前输出主要是警告、建议或辅助能力状态。",
		}
	}
	fallback := strings.TrimSpace(verifyMessage)
	if fallback == "" {
		fallback = "候选配置验证失败，但 doctor 未识别到需要自动处理的硬故障。"
	}

	return DoctorSummary{
		Category:    "ignored",
		UserMessage: fallback,
	}
}
func containsAny(text string, needles ...string) bool {
	for _, needle := range needles {
		needle = strings.TrimSpace(strings.ToLower(needle))
		if needle != "" && strings.Contains(text, needle) {
			return true
		}
	}
	return false
}

func commandFailureText(output string, err error) string {
	text := strings.TrimSpace(output)
	if text != "" {
		return text
	}
	if err != nil {
		return strings.TrimSpace(err.Error())
	}
	return "unknown error"
}

func shouldRollbackCandidate(summary DoctorSummary, verifyMessage string) bool {
	_ = verifyMessage
	return summary.Category == "rollback"
}
func (d *Detector) writeDoctorLog(result DoctorResult) (string, error) {
	if strings.TrimSpace(d.cfg.RootDir) == "" {
		return "", fmt.Errorf("root dir is empty")
	}
	logDir := filepath.Join(d.cfg.RootDir, ".guard-state", "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(logDir, "doctor-"+time.Now().UTC().Format("20060102-150405")+".log")
	builder := strings.Builder{}
	builder.WriteString("command: ")
	builder.WriteString(result.Command)
	builder.WriteString("\r\n")
	builder.WriteString("timedOut: ")
	builder.WriteString(strconv.FormatBool(result.TimedOut))
	builder.WriteString("\r\n")
	if result.Err != nil {
		builder.WriteString("error: ")
		builder.WriteString(result.Err.Error())
		builder.WriteString("\r\n")
	}
	builder.WriteString("\r\n")
	builder.WriteString(result.Output)
	if err := os.WriteFile(path, []byte(builder.String()), 0o644); err != nil {
		return "", err
	}
	return path, nil
}
func (d *Detector) archiveBadCandidateTargets(targets []string) ([]string, error) {
	manifestPath := d.manifestPath()
	if manifestPath == "" {
		return nil, fmt.Errorf("manifest path is empty")
	}
	svc := backup.NewService(nil)
	return svc.ArchiveCandidateTargetsAsBad(manifestPath, targets)
}
func (d *Detector) rollbackCandidateTargets(targets []string) error {
	manifestPath := d.manifestPath()
	if manifestPath == "" {
		return fmt.Errorf("manifest path is empty")
	}
	svc := backup.NewService(nil)
	return svc.RollbackCandidatesToTrusted(manifestPath, targets)
}
func summarizeArchivedPaths(paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	if len(paths) == 1 {
		return paths[0]
	}
	return fmt.Sprintf("共 %d 份，最新：%s", len(paths), paths[len(paths)-1])
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
	base := d.offlineGraceSeconds()
	if !d.shouldDelayOfflineStop() {
		return base
	}

	hold := base + d.healthCommandTimeoutSeconds() + d.doctorCommandTimeoutSeconds() + 30
	if hold < 180 {
		hold = 180
	}
	return hold
}

func (d *Detector) shouldDelayOfflineStop() bool {
	if d.candidateBatchKey != "" {
		return true
	}
	if d.hasPendingCandidateTarget("openclaw") {
		return true
	}
	until := d.readStartupProtectionUntil()
	return !until.IsZero() && time.Now().UTC().Before(until)
}

func (d *Detector) hasPendingCandidateTarget(target string) bool {
	target = strings.TrimSpace(target)
	if target == "" {
		return false
	}

	manifest, err := d.loadManifest()
	if err != nil {
		return false
	}

	for _, snap := range manifest.CandidateTargets {
		if snap.State == backup.SnapshotStateBad {
			continue
		}
		if strings.TrimSpace(snap.TargetKeyOrName()) == target {
			return true
		}
	}
	return false
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
	if _, err := d.runOpenClaw(ctx, "gateway", "status", "--require-rpc"); err == nil {
		return ProbeOnline
	}
	return ProbeUnknown
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
		CandidateStatus:   d.candidateStatus,
		CandidateTargets:  append([]string(nil), d.candidateTargets...),
		CandidateSince:    d.candidateObservedSince,
		HealthStatus:      d.healthStatus,
		HealthMessage:     d.healthMessage,
		LastHealthCheckAt: d.lastHealthCheckAt,
		DiagnosisStatus:   d.diagnosisStatus,
		DiagnosisSummary:  d.diagnosisSummary,
		DiagnosisAt:       d.diagnosisAt,
		DoctorLogPath:     d.doctorLogPath,
		RollbackStatus:    d.rollbackStatus,
		RollbackMessage:   d.rollbackMessage,
		RollbackAt:        d.rollbackAt,
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

func (d *Detector) finalizeStartupProtection(ctx context.Context) {
	until := d.readStartupProtectionUntil()
	if until.IsZero() {
		return
	}
	if time.Now().UTC().Before(until) {
		return
	}

	manifestPath := d.manifestPath()
	if manifestPath == "" {
		d.clearStartupProtectionWindow()
		return
	}

	manifest, err := d.loadManifest()
	if err == nil {
		for _, snap := range manifest.CandidateTargets {
			if snap.State == backup.SnapshotStateBad {
				continue
			}
			target := strings.TrimSpace(snap.TargetKeyOrName())
			if target == "" {
				continue
			}
			if target == "openclaw" {
				if d.logger != nil {
					d.logger.Printf("startup protection finalize skipped: openclaw candidate still pending")
				}
				d.clearStartupProtectionWindow()
				return
			}
		}
	}

	svc := backup.NewService(nil)
	if _, err := svc.RefreshBaseline(manifestPath, "openclaw"); err != nil {
		if d.logger != nil {
			d.logger.Printf("startup protection finalize failed: %v", err)
		}
		d.notifyAnomaly(ctx, "startup_protect_refresh_failed", "OpenClaw 启动保护结束后刷新 trusted 失败："+strings.TrimSpace(err.Error()))
		return
	}

	d.clearStartupProtectionWindow()

	if d.logger != nil {
		d.logger.Printf("startup protection finalized, trusted baseline refreshed for openclaw")
	}
}
func (d *Detector) manifestPath() string {
	if strings.TrimSpace(d.cfg.RootDir) == "" {
		return ""
	}
	return filepath.Join(d.cfg.RootDir, ".guard-state", "manifest.json")
}

func (d *Detector) loadManifest() (backup.Manifest, error) {
	path := d.manifestPath()
	if path == "" {
		return backup.Manifest{}, os.ErrNotExist
	}
	return backup.LoadManifest(path)
}

func candidateBatchInfo(candidates []backup.Snapshot) (string, []string) {
	if len(candidates) == 0 {
		return "", nil
	}

	targets := make([]string, 0, len(candidates))
	latest := time.Time{}
	for _, snap := range candidates {
		if snap.State == backup.SnapshotStateBad {
			continue
		}

		target := strings.TrimSpace(snap.TargetKeyOrName())
		if target == "" {
			continue
		}
		targets = append(targets, target)
		if snap.CreatedAt.After(latest) {
			latest = snap.CreatedAt
		}
	}
	if len(targets) == 0 {
		return "", nil
	}
	sort.Strings(targets)
	return strings.Join(targets, ",") + "|" + latest.UTC().Format(time.RFC3339Nano), targets
}

func (d *Detector) shouldRunHealthCheck() bool {
	if d.lastHealthCheckAt.IsZero() {
		return true
	}
	return time.Since(d.lastHealthCheckAt) >= time.Duration(d.healthCheckIntervalSeconds())*time.Second
}
func (d *Detector) clearFailureOutcome() {
	d.diagnosisStatus = ""
	d.diagnosisSummary = ""
	d.diagnosisAt = time.Time{}
	d.doctorLogPath = ""
	d.rollbackStatus = ""
	d.rollbackMessage = ""
	d.rollbackAt = time.Time{}
}
func (d *Detector) clearCandidateVerification() {
	d.candidateBatchKey = ""
	d.candidateTargets = nil
	d.candidateStatus = "none"
	d.candidateObservedSince = time.Time{}
	d.lastHealthCheckAt = time.Time{}
	d.healthStatus = ""
	d.healthMessage = ""
	d.clearFailureOutcome()
}

func (d *Detector) resetCandidateVerification(status, message string) {
	if strings.TrimSpace(status) == "" {
		status = "pending"
	}
	d.candidateObservedSince = time.Time{}
	if d.candidateBatchKey != "" {
		d.candidateStatus = status
	}
	if strings.TrimSpace(message) != "" && d.candidateBatchKey != "" {
		d.healthMessage = strings.TrimSpace(message)
	}
}

func (d *Detector) verifyOpenClawHealth(ctx context.Context, targets []string) (bool, string) {
	verifyCtx, cancel := context.WithTimeout(ctx, time.Duration(d.healthCommandTimeoutSeconds())*time.Second)
	defer cancel()

	if out, err := d.runOpenClaw(verifyCtx, "gateway", "status", "--require-rpc"); err != nil {
		return false, "gateway status --require-rpc 未通过：" + commandFailureText(out, err)
	}

	targetSummary := strings.Join(targets, ",")
	if strings.TrimSpace(targetSummary) == "" {
		targetSummary = "unknown"
	}

	return true, "gateway status --require-rpc 通过；候选目标：" + targetSummary
}
func (d *Detector) verifyCandidatePromotionReady(ctx context.Context) (bool, string) {
	doctorResult, doctorErr := d.runDoctorDiagnosis(ctx)
	summary := d.summarizeDoctorOutput(doctorResult, "")

	if doctorErr != nil && strings.TrimSpace(doctorResult.Output) == "" {
		return false, "OpenClaw doctor 运行失败，未获取到有效诊断输出：" + strings.TrimSpace(doctorErr.Error())
	}

	switch summary.Category {
	case "rollback":
		return false, "OpenClaw doctor 检测到配置硬故障：" + summary.UserMessage
	case "self_heal":
		return false, "OpenClaw doctor 检测到运行层异常，先不升格 trusted：" + summary.UserMessage
	default:
		return true, "OpenClaw doctor 未识别到需要自动回滚的硬故障。"
	}
}
func (d *Detector) promoteCandidateTargets(targets []string) error {
	manifestPath := d.manifestPath()
	if manifestPath == "" {
		return fmt.Errorf("manifest path is empty")
	}
	svc := backup.NewService(nil)
	for _, target := range targets {
		if err := svc.PromoteSnapshotToHealthy(manifestPath, target); err != nil {
			return err
		}
	}
	return nil
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

func (d *Detector) candidateStableSeconds() int {
	if d.cfg.CandidateStableSeconds <= 0 {
		return 120
	}
	return d.cfg.CandidateStableSeconds
}

func (d *Detector) healthCheckIntervalSeconds() int {
	if d.cfg.HealthCheckIntervalSec <= 0 {
		return 30
	}
	return d.cfg.HealthCheckIntervalSec
}

func (d *Detector) healthCommandTimeoutSeconds() int {
	if d.cfg.HealthCommandTimeoutSec <= 0 {
		return 20
	}
	return d.cfg.HealthCommandTimeoutSec
}
func (d *Detector) doctorCommandTimeoutSeconds() int {
	if d.cfg.DoctorCommandTimeoutSec <= 0 {
		return 60
	}
	return d.cfg.DoctorCommandTimeoutSec
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
