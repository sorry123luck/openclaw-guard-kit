//go:build windows

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type GuardCLI struct {
	GuardExePath string
	RootDir      string
	AgentID      string
	ConfigPath   string
}

type StatusSnapshot struct {
	MonitoringStatus string
	DetectorStatus   string
	GatewayStatus    string
	GuardStatus      string

	AgentID      string
	ConfigPath   string
	GuardExePath string

	PipeName     string
	State        string
	WatchTargets string
	ActiveLeases string
	QueueDepth   string
	LastEvent    string
	StartedAt    string

	MonitoringPaused      bool
	DetectorNotifyType    string
	DetectorNotifyMessage string
	DetectorNotifyAt      time.Time
	CandidateStatus       string
	CandidateTargets      string
	HealthStatus          string
	HealthMessage         string
	LastHealthCheckAt     time.Time
	CandidateSince        time.Time
	DiagnosisStatus       string
	DiagnosisSummary      string
	DiagnosisAt           time.Time
	DoctorLogPath         string
	RollbackStatus        string
	RollbackMessage       string
	RollbackAt            time.Time
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
	LastNotifyType    string    `json:"lastNotifyType,omitempty"`
	LastNotifyMessage string    `json:"lastNotifyMessage,omitempty"`
	LastNotifyAt      time.Time `json:"lastNotifyAt,omitempty"`
}

func (c *GuardCLI) detectorStatusPath() string {
	if c.RootDir == "" {
		return ""
	}
	return filepath.Join(c.RootDir, ".guard-state", "detector-status.json")
}

func mapDetectorState(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "starting":
		return "启动中"
	case "online":
		return "运行中"
	case "transition":
		return "过渡中"
	case "offline_confirmed":
		return "离线确认中"
	case "offline":
		return "离线"
	default:
		return "未知"
	}
}

func mapGatewayState(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "online":
		return "在线"
	case "offline":
		return "离线"
	default:
		return "未知"
	}
}

func (c *GuardCLI) readDetectorStatus() detectorStatusFile {
	path := c.detectorStatusPath()
	if path == "" {
		return detectorStatusFile{}
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return detectorStatusFile{}
	}

	var s detectorStatusFile
	if err := json.Unmarshal(raw, &s); err != nil {
		return detectorStatusFile{}
	}

	return s
}
func NewGuardCLI(cfg UIConfig) *GuardCLI {
	return &GuardCLI{
		GuardExePath: cfg.GuardExePath,
		RootDir:      cfg.RootDir,
		AgentID:      cfg.AgentID,
		ConfigPath:   cfg.ConfigPath,
	}
}

func (c *GuardCLI) Snapshot(ctx context.Context) StatusSnapshot {
	snap := StatusSnapshot{
		MonitoringStatus: "未知",
		GuardStatus:      "未知",
		AgentID:          c.AgentID,
		ConfigPath:       c.ConfigPath,
		GuardExePath:     c.GuardExePath,
	}

	if state, err := c.MonitoringStatus(ctx); err == nil {
		snap.MonitoringStatus = state
	} else {
		snap.MonitoringStatus = "异常"
	}

	detectorFile := c.readDetectorStatus()
	if detectorFile.DetectorState != "" {
		snap.DetectorStatus = mapDetectorState(detectorFile.DetectorState)
	}
	if detectorFile.GatewayStatus != "" {
		snap.GatewayStatus = mapGatewayState(detectorFile.GatewayStatus)
	}
	snap.DetectorNotifyType = strings.TrimSpace(detectorFile.LastNotifyType)
	snap.DetectorNotifyMessage = strings.TrimSpace(detectorFile.LastNotifyMessage)
	snap.DetectorNotifyAt = detectorFile.LastNotifyAt
	snap.CandidateStatus = strings.TrimSpace(detectorFile.CandidateStatus)
	snap.CandidateTargets = strings.Join(detectorFile.CandidateTargets, ", ")
	snap.HealthStatus = strings.TrimSpace(detectorFile.HealthStatus)
	snap.HealthMessage = strings.TrimSpace(detectorFile.HealthMessage)
	snap.LastHealthCheckAt = detectorFile.LastHealthCheckAt
	snap.CandidateSince = detectorFile.CandidateSince
	snap.DiagnosisStatus = strings.TrimSpace(detectorFile.DiagnosisStatus)
	snap.DiagnosisSummary = strings.TrimSpace(detectorFile.DiagnosisSummary)
	snap.DiagnosisAt = detectorFile.DiagnosisAt
	snap.DoctorLogPath = strings.TrimSpace(detectorFile.DoctorLogPath)
	snap.RollbackStatus = strings.TrimSpace(detectorFile.RollbackStatus)
	snap.RollbackMessage = strings.TrimSpace(detectorFile.RollbackMessage)
	snap.RollbackAt = detectorFile.RollbackAt
	if details, err := c.GuardStatusDetails(ctx); err == nil {
		if v := strings.TrimSpace(details["guardStatus"]); v != "" {
			snap.GuardStatus = v
		}
		if v := strings.TrimSpace(details["agentId"]); v != "" {
			snap.AgentID = v
		}
		snap.PipeName = strings.TrimSpace(details["pipe"])
		snap.State = strings.TrimSpace(details["state"])
		snap.WatchTargets = strings.TrimSpace(details["watchTargets"])
		snap.ActiveLeases = strings.TrimSpace(details["activeLeases"])
		snap.QueueDepth = strings.TrimSpace(details["queueDepth"])
		snap.LastEvent = strings.TrimSpace(details["lastEvent"])
		snap.StartedAt = strings.TrimSpace(details["startedAt"])
	} else {
		snap.GuardStatus = "异常"
	}

	snap.MonitoringPaused = c.IsMonitoringPaused()
	if snap.MonitoringPaused {
		snap.GuardStatus = "已暂停"
		snap.State = "paused"
	}

	return snap
}

