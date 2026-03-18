package notify

import (
	"context"

	"openclaw-guard-kit/internal/protocol"
	"openclaw-guard-kit/logging"
)

type Notifier interface {
	Notify(context.Context, protocol.Event) error
}

type LogNotifier struct {
	logger *logging.Logger
}

func NewLogNotifier(logger *logging.Logger) *LogNotifier {
	return &LogNotifier{logger: logger}
}

func (n *LogNotifier) Notify(_ context.Context, event protocol.Event) error {
	n.logger.Debug(
		"notify",
		"type", event.Type,
		"agent", event.AgentID,
		"target", event.Target,
		"targetKey", event.TargetKey,
		"kind", event.Kind,
		"path", event.Path,
		"message", event.Message,
	)
	return nil
}
