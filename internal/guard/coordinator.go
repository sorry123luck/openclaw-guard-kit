package guard

import (
	"context"

	"openclaw-guard-kit/internal/protocol"
)

type Logger interface {
	Debug(msg string, kv ...any)
	Info(msg string, kv ...any)
	Error(msg string, kv ...any)
}

type Dispatcher interface {
	Dispatch(context.Context, protocol.Event) error
}

type Coordinator struct {
	logger     Logger
	dispatcher Dispatcher
}

func NewCoordinator(
	logger Logger,
	dispatcher Dispatcher,
) *Coordinator {
	return &Coordinator{
		logger:     logger,
		dispatcher: dispatcher,
	}
}

func (c *Coordinator) Start(ctx context.Context) error {
	if c.logger != nil {
		c.logger.Info("guard coordinator started")
	}
	return c.HandleEvent(ctx, protocol.Event{
		Type:    "guard.coordinator.started",
		Message: "guard coordinator started",
	})
}

func (c *Coordinator) Stop(ctx context.Context) error {
	if c.logger != nil {
		c.logger.Info("guard coordinator stopping")
	}
	return c.HandleEvent(ctx, protocol.Event{
		Type:    "guard.coordinator.stopped",
		Message: "guard coordinator stopped",
	})
}

func (c *Coordinator) HandleEvent(ctx context.Context, event protocol.Event) error {
	if c.logger != nil {
		c.logger.Debug(
			"guard coordinator handling event",
			"type", event.Type,
			"target", event.Target,
			"targetKey", event.TargetKey,
		)
	}

	if c.dispatcher == nil {
		return nil
	}

	return c.dispatcher.Dispatch(ctx, event)
}
