package notify

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"openclaw-guard-kit/internal/protocol"
)

type WeComNotifier struct{}

type WecomInboundMessage struct {
	BotID       string    `json:"botId"`
	UserID      string    `json:"userId"`
	DisplayName string    `json:"displayName"`
	ChatType    string    `json:"chatType"`
	MsgType     string    `json:"msgType"`
	Content     string    `json:"content"`
	Raw         string    `json:"raw"`
	ReceivedAt  time.Time `json:"receivedAt"`
}

type WecomInboundSink func(WecomInboundMessage)

var (
	wecomSinkMu       sync.Mutex
	wecomSinkSeq      int64
	wecomInboundSinks = map[int64]WecomInboundSink{}
)

func RegisterWecomInboundSink(fn WecomInboundSink) func() {
	if fn == nil {
		return func() {}
	}

	id := atomic.AddInt64(&wecomSinkSeq, 1)

	wecomSinkMu.Lock()
	wecomInboundSinks[id] = fn
	wecomSinkMu.Unlock()

	return func() {
		wecomSinkMu.Lock()
		delete(wecomInboundSinks, id)
		wecomSinkMu.Unlock()
	}
}

func publishWecomInboundMessage(msg WecomInboundMessage) {
	wecomSinkMu.Lock()
	sinks := make([]WecomInboundSink, 0, len(wecomInboundSinks))
	for _, fn := range wecomInboundSinks {
		sinks = append(sinks, fn)
	}
	wecomSinkMu.Unlock()

	for _, fn := range sinks {
		func(s WecomInboundSink) {
			defer func() {
				_ = recover()
			}()
			s(msg)
		}(fn)
	}
}

type wecomBridgeCommand struct {
	Type      string `json:"type"`
	BotID     string `json:"botId,omitempty"`
	Secret    string `json:"secret,omitempty"`
	UserID    string `json:"userId,omitempty"`
	Text      string `json:"text,omitempty"`
	RequestID string `json:"requestId,omitempty"`
}

type wecomBridgeEvent struct {
	Type        string `json:"type"`
	RequestID   string `json:"requestId,omitempty"`
	OK          bool   `json:"ok,omitempty"`
	Message     string `json:"message,omitempty"`
	Error       string `json:"error,omitempty"`
	BotID       string `json:"botId,omitempty"`
	UserID      string `json:"userId,omitempty"`
	DisplayName string `json:"displayName,omitempty"`
	ChatType    string `json:"chatType,omitempty"`
	MsgType     string `json:"msgType,omitempty"`
	Content     string `json:"content,omitempty"`
	Raw         string `json:"raw,omitempty"`
}

type wecomSendResult struct {
	OK    bool
	Error string
}

type wecomBridgeState struct {
	mu        sync.Mutex
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	botID     string
	secret    string
	running   bool
	readyCh   chan error
	doneCh    chan struct{}
	pending   map[string]chan wecomSendResult
	requestID uint64
}

var globalWecomBridge = &wecomBridgeState{
	pending: map[string]chan wecomSendResult{},
}

func VerifyWecomCredentials(botID, secret string) error {
	botID = strings.TrimSpace(botID)
	secret = strings.TrimSpace(secret)

	if botID == "" {
		return fmt.Errorf("wecom bot id 不能为空")
	}
	if secret == "" {
		return fmt.Errorf("wecom secret 不能为空")
	}
	return nil
}

func EnsureWecomBridge(botID, secret string) error {
	if err := VerifyWecomCredentials(botID, secret); err != nil {
		return err
	}

	s := globalWecomBridge

	s.mu.Lock()
	defer s.mu.Unlock()

	botID = strings.TrimSpace(botID)
	secret = strings.TrimSpace(secret)

	if s.running && s.botID == botID && s.secret == secret {
		return nil
	}

	if err := s.stopLocked(); err != nil {
		return err
	}

	if err := s.startLocked(botID, secret); err != nil {
		return err
	}

	return nil
}

func StopWecomBridge() {
	s := globalWecomBridge

	s.mu.Lock()
	defer s.mu.Unlock()

	_ = s.stopLocked()
}

