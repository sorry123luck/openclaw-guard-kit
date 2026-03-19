package process

import (
	"context"

	"openclaw-guard-kit/internal/protocol"
)

type Logger interface {
	Debug(msg string, kv ...any)
}

type Supervisor interface {
	OnEvent(context.Context, protocol.Event) error
}

type NoopSupervisor struct {
	logger Logger
}

func NewNoopSupervisor(logger Logger) *NoopSupervisor {
	return &NoopSupervisor{logger: logger}
}

func (s *NoopSupervisor) OnEvent(_ context.Context, event protocol.Event) error {
	if s.logger != nil {
		s.logger.Debug(
			"process supervisor noop",
			"type", event.Type,
			"target", event.Target,
		)
	}
	return nil
}