func (c *GuardCLI) GuardStatus(ctx context.Context) (string, error) {
	details, err := c.GuardStatusDetails(ctx)
	if err != nil {
		return "", err
	}
	if v := strings.TrimSpace(details["guardStatus"]); v != "" {
		return v, nil
	}
	return "未知", nil
}

func (c *GuardCLI) GuardStatusDetails(ctx context.Context) (map[string]string, error) {
	out, err := c.run(ctx, "status")
	if err != nil {
		lowerErr := strings.ToLower(err.Error())
		if strings.Contains(lowerErr, "guard is not running") {
			return map[string]string{
				"guardStatus": "未运行",
			}, nil
		}
		return nil, err
	}

	details := map[string]string{}
	lower := strings.ToLower(out)

	if strings.Contains(lower, "guard is running") {
		details["guardStatus"] = "运行中"
	} else if strings.Contains(lower, "guard is not running") {
		details["guardStatus"] = "未运行"
	} else {
		details["guardStatus"] = strings.TrimSpace(out)
	}

	lines := strings.FieldsFunc(out, func(r rune) bool { return r == '\r' || r == '\n' })
	for _, line := range lines {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "pipe:"):
			details["pipe"] = strings.TrimSpace(strings.TrimPrefix(line, "pipe:"))
		case strings.HasPrefix(line, "state:"):
			details["state"] = strings.TrimSpace(strings.TrimPrefix(line, "state:"))
		case strings.HasPrefix(line, "agent:"):
			details["agentId"] = strings.TrimSpace(strings.TrimPrefix(line, "agent:"))
		case strings.HasPrefix(line, "watchTargets:"):
			details["watchTargets"] = strings.TrimSpace(strings.TrimPrefix(line, "watchTargets:"))
		case strings.HasPrefix(line, "activeLeases:"):
			details["activeLeases"] = strings.TrimSpace(strings.TrimPrefix(line, "activeLeases:"))
		case strings.HasPrefix(line, "queueDepth:"):
			details["queueDepth"] = strings.TrimSpace(strings.TrimPrefix(line, "queueDepth:"))
		case strings.HasPrefix(line, "lastEvent:"):
			details["lastEvent"] = strings.TrimSpace(strings.TrimPrefix(line, "lastEvent:"))
		case strings.HasPrefix(line, "startedAt:"):
			details["startedAt"] = strings.TrimSpace(strings.TrimPrefix(line, "startedAt:"))
		}
	}

	return details, nil
}
func (c *GuardCLI) monitoringArgs(subcommand string) []string {
	args := []string{subcommand}

	if strings.TrimSpace(c.ConfigPath) != "" {
		args = append(args, "--config", c.ConfigPath)
	}
	if strings.TrimSpace(c.RootDir) != "" {
		args = append(args, "--root", c.RootDir)
	}
	if strings.TrimSpace(c.AgentID) != "" {
		args = append(args, "--agent", c.AgentID)
	}

	return args
}

