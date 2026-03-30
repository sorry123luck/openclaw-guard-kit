package notify

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

// PairingResult 配对结果
type PairingResult struct {
	AccountID   string // bot id
	SenderID    string // chat_id (转成 string)
	DisplayName string // 用户显示名
	Success     bool
	Error       string
}

// TelegramPairingWatcher 负责等待 Telegram 配对消息。
// 这一版不再自己轮询 getUpdates，而是复用 telegram.go 里的共享入站监听。
type TelegramPairingWatcher struct {
	token       string
	pairingCode string
	botID       string // AccountID 用

	resultCh    chan PairingResult
	stopCh      chan struct{}
	unsubscribe func()

	startOnce  sync.Once
	finishOnce sync.Once
}

// NewTelegramPairingWatcher 创建新的配对 watcher
func NewTelegramPairingWatcher(token, pairingCode string) *TelegramPairingWatcher {
	return &TelegramPairingWatcher{
		token:       strings.TrimSpace(token),
		pairingCode: strings.ToUpper(strings.TrimSpace(pairingCode)),
		resultCh:    make(chan PairingResult, 1),
		stopCh:      make(chan struct{}, 1),
	}
}

// Start 启动配对监听
func (w *TelegramPairingWatcher) Start(ctx context.Context) {
	if w == nil {
		return
	}

	w.startOnce.Do(func() {
		if strings.TrimSpace(w.token) == "" {
			w.finish(PairingResult{
				Success: false,
				Error:   "telegram token 不能为空",
			})
			return
		}
		if strings.TrimSpace(w.pairingCode) == "" {
			w.finish(PairingResult{
				Success: false,
				Error:   "pairing code 不能为空",
			})
			return
		}

		// 第一步：获取 bot id
		botID, err := getTelegramBotID(w.token)
		if err != nil {
			w.finish(PairingResult{
				Success: false,
				Error:   "获取 bot ID 失败: " + err.Error(),
			})
			return
		}
		w.botID = botID

		// 第二步：确保共享入站轮询已启动
		if err := EnsureTelegramInboundPolling(w.token); err != nil {
			w.finish(PairingResult{
				Success: false,
				Error:   "启动 Telegram 入站监听失败: " + err.Error(),
			})
			return
		}

		// 第三步：注册本 watcher 的入站回调
		w.unsubscribe = RegisterTelegramInboundSink(func(msg TelegramInboundMessage) {
			w.handleInbound(msg)
		})

		// 第四步：等待超时 / stop / ctx cancel
		go func() {
			timer := time.NewTimer(defaultPendingBindingTTL)
			defer timer.Stop()

			select {
			case <-ctx.Done():
				w.finish(PairingResult{
					Success: false,
					Error:   fmt.Sprintf("配对超时（%d秒内未收到配对消息）", int(defaultPendingBindingTTL.Seconds())),
				})
			case <-w.stopCh:
				w.finish(PairingResult{
					Success: false,
					Error:   fmt.Sprintf("配对超时（%d秒内未收到配对消息）", int(defaultPendingBindingTTL.Seconds())),
				})
			case <-timer.C:
				w.finish(PairingResult{
					Success: false,
					Error:   fmt.Sprintf("配对超时（%d秒内未收到配对消息）", int(defaultPendingBindingTTL.Seconds())),
				})
			}
		}()
	})
}

// Stop 停止监听
func (w *TelegramPairingWatcher) Stop() {
	if w == nil {
		return
	}

	select {
	case w.stopCh <- struct{}{}:
	default:
	}
}

// ResultCh 返回结果通道
func (w *TelegramPairingWatcher) ResultCh() <-chan PairingResult {
	return w.resultCh
}

func (w *TelegramPairingWatcher) handleInbound(msg TelegramInboundMessage) {
	if w == nil {
		return
	}
	if strings.TrimSpace(msg.BotID) != strings.TrimSpace(w.botID) {
		return
	}

	text := strings.TrimSpace(msg.Text)
	expectedCmd := "/pair " + w.pairingCode

	if !strings.EqualFold(text, expectedCmd) && !strings.EqualFold(text, w.pairingCode) {
		return
	}

	displayName := strings.TrimSpace(msg.DisplayName)
	if displayName == "" {
		displayName = strconv.FormatInt(msg.ChatID, 10)
	}

	w.finish(PairingResult{
		AccountID:   w.botID,
		SenderID:    strconv.FormatInt(msg.ChatID, 10),
		DisplayName: displayName,
		Success:     true,
	})
}

func (w *TelegramPairingWatcher) finish(result PairingResult) {
	w.finishOnce.Do(func() {
		if w.unsubscribe != nil {
			w.unsubscribe()
			w.unsubscribe = nil
		}

		select {
		case w.resultCh <- result:
		default:
		}
		close(w.resultCh)
	})
}