func SendWecomMessage(botID, secret, userID, text string) (bool, string) {
	botID = strings.TrimSpace(botID)
	secret = strings.TrimSpace(secret)
	userID = strings.TrimSpace(userID)
	text = strings.TrimSpace(text)

	if err := VerifyWecomCredentials(botID, secret); err != nil {
		return false, err.Error()
	}
	if userID == "" {
		return false, "wecom user id 不能为空"
	}
	if text == "" {
		return false, "消息内容不能为空"
	}

	if err := EnsureWecomBridge(botID, secret); err != nil {
		return false, err.Error()
	}

	s := globalWecomBridge

	s.mu.Lock()
	if !s.running || s.stdin == nil {
		s.mu.Unlock()
		return false, "wecom bridge 未启动"
	}

	reqID := fmt.Sprintf("wecom-send-%d", atomic.AddUint64(&s.requestID, 1))
	ch := make(chan wecomSendResult, 1)
	s.pending[reqID] = ch

	cmd := wecomBridgeCommand{
		Type:      "send_text",
		UserID:    userID,
		Text:      text,
		RequestID: reqID,
	}

	data, err := json.Marshal(cmd)
	if err != nil {
		delete(s.pending, reqID)
		s.mu.Unlock()
		return false, fmt.Sprintf("构造 wecom bridge 请求失败: %v", err)
	}

	if _, err := io.WriteString(s.stdin, string(data)+"\n"); err != nil {
		delete(s.pending, reqID)
		s.mu.Unlock()
		return false, fmt.Sprintf("写入 wecom bridge 失败: %v", err)
	}
	s.mu.Unlock()

	select {
	case result := <-ch:
		if !result.OK {
			return false, strings.TrimSpace(result.Error)
		}
		return true, ""
	case <-time.After(20 * time.Second):
		s.mu.Lock()
		delete(s.pending, reqID)
		s.mu.Unlock()
		return false, "等待 wecom bridge 发送结果超时"
	}
}

func (w WeComNotifier) Notify(ctx context.Context, e protocol.Event) error {
	if isQuietEventType(e.Type) {
		return nil
	}

	_ = ctx

	botID, secret := resolveWecomCredentials()
	userID := resolveBoundWecomUserID()

	if botID == "" || secret == "" || userID == "" {
		return nil
	}

	text := buildWecomEventText(e)
	ok, errText := SendWecomMessage(botID, secret, userID, text)
	if !ok {
		return fmt.Errorf("%s", errText)
	}

	return nil
}

func buildWecomEventText(e protocol.Event) string {
	return buildChannelEventText(e)
}

func resolveWecomCredentials() (string, string) {
	botID, secret := GetWecomCredentials()

	if strings.TrimSpace(botID) == "" {
		botID = strings.TrimSpace(os.Getenv("WECOM_BOT_ID"))
	}
	if strings.TrimSpace(secret) == "" {
		secret = strings.TrimSpace(os.Getenv("WECOM_SECRET"))
	}

	return strings.TrimSpace(botID), strings.TrimSpace(secret)
}

func resolveBoundWecomUserID() string {
	store := getStore()
	if store == nil {
		return strings.TrimSpace(os.Getenv("WECOM_USER_ID"))
	}

	bindings := store.ListBindings()
	for _, binding := range bindings {
		if !strings.EqualFold(strings.TrimSpace(binding.Channel), "wecom") {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(binding.Status), BindingStatusBound) {
			continue
		}
		if !binding.NotifyEnabled {
			continue
		}
		if strings.TrimSpace(binding.SenderID) == "" {
			continue
		}
		return strings.TrimSpace(binding.SenderID)
	}

	return strings.TrimSpace(os.Getenv("WECOM_USER_ID"))
}

func (s *wecomBridgeState) startLocked(botID, secret string) error {
	helperArgs, err := resolveWecomBridgeCommand()
	if err != nil {
		return err
	}

	if len(helperArgs) == 0 {
		return fmt.Errorf("未找到 wecom bridge helper")
	}

	// 构建完整的命令字符串用于调试
	cmdStr := helperArgs[0]
	for _, arg := range helperArgs[1:] {
		cmdStr += " " + arg
	}

	cmd := exec.Command(helperArgs[0], helperArgs[1:]...)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("创建 wecom bridge stdout 失败: %v", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("创建 wecom bridge stderr 失败: %v", err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("创建 wecom bridge stdin 失败: %v", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动 wecom bridge helper 失败: %v | 命令: %s | node路径: %s | 脚本路径: %s", err, cmdStr, helperArgs[0], helperArgs[1])
	}

	s.cmd = cmd
	s.stdin = stdin
	s.botID = botID
	s.secret = secret
	s.running = true
	s.readyCh = make(chan error, 1)
	readyCh := s.readyCh
	s.doneCh = make(chan struct{})

	go s.readStdout(stdout)
	go s.readStderr(stderr)
	go s.waitProcess()

	startCmd := wecomBridgeCommand{
		Type:   "start",
		BotID:  botID,
		Secret: secret,
	}

	data, err := json.Marshal(startCmd)
	if err != nil {
		_ = s.stopLocked()
		return fmt.Errorf("构造 wecom bridge start 请求失败: %v", err)
	}

	if _, err := io.WriteString(s.stdin, string(data)+"\n"); err != nil {
		_ = s.stopLocked()
		return fmt.Errorf("写入 wecom bridge start 请求失败: %v", err)
	}

	select {
	case readyErr := <-readyCh:
		if readyErr != nil {
			_ = s.stopLocked()
			return fmt.Errorf("wecom bridge 启动失败: %v | 命令: %s", readyErr, cmdStr)
		}
		return nil
	case <-time.After(15 * time.Second):
		_ = s.stopLocked()
		return fmt.Errorf("等待 wecom bridge ready 超时 (15秒) | 命令: %s | node路径: %s | 脚本路径: %s", cmdStr, helperArgs[0], helperArgs[1])
	}
}

func (s *wecomBridgeState) stopLocked() error {
	if !s.running {
		return nil
	}

	if s.stdin != nil {
		stopCmd := wecomBridgeCommand{Type: "stop"}
		if data, err := json.Marshal(stopCmd); err == nil {
			_, _ = io.WriteString(s.stdin, string(data)+"\n")
		}
		_ = s.stdin.Close()
		s.stdin = nil
	}

	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}

	for reqID, ch := range s.pending {
		select {
		case ch <- wecomSendResult{OK: false, Error: "wecom bridge 已停止"}:
		default:
		}
		delete(s.pending, reqID)
	}

	s.cmd = nil
	s.botID = ""
	s.secret = ""
	s.running = false
	s.readyCh = nil
	s.doneCh = nil

	return nil
}

func (s *wecomBridgeState) readStdout(r io.Reader) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var evt wecomBridgeEvent
		if err := json.Unmarshal([]byte(line), &evt); err != nil {
			continue
		}

		s.handleEvent(evt)
	}

	if err := scanner.Err(); err != nil {
		s.signalReady(fmt.Errorf("读取 wecom bridge 输出失败: %v", err))
	}
}

