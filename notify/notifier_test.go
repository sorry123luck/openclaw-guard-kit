package notify

import (
	"context"
	"openclaw-guard-kit/internal/protocol"
	"os"
	"testing"
)

type testNotifier struct {
	called bool
}

func (n *testNotifier) Notify(ctx context.Context, e protocol.Event) error {
	n.called = true
	return nil
}

func TestBroadcastCallsNotifiers(t *testing.T) {
	tn := &testNotifier{}
	RegisterNotifier(tn)

	e := protocol.Event{Type: "test", Target: "t", TargetKey: "k"}
	Broadcast(context.Background(), e)

	if !tn.called {
		t.Fatalf("expected testNotifier to be called by Broadcast")
	}
}

func TestFeishuNotifierBroadcast(t *testing.T) {
	// Set env var
	old := os.Getenv("FEISHU_WEBHOOK_URL")
	err := os.Setenv("FEISHU_WEBHOOK_URL", "http://example.com")
	if err != nil {
		t.Fatalf("failed to set env: %v", err)
	}
	t.Cleanup(func() {
		if old == "" {
			os.Unsetenv("FEISHU_WEBHOOK_URL")
		} else {
			os.Setenv("FEISHU_WEBHOOK_URL", old)
		}
	})

	// Register notifier (init already done via import, but ensure)
	RegisterNotifier(FeishuNotifier{})

	e := protocol.Event{Type: "test", Target: "t", TargetKey: "k"}
	// Should not panic
	Broadcast(context.Background(), e)
	// No assertion on actual sending; just ensure no error from notifier (it logs but returns nil)
}

func TestTelegramNotifierBroadcast(t *testing.T) {
	oldToken := os.Getenv("TELEGRAM_BOT_TOKEN")
	oldChat := os.Getenv("TELEGRAM_CHAT_ID")
	err := os.Setenv("TELEGRAM_BOT_TOKEN", "fake-token")
	if err != nil {
		t.Fatalf("failed to set token env: %v", err)
	}
	err2 := os.Setenv("TELEGRAM_CHAT_ID", "fake-chat")
	if err2 != nil {
		t.Fatalf("failed to set chat env: %v", err2)
	}
	t.Cleanup(func() {
		if oldToken == "" {
			os.Unsetenv("TELEGRAM_BOT_TOKEN")
		} else {
			os.Setenv("TELEGRAM_BOT_TOKEN", oldToken)
		}
		if oldChat == "" {
			os.Unsetenv("TELEGRAM_CHAT_ID")
		} else {
			os.Setenv("TELEGRAM_CHAT_ID", oldChat)
		}
	})

	RegisterNotifier(TelegramNotifier{})

	e := protocol.Event{Type: "test", Target: "t", TargetKey: "k"}
	Broadcast(context.Background(), e)
}
