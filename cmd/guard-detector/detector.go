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
	"sync"
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
	DetStateExiting
)
const (
	remoteCommandStart   = "start"
	remoteCommandRestart = "restart"

	remoteCommandFastProbeSeconds  = 30
	remoteCommandFastProbeInterval = 1 * time.Second

	remoteCommandVerifyTotalWait = 20 * time.Second
	remoteCommandVerifyStep      = 1 * time.Second
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

	telegramInboundStop func()
	feishuInboundStop   func()
	wecomInboundStop    func()

	remoteCmdMu          sync.Mutex
	lastRemoteCommandKey string
	lastRemoteCommandAt  time.Time
	fastProbeMu          sync.Mutex
	fastProbeUntil       time.Time
	shutdownRequested    bool
	shutdownMessage      string
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
func (d *Detector) beginShutdown(message string) {
	message = strings.TrimSpace(message)
	if message == "" {
		message = "Detector 退出中，已关闭 UI 和守护程序。"
	}

	d.shutdownRequested = true
	d.shutdownMessage = message
	d.state = DetStateExiting
	d.lastNotifyType = "detector.exiting"
	d.lastNotifyMessage = message
	d.lastNotifyAt = time.Now().UTC()

	if d.logger != nil {
		d.logger.Printf("%s", message)
	}
}

func (d *Detector) shutdown(ctx context.Context) error {
	d.beginShutdown("Detector 退出中，已关闭 UI 和守护程序。")
	d.writeStatusFile()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	d.stopUIIfNeeded(shutdownCtx)
	d.stopGuardIfNeeded(shutdownCtx)
	cancel()

	d.refreshAttachmentFlags(context.Background())
	d.writeStatusFile()
	return ctx.Err()
}
func (d *Detector) Run(ctx context.Context) error {
	d.registerRemoteCommandHooks()
	defer d.unregisterRemoteCommandHooks()

	d.state = DetStateStarting
	d.tick(ctx)

	ticker := time.NewTicker(time.Duration(d.probeIntervalSeconds()) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return d.shutdown(ctx)

		case <-ticker.C:
			// 关键修复：
			// Ctrl+C 时如果 ctx.Done 和 ticker 同时到达，不要再继续走一次 tick，
			// 否则可能把 detector 自己退出误发成 OpenClaw 正在关闭。
			if ctx.Err() != nil {
				return d.shutdown(ctx)
			}
			if d.shutdownRequested {
				return d.shutdown(ctx)
			}
			d.tick(ctx)
		}
	}
}

func (d *Detector) tick(ctx context.Context) {
	if d.shutdownRequested {
		d.state = DetStateExiting
		d.writeStatusFile()
		return
	}
	d.refreshRemoteCommandChannels()
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
	if d.shutdownRequested {
		return
	}
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
func (d *Detector) registerRemoteCommandHooks() {
	if d.telegramInboundStop == nil {
		d.telegramInboundStop = notify.RegisterTelegramInboundSink(func(msg notify.TelegramInboundMessage) {
			d.handleTelegramRemoteCommand(msg)
		})
	}

	if d.feishuInboundStop == nil {
		d.feishuInboundStop = notify.RegisterFeishuInboundSink(func(msg notify.FeishuInboundMessage) {
			d.handleFeishuRemoteCommand(msg)
		})
	}

	if d.wecomInboundStop == nil {
		d.wecomInboundStop = notify.RegisterWecomInboundSink(func(msg notify.WecomInboundMessage) {
			d.handleWecomRemoteCommand(msg)
		})
	}

	d.refreshRemoteCommandChannels()
}

func (d *Detector) unregisterRemoteCommandHooks() {
	if d.telegramInboundStop != nil {
		d.telegramInboundStop()
		d.telegramInboundStop = nil
	}
	if d.feishuInboundStop != nil {
		d.feishuInboundStop()
		d.feishuInboundStop = nil
	}
	if d.wecomInboundStop != nil {
		d.wecomInboundStop()
		d.wecomInboundStop = nil
	}
}

func (d *Detector) refreshRemoteCommandChannels() {
	if strings.TrimSpace(d.cfg.RootDir) != "" {
		notify.SetRootDir(d.cfg.RootDir)
		notify.InitCredentialsStore(d.cfg.RootDir)
	}

	if token := strings.TrimSpace(notify.GetTelegramToken()); token != "" {
		if err := notify.EnsureTelegramInboundPolling(token); err != nil && d.logger != nil {
			d.logger.Printf("ensure telegram inbound polling failed: %v", err)
		}
	}

	if appID, appSecret := notify.GetFeishuCredentials(); strings.TrimSpace(appID) != "" && strings.TrimSpace(appSecret) != "" {
		if err := notify.EnsureFeishuInboundListener(appID, appSecret); err != nil && d.logger != nil {
			d.logger.Printf("ensure feishu inbound listener failed: %v", err)
		}
	}

	if botID, secret := notify.GetWecomCredentials(); strings.TrimSpace(botID) != "" && strings.TrimSpace(secret) != "" {
		if err := notify.EnsureWecomBridge(botID, secret); err != nil && d.logger != nil {
			d.logger.Printf("ensure wecom bridge failed: %v", err)
		}
	}
}

func normalizeRemoteCommandText(text string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(text)), " "))
}

