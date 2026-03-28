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

	"openclaw-guard-kit/internal/guard"
	"openclaw-guard-kit/internal/protocol"
	"openclaw-guard-kit/logging"
)

var rootDir string
var guardClient *guard.Client
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

// SetGuardClient sets the guard client used for guarded writes.
// It should be called once at startup after the guard executable path is known.
func SetGuardClient(guardExePath string, dir string, agentID string) {
	rootDir = dir
	guardClient = guard.NewClient(guardExePath, dir, agentID)
}

// getGuardClient returns the guard client if set, otherwise nil.
func getGuardClient() *guard.Client {
	return guardClient
}

// GetGuardClient returns the guard client for use by other packages.
func GetGuardClient() *guard.Client {
	return guardClient
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
		if store != nil {
			store.SetGuardClient(getGuardClient())
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
		if store != nil {
			store.SetGuardClient(getGuardClient())
		}
		return store
	}

	if err := store.Load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Printf("notify: failed to reload bindings store: %v", err)
	}

	return store
}

// WriteFileGuarded writes data to a file using the guard request-write/complete-write protocol.
// If guardClient is not set, it returns an error (no fallback to direct write).
func WriteFileGuarded(ctx context.Context, path string, data []byte) error {
	if guardClient == nil {
		return fmt.Errorf("guard client not set, guarded write required for %s", path)
	}
	return guardClient.WriteFile(ctx, path, data)
}

// RemoveFileGuarded removes a file using the guard protocol (by writing empty content).
// If guardClient is not set, it returns an error (no fallback to direct removal).
func RemoveFileGuarded(ctx context.Context, path string) error {
	if guardClient == nil {
		return fmt.Errorf("guard client not set, guarded removal required for %s", path)
	}
	return guardClient.RemoveFile(ctx, path)
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

type ConsoleNotifier struct{}

func (ConsoleNotifier) Notify(_ context.Context, e protocol.Event) error {
	if isQuietEventType(e.Type) {
		return nil
	}
	log.Printf("notify: %+v", e)
	return nil
}

var notifiers []Notifier

func RegisterNotifier(n Notifier) {
	if n == nil {
		return
	}
	notifiers = append(notifiers, n)
}

func Broadcast(ctx context.Context, e protocol.Event) error {
	if isQuietEventType(e.Type) {
		return nil
	}
	for _, n := range notifiers {
		if n == nil {
			continue
		}
		if err := n.Notify(ctx, e); err != nil {
			log.Printf("notify error: %v", err)
		}
	}
	return nil
}

func init() {
	RegisterNotifier(ConsoleNotifier{})
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
		protocol.MessageWriteFailed,
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
		protocol.EventRestoreFailed,
		protocol.MessageWriteRequest,
		protocol.MessageWriteGranted,
		protocol.MessageWriteCompleted,
		protocol.MessageWriteFailed,
		protocol.MessageWriteReleased:
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
		summary = "检测到 OpenClaw 配置发生未授权修改，已触发保护处理。\n如需手动修改配置，或执行插件安装/升级，请在右下角右键 Guard 图标，点击“暂停监控”；确认 OpenClaw 正常后，再点击“恢复监控”。"
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
