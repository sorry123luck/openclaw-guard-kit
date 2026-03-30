package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"openclaw-guard-kit/internal/protocol"

	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"
)

const (
	feishuTenantTokenURL = "https://open.feishu.cn/open-apis/auth/v3/tenant_access_token/internal/"
	feishuSendMessageURL = "https://open.feishu.cn/open-apis/im/v1/messages?receive_id_type=open_id"
)

type FeishuNotifier struct{}
type FeishuInboundMessage struct {
	AppID       string
	OpenID      string
	DisplayName string
	MsgType     string
	Content     string
	ReceivedAt  time.Time
}

type FeishuInboundSink func(FeishuInboundMessage)

var (
	feishuSinkMu       sync.Mutex
	feishuSinkSeq      int64
	feishuInboundSinks = map[int64]FeishuInboundSink{}
)

type feishuListenerState struct {
	mu        sync.Mutex
	appID     string
	appSecret string
	running   bool
	cancel    context.CancelFunc
}

var globalFeishuListener = &feishuListenerState{}

func RegisterFeishuInboundSink(fn FeishuInboundSink) func() {
	if fn == nil {
		return func() {}
	}

	id := atomic.AddInt64(&feishuSinkSeq, 1)

	feishuSinkMu.Lock()
	feishuInboundSinks[id] = fn
	feishuSinkMu.Unlock()

	return func() {
		feishuSinkMu.Lock()
		delete(feishuInboundSinks, id)
		feishuSinkMu.Unlock()
	}
}

func publishFeishuInboundMessage(msg FeishuInboundMessage) {
	feishuSinkMu.Lock()
	sinks := make([]FeishuInboundSink, 0, len(feishuInboundSinks))
	for _, fn := range feishuInboundSinks {
		sinks = append(sinks, fn)
	}
	feishuSinkMu.Unlock()

	for _, fn := range sinks {
		func(s FeishuInboundSink) {
			defer func() { _ = recover() }()
			s(msg)
		}(fn)
	}
}

func EnsureFeishuInboundListener(appID, appSecret string) error {
	appID = strings.TrimSpace(appID)
	appSecret = strings.TrimSpace(appSecret)

	if appID == "" {
		return fmt.Errorf("feishu app id 不能为空")
	}
	if appSecret == "" {
		return fmt.Errorf("feishu app secret 不能为空")
	}

	s := globalFeishuListener
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running && s.appID == appID && s.appSecret == appSecret {
		return nil
	}

	s.stopLocked()
	s.startLocked(appID, appSecret)
	return nil
}

func StopFeishuInboundListener() {
	s := globalFeishuListener
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopLocked()
}

func (s *feishuListenerState) startLocked(appID, appSecret string) {
	ctx, cancel := context.WithCancel(context.Background())
	s.appID = appID
	s.appSecret = appSecret
	s.running = true
	s.cancel = cancel

	go s.run(ctx, appID, appSecret)
}

func (s *feishuListenerState) stopLocked() {
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	s.running = false
	s.appID = ""
	s.appSecret = ""
}

func (s *feishuListenerState) run(ctx context.Context, appID, appSecret string) {
	eventHandler := dispatcher.NewEventDispatcher("", "").
		OnP2MessageReceiveV1(func(evctx context.Context, event *larkim.P2MessageReceiveV1) error {
			handleFeishuInboundEvent(evctx, appID, appSecret, event)
			return nil
		})

	client := larkws.NewClient(
		appID,
		appSecret,
		larkws.WithEventHandler(eventHandler),
		larkws.WithAutoReconnect(true),
		larkws.WithLogLevel(larkcore.LogLevelError),
	)

	_ = client.Start(ctx)

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running && s.appID == appID && s.appSecret == appSecret {
		s.running = false
		s.cancel = nil
	}
}

