package notify

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"openclaw-guard-kit/internal/protocol"
	"openclaw-guard-kit/logging"
)

var rootDir string
var store *Store

// SetRootDir sets the root directory for storing binding files.
// It should be called once at startup.
func SetRootDir(dir string) {
	rootDir = dir
}

// GetRootDir returns the root directory for storing binding files.
func GetRootDir() string {
	return rootDir
}

// getStore returns the store instance, initializing it if needed.
func getStore() *Store {
	storePath := filepath.Join(GetRootDir(), ".guard-state", "bindings.json")

	if store == nil {
		var err error
		store, err = NewStore(storePath)
		if err != nil {
			log.Printf("notify: failed to initialize store: %v", err)
			return nil
		}
		return store
	}

	if store.path != storePath {
		var err error
		store, err = NewStore(storePath)
		if err != nil {
			log.Printf("notify: failed to switch store: %v", err)
			return nil
		}
		return store
	}

	if err := store.Load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Printf("notify: failed to reload bindings store: %v", err)
	}

	return store
}

type Notifier interface {
	Notify(ctx context.Context, e protocol.Event) error
}

type LogNotifier struct {
	logger *logging.Logger
}

func NewLogNotifier(logger *logging.Logger) Notifier {
	return &LogNotifier{logger: logger}
}

func (n *LogNotifier) Notify(_ context.Context, e protocol.Event) error {
	if n == nil || n.logger == nil {
		return nil
	}
	if isQuietEventType(e.Type) {
		return nil
	}

	module := moduleForEvent(e.Type)
	msg := fmt.Sprintf("[%s] %s", module, e.Type)

	kv := []any{
		"agent", e.AgentID,
		"target", e.Target,
		"targetKey", e.TargetKey,
		"kind", e.Kind,
		"path", e.Path,
	}

	if strings.TrimSpace(e.Message) != "" {
		kv = append(kv, "message", e.Message)
	}
	for k, v := range e.Data {
		kv = append(kv, k, v)
	}

	if isErrorEventType(e.Type) {
		n.logger.Error(msg, kv...)
	} else {
		n.logger.Info(msg, kv...)
	}
	return nil
}

func isQuietEventType(t string) bool {
	switch t {
	case protocol.TypeGuardStatusRequest,
		protocol.TypeGuardStatusResponse,
		protocol.TypeGuardStopRequest,
		protocol.TypeGuardStopResponse,
		protocol.EventWatchStarted,
		protocol.EventWatchStopped,
		protocol.EventServiceStarting,
		protocol.EventServiceStarted,
		protocol.EventServiceStopping,
		protocol.EventServiceStopped,
		protocol.EventGuardCoordinatorStarted,
		protocol.EventGuardCoordinatorStopped:
		return true
	default:
		return false
	}
}

func isErrorEventType(t string) bool {
	switch t {
	case protocol.EventRestoreFailed,
		protocol.MessageError,
		protocol.EventGuardAnomaly:
		return true
	default:
		return false
	}
}

func moduleForEvent(t string) string {
	switch t {
	case protocol.EventServiceStarting,
		protocol.EventServiceStarted,
		protocol.EventServiceStopping,
		protocol.EventServiceStopped,
		protocol.EventWatchStarted,
		protocol.EventWatchStopped,
		protocol.EventOpenClawOnline,
		protocol.EventOpenClawTransition,
		protocol.EventOpenClawRecovered,
		protocol.EventOpenClawOfflineConfirmed,
		protocol.EventGuardAnomaly:
		return "runtime"

	case protocol.EventDriftDetected,
		protocol.EventRestoreCompleted,
		protocol.EventRestoreFailed:
		return "security"

	case protocol.TypeGuardStopRequest,
		protocol.TypeGuardStopResponse:
		return "runtime"

	default:
		return "event"
	}
}

func buildChannelEventText(e protocol.Event) string {
	var summary string

	switch e.Type {
	case protocol.EventOpenClawOnline:
		summary = "OpenClaw 已上线。"
	case protocol.EventOpenClawTransition:
		summary = "OpenClaw 关闭中或重启中，正在等待确认。"
	case protocol.EventOpenClawRecovered:
		summary = "OpenClaw 已恢复。"
	case protocol.EventOpenClawOfflineConfirmed:
		summary = "OpenClaw 已确认关闭，守护程序已退出。"
	case protocol.EventGuardAnomaly:
		if strings.TrimSpace(e.Message) != "" {
			summary = strings.TrimSpace(e.Message)
		} else {
			summary = "守护程序异常，请检查日志。"
		}
	case protocol.EventDriftDetected:
		summary = "检测到 OpenClaw 配置发生未授权修改，已触发保护处理。"
	case protocol.EventRestoreCompleted:
		summary = "已自动恢复到最近一次受保护的配置状态。"
	case protocol.EventRestoreFailed:
		summary = "自动恢复失败，请尽快检查配置与守护日志。"
	default:
		return buildGenericChannelEventText(e)
	}

	var b strings.Builder
	b.WriteString("OpenClaw 通知")
	b.WriteString("\n说明：")
	b.WriteString(summary)

	if strings.TrimSpace(e.AgentID) != "" {
		b.WriteString("\nAgent：")
		b.WriteString(strings.TrimSpace(e.AgentID))
	}
	if ts := formatEventTime(e.At); ts != "" {
		b.WriteString("\n时间：")
		b.WriteString(ts)
	}
	if strings.TrimSpace(e.TargetKey) != "" {
		b.WriteString("\nTargetKey：")
		b.WriteString(strings.TrimSpace(e.TargetKey))
	} else if strings.TrimSpace(e.Target) != "" {
		b.WriteString("\nTarget：")
		b.WriteString(strings.TrimSpace(e.Target))
	}
	if strings.TrimSpace(e.Path) != "" {
		b.WriteString("\nPath：")
		b.WriteString(strings.TrimSpace(e.Path))
	}

	return b.String()
}

func buildGenericChannelEventText(e protocol.Event) string {
	var b strings.Builder

	b.WriteString("OpenClaw 通知")
	if strings.TrimSpace(e.Type) != "" {
		b.WriteString("\n事件：")
		b.WriteString(strings.TrimSpace(e.Type))
	}
	if strings.TrimSpace(e.Message) != "" {
		b.WriteString("\n说明：")
		b.WriteString(strings.TrimSpace(e.Message))
	}
	if strings.TrimSpace(e.AgentID) != "" {
		b.WriteString("\nAgent：")
		b.WriteString(strings.TrimSpace(e.AgentID))
	}
	if ts := formatEventTime(e.At); ts != "" {
		b.WriteString("\n时间：")
		b.WriteString(ts)
	}
	if strings.TrimSpace(e.TargetKey) != "" {
		b.WriteString("\nTargetKey：")
		b.WriteString(strings.TrimSpace(e.TargetKey))
	} else if strings.TrimSpace(e.Target) != "" {
		b.WriteString("\nTarget：")
		b.WriteString(strings.TrimSpace(e.Target))
	}
	if strings.TrimSpace(e.Kind) != "" {
		b.WriteString("\nKind：")
		b.WriteString(strings.TrimSpace(e.Kind))
	}
	if strings.TrimSpace(e.Path) != "" {
		b.WriteString("\nPath：")
		b.WriteString(strings.TrimSpace(e.Path))
	}

	return b.String()
}

func formatEventTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Local().Format("2006/1/2 15:04:05")
}
