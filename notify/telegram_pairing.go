package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func init() {
	_ = url.Values{}
}

// telegramChat Telegram 聊天信息
type telegramChat struct {
	ID        int64  `json:"id"`
	Title     string `json:"title"`
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
}

// telegramUser Telegram 用户信息
type telegramUser struct {
	ID        int64  `json:"id"`
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
}

// TelegramPairingWatcher 负责轮询 Telegram 配对
type TelegramPairingWatcher struct {
	token       string
	pairingCode string
	botID       string // AccountID 用
	resultCh    chan PairingResult
	stopCh      chan struct{}
}

// PairingResult 配对结果
type PairingResult struct {
	AccountID   string // bot id
	SenderID    string // chat_id (转成 string)
	DisplayName string // 用户显示名
	Success     bool
	Error       string
}

// NewTelegramPairingWatcher 创建新的配对 watcher
func NewTelegramPairingWatcher(token, pairingCode string) *TelegramPairingWatcher {
	return &TelegramPairingWatcher{
		token:       token,
		pairingCode: strings.ToUpper(strings.TrimSpace(pairingCode)),
		resultCh:    make(chan PairingResult, 1),
		stopCh:      make(chan struct{}, 1),
	}
}

// Start 启动轮询
func (w *TelegramPairingWatcher) Start(ctx context.Context) {
	defer close(w.resultCh)

	// 第一步：获取 bot id
	botID, err := w.getBotID()
	if err != nil {
		w.resultCh <- PairingResult{Success: false, Error: "获取 bot ID 失败: " + err.Error()}
		return
	}
	w.botID = botID

	// 第二步：启动 getUpdates 轮询
	w.pollUpdates(ctx)
}

// Stop 停止轮询
func (w *TelegramPairingWatcher) Stop() {
	select {
	case w.stopCh <- struct{}{}:
	default:
	}
}

// ResultCh 返回结果通道
func (w *TelegramPairingWatcher) ResultCh() <-chan PairingResult {
	return w.resultCh
}

// getBotID 调用 getMe 获取 bot id
func (w *TelegramPairingWatcher) getBotID() (string, error) {
	u := fmt.Sprintf("https://api.telegram.org/bot%s/getMe", w.token)
	resp, err := http.Get(u)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("getMe 返回: %s", resp.Status)
	}

	var data struct {
		Ok     bool `json:"ok"`
		Result struct {
			ID        int64  `json:"id"`
			Username  string `json:"username"`
			FirstName string `json:"first_name"`
			LastName  string `json:"last_name"`
		} `json:"result"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return "", err
	}
	if !data.Ok {
		return "", fmt.Errorf("getMe 返回错误")
	}

	return strconv.FormatInt(data.Result.ID, 10), nil
}

// pollUpdates 轮询 getUpdates 等待配对消息
func (w *TelegramPairingWatcher) pollUpdates(ctx context.Context) {
	timeout := defaultPendingBindingTTL
	deadline := time.Now().Add(timeout)

	var offset int64 = 0

	for time.Now().Before(deadline) {
		// 检查是否收到停止信号
		select {
		case <-w.stopCh:
			w.resultCh <- PairingResult{
				Success: false,
				Error:   fmt.Sprintf("配对超时（%d秒内未收到配对消息）", int(timeout.Seconds())),
			}
			return
		default:
		}

		// 计算剩余时间
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}

		// 设置超时（剩余时间或 30 秒，取较小值）
		pollTimeout := 30
		if remaining.Seconds() < 30 {
			pollTimeout = int(remaining.Seconds())
		}

		v := make(url.Values)
		v.Set("offset", strconv.FormatInt(offset+1, 10))
		v.Set("timeout", strconv.Itoa(pollTimeout))

		u := fmt.Sprintf("https://api.telegram.org/bot%s/getUpdates?%s", w.token, v.Encode())

		req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
		if err != nil {
			time.Sleep(2 * time.Second)
			continue
		}

		client := &http.Client{Timeout: time.Duration(pollTimeout+5) * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			time.Sleep(2 * time.Second)
			continue
		}

		var updates struct {
			Ok     bool `json:"ok"`
			Result []struct {
				UpdateID int64 `json:"update_id"`
				Message  struct {
					Chat telegramChat `json:"chat"`
					From telegramUser `json:"from"`
					Text string       `json:"text"`
				} `json:"message"`
			} `json:"result"`
		}

		if err := json.NewDecoder(resp.Body).Decode(&updates); err != nil {
			resp.Body.Close()
			time.Sleep(2 * time.Second)
			continue
		}
		resp.Body.Close()

		if !updates.Ok {
			time.Sleep(2 * time.Second)
			continue
		}

		for _, upd := range updates.Result {
			offset = upd.UpdateID

			// 跳过无效消息
			if upd.Message.Chat.ID == 0 {
				continue
			}

			// 检查消息文本是否匹配配对码
			text := strings.TrimSpace(upd.Message.Text)
			expectedCmd := "/pair " + w.pairingCode

			if strings.EqualFold(text, expectedCmd) || strings.EqualFold(text, w.pairingCode) {
				// 配对成功
				senderID := strconv.FormatInt(upd.Message.Chat.ID, 10)
				displayName := extractTelegramDisplayName(upd.Message.Chat, upd.Message.From)

				w.resultCh <- PairingResult{
					AccountID:   w.botID,
					SenderID:    senderID,
					DisplayName: displayName,
					Success:     true,
				}
				return
			}
		}
	}

	// 超时
	w.resultCh <- PairingResult{Success: false, Error: "配对超时（60秒内未收到配对消息）"}
}

// extractTelegramDisplayName 提取显示名
func extractTelegramDisplayName(chat telegramChat, from telegramUser) string {
	// 优先用 from 的名字
	if from.FirstName != "" {
		name := from.FirstName
		if from.LastName != "" {
			name += " " + from.LastName
		}
		return name
	}

	// 其次用 username
	if from.Username != "" {
		return "@" + from.Username
	}

	// 再其次用 chat title（群聊场景）
	if chat.Title != "" {
		return chat.Title
	}

	// 最后用 chat id
	return strconv.FormatInt(chat.ID, 10)
}
