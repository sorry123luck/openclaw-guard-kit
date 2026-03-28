package notify

import (
	"context"
	"errors"
	"fmt"

	"openclaw-guard-kit/internal/protocol"
	"openclaw-guard-kit/logging"
)

type Manager struct {
	logger    *logging.Logger
	notifiers []Notifier
}

func NewManager(logger *logging.Logger, notifiers ...Notifier) *Manager {
	filtered := make([]Notifier, 0, len(notifiers))
	for _, notifier := range notifiers {
		if notifier == nil {
			continue
		}
		filtered = append(filtered, notifier)
	}

	return &Manager{
		logger:    logger,
		notifiers: filtered,
	}
}

func NewMultiNotifier(logger *logging.Logger, notifiers ...Notifier) *Manager {
	return NewManager(logger, notifiers...)
}

func (m *Manager) Register(notifier Notifier) {
	if m == nil || notifier == nil {
		return
	}
	m.notifiers = append(m.notifiers, notifier)
}

func (m *Manager) Notify(ctx context.Context, event protocol.Event) error {
	if m == nil {
		return nil
	}

	// 全局静默过滤：内部探活/状态查询事件，不允许进入任何通知渠道
	if shouldSkipAllChannelDispatch(event.Type) {
		if m.logger != nil {
			m.logger.Debug(
				"notify manager skipped quiet event",
				"type", event.Type,
				"target", event.Target,
			)
		}
		return nil
	}

	if len(m.notifiers) == 0 {
		if m.logger != nil {
			m.logger.Debug(
				"notify manager skipped: no notifiers",
				"type", event.Type,
				"target", event.Target,
			)
		}
		return nil
	}

	var errs []error

	for i, notifier := range m.notifiers {
		if notifier == nil {
			continue
		}

		if err := notifier.Notify(ctx, event); err != nil {
			wrapped := fmt.Errorf("notifier[%d] failed: %w", i, err)
			errs = append(errs, wrapped)

			if m.logger != nil {
				m.logger.Error(
					"notify manager dispatch failed",
					"index", i,
					"type", event.Type,
					"target", event.Target,
					"error", err,
				)
			}
			continue
		}

		if m.logger != nil {
			m.logger.Debug(
				"notify manager dispatched",
				"index", i,
				"type", event.Type,
				"target", event.Target,
			)
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
}

func shouldSkipAllChannelDispatch(eventType string) bool {
	switch eventType {
	case protocol.TypeGuardStatusRequest, protocol.TypeGuardStatusResponse:
		return true
	default:
		return false
	}
}