func parseRemoteCommandAction(text string) (string, bool) {
	switch normalizeRemoteCommandText(text) {
	case "启动openclaw", "启动 openclaw", "打开openclaw", "打开 openclaw", "openclaw gateway start":
		return remoteCommandStart, true
	case "重启openclaw", "重启 openclaw", "重新启动openclaw", "重新启动 openclaw":
		return remoteCommandRestart, true
	default:
		return "", false
	}
}

func (d *Detector) handleTelegramRemoteCommand(msg notify.TelegramInboundMessage) {
	text := strings.TrimSpace(msg.Text)
	if text == "" {
		return
	}

	action, ok := parseRemoteCommandAction(text)
	if !ok {
		return
	}

	if !d.isBoundTelegramSender(msg) {
		if d.logger != nil {
			d.logger.Printf("ignore unbound telegram remote command | bot=%s chat=%d raw=%s", msg.BotID, msg.ChatID, msg.Text)
		}
		return
	}

	key := strings.TrimSpace(msg.BotID) + "|" + strconv.FormatInt(msg.ChatID, 10) + "|" + normalizeRemoteCommandText(text)
	if d.shouldDropRemoteCommand(key) {
		return
	}

	go d.executeRemoteCommand(action, func(reply string) {
		d.replyTelegramRemoteCommand(msg, reply)
	})
}

func (d *Detector) handleFeishuRemoteCommand(msg notify.FeishuInboundMessage) {
	if !strings.EqualFold(strings.TrimSpace(msg.MsgType), "text") && strings.TrimSpace(msg.Content) == "" {
		return
	}

	action, ok := parseRemoteCommandAction(msg.Content)
	if !ok {
		return
	}

	if !d.isBoundFeishuSender(msg) {
		if d.logger != nil {
			d.logger.Printf("ignore unbound feishu remote command | app=%s openID=%s raw=%s", msg.AppID, msg.OpenID, msg.Content)
		}
		return
	}

	key := strings.TrimSpace(msg.AppID) + "|" + strings.TrimSpace(msg.OpenID) + "|" + normalizeRemoteCommandText(msg.Content)
	if d.shouldDropRemoteCommand(key) {
		return
	}

	go d.executeRemoteCommand(action, func(reply string) {
		d.replyFeishuRemoteCommand(msg, reply)
	})
}

func (d *Detector) handleWecomRemoteCommand(msg notify.WecomInboundMessage) {
	if !strings.EqualFold(strings.TrimSpace(msg.MsgType), "text") && strings.TrimSpace(msg.Content) == "" {
		return
	}

	action, ok := parseRemoteCommandAction(msg.Content)
	if !ok {
		return
	}

	if !d.isBoundWecomSender(msg) {
		if d.logger != nil {
			d.logger.Printf("ignore unbound wecom remote command | bot=%s user=%s raw=%s", msg.BotID, msg.UserID, msg.Content)
		}
		return
	}

	key := strings.TrimSpace(msg.BotID) + "|" + strings.TrimSpace(msg.UserID) + "|" + normalizeRemoteCommandText(msg.Content)
	if d.shouldDropRemoteCommand(key) {
		return
	}

	go d.executeRemoteCommand(action, func(reply string) {
		d.replyWecomRemoteCommand(msg, reply)
	})
}

func (d *Detector) shouldDropRemoteCommand(key string) bool {
	key = strings.TrimSpace(key)
	if key == "" {
		return false
	}

	d.remoteCmdMu.Lock()
	defer d.remoteCmdMu.Unlock()

	if key == d.lastRemoteCommandKey && time.Since(d.lastRemoteCommandAt) < 10*time.Second {
		return true
	}

	d.lastRemoteCommandKey = key
	d.lastRemoteCommandAt = time.Now().UTC()
	return false
}

