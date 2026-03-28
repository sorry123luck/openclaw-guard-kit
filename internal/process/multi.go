package process

import (
	"context"
	"errors"
	"sync"

	"openclaw-guard-kit/internal/protocol"
	"openclaw-guard-kit/logging"
)

type MultiSupervisor struct {
	logger      *logging.Logger
	mu          sync.RWMutex
	supervisors []Supervisor
}

func NewMultiSupervisor(logger *logging.Logger, supervisors ...Supervisor) *MultiSupervisor {
	m := &MultiSupervisor{
		logger: logger,
	}
	for _, s := range supervisors {
		if s != nil {
			m.supervisors = append(m.supervisors, s)
		}
	}
	return m
}

func (m *MultiSupervisor) Add(s Supervisor) {
	if s == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.supervisors = append(m.supervisors, s)
}

func (m *MultiSupervisor) OnEvent(ctx context.Context, event protocol.Event) error {
	m.mu.RLock()
	items := make([]Supervisor, 0, len(m.supervisors))
	items = append(items, m.supervisors...)
	m.mu.RUnlock()

	var errs []error
	for _, s := range items {
		if s == nil {
			continue
		}
		if err := s.OnEvent(ctx, event); err != nil {
			errs = append(errs, err)
			if m.logger != nil {
				m.logger.Error(
					"supervisor failed",
					"type", event.Type,
					"target", event.Target,
					"error", err,
				)
			}
		}
	}
	return errors.Join(errs...)
}
