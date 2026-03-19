package runtime

import (
	"context"
	"errors"

	"openclaw-guard-kit/internal/protocol"
)

type EventBus struct {
	logger     Logger
	notifier   Notifier
	supervisor Supervisor
	robotHub   RobotHub
}

func NewEventBus(
	logger Logger,
	notifier Notifier,
	supervisor Supervisor,
	robotHub RobotHub,
) *EventBus {
	return &EventBus{
		logger:     logger,
		notifier:   notifier,
		supervisor: supervisor,
		robotHub:   robotHub,
	}
}

func (b *EventBus) Publish(ctx context.Context, event protocol.Event) error {
	if b.logger != nil {
		b.logger.Debug(
			"event publish",
			"type", event.Type,
			"target", event.Target,
			"targetKey", event.TargetKey,
		)
	}

	var errs []error

	if b.notifier != nil {
		if err := b.notifier.Notify(ctx, event); err != nil {
			errs = append(errs, err)
		}
	}

	if b.supervisor != nil {
		if err := b.supervisor.OnEvent(ctx, event); err != nil {
			errs = append(errs, err)
		}
	}

	if b.robotHub != nil {
		if err := b.robotHub.Broadcast(ctx, event); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}