func (d *Detector) isBoundTelegramSender(msg notify.TelegramInboundMessage) bool {
	root := strings.TrimSpace(d.cfg.RootDir)
	if root == "" {
		return false
	}

	store, err := notify.NewStore(notify.BindingsPath(root))
	if err != nil {
		if d.logger != nil {
			d.logger.Printf("load bindings store failed: %v", err)
		}
		return false
	}

	botID := strings.TrimSpace(msg.BotID)
	chatID := strconv.FormatInt(msg.ChatID, 10)
	if botID == "" || chatID == "0" {
		return false
	}

	for _, b := range store.ListBindings() {
		if !strings.EqualFold(strings.TrimSpace(b.Channel), "telegram") {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(b.Status), notify.BindingStatusBound) {
			continue
		}
		if strings.TrimSpace(b.AccountID) != botID {
			continue
		}
		if strings.TrimSpace(b.SenderID) != chatID {
			continue
		}
		if !b.RemoteCommandEnabled {
			if d.logger != nil {
				d.logger.Printf("ignore telegram remote command: remote command disabled | bot=%s chat=%s", botID, chatID)
			}
			return false
		}
		return true
	}

	return false
}

func (d *Detector) isBoundFeishuSender(msg notify.FeishuInboundMessage) bool {
	root := strings.TrimSpace(d.cfg.RootDir)
	if root == "" {
		return false
	}

	store, err := notify.NewStore(notify.BindingsPath(root))
	if err != nil {
		if d.logger != nil {
			d.logger.Printf("load bindings store failed: %v", err)
		}
		return false
	}

	appID := strings.TrimSpace(msg.AppID)
	openID := strings.TrimSpace(msg.OpenID)
	if appID == "" || openID == "" {
		return false
	}

	for _, b := range store.ListBindings() {
		if !strings.EqualFold(strings.TrimSpace(b.Channel), "feishu") {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(b.Status), notify.BindingStatusBound) {
			continue
		}
		if strings.TrimSpace(b.AccountID) != appID {
			continue
		}
		if strings.TrimSpace(b.SenderID) != openID {
			continue
		}
		if !b.RemoteCommandEnabled {
			if d.logger != nil {
				d.logger.Printf("ignore feishu remote command: remote command disabled | app=%s openID=%s", appID, openID)
			}
			return false
		}
		return true
	}

	return false
}

func (d *Detector) isBoundWecomSender(msg notify.WecomInboundMessage) bool {
	root := strings.TrimSpace(d.cfg.RootDir)
	if root == "" {
		return false
	}

	store, err := notify.NewStore(notify.BindingsPath(root))
	if err != nil {
		if d.logger != nil {
			d.logger.Printf("load bindings store failed: %v", err)
		}
		return false
	}

	botID := strings.TrimSpace(msg.BotID)
	userID := strings.TrimSpace(msg.UserID)
	if botID == "" || userID == "" {
		return false
	}

	for _, b := range store.ListBindings() {
		if !strings.EqualFold(strings.TrimSpace(b.Channel), "wecom") {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(b.Status), notify.BindingStatusBound) {
			continue
		}
		if strings.TrimSpace(b.AccountID) != botID {
			continue
		}
		if strings.TrimSpace(b.SenderID) != userID {
			continue
		}
		if !b.RemoteCommandEnabled {
			if d.logger != nil {
				d.logger.Printf("ignore wecom remote command: remote command disabled | bot=%s user=%s", botID, userID)
			}
			return false
		}
		return true
	}

	return false
}

func (d *Detector) executeRemoteCommand(action string, reply func(string)) {
	if reply == nil {
		return
	}

	probeBefore := d.probeOpenClaw(context.Background())
	d.armFastProbeWindow(remoteCommandFastProbeSeconds)

	var args []string
	var startReply string

	switch action {
	case remoteCommandRestart:
		if probeBefore == ProbeOnline {
			args = []string{"gateway", "restart"}
			startReply = "已收到远程重启命令，正在尝试重启 OpenClaw。"
		} else {
			args = []string{"gateway", "start"}
			startReply = "当前检测为离线，已按启动命令处理，正在尝试拉起 OpenClaw。"
		}
	default:
		if probeBefore == ProbeOnline {
			reply("OpenClaw 当前已经在线，无需再次启动。")
			return
		}
		args = []string{"gateway", "start"}
		startReply = "已收到远程启动命令，正在尝试启动 OpenClaw。"
	}

	reply(startReply)

	cmdCtx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	out, err := d.runOpenClaw(cmdCtx, args...)
	cancel()

	if err != nil {
		msg := ""
		if strings.TrimSpace(out) != "" {
			msg = "启动命令执行失败：" + strings.TrimSpace(out)
		} else {
			msg = fmt.Sprintf("启动命令执行失败：%v", err)
		}

		msg = msg + "。如首次冷启动失败，可再次发送“启动openclaw”重试。"

		d.dispatchEvent(context.Background(), protocol.EventGuardAnomaly, msg, map[string]string{
			"component": "openclaw_remote_start_failed",
			"action":    action,
		})

		reply(msg)
		return
	}

	if d.waitForOpenClawOnline(remoteCommandVerifyTotalWait, remoteCommandVerifyStep) {
		bg := context.Background()
		d.ensureGuardRunning(bg)
		d.ensureUIRunning(bg)
		d.refreshAttachmentFlags(bg)
		d.writeStatusFile()
		reply("OpenClaw 已检测到在线，守护与面板正在同步恢复。")
		return
	}

	msg := "启动命令已执行，但 20 秒内仍未检测到 OpenClaw 在线。若这是首次冷启动失败，可再次发送“启动openclaw”再试一次。"

	d.dispatchEvent(context.Background(), protocol.EventGuardAnomaly, msg, map[string]string{
		"component": "openclaw_remote_start_timeout",
		"action":    action,
	})

	reply(msg)
}

