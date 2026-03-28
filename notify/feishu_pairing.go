package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"
)

const feishuGetUserURLPrefix = "https://open.feishu.cn/open-apis/contact/v3/users/"

type FeishuPairingWatcher struct {
	appID       string
	appSecret   string
	pairingCode string

	ctx    context.Context
	cancel context.CancelFunc

	resultCh  chan *PairingResult
	startOnce sync.Once
	doneOnce  sync.Once
}

func NewFeishuPairingWatcher(appID, appSecret, pairingCode string) *FeishuPairingWatcher {
	ctx, cancel := context.WithCancel(context.Background())

	return &FeishuPairingWatcher{
		appID:       strings.TrimSpace(appID),
		appSecret:   strings.TrimSpace(appSecret),
		pairingCode: strings.ToUpper(strings.TrimSpace(pairingCode)),
		ctx:         ctx,
		cancel:      cancel,
		resultCh:    make(chan *PairingResult, 1),
	}
}

func (w *FeishuPairingWatcher) Start() {
	if w == nil {
		return
	}
	w.startOnce.Do(func() {
		go w.run()
	})
}

func (w *FeishuPairingWatcher) Stop() {
	if w == nil {
		return
	}
	w.finish(nil)
}

func (w *FeishuPairingWatcher) ResultCh() <-chan *PairingResult {
	if w == nil {
		ch := make(chan *PairingResult)
		close(ch)
		return ch
	}
	return w.resultCh
}

func (w *FeishuPairingWatcher) run() {
	if strings.TrimSpace(w.appID) == "" {
		w.finish(&PairingResult{
			Success:   false,
			AccountID: "",
			Error:     "feishu app id 不能为空",
		})
		return
	}
	if strings.TrimSpace(w.appSecret) == "" {
		w.finish(&PairingResult{
			Success:   false,
			AccountID: w.appID,
			Error:     "feishu app secret 不能为空",
		})
		return
	}
	if strings.TrimSpace(w.pairingCode) == "" {
		w.finish(&PairingResult{
			Success:   false,
			AccountID: w.appID,
			Error:     "pairing code 不能为空",
		})
		return
	}

	eventHandler := dispatcher.NewEventDispatcher("", "").
		OnP2MessageReceiveV1(func(ctx context.Context, event *larkim.P2MessageReceiveV1) error {
			w.handleMessageEvent(ctx, event)
			return nil
		})

	client := larkws.NewClient(
		w.appID,
		w.appSecret,
		larkws.WithEventHandler(eventHandler),
		larkws.WithAutoReconnect(false),
		larkws.WithLogLevel(larkcore.LogLevelError),
	)

	timeoutCtx, timeoutCancel := context.WithTimeout(w.ctx, defaultPendingBindingTTL)
	defer timeoutCancel()

	go func() {
		err := client.Start(timeoutCtx)
		if err != nil && timeoutCtx.Err() == nil {
			w.finish(&PairingResult{
				Success:   false,
				AccountID: w.appID,
				Error:     fmt.Sprintf("飞书长连接启动失败: %v", err),
			})
		}
	}()

	<-timeoutCtx.Done()
	if timeoutCtx.Err() == context.DeadlineExceeded {
		w.finish(&PairingResult{
			Success:   false,
			AccountID: w.appID,
			Error:     fmt.Sprintf("配对超时（%d秒内未收到配对消息）", int(defaultPendingBindingTTL.Seconds())),
		})
	}
}

func (w *FeishuPairingWatcher) handleMessageEvent(ctx context.Context, event *larkim.P2MessageReceiveV1) {
	if w == nil || event == nil || event.Event == nil {
		return
	}
	if w.ctx.Err() != nil {
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
	if !matchFeishuPairingCode(text, w.pairingCode) {
		return
	}

	openID := ""
	if sender.SenderId.OpenId != nil {
		openID = strings.TrimSpace(*sender.SenderId.OpenId)
	}
	if openID == "" {
		return
	}

	displayName := w.resolveDisplayName(ctx, openID)
	if displayName == "" {
		displayName = openID
	}

	w.finish(&PairingResult{
		Success:     true,
		AccountID:   w.appID,
		SenderID:    openID,
		DisplayName: displayName,
	})
}

func (w *FeishuPairingWatcher) resolveDisplayName(ctx context.Context, openID string) string {
	openID = strings.TrimSpace(openID)
	if openID == "" {
		return ""
	}

	name, err := getFeishuUserName(ctx, w.appID, w.appSecret, openID)
	if err != nil {
		return openID
	}
	if strings.TrimSpace(name) == "" {
		return openID
	}
	return strings.TrimSpace(name)
}

func (w *FeishuPairingWatcher) finish(result *PairingResult) {
	w.doneOnce.Do(func() {
		if result != nil {
			select {
			case w.resultCh <- result:
			default:
			}
		}
		close(w.resultCh)
		w.cancel()
	})
}

func extractFeishuTextMessage(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	var textPayload struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(raw), &textPayload); err == nil {
		return strings.TrimSpace(textPayload.Text)
	}

	return raw
}

func matchFeishuPairingCode(text, pairingCode string) bool {
	text = strings.TrimSpace(text)
	pairingCode = strings.ToUpper(strings.TrimSpace(pairingCode))
	if text == "" || pairingCode == "" {
		return false
	}

	if strings.ToUpper(text) == pairingCode {
		return true
	}

	fields := strings.Fields(text)
	if len(fields) == 2 && strings.EqualFold(fields[0], "/pair") && strings.ToUpper(fields[1]) == pairingCode {
		return true
	}

	return false
}

func getFeishuUserName(ctx context.Context, appID, appSecret, openID string) (string, error) {
	token, err := getTenantAccessToken(ctx, appID, appSecret)
	if err != nil {
		return "", err
	}

	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	u := feishuGetUserURLPrefix + url.PathEscape(openID) + "?user_id_type=open_id"
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, u, nil)
	if err != nil {
		return "", fmt.Errorf("创建飞书用户信息请求失败: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("获取飞书用户信息失败: %w", err)
	}
	defer resp.Body.Close()

	respBytes, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("获取飞书用户信息失败: http=%s body=%s", resp.Status, strings.TrimSpace(string(respBytes)))
	}

	var result struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			User struct {
				Name string `json:"name"`
			} `json:"user"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBytes, &result); err != nil {
		return "", fmt.Errorf("解析飞书用户信息失败: %w", err)
	}
	if result.Code != 0 {
		if strings.TrimSpace(result.Msg) != "" {
			return "", fmt.Errorf("获取飞书用户信息失败: %s", strings.TrimSpace(result.Msg))
		}
		return "", fmt.Errorf("获取飞书用户信息失败: code=%d", result.Code)
	}

	return strings.TrimSpace(result.Data.User.Name), nil
}

func derefString(v *string) string {
	if v == nil {
		return ""
	}
	return strings.TrimSpace(*v)
}