func (c *GuardCLI) MonitoringStatus(ctx context.Context) (string, error) {
	out, err := c.run(ctx, c.monitoringArgs("monitoring-status")...)
	if err != nil {
		return "", err
	}

	lower := strings.ToLower(out)
	switch {
	case strings.Contains(lower, "monitoring: paused"):
		return "已暂停", nil
	case strings.Contains(lower, "monitoring: active"):
		return "监控中", nil
	default:
		return strings.TrimSpace(out), nil
	}
}
func (c *GuardCLI) OpenLogsDir() string {
	// 优先尊重配置文件中的日志路径
	if strings.TrimSpace(c.ConfigPath) != "" {
		if logDir := extractLogDirFromConfig(c.ConfigPath); logDir != "" {
			// 如果配置的是日志文件，取其所在目录
			if info, err := os.Stat(logDir); err == nil && !info.IsDir() {
				logDir = filepath.Dir(logDir)
			}
			// 如果目录存在，直接用它
			if info, err := os.Stat(logDir); err == nil && info.IsDir() {
				return logDir
			}
		}
	}

	// 回退到 guard 默认日志目录
	return filepath.Join(c.RootDir, ".guard-state", "logs")
}

// extractLogDirFromConfig 从配置文件中读取 logFile 字段
func extractLogDirFromConfig(configPath string) string {
	raw, err := os.ReadFile(configPath)
	if err != nil {
		return ""
	}

	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		return ""
	}

	v, ok := root["logFile"]
	if !ok || v == nil {
		return ""
	}

	logFile, ok := v.(string)
	if !ok {
		return ""
	}

	return strings.TrimSpace(logFile)
}

func (c *GuardCLI) OpenConfigDir() string {
	return filepath.Join(c.RootDir, ".guard-state")
}
func (c *GuardCLI) monitoringPausePath() string {
	return filepath.Join(c.RootDir, ".guard-state", "monitor.paused")
}

func (c *GuardCLI) IsMonitoringPaused() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	out, err := c.run(ctx, c.monitoringArgs("monitoring-status")...)
	if err != nil {
		return false
	}

	lower := strings.ToLower(strings.TrimSpace(out))
	return strings.Contains(lower, "monitoring: paused")
}

func (c *GuardCLI) PauseMonitoring(ctx context.Context) error {
	_, err := c.run(ctx, c.monitoringArgs("pause-monitoring")...)
	return err
}

func (c *GuardCLI) ResumeMonitoring(ctx context.Context) error {
	_, err := c.run(ctx, c.monitoringArgs("resume-monitoring")...)
	return err
}
func (c *GuardCLI) CandidateStatus(ctx context.Context) (string, error) {
	return c.run(ctx, "candidate-status", "--root", c.RootDir)
}

func (c *GuardCLI) PromoteCandidate(ctx context.Context, target string) error {
	_, err := c.run(ctx, "promote-candidate", "--root", c.RootDir, "--target", strings.TrimSpace(target))
	return err
}

func (c *GuardCLI) DiscardCandidate(ctx context.Context, target string) error {
	_, err := c.run(ctx, "discard-candidate", "--root", c.RootDir, "--target", strings.TrimSpace(target))
	return err
}
func (c *GuardCLI) TestGuardWrite(ctx context.Context) (string, error) {
	requestID := fmt.Sprintf("ui-test-%d", time.Now().Unix())
	requestOut, err := c.run(
		ctx,
		"request-write",
		"--agent", c.AgentID,
		"--target-key", "openclaw",
		"--kind", "openclaw",
		"--client", "guard-ui",
		"--request", requestID,
		"--lease", "30",
	)
	if err != nil {
		return "", err
	}

	leaseID := extractField(requestOut, "leaseId:")
	if leaseID == "" {
		return requestOut, fmt.Errorf("request-write succeeded but leaseId was not found in output")
	}

	completeOut, err := c.run(
		ctx,
		"complete-write",
		"--lease-id", leaseID,
		"--target-key", "openclaw",
		"--kind", "openclaw",
		"--client", "guard-ui",
		"--request", requestID,
	)
	if err != nil {
		return requestOut + "\r\n\r\n" + completeOut, err
	}

	return strings.TrimSpace(requestOut + "\r\n\r\n" + completeOut), nil
}

