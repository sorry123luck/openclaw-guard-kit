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
)

const feishuGetUserURLPrefix = "https://open.feishu.cn/open-apis/contact/v3/users/"

type FeishuPairingWatcher struct {
	appID       string
	appSecret   string
	pairingCode string

	resultCh    chan *PairingResult
	stopCh      chan struct{}
	unsubscribe func()

	startOnce  sync.Once
	finishOnce sync.Once
}

func NewFeishuPairingWatcher(appID, appSecret, pairingCode string) *FeishuPairingWatcher {
	return &FeishuPairingWatcher{
		appID:       strings.TrimSpace(appID),
		appSecret:   strings.TrimSpace(appSecret),
		pairingCode: strings.ToUpper(strings.TrimSpace(pairingCode)),
		resultCh:    make(chan *PairingResult, 1),
		stopCh:      make(chan struct{}, 1),
	}
}

func (w *FeishuPairingWatcher) Start() {
	if w == nil {
		return
	}

	w.startOnce.Do(func() {
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

		if err := EnsureFeishuInboundListener(w.appID, w.appSecret); err != nil {
			w.finish(&PairingResult{
				Success:   false,
				AccountID: w.appID,
				Error:     "启动飞书入站监听失败: " + err.Error(),
			})
			return
		}

		w.unsubscribe = RegisterFeishuInboundSink(func(msg FeishuInboundMessage) {
			w.handleInbound(msg)
		})

		go func() {
			timer := time.NewTimer(defaultPendingBindingTTL)
			defer timer.Stop()

			select {
			case <-w.stopCh:
				w.finish(&PairingResult{
					Success:   false,
					AccountID: w.appID,
					Error:     fmt.Sprintf("配对超时（%d秒内未收到配对消息）", int(defaultPendingBindingTTL.Seconds())),
				})
			case <-timer.C:
				w.finish(&PairingResult{
					Success:   false,
					AccountID: w.appID,
					Error:     fmt.Sprintf("配对超时（%d秒内未收到配对消息）", int(defaultPendingBindingTTL.Seconds())),
				})
			}
		}()
	})
}

func (w *FeishuPairingWatcher) Stop() {
	if w == nil {
		return
	}
	select {
	case w.stopCh <- struct{}{}:
	default:
	}
}

func (w *FeishuPairingWatcher) ResultCh() <-chan *PairingResult {
	if w == nil {
		ch := make(chan *PairingResult)
		close(ch)
		return ch
	}
	return w.resultCh
}

func (w *FeishuPairingWatcher) handleInbound(msg FeishuInboundMessage) {
	if w == nil {
		return
	}
	if strings.TrimSpace(msg.AppID) != strings.TrimSpace(w.appID) {
		return
	}
	if !strings.EqualFold(strings.TrimSpace(msg.MsgType), "text") {
		return
	}

	text := strings.TrimSpace(msg.Content)
	if !matchFeishuPairingCode(text, w.pairingCode) {
		return
	}

	openID := strings.TrimSpace(msg.OpenID)
	if openID == "" {
		return
	}

	displayName := strings.TrimSpace(msg.DisplayName)
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

func (w *FeishuPairingWatcher) finish(result *PairingResult) {
	w.finishOnce.Do(func() {
		if w.unsubscribe != nil {
			w.unsubscribe()
			w.unsubscribe = nil
		}

		if result != nil {
			select {
			case w.resultCh <- result:
			default:
			}
		}
		close(w.resultCh)
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
