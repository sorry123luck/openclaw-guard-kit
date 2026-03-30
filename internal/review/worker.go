//go:build windows

package review

import (
	"context"
	"fmt"
	"log"
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

// ReviewWorker 负责候选的后续处理流程：健康检查、稳定窗口、doctor诊断、promote/rollback等
type ReviewWorker struct {
	rootDir   string
	cfg       *ReviewConfig
	logger    *log.Logger
	notifier  *internalnotify.MultiNotifier
	backupSvc *backup.Service

	// State fields (mirroring the review status)
	candidateBatchKey      string
	candidateStatus        string
	candidateTargets       []string
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

	// Notification deduplication fields
	lastLifecycleKey  string
	lastAnomalyKey    string
	lastAnomalyAt     time.Time
	lastNotifyType    string
	lastNotifyMessage string
	lastNotifyAt      time.Time
}

// NewReviewWorker 创建一个新的审查 worker
func NewReviewWorker(logger *log.Logger, cfg *ReviewConfig, backupSvc *backup.Service, notifier *internalnotify.MultiNotifier, rootDir string) *ReviewWorker {
	return &ReviewWorker{
		rootDir:   rootDir,
		cfg:       cfg,
		logger:    logger,
		notifier:  notifier,
		backupSvc: backupSvc,
	}
}

// loadState 从 review-status.json 加载状态到 worker 字段
func (w *ReviewWorker) loadState() error {
	status, err := ReadReviewStatusFile(w.rootDir)
	if err != nil {
		return err
	}
	w.candidateBatchKey = status.CandidateBatchKey
	w.candidateStatus = status.CandidateStatus
	w.candidateTargets = status.CandidateTargets
	w.candidateObservedSince = status.CandidateSince
	w.lastHealthCheckAt = status.LastHealthCheckAt
	w.healthStatus = status.HealthStatus
	w.healthMessage = status.HealthMessage
	w.diagnosisStatus = status.DiagnosisStatus
	w.diagnosisSummary = status.DiagnosisSummary
	w.diagnosisAt = status.DiagnosisAt
	w.doctorLogPath = status.DoctorLogPath
	w.rollbackStatus = status.RollbackStatus
	w.rollbackMessage = status.RollbackMessage
	w.rollbackAt = status.RollbackAt
	return nil
}

// saveState 将 worker 当前状态保存到 review-status.json
func (w *ReviewWorker) saveState() error {
	status := &ReviewStatusFile{
		CandidateBatchKey: w.candidateBatchKey,
		CandidateStatus:   w.candidateStatus,
		CandidateTargets:  w.candidateTargets,
		CandidateSince:    w.candidateObservedSince,
		LastHealthCheckAt: w.lastHealthCheckAt,
		HealthStatus:      w.healthStatus,
		HealthMessage:     w.healthMessage,
		DiagnosisStatus:   w.diagnosisStatus,
		DiagnosisSummary:  w.diagnosisSummary,
		DiagnosisAt:       w.diagnosisAt,
		DoctorLogPath:     w.doctorLogPath,
		RollbackStatus:    w.rollbackStatus,
		RollbackMessage:   w.rollbackMessage,
		RollbackAt:        w.rollbackAt,
	}
	return WriteReviewStatusFile(w.rootDir, status)
}

// ProcessManifest 处理给定的 manifest，执行候选验证流程
// 此方法应由外部调用（例如 watch 服务）在 OpenClaw 在线时周期性调用
func (w *ReviewWorker) ProcessManifest(ctx context.Context, manifest backup.Manifest) error {
	// 加载状态
	if err := w.loadState(); err != nil {
		return err
	}

	// 执行候选验证逻辑
	w.handleCandidateVerification(ctx, manifest)

	// 保存状态
	if err := w.saveState(); err != nil {
		return err
	}
	return nil
}

// 下面是从 detector.go 搬迁过来的方法，已作必要的调整以适用于 ReviewWorker

func (w *ReviewWorker) handleCandidateVerification(ctx context.Context, manifest backup.Manifest) {
	batchKey, targets := candidateBatchInfo(manifest.CandidateTargets)
	if batchKey == "" || len(targets) == 0 {
		w.clearCandidateVerification()
		return
	}

	if batchKey != w.candidateBatchKey {
		w.candidateBatchKey = batchKey
		w.candidateTargets = targets
		w.candidateStatus = "pending"
		w.candidateObservedSince = time.Time{}
		w.lastHealthCheckAt = time.Time{}
		w.healthStatus = "pending"
		w.healthMessage = "发现待验证候选快照，等待健康检查。"
		w.clearFailureOutcome()
	}

	if !w.shouldRunHealthCheck() {
		return
	}

	now := time.Now().UTC()
	w.lastHealthCheckAt = now

	ok, message := w.verifyOpenClawHealth(ctx, targets)
	if !ok {
		w.handleCandidateFailure(ctx, batchKey, targets, message)
		return
	}

	if w.candidateObservedSince.IsZero() {
		w.candidateObservedSince = now
	}
	w.candidateStatus = "verifying"
	w.healthStatus = "ok"
	w.healthMessage = message

	if time.Since(w.candidateObservedSince) < time.Duration(w.candidateStableSeconds())*time.Second {
		return
	}

	ready, readyMessage := w.verifyCandidatePromotionReady(ctx)
	if !ready {
		w.handleCandidateFailure(ctx, batchKey, targets, readyMessage)
		return
	}

	if err := w.promoteCandidateTargets(targets); err != nil {
		w.candidateStatus = "blocked"
		w.healthStatus = "failed"
		w.healthMessage = "候选快照升格失败：" + err.Error()
		w.notifyAnomaly(ctx, "candidate_promote_failed|"+batchKey, "候选配置验证通过，但升格 trusted 失败："+err.Error())
		return
	}

	w.notifyLifecycle(ctx, protocol.EventCandidatePromoted, "候选配置已验证通过，已升格为可信恢复点。")
	w.clearFailureOutcome()
	w.candidateStatus = "promoted"
	w.healthStatus = "ok"
	w.healthMessage = "候选配置已通过验证并升格为 trusted。"
	w.candidateBatchKey = ""
	w.candidateTargets = nil
	w.candidateObservedSince = time.Time{}
}

func (w *ReviewWorker) handleCandidateFailure(ctx context.Context, batchKey string, targets []string, verifyMessage string) {
	now := time.Now().UTC()
	w.candidateStatus = "diagnosing"
	w.candidateObservedSince = time.Time{}
	w.healthStatus = "failed"
	w.healthMessage = strings.TrimSpace(verifyMessage)
	w.diagnosisStatus = "running"
	w.diagnosisSummary = "正在调用 OpenClaw doctor 进行诊断。"
	w.diagnosisAt = now
	w.rollbackStatus = "pending"
	w.rollbackMessage = "等待诊断完成后回退 trusted。"
	w.rollbackAt = time.Time{}
	// 注意：原 detector 在这里会调用 w.writeStatusFile()，但我们在 Worker 中不写 detector-status.json
	// 而是在 ProcessManifest 末尾统一保存 review-status.json，所以这里不写文件

	doctorResult, doctorErr := w.runDoctorDiagnosis(ctx)
	logPath, logErr := w.writeDoctorLog(doctorResult)
	if logErr == nil {
		w.doctorLogPath = logPath
	}

	summary := summarizeDoctorOutput(doctorResult, verifyMessage)
	w.diagnosisStatus = "done"
	w.diagnosisSummary = summary.UserMessage
	w.diagnosisAt = time.Now().UTC()
	if doctorErr != nil && strings.TrimSpace(doctorResult.Output) == "" {
		w.diagnosisStatus = "failed"
		w.diagnosisSummary = "OpenClaw doctor 运行失败，未获取到有效诊断输出：" + strings.TrimSpace(doctorErr.Error())
	}
	if logErr != nil {
		if w.diagnosisSummary == "" {
			w.diagnosisSummary = "OpenClaw doctor 已执行，但写入诊断日志失败。"
		}
		w.diagnosisSummary = strings.TrimSpace(w.diagnosisSummary) + "；诊断日志写入失败：" + strings.TrimSpace(logErr.Error())
	}

	if !shouldRollbackCandidate(summary, verifyMessage) {
		w.candidateStatus = "self_heal"
		w.rollbackStatus = "skipped"
		w.rollbackMessage = "doctor 未识别到应自动回滚的硬故障，暂不回滚，等待后续重试或人工确认。"
		w.rollbackAt = time.Now().UTC()
		w.notifyAnomaly(ctx, "candidate_self_heal|"+batchKey, "候选配置验证未通过，但 doctor 未识别到应自动回滚的硬故障，暂不回滚："+w.diagnosisSummary)
		// 注意：review worker 不应该负责确保 guard/ui 运行，那是 detector 的职责
		// 因此这里不调用 w.ensureGuardRunning/ensureUIRunning
		return
	}

	archivedPaths, archiveErr := w.archiveBadCandidateTargets(targets)
	archiveSummary := summarizeArchivedPaths(archivedPaths)
	if archiveErr != nil {
		w.logf("archive bad candidate failed: %v", archiveErr)
	}

	if err := w.rollbackCandidateTargets(targets); err != nil {
		w.rollbackStatus = "failed"
		w.rollbackMessage = "恢复 trusted 失败：" + strings.TrimSpace(err.Error())
		if archiveSummary != "" {
			w.rollbackMessage += "；异常版本已归档：" + archiveSummary
		}
		if archiveErr != nil {
			w.rollbackMessage += "；异常版本归档部分失败：" + strings.TrimSpace(archiveErr.Error())
		}
		w.rollbackAt = time.Now().UTC()
		w.candidateStatus = "failed"
		w.notifyAnomaly(ctx, "candidate_failure_recovery_failed|"+batchKey, "候选配置验证失败，doctor 已完成诊断，但恢复 trusted 失败："+strings.TrimSpace(err.Error()))
		return
	}

	w.rollbackStatus = "done"
	w.rollbackMessage = "已恢复到最近 trusted 稳定版本。"
	if archiveSummary != "" {
		w.rollbackMessage += "；异常版本已归档：" + archiveSummary
	}
	if archiveErr != nil {
		w.rollbackMessage += "；异常版本归档部分失败：" + strings.TrimSpace(archiveErr.Error())
	}
	w.rollbackAt = time.Now().UTC()
	w.notifyAnomaly(ctx, "candidate_health_failed|"+batchKey, "候选配置验证失败，已执行 doctor 诊断并恢复 trusted："+w.diagnosisSummary)
	w.clearCandidateVerification()
}

func (w *ReviewWorker) runDoctorDiagnosis(ctx context.Context) (DoctorResult, error) {
	doctorCtx, cancel := context.WithTimeout(ctx, time.Duration(w.doctorCommandTimeoutSeconds())*time.Second)
	defer cancel()

	cmdName := strings.TrimSpace(w.cfg.OpenClawPath)
	if cmdName == "" {
		cmdName = "openclaw"
	}

	args := []string{"doctor", "--non-interactive"}
	if w.cfg.DoctorDeep {
		args = append(args, "--deep")
	}

	cmd := exec.CommandContext(doctorCtx, cmdName, args...)
	if strings.TrimSpace(w.cfg.RootDir) != "" {
		cmd.Dir = w.cfg.RootDir
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

// DoctorResult 来自 detector.go 的定义，这里复制以避免循环依赖
type DoctorResult struct {
	Command  string
	Output   string
	TimedOut bool
	Err      error
}

// DoctorSummary 来自 detector.go 的定义
type DoctorSummary struct {
	Category    string
	UserMessage string
}

func summarizeDoctorOutput(result DoctorResult, verifyMessage string) DoctorSummary {
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

func (w *ReviewWorker) writeDoctorLog(result DoctorResult) (string, error) {
	if strings.TrimSpace(w.cfg.RootDir) == "" {
		return "", fmt.Errorf("root dir is empty")
	}
	logDir := filepath.Join(w.cfg.RootDir, ".guard-state", "logs")
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

func (w *ReviewWorker) archiveBadCandidateTargets(targets []string) ([]string, error) {
	manifestPath := w.manifestPath()
	if manifestPath == "" {
		return nil, fmt.Errorf("manifest path is empty")
	}
	svc := backup.NewService(nil)
	return svc.ArchiveCandidateTargetsAsBad(manifestPath, targets)
}

func (w *ReviewWorker) rollbackCandidateTargets(targets []string) error {
	manifestPath := w.manifestPath()
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

func (w *ReviewWorker) notifyLifecycle(ctx context.Context, eventType, message string) {
	key := eventType + "|" + strings.TrimSpace(message)
	if key == w.lastLifecycleKey {
		return
	}
	w.lastLifecycleKey = key
	w.dispatchEvent(ctx, eventType, message, nil)
}

func (w *ReviewWorker) notifyAnomaly(ctx context.Context, key, message string) {
	key = strings.TrimSpace(key)
	if key == "" {
		key = strings.TrimSpace(message)
	}
	if key == w.lastAnomalyKey && time.Since(w.lastAnomalyAt) < 5*time.Minute {
		return
	}
	w.lastAnomalyKey = key
	w.lastAnomalyAt = time.Now().UTC()
	w.dispatchEvent(ctx, protocol.EventGuardAnomaly, message, map[string]string{
		"component": key,
	})
}

func (w *ReviewWorker) dispatchEvent(ctx context.Context, eventType, message string, data map[string]string) {
	if w.logger != nil {
		w.logger.Printf("review notify | type=%s message=%s", eventType, message)
	}

	now := time.Now().UTC()
	w.lastNotifyType = strings.TrimSpace(eventType)
	w.lastNotifyMessage = strings.TrimSpace(message)
	w.lastNotifyAt = now

	if w.notifier == nil {
		return
	}

	if strings.TrimSpace(w.cfg.RootDir) != "" {
		notify.SetRootDir(w.cfg.RootDir)
		notify.InitCredentialsStore(w.cfg.RootDir)
	}

	event := protocol.Event{
		Type:    eventType,
		AgentID: strings.TrimSpace(w.cfg.AgentID),
		Message: strings.TrimSpace(message),
		At:      now,
		Data:    data,
	}

	if err := w.notifier.Notify(ctx, event); err != nil && w.logger != nil {
		w.logger.Printf("review notify failed | type=%s error=%v", eventType, err)
	}
}

func (w *ReviewWorker) clearFailureOutcome() {
	w.diagnosisStatus = ""
	w.diagnosisSummary = ""
	w.diagnosisAt = time.Time{}
	w.doctorLogPath = ""
	w.rollbackStatus = ""
	w.rollbackMessage = ""
	w.rollbackAt = time.Time{}
}

func (w *ReviewWorker) clearCandidateVerification() {
	w.candidateBatchKey = ""
	w.candidateTargets = nil
	w.candidateStatus = "none"
	w.candidateObservedSince = time.Time{}
	w.lastHealthCheckAt = time.Time{}
	w.healthStatus = ""
	w.healthMessage = ""
	w.clearFailureOutcome()
}

func (w *ReviewWorker) resetCandidateVerification(status, message string) {
	if strings.TrimSpace(status) == "" {
		status = "pending"
	}
	w.candidateObservedSince = time.Time{}
	if w.candidateBatchKey != "" {
		w.candidateStatus = status
	}
	if strings.TrimSpace(message) != "" && w.candidateBatchKey != "" {
		w.healthMessage = strings.TrimSpace(message)
	}
}

func (w *ReviewWorker) shouldRunHealthCheck() bool {
	if w.lastHealthCheckAt.IsZero() {
		return true
	}
	return time.Since(w.lastHealthCheckAt) >= time.Duration(w.healthCheckIntervalSeconds())*time.Second
}

func (w *ReviewWorker) verifyOpenClawHealth(ctx context.Context, targets []string) (bool, string) {
	verifyCtx, cancel := context.WithTimeout(ctx, time.Duration(w.healthCommandTimeoutSeconds())*time.Second)
	defer cancel()

	if out, err := w.runOpenClaw(verifyCtx, "gateway", "status", "--require-rpc"); err != nil {
		return false, "gateway status --require-rpc 未通过：" + commandFailureText(out, err)
	}

	targetSummary := strings.Join(targets, ",")
	if strings.TrimSpace(targetSummary) == "" {
		targetSummary = "unknown"
	}

	return true, "gateway status --require-rpc 通过；候选目标：" + targetSummary
}

func (w *ReviewWorker) verifyCandidatePromotionReady(ctx context.Context) (bool, string) {
	doctorResult, doctorErr := w.runDoctorDiagnosis(ctx)
	summary := summarizeDoctorOutput(doctorResult, "")

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

func (w *ReviewWorker) promoteCandidateTargets(targets []string) error {
	manifestPath := w.manifestPath()
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

func (w *ReviewWorker) manifestPath() string {
	if strings.TrimSpace(w.cfg.RootDir) == "" {
		return ""
	}
	return filepath.Join(w.cfg.RootDir, ".guard-state", "manifest.json")
}

func (w *ReviewWorker) runOpenClaw(ctx context.Context, args ...string) (string, error) {
	cmdName := strings.TrimSpace(w.cfg.OpenClawPath)
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

// 下面是从 detector.go 搬迁过来的辅助函数：candidateBatchInfo
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

// 下面是一些配置访问器方法，保持与 detector 中的行为一致（如果配置 <=0 则返回默认值）
func (w *ReviewWorker) candidateStableSeconds() int {
	if w.cfg.CandidateStableSeconds <= 0 {
		return 120
	}
	return w.cfg.CandidateStableSeconds
}

func (w *ReviewWorker) healthCheckIntervalSeconds() int {
	if w.cfg.HealthCheckIntervalSec <= 0 {
		return 30
	}
	return w.cfg.HealthCheckIntervalSec
}

func (w *ReviewWorker) healthCommandTimeoutSeconds() int {
	if w.cfg.HealthCommandTimeoutSec <= 0 {
		return 20
	}
	return w.cfg.HealthCommandTimeoutSec
}

func (w *ReviewWorker) doctorCommandTimeoutSeconds() int {
	if w.cfg.DoctorCommandTimeoutSec <= 0 {
		return 60
	}
	return w.cfg.DoctorCommandTimeoutSec
}

// 下面是一些日志帮助函数
func (w *ReviewWorker) logf(format string, args ...interface{}) {
	if w.logger == nil {
		return
	}
	w.logger.Printf(format, args...)
}
