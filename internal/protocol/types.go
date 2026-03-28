package protocol

import "time"

const (
	// 守护事件
	EventPrepareCompleted  = "prepare.completed"
	EventWatchStarted      = "watch.started"
	EventDriftDetected     = "drift.detected"
	EventRestoreCompleted  = "restore.completed"
	EventRestoreFailed     = "restore.failed"
	EventWatchStopped      = "watch.stopped"
	EventCandidateCreated  = "candidate.created"
	EventCandidatePromoted = "candidate.promoted"

	// service / runtime 生命周期事件
	EventServiceStarting         = "service.starting"
	EventServiceStarted          = "service.started"
	EventServiceStopping         = "service.stopping"
	EventServiceStopped          = "service.stopped"
	EventGuardCoordinatorStarted = "guard.coordinator.started"
	EventGuardCoordinatorStopped = "guard.coordinator.stopped"
	// detector / OpenClaw 产品级生命周期通知
	EventOpenClawOnline           = "openclaw.online"
	EventOpenClawTransition       = "openclaw.transition"
	EventOpenClawRecovered        = "openclaw.recovered"
	EventOpenClawOfflineConfirmed = "openclaw.offline_confirmed"
	EventGuardAnomaly             = "guard.anomaly"

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

	// guard 管理消息
	TypeGuardStatusRequest  = "guard.status.request"
	TypeGuardStatusResponse = "guard.status.response"
	TypeGuardStopRequest    = "guard.stop.request"
	TypeGuardStopResponse   = "guard.stop.response"

	// 通用错误响应
	MessageError = "error"
)

const (
	TargetOpenClaw    = "openclaw"
	TargetAuthProfile = "auth-profiles"

	KindOpenClaw    = "openclaw"
	KindAuthProfile = "auth-profiles"
	KindModels      = "models"

	WriteModeReject = "reject"
	WriteModeBlock  = "block"

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
	Target    string            `json:"target,omitempty"`
	TargetKey string            `json:"targetKey,omitempty"`
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

	Target string `json:"target,omitempty"`

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

	PipeName string            `json:"pipeName,omitempty"`
	Message  string            `json:"message,omitempty"`
	At       time.Time         `json:"at"`
	Data     map[string]string `json:"data,omitempty"`
}