// CompleteTelegramBinding 完成 Telegram 绑定（软件通知绑定，不绑 Agent）
// 返回完整输出（含 confirmationSent 字段），UI 根据此字段判断确认消息是否发送成功
func (c *GuardCLI) CompleteTelegramBinding(ctx context.Context, accountID, senderID, displayName, code string) (string, error) {
	out, err := c.run(
		ctx,
		"complete-telegram-binding",
		"--root", c.RootDir,
		"--account-id", accountID,
		"--sender-id", senderID,
		"--display-name", displayName,
		"--code", code,
	)
	return out, err
}

// SaveTelegramCredentials 保存 Telegram 凭证
func (c *GuardCLI) SaveTelegramCredentials(ctx context.Context, token string) error {
	_, err := c.run(
		ctx,
		"save-telegram-credentials",
		"--root", c.RootDir,
		"--token", token,
	)
	return err
}

// UnbindTelegram 解除 Telegram 绑定
func (c *GuardCLI) UnbindTelegram(ctx context.Context) error {
	_, err := c.run(
		ctx,
		"unbind-telegram",
		"--root", c.RootDir,
	)
	return err
}
func (c *GuardCLI) SaveFeishuCredentials(ctx context.Context, appID, appSecret string) error {
	_, err := c.run(
		ctx,
		"save-feishu-credentials",
		"--root", c.RootDir,
		"--account-id", appID,
		"--app-secret", appSecret,
	)
	return err
}

func (c *GuardCLI) CompleteFeishuBinding(ctx context.Context, accountID, senderID, displayName, code string) (string, error) {
	out, err := c.run(
		ctx,
		"complete-feishu-binding",
		"--root", c.RootDir,
		"--account-id", accountID,
		"--sender-id", senderID,
		"--display-name", displayName,
		"--code", code,
	)
	return out, err
}

func (c *GuardCLI) UnbindFeishu(ctx context.Context) error {
	_, err := c.run(
		ctx,
		"unbind-feishu",
		"--root", c.RootDir,
	)
	return err
}

func (c *GuardCLI) TestFeishuMessage(ctx context.Context, message string) (string, error) {
	args := []string{
		"test-feishu-message",
		"--root", c.RootDir,
	}
	if strings.TrimSpace(message) != "" {
		args = append(args, "--message", message)
	}

	out, err := c.run(ctx, args...)
	return out, err
}
func (c *GuardCLI) SaveWecomCredentials(ctx context.Context, botID, secret string) error {
	_, err := c.run(
		ctx,
		"save-wecom-credentials",
		"--root", c.RootDir,
		"--bot-id", botID,
		"--secret", secret,
	)
	return err
}
func (c *GuardCLI) TestWecomConnection(ctx context.Context) (string, error) {
	out, err := c.run(
		ctx,
		"test-wecom-connection",
		"--root", c.RootDir,
	)
	return out, err
}
func (c *GuardCLI) CompleteWecomBinding(ctx context.Context, accountID, senderID, displayName, code string) (string, error) {
	out, err := c.run(
		ctx,
		"complete-wecom-binding",
		"--root", c.RootDir,
		"--account-id", accountID,
		"--sender-id", senderID,
		"--display-name", displayName,
		"--code", code,
	)
	return out, err
}

func (c *GuardCLI) UnbindWecom(ctx context.Context) error {
	_, err := c.run(
		ctx,
		"unbind-wecom",
		"--root", c.RootDir,
	)
	return err
}

func (c *GuardCLI) TestWecomMessage(ctx context.Context, message string) (string, error) {
	args := []string{
		"test-wecom-message",
		"--root", c.RootDir,
	}
	if strings.TrimSpace(message) != "" {
		args = append(args, "--message", message)
	}

	out, err := c.run(ctx, args...)
	return out, err
}
func extractField(text, prefix string) string {
	lines := strings.FieldsFunc(text, func(r rune) bool { return r == '\r' || r == '\n' })
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	return ""
}

func (c *GuardCLI) run(ctx context.Context, args ...string) (string, error) {
	timeoutCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	cmd := exec.CommandContext(timeoutCtx, c.GuardExePath, args...)
	output, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(output))
	if err != nil {
		if text != "" {
			return text, fmt.Errorf("%s", text)
		}
		return text, err
	}
	return text, nil
}