func handleFeishuInboundEvent(ctx context.Context, appID, appSecret string, event *larkim.P2MessageReceiveV1) {
	if event == nil || event.Event == nil {
		return
	}

	msg := event.Event.Message
	sender := event.Event.Sender
	if msg == nil || sender == nil || sender.SenderId == nil {
		return
	}

	msgType := derefString(msg.MessageType)
	if msgType != "" && !strings.EqualFold(msgType, "text") {
		return
	}

	text := extractFeishuTextMessage(derefString(msg.Content))
	if strings.TrimSpace(text) == "" {
		return
	}

	openID := ""
	if sender.SenderId.OpenId != nil {
		openID = strings.TrimSpace(*sender.SenderId.OpenId)
	}
	if openID == "" {
		return
	}

	displayName := openID
	if name, err := getFeishuUserName(ctx, appID, appSecret, openID); err == nil && strings.TrimSpace(name) != "" {
		displayName = strings.TrimSpace(name)
	}

	publishFeishuInboundMessage(FeishuInboundMessage{
		AppID:       strings.TrimSpace(appID),
		OpenID:      openID,
		DisplayName: displayName,
		MsgType:     "text",
		Content:     text,
		ReceivedAt:  time.Now(),
	})
}

type feishuTenantTokenRequest struct {
	AppID     string `json:"app_id"`
	AppSecret string `json:"app_secret"`
}

type feishuTenantTokenResponse struct {
	Code              int    `json:"code"`
	Msg               string `json:"msg"`
	TenantAccessToken string `json:"tenant_access_token"`
	Expire            int64  `json:"expire"`
}

type feishuSendMessageRequest struct {
	ReceiveID string `json:"receive_id"`
	MsgType   string `json:"msg_type"`
	Content   string `json:"content"`
}

type feishuSendMessageResponse struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		MessageID string `json:"message_id"`
	} `json:"data"`
}

