package notify

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

type WecomPairingWatcher struct {
	botID       string
	pairingCode string

	resultCh chan *PairingResult

	startOnce   sync.Once
	finishOnce  sync.Once
	unsubscribe func()
}

func NewWecomPairingWatcher(botID, code string) *WecomPairingWatcher {
	return &WecomPairingWatcher{
		botID:       strings.TrimSpace(botID),
		pairingCode: strings.ToUpper(strings.TrimSpace(code)),
		resultCh:    make(chan *PairingResult, 1),
	}
}

func (w *WecomPairingWatcher) Start() {
	if w == nil {
		return
	}

	w.startOnce.Do(func() {
		w.unsubscribe = RegisterWecomInboundSink(func(msg WecomInboundMessage) {
			w.handleInbound(msg)
		})

		go func() {
			timer := time.NewTimer(defaultPendingBindingTTL)
			defer timer.Stop()
			<-timer.C
			w.finish(&PairingResult{
				Success:   false,
				AccountID: w.botID,
				Error:     fmt.Sprintf("配对超时（%d秒内未收到配对消息）", int(defaultPendingBindingTTL.Seconds())),
			})
		}()
	})
}

func (w *WecomPairingWatcher) Stop() {
	if w == nil {
		return
	}
	w.finish(nil)
}

func (w *WecomPairingWatcher) ResultCh() <-chan *PairingResult {
	if w == nil {
		ch := make(chan *PairingResult)
		close(ch)
		return ch
	}
	return w.resultCh
}

func (w *WecomPairingWatcher) handleInbound(msg WecomInboundMessage) {
	if w == nil {
		return
	}

	if !strings.EqualFold(strings.TrimSpace(msg.ChatType), "dm") {
		return
	}
	if !strings.EqualFold(strings.TrimSpace(msg.MsgType), "text") {
		return
	}

	text := strings.TrimSpace(msg.Content)
	if !matchWecomPairingCode(text, w.pairingCode) {
		return
	}

	userID := strings.TrimSpace(msg.UserID)
	if userID == "" {
		return
	}

	displayName := userID
	if strings.TrimSpace(msg.DisplayName) != "" {
		displayName = strings.TrimSpace(msg.DisplayName)
	}

	w.finish(&PairingResult{
		Success:     true,
		AccountID:   w.botID,
		SenderID:    userID,
		DisplayName: displayName,
	})
}

func (w *WecomPairingWatcher) finish(result *PairingResult) {
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

func matchWecomPairingCode(text, pairingCode string) bool {
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
