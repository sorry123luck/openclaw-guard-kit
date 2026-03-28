package robot

import (
	"context"
	"errors"
	"sync"

	"openclaw-guard-kit/internal/protocol"
)

type Logger interface {
	Debug(msg string, kv ...any)
	Error(msg string, kv ...any)
}

type Manager struct {
	mu     sync.RWMutex
	logger Logger
	bots   []Bot
}

func NewManager(logger Logger) *Manager {
	return &Manager{
		logger: logger,
		bots:   make([]Bot, 0),
	}
}

func (m *Manager) Register(bot Bot) {
	if bot == nil {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.bots = append(m.bots, bot)

	if m.logger != nil {
		m.logger.Debug("robot registered", "name", bot.Name())
	}
}

func (m *Manager) StartAll(ctx context.Context) error {
	m.mu.RLock()
	bots := append([]Bot(nil), m.bots...)
	m.mu.RUnlock()

	var errs []error
	for _, bot := range bots {
		if bot == nil {
			continue
		}
		if err := bot.Start(ctx); err != nil {
			errs = append(errs, err)
			if m.logger != nil {
				m.logger.Error("robot start failed", "name", bot.Name(), "error", err)
			}
			continue
		}
		if m.logger != nil {
			m.logger.Debug("robot started", "name", bot.Name())
		}
	}

	return errors.Join(errs...)
}

func (m *Manager) StopAll(ctx context.Context) error {
	m.mu.RLock()
	bots := append([]Bot(nil), m.bots...)
	m.mu.RUnlock()

	var errs []error
	for i := len(bots) - 1; i >= 0; i-- {
		bot := bots[i]
		if bot == nil {
			continue
		}
		if err := bot.Stop(ctx); err != nil {
			errs = append(errs, err)
			if m.logger != nil {
				m.logger.Error("robot stop failed", "name", bot.Name(), "error", err)
			}
			continue
		}
		if m.logger != nil {
			m.logger.Debug("robot stopped", "name", bot.Name())
		}
	}

	return errors.Join(errs...)
}

func (m *Manager) Broadcast(ctx context.Context, event protocol.Event) error {
	m.mu.RLock()
	bots := append([]Bot(nil), m.bots...)
	m.mu.RUnlock()

	var errs []error
	for _, bot := range bots {
		if bot == nil {
			continue
		}
		if err := bot.Publish(ctx, event); err != nil {
			errs = append(errs, err)
			if m.logger != nil {
				m.logger.Error("robot publish failed", "name", bot.Name(), "type", event.Type, "error", err)
			}
			continue
		}
	}

	return errors.Join(errs...)
}
