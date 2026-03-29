package runtime

import (
	"context"
	"errors"
	"sync"
	"time"

	"openclaw-guard-kit/internal/protocol"
)

type Logger interface {
	Debug(msg string, kv ...any)
	Info(msg string, kv ...any)
	Error(msg string, kv ...any)
}

type PipeServer interface {
	Start(context.Context) error
	Stop(context.Context) error
}

type Watcher interface {
	Start(context.Context) error
	Stop(context.Context) error
}

type Notifier interface {
	Notify(context.Context, protocol.Event) error
}

type Supervisor interface {
	OnEvent(context.Context, protocol.Event) error
}

type RobotHub interface {
	StartAll(context.Context) error
	StopAll(context.Context) error
	Broadcast(context.Context, protocol.Event) error
}

type GuardCoordinator interface {
	HandleEvent(context.Context, protocol.Event) error
}

type Runtime struct {
	mu sync.RWMutex

	Config any
	Logger Logger

	PipeServer PipeServer
	Watcher    Watcher

	Notifier   Notifier
	Supervisor Supervisor
	RobotHub   RobotHub

	EventBus   *EventBus
	Dispatcher *Dispatcher

	GuardCoordinator GuardCoordinator
}

func New() *Runtime {
	return &Runtime{}
}

func (r *Runtime) SetConfig(cfg any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Config = cfg
}

func (r *Runtime) SetLogger(logger Logger) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Logger = logger
}

func (r *Runtime) SetEventBus(bus *EventBus) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.EventBus = bus
}

func (r *Runtime) SetDispatcher(dispatcher *Dispatcher) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Dispatcher = dispatcher
}

func (r *Runtime) Validate() error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if r.Logger == nil {
		return errors.New("runtime logger is nil")
	}
	if r.Notifier == nil {
		return errors.New("runtime notifier is nil")
	}
	if r.Supervisor == nil {
		return errors.New("runtime supervisor is nil")
	}
	if r.RobotHub == nil {
		return errors.New("runtime robot hub is nil")
	}
	if r.EventBus == nil {
		return errors.New("runtime event bus is nil")
	}
	if r.Dispatcher == nil {
		return errors.New("runtime dispatcher is nil")
	}
	if r.GuardCoordinator == nil {
		return errors.New("runtime guard coordinator is nil")
	}

	return nil
}

func (r *Runtime) Emit(ctx context.Context, event protocol.Event) error {
	r.mu.RLock()
	dispatcher := r.Dispatcher
	coordinator := r.GuardCoordinator
	logger := r.Logger
	r.mu.RUnlock()

	if event.At.IsZero() {
		event.At = time.Now().UTC()
	}

	if dispatcher != nil {
		return dispatcher.Dispatch(ctx, event)
	}

	if coordinator != nil {
		return coordinator.HandleEvent(ctx, event)
	}

	if logger != nil {
		fields := []any{
			"agent", event.AgentID,
			"target", event.Target,
			"targetKey", event.TargetKey,
			"kind", event.Kind,
			"path", event.Path,
			"message", event.Message,
		}

		if reason := event.Data["reason"]; reason != "" {
			fields = append(fields, "reason", reason)
		}
		if result := event.Data["result"]; result != "" {
			fields = append(fields, "result", result)
		}
		if stateFile := event.Data["stateFile"]; stateFile != "" {
			fields = append(fields, "stateFile", stateFile)
		}
		if baselineSha := event.Data["baselineSha"]; baselineSha != "" {
			fields = append(fields, "baselineSha", baselineSha)
		}
		if baselineSize := event.Data["baselineSize"]; baselineSize != "" {
			fields = append(fields, "baselineSize", baselineSize)
		}
		if errorMessage := event.Data["error"]; errorMessage != "" {
			fields = append(fields, "error", errorMessage)
		}

		logger.Info(string(event.Type), fields...)
	}

	if dispatcher != nil {
		if err := dispatcher.Dispatch(ctx, event); err != nil && logger != nil {
			logger.Error(
				"dispatch failed",
				"agent", event.AgentID,
				"targetKey", event.TargetKey,
				"kind", event.Kind,
				"path", event.Path,
				"error", err,
			)
		}
	}

	return nil
}