func (s *wecomBridgeState) readStderr(r io.Reader) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		s.signalReady(fmt.Errorf("wecom bridge 启动失败: %s", line))
		return
	}
}

func (s *wecomBridgeState) waitProcess() {
	if s.cmd != nil {
		_ = s.cmd.Wait()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for reqID, ch := range s.pending {
		select {
		case ch <- wecomSendResult{OK: false, Error: "wecom bridge 进程已退出"}:
		default:
		}
		delete(s.pending, reqID)
	}

	s.running = false
	s.cmd = nil
	s.stdin = nil
	s.botID = ""
	s.secret = ""
	s.readyCh = nil
	s.doneCh = nil
}

func (s *wecomBridgeState) handleEvent(evt wecomBridgeEvent) {
	switch strings.TrimSpace(evt.Type) {
	case "ready":
		s.signalReady(nil)
	case "error":
		if strings.TrimSpace(evt.RequestID) != "" {
			s.finishPending(evt.RequestID, wecomSendResult{
				OK:    false,
				Error: firstNonEmpty(evt.Error, evt.Message, "wecom bridge 返回错误"),
			})
			return
		}
		s.signalReady(fmt.Errorf("%s", firstNonEmpty(evt.Error, evt.Message, "wecom bridge 返回错误")))
	case "sent":
		s.finishPending(evt.RequestID, wecomSendResult{
			OK:    evt.OK,
			Error: strings.TrimSpace(firstNonEmpty(evt.Error, evt.Message)),
		})
	case "message":
		publishWecomInboundMessage(WecomInboundMessage{
			BotID:       strings.TrimSpace(evt.BotID),
			UserID:      strings.TrimSpace(evt.UserID),
			DisplayName: strings.TrimSpace(evt.DisplayName),
			ChatType:    strings.TrimSpace(evt.ChatType),
			MsgType:     strings.TrimSpace(evt.MsgType),
			Content:     strings.TrimSpace(evt.Content),
			Raw:         strings.TrimSpace(evt.Raw),
			ReceivedAt:  time.Now(),
		})
	}
}

func (s *wecomBridgeState) signalReady(err error) {
	ch := s.readyCh
	if ch == nil {
		return
	}

	select {
	case ch <- err:
	default:
	}
}

func (s *wecomBridgeState) finishPending(reqID string, result wecomSendResult) {
	reqID = strings.TrimSpace(reqID)
	if reqID == "" {
		return
	}

	s.mu.Lock()
	ch := s.pending[reqID]
	delete(s.pending, reqID)
	s.mu.Unlock()

	if ch == nil {
		return
	}

	select {
	case ch <- result:
	default:
	}
}

func resolveWecomBridgeCommand() ([]string, error) {
	if custom := strings.TrimSpace(os.Getenv("WECOM_BRIDGE_PATH")); custom != "" {
		return buildBridgeExecArgs(custom), nil
	}

	exePath, _ := os.Executable()
	exeDir := filepath.Dir(exePath)

	candidates := []string{
		filepath.Join(exeDir, "tools", "wecom-bridge", "index.mjs"),
		filepath.Join("tools", "wecom-bridge", "index.mjs"),
	}

	for _, p := range candidates {
		if p == "" {
			continue
		}
		if _, err := os.Stat(p); err == nil {
			return buildBridgeExecArgs(p), nil
		}
	}

	return nil, fmt.Errorf("未找到 wecom bridge helper，请设置 WECOM_BRIDGE_PATH 或补充 tools/wecom-bridge/index.mjs")
}

func buildBridgeExecArgs(path string) []string {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}

	lower := strings.ToLower(path)
	if strings.HasSuffix(lower, ".mjs") || strings.HasSuffix(lower, ".js") {
		nodeBin := strings.TrimSpace(os.Getenv("WECOM_BRIDGE_NODE"))
		if nodeBin == "" {
			nodeBin = "node"
		}
		return []string{nodeBin, path}
	}

	return []string{path}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v != "" {
			return v
		}
	}
	return ""
}