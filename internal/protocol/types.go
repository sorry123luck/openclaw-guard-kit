package protocol

import "time"

const (
	// 守护事件
	EventPrepareCompleted = "prepare.completed"
	EventWatchStarted     = "watch.started"
	EventDriftDetected    = "drift.detected"
	EventRestoreCompleted = "restore.completed"
	EventRestoreFailed    = "restore.failed"
	EventWatchStopped     = "watch.stopped"

	// 运行时发现 / 管理事件
	EventTargetDiscovered = "target.discovered"
	EventTargetManaged    = "target.managed"
	EventTargetUnmanaged  = "target.unmanaged"

	// 写入协议消息
	MessageWriteRequest   = "write.request"
	MessageWriteGranted   = "write.granted"
	MessageWriteWait      = "write.wait"
	MessageWriteCompleted = "write.completed"
	MessageWriteFailed    = "write.failed"
	MessageWriteReleased  = "write.released"

	// 通用错误响应
	MessageError = "error"
)

const (
	// 兼容旧 target 常量
	TargetOpenClaw    = "openclaw"
	TargetAuthProfile = "auth-profiles"

	// 新 kind 常量
	KindOpenClaw    = "openclaw"
	KindAuthProfile = "auth-profiles"
	KindModels      = "models"

	// 写入模式
	WriteModeReject = "reject"
	WriteModeBlock  = "block"

	// 响应状态
	StatusGranted   = "granted"
	StatusWaiting   = "waiting"
	StatusBusy      = "busy"
	StatusDenied    = "denied"
	StatusTimeout   = "timeout"
	StatusCompleted = "completed"
	StatusFailed    = "failed"
	StatusReleased  = "released"
	StatusError     = "error"
)

type Event struct {
	Type      string            `json:"type"`
	AgentID   string            `json:"agentId,omitempty"`
	Target    string            `json:"target,omitempty"`    // 兼容旧字段
	TargetKey string            `json:"targetKey,omitempty"` // 新字段
	Kind      string            `json:"kind,omitempty"`
	Path      string            `json:"path,omitempty"`
	Message   string            `json:"message,omitempty"`
	At        time.Time         `json:"at"`
	Data      map[string]string `json:"data,omitempty"`
}

type Message struct {
	Type      string `json:"type"`
	RequestID string `json:"requestId,omitempty"`
	LeaseID   string `json:"leaseId,omitempty"`
	ClientID  string `json:"clientId,omitempty"`
	AgentID   string `json:"agentId,omitempty"`

	// 兼容旧协议字段
	Target string `json:"target,omitempty"`

	// 新协议字段
	TargetKey     string    `json:"targetKey,omitempty"`
	Kind          string    `json:"kind,omitempty"`
	Path          string    `json:"path,omitempty"`
	LeaseSeconds  int       `json:"leaseSeconds,omitempty"`
	WaitSeconds   int       `json:"waitSeconds,omitempty"`
	QueuePosition int       `json:"queuePosition,omitempty"`
	Mode          string    `json:"mode,omitempty"`
	Reason        string    `json:"reason,omitempty"`
	Status        string    `json:"status,omitempty"`
	ExpiresAt     time.Time `json:"expiresAt,omitempty"`

	Message string            `json:"message,omitempty"`
	At      time.Time         `json:"at"`
	Data    map[string]string `json:"data,omitempty"`
}