// VerifyFeishuCredentials 校验飞书 App ID / App Secret 是否有效。
// 本质上就是尝试获取 tenant_access_token。
func VerifyFeishuCredentials(appID, appSecret string) error {
	appID = strings.TrimSpace(appID)
	appSecret = strings.TrimSpace(appSecret)

	if appID == "" {
		return fmt.Errorf("feishu app id 不能为空")
	}
	if appSecret == "" {
		return fmt.Errorf("feishu app secret 不能为空")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	_, err := getTenantAccessToken(ctx, appID, appSecret)
	return err
}

// SendFeishuMessage 向指定 open_id 发送文本消息。
// 成功返回 true, ""；失败返回 false, 错误文本。
func SendFeishuMessage(appID, appSecret, openID, text string) (bool, string) {
	appID = strings.TrimSpace(appID)
	appSecret = strings.TrimSpace(appSecret)
	openID = strings.TrimSpace(openID)
	text = strings.TrimSpace(text)

	if appID == "" {
		return false, "feishu app id 不能为空"
	}
	if appSecret == "" {
		return false, "feishu app secret 不能为空"
	}
	if openID == "" {
		return false, "feishu open_id 不能为空"
	}
	if text == "" {
		return false, "消息内容不能为空"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	token, err := getTenantAccessToken(ctx, appID, appSecret)
	if err != nil {
		return false, err.Error()
	}

	contentBytes, err := json.Marshal(map[string]string{
		"text": text,
	})
	if err != nil {
		return false, fmt.Sprintf("构造飞书消息内容失败: %v", err)
	}

	reqBody := feishuSendMessageRequest{
		ReceiveID: openID,
		MsgType:   "text",
		Content:   string(contentBytes),
	}

	rawReq, err := json.Marshal(reqBody)
	if err != nil {
		return false, fmt.Sprintf("构造飞书请求失败: %v", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, feishuSendMessageURL, bytes.NewReader(rawReq))
	if err != nil {
		return false, fmt.Sprintf("创建飞书消息请求失败: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, fmt.Sprintf("调用飞书发送消息失败: %v", err)
	}
	defer resp.Body.Close()

	respBytes, _ := io.ReadAll(resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if len(respBytes) > 0 {
			return false, fmt.Sprintf("飞书发送消息失败: http=%s body=%s", resp.Status, strings.TrimSpace(string(respBytes)))
		}
		return false, fmt.Sprintf("飞书发送消息失败: http=%s", resp.Status)
	}

	var result feishuSendMessageResponse
	if err := json.Unmarshal(respBytes, &result); err != nil {
		if len(respBytes) > 0 {
			return false, fmt.Sprintf("解析飞书发送响应失败: %v; body=%s", err, strings.TrimSpace(string(respBytes)))
		}
		return false, fmt.Sprintf("解析飞书发送响应失败: %v", err)
	}

	if result.Code != 0 {
		if strings.TrimSpace(result.Msg) != "" {
			return false, fmt.Sprintf("飞书发送消息失败: %s", strings.TrimSpace(result.Msg))
		}
		return false, fmt.Sprintf("飞书发送消息失败: code=%d", result.Code)
	}

	return true, ""
}

// Notify 从 credentials store + bindings store 自动读取飞书配置并发送通知。
func (n FeishuNotifier) Notify(ctx context.Context, e protocol.Event) error {
	if isQuietEventType(e.Type) {
		return nil
	}

	appID, appSecret := resolveFeishuCredentials()
	openID := resolveBoundFeishuOpenID()

	if appID == "" || appSecret == "" || openID == "" {
		return nil
	}

	text := buildFeishuEventText(e)
	ok, errText := SendFeishuMessage(appID, appSecret, openID, text)
	if !ok {
		return fmt.Errorf("%s", errText)
	}

	return nil
}

func buildFeishuEventText(e protocol.Event) string {
	return buildChannelEventText(e)
}

func resolveFeishuCredentials() (string, string) {
	appID, appSecret := GetFeishuCredentials()

	if strings.TrimSpace(appID) == "" {
		appID = strings.TrimSpace(os.Getenv("FEISHU_APP_ID"))
	}
	if strings.TrimSpace(appSecret) == "" {
		appSecret = strings.TrimSpace(os.Getenv("FEISHU_APP_SECRET"))
	}

	return strings.TrimSpace(appID), strings.TrimSpace(appSecret)
}

func resolveBoundFeishuOpenID() string {
	store := getStore()
	if store == nil {
		return strings.TrimSpace(os.Getenv("FEISHU_OPEN_ID"))
	}

	bindings := store.ListBindings()
	for _, binding := range bindings {
		if !strings.EqualFold(strings.TrimSpace(binding.Channel), "feishu") {
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

	return strings.TrimSpace(os.Getenv("FEISHU_OPEN_ID"))
}

func getTenantAccessToken(ctx context.Context, appID, appSecret string) (string, error) {
	reqBody := feishuTenantTokenRequest{
		AppID:     strings.TrimSpace(appID),
		AppSecret: strings.TrimSpace(appSecret),
	}

	rawReq, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("构造飞书凭证请求失败: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, feishuTenantTokenURL, bytes.NewReader(rawReq))
	if err != nil {
		return "", fmt.Errorf("创建飞书鉴权请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("调用飞书鉴权接口失败: %w", err)
	}
	defer resp.Body.Close()

	respBytes, _ := io.ReadAll(resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if len(respBytes) > 0 {
			return "", fmt.Errorf("飞书鉴权失败: http=%s body=%s", resp.Status, strings.TrimSpace(string(respBytes)))
		}
		return "", fmt.Errorf("飞书鉴权失败: http=%s", resp.Status)
	}

	var result feishuTenantTokenResponse
	if err := json.Unmarshal(respBytes, &result); err != nil {
		if len(respBytes) > 0 {
			return "", fmt.Errorf("解析飞书鉴权响应失败: %w; body=%s", err, strings.TrimSpace(string(respBytes)))
		}
		return "", fmt.Errorf("解析飞书鉴权响应失败: %w", err)
	}

	if result.Code != 0 {
		if strings.TrimSpace(result.Msg) != "" {
			return "", fmt.Errorf("飞书鉴权失败: %s", strings.TrimSpace(result.Msg))
		}
		return "", fmt.Errorf("飞书鉴权失败: code=%d", result.Code)
	}

	token := strings.TrimSpace(result.TenantAccessToken)
	if token == "" {
		return "", fmt.Errorf("飞书鉴权失败: tenant_access_token 为空")
	}

	return token, nil
}

func init() {
	RegisterNotifier(FeishuNotifier{})
}
