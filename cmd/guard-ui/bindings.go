//go:build windows

package main

import (
	"fmt"
	"strings"
	"time"

	"openclaw-guard-kit/notify"
)

// loadBindingsSummary reads the bindings from notify.Store and returns a summary.
func loadBindingsSummary(rootDir string) string {
	if rootDir == "" {
		return "当前未找到任何绑定。"
	}

	// 使用 notify.Store 读取 bindings.json
	bindingsPath := notify.BindingsPath(rootDir)
	store, err := notify.NewStore(bindingsPath)
	if err != nil {
		// 文件不存在或读取失败，显示友好文案
		return "当前未找到任何绑定。"
	}

	bindings := store.ListBindings()
	if len(bindings) == 0 {
		return "当前未找到任何绑定。"
	}

	lines := make([]string, 0, len(bindings)+2)
	lines = append(lines, fmt.Sprintf("共 %d 条绑定：", len(bindings)), "")

	for i, b := range bindings {
		status := b.Status
		displayName := b.DisplayName
		if displayName == "" {
			displayName = "-"
		}

		// 显示最后测试结果
		testResult := "-"
		if b.LastTestAt.IsZero() {
			testResult = "待测试"
		} else {
			testResult = b.ConnectionStatus
			if b.LastTestResult != "" {
				testResult = fmt.Sprintf("%s (%s)", b.ConnectionStatus, b.LastTestResult)
			}
		}

		lines = append(lines,
			fmt.Sprintf("[%d] %s | %s | %s | 测试: %s",
				i+1,
				b.Channel,
				status,
				displayName,
				testResult,
			),
		)
	}

	return strings.Join(lines, "\r\n")
}

// loadPendingSummary reads the pending bindings from notify.Store and returns a summary.
func loadPendingSummary(rootDir string) string {
	if rootDir == "" {
		return "暂无待绑定会话。"
	}

	// 使用 notify.Store 读取 pending bindings
	bindingsPath := notify.BindingsPath(rootDir)
	store, err := notify.NewStore(bindingsPath)
	if err != nil {
		// 文件不存在或读取失败，显示友好文案
		return "暂无待绑定会话。"
	}

	pendingList := store.ListPendingBindings()
	if len(pendingList) == 0 {
		return "暂无待绑定会话。"
	}

	lines := []string{fmt.Sprintf("共 %d 条待绑定会话：", len(pendingList))}
	for i, pb := range pendingList {
		expired := ""
		if !pb.ExpiresAt.IsZero() && pb.ExpiresAt.Before(time.Now()) {
			expired = " (已过期)"
		}
		lines = append(lines, fmt.Sprintf("[%d] %s | %s | %s | 配对码: %s%s",
			i+1,
			pb.Channel,
			pb.DisplayName,
			pb.AccountID,
			pb.PairingCode,
			expired,
		))
	}
	return strings.Join(lines, "\r\n")
}

// loadEventsSummary returns a summary of recent events from the snapshot.
func loadEventsSummary(snapshot StatusSnapshot) string {
	lines := []string{
		"最近事件（当前先显示状态摘要）",
		"",
		fmt.Sprintf("守护状态：%s", fallbackText(snapshot.GuardStatus, "未知")),
		fmt.Sprintf("最后事件：%s", fallbackText(snapshot.LastEvent, "-")),
		fmt.Sprintf("启动时间：%s", fallbackText(snapshot.StartedAt, "-")),
		fmt.Sprintf("候选状态：%s", fallbackText(snapshot.CandidateStatus, "none")),
		fmt.Sprintf("候选目标：%s", fallbackText(snapshot.CandidateTargets, "-")),
		fmt.Sprintf("健康检查：%s", fallbackText(snapshot.HealthStatus, "-")),
		fmt.Sprintf("健康说明：%s", fallbackText(snapshot.HealthMessage, "-")),
		fmt.Sprintf("诊断状态：%s", fallbackText(snapshot.DiagnosisStatus, "-")),
		fmt.Sprintf("诊断结果：%s", fallbackText(snapshot.DiagnosisSummary, "-")),
		fmt.Sprintf("回退状态：%s", fallbackText(snapshot.RollbackStatus, "-")),
		fmt.Sprintf("回退说明：%s", fallbackText(snapshot.RollbackMessage, "-")),
	}

	if !snapshot.CandidateSince.IsZero() {
		lines = append(lines, fmt.Sprintf("候选开始：%s", snapshot.CandidateSince.Format(time.RFC3339)))
	}
	if !snapshot.LastHealthCheckAt.IsZero() {
		lines = append(lines, fmt.Sprintf("最近健康检查：%s", snapshot.LastHealthCheckAt.Format(time.RFC3339)))
	}
	if !snapshot.DiagnosisAt.IsZero() {
		lines = append(lines, fmt.Sprintf("最近诊断：%s", snapshot.DiagnosisAt.Format(time.RFC3339)))
	}
	if !snapshot.RollbackAt.IsZero() {
		lines = append(lines, fmt.Sprintf("最近回退：%s", snapshot.RollbackAt.Format(time.RFC3339)))
	}
	if strings.TrimSpace(snapshot.DoctorLogPath) != "" {
		lines = append(lines, fmt.Sprintf("诊断日志：%s", snapshot.DoctorLogPath))
	}

	if strings.TrimSpace(snapshot.WatchTargets) != "" {
		lines = append(lines, fmt.Sprintf("监控目标：%s", snapshot.WatchTargets))
	}

	lines = append(lines,
		"",
		"说明：此处展示最近的系统事件摘要。详细事件列表将在后续版本中提供。",
	)

	return strings.Join(lines, "\r\n")
}

