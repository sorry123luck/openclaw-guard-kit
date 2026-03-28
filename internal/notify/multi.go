package notify

import (
	"context"
	"errors"
	"sync"

	"openclaw-guard-kit/internal/protocol"
	"openclaw-guard-kit/logging"
)

type Notifier interface {
	Notify(context.Context, protocol.Event) error
}

type MultiNotifier struct {
	logger    *logging.Logger
	mu        sync.RWMutex
	notifiers []Notifier
}

func NewMultiNotifier(logger *logging.Logger, notifiers ...Notifier) *MultiNotifier {
	m := &MultiNotifier{
		logger: logger,
	}
	for _, n := range notifiers {
		if n != nil {
			m.notifiers = append(m.notifiers, n)
		}
	}
	return m
}

func (m *MultiNotifier) Add(n Notifier) {
	if n == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.notifiers = append(m.notifiers, n)
}

func (m *MultiNotifier) Notify(ctx context.Context, event protocol.Event) error {
	if shouldSkipAllChannelDispatch(event.Type) {
		if m.logger != nil {
			m.logger.Debug(
				"notifier skipped quiet event",
				"type", event.Type,
				"target", event.Target,
			)
		}
		return nil
	}

	m.mu.RLock()
	items := make([]Notifier, 0, len(m.notifiers))
	items = append(items, m.notifiers...)
	m.mu.RUnlock()

	var errs []error
	for _, n := range items {
		if n == nil {
			continue
		}
		if err := n.Notify(ctx, event); err != nil {
			errs = append(errs, err)
			if m.logger != nil {
				m.logger.Error(
					"notifier failed",
					"type", event.Type,
					"target", event.Target,
					"error", err,
				)
			}
		}
	}
	return errors.Join(errs...)
}

func shouldSkipAllChannelDispatch(eventType string) bool {
	switch eventType {
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
