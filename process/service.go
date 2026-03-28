package process

import (
	"context"

	"openclaw-guard-kit/internal/protocol"
	"openclaw-guard-kit/logging"
)

type Supervisor interface {
	OnEvent(context.Context, protocol.Event) error
}

type NoopSupervisor struct {
	logger *logging.Logger
}

func NewNoopSupervisor(logger *logging.Logger) *NoopSupervisor {
	return &NoopSupervisor{logger: logger}
}

func (s *NoopSupervisor) OnEvent(_ context.Context, event protocol.Event) error {
	if s == nil || s.logger == nil {
		return nil
	}

	s.logger.Debug(
		"process supervisor noop",
		"type", event.Type,
		"agent", event.AgentID,
		"target", event.Target,
		"targetKey", event.TargetKey,
		"kind", event.Kind,
		"path", event.Path,
	)

	return nil
}