// loadSystemSummary returns a summary of the system from the config and snapshot.
func loadSystemSummary(cfg UIConfig, snapshot StatusSnapshot) string {
	lines := []string{
		"系统页实时摘要",
		"",
		fmt.Sprintf("guard.exe：%s", cfg.GuardExePath),
		fmt.Sprintf("当前 Agent：%s", fallbackText(snapshot.AgentID, cfg.AgentID)),
		fmt.Sprintf("配置文件：%s", cfg.ConfigPath),
		fmt.Sprintf("根目录：%s", cfg.RootDir),
		fmt.Sprintf("Pipe：%s", fallbackText(snapshot.PipeName, "-")),
		fmt.Sprintf("状态：%s", fallbackText(snapshot.State, "-")),
		fmt.Sprintf("守护状态：%s", fallbackText(snapshot.GuardStatus, "-")),
		fmt.Sprintf("监控目标：%s", fallbackText(snapshot.WatchTargets, "-")),
		fmt.Sprintf("最后事件：%s", fallbackText(snapshot.LastEvent, "-")),
		fmt.Sprintf("启动时间：%s", fallbackText(snapshot.StartedAt, "-")),
		fmt.Sprintf("候选状态：%s", fallbackText(snapshot.CandidateStatus, "none")),
		fmt.Sprintf("候选目标：%s", fallbackText(snapshot.CandidateTargets, "-")),
		fmt.Sprintf("健康检查：%s", fallbackText(snapshot.HealthStatus, "-")),
		fmt.Sprintf("健康说明：%s", fallbackText(snapshot.HealthMessage, "-")),
		fmt.Sprintf("诊断状态：%s", fallbackText(snapshot.DiagnosisStatus, "-")),
		fmt.Sprintf("诊断结果：%s", fallbackText(snapshot.DiagnosisSummary, "-")),
		fmt.Sprintf("回退状态：%s", fallbackText(snapshot.RollbackStatus, "-")),
		fmt.Sprintf("回退说明：%s", fallbackText(snapshot.RollbackMessage, "-")),
	}

	if !snapshot.CandidateSince.IsZero() {
		lines = append(lines, fmt.Sprintf("候选开始：%s", snapshot.CandidateSince.Format(time.RFC3339)))
	}
	if !snapshot.LastHealthCheckAt.IsZero() {
		lines = append(lines, fmt.Sprintf("最近健康检查：%s", snapshot.LastHealthCheckAt.Format(time.RFC3339)))
	}
	if !snapshot.DiagnosisAt.IsZero() {
		lines = append(lines, fmt.Sprintf("最近诊断：%s", snapshot.DiagnosisAt.Format(time.RFC3339)))
	}
	if !snapshot.RollbackAt.IsZero() {
		lines = append(lines, fmt.Sprintf("最近回退：%s", snapshot.RollbackAt.Format(time.RFC3339)))
	}
	if strings.TrimSpace(snapshot.DoctorLogPath) != "" {
		lines = append(lines, fmt.Sprintf("诊断日志：%s", snapshot.DoctorLogPath))
	}

	return strings.Join(lines, "\r\n")
}