func (d *Detector) replyTelegramRemoteCommand(msg notify.TelegramInboundMessage, text string) {
	token := strings.TrimSpace(notify.GetTelegramToken())
	if token == "" || msg.ChatID == 0 {
		if d.logger != nil {
			d.logger.Printf("skip telegram remote reply: token or chat missing")
		}
		return
	}

	ok, errMsg := notify.SendTelegramMessage(token, msg.ChatID, strings.TrimSpace(text))
	if !ok && d.logger != nil {
		d.logger.Printf("telegram remote reply failed: %s", errMsg)
	}
}

func (d *Detector) replyFeishuRemoteCommand(msg notify.FeishuInboundMessage, text string) {
	appID, appSecret := notify.GetFeishuCredentials()
	if strings.TrimSpace(appID) == "" {
		appID = strings.TrimSpace(msg.AppID)
	}
	if strings.TrimSpace(appID) == "" || strings.TrimSpace(appSecret) == "" || strings.TrimSpace(msg.OpenID) == "" {
		if d.logger != nil {
			d.logger.Printf("skip feishu remote reply: credentials or openID missing")
		}
		return
	}

	ok, errMsg := notify.SendFeishuMessage(appID, appSecret, strings.TrimSpace(msg.OpenID), strings.TrimSpace(text))
	if !ok && d.logger != nil {
		d.logger.Printf("feishu remote reply failed: %s", errMsg)
	}
}

func (d *Detector) replyWecomRemoteCommand(msg notify.WecomInboundMessage, text string) {
	sendBotID, secret := notify.GetWecomCredentials()
	if strings.TrimSpace(sendBotID) == "" {
		sendBotID = strings.TrimSpace(msg.BotID)
	}
	if strings.TrimSpace(sendBotID) == "" || strings.TrimSpace(secret) == "" || strings.TrimSpace(msg.UserID) == "" {
		if d.logger != nil {
			d.logger.Printf("skip wecom remote reply: credentials or user missing")
		}
		return
	}

	ok, errMsg := notify.SendWecomMessage(sendBotID, secret, strings.TrimSpace(msg.UserID), strings.TrimSpace(text))
	if !ok && d.logger != nil {
		d.logger.Printf("wecom remote reply failed: %s", errMsg)
	}
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
	case DetStateExiting:
		return "exiting"
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
func (d *Detector) currentProbeInterval() time.Duration {
	base := time.Duration(d.probeIntervalSeconds()) * time.Second

	d.fastProbeMu.Lock()
	until := d.fastProbeUntil
	d.fastProbeMu.Unlock()

	if !until.IsZero() && time.Now().UTC().Before(until) {
		return remoteCommandFastProbeInterval
	}

	return base
}

func (d *Detector) armFastProbeWindow(seconds int) {
	if seconds <= 0 {
		seconds = remoteCommandFastProbeSeconds
	}

	d.fastProbeMu.Lock()
	d.fastProbeUntil = time.Now().UTC().Add(time.Duration(seconds) * time.Second)
	d.fastProbeMu.Unlock()

	if d.logger != nil {
		d.logger.Printf("fast probe window armed for %d seconds", seconds)
	}
}

func (d *Detector) waitForOpenClawOnline(totalWait, step time.Duration) bool {
	if totalWait <= 0 {
		totalWait = remoteCommandVerifyTotalWait
	}
	if step <= 0 {
		step = remoteCommandVerifyStep
	}

	deadline := time.Now().Add(totalWait)
	for time.Now().Before(deadline) {
		if d.probeOpenClaw(context.Background()) == ProbeOnline {
			return true
		}
		time.Sleep(step)
	}

	return d.probeOpenClaw(context.Background()) == ProbeOnline
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
