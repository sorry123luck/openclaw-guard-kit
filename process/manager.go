package process

import (
	"context"
	"errors"
	"fmt"

	"openclaw-guard-kit/internal/protocol"
	"openclaw-guard-kit/logging"
)

type Manager struct {
	logger      *logging.Logger
	supervisors []Supervisor
}

func NewManager(logger *logging.Logger, supervisors ...Supervisor) *Manager {
	filtered := make([]Supervisor, 0, len(supervisors))
	for _, supervisor := range supervisors {
		if supervisor == nil {
			continue
		}
		filtered = append(filtered, supervisor)
	}

	return &Manager{
		logger:      logger,
		supervisors: filtered,
	}
}

func (m *Manager) Register(supervisor Supervisor) {
	if m == nil || supervisor == nil {
		return
	}
	m.supervisors = append(m.supervisors, supervisor)
}

func (m *Manager) OnEvent(ctx context.Context, event protocol.Event) error {
	if m == nil {
		return nil
	}

	if len(m.supervisors) == 0 {
		if m.logger != nil {
			m.logger.Debug(
				"process manager skipped: no supervisors",
				"type", event.Type,
				"target", event.Target,
			)
		}
		return nil
	}

	var errs []error

	for i, supervisor := range m.supervisors {
		if supervisor == nil {
			continue
		}

		if err := supervisor.OnEvent(ctx, event); err != nil {
			wrapped := fmt.Errorf("supervisor[%d] failed: %w", i, err)
			errs = append(errs, wrapped)

			if m.logger != nil {
				m.logger.Error(
					"process manager dispatch failed",
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
				"process manager dispatched",
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
