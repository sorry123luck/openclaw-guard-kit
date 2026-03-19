package runtime

import (
	"context"
	"errors"
	"sync"

	"openclaw-guard-kit/internal/protocol"
)

type EventHandler func(context.Context, protocol.Event) error

type Dispatcher struct {
	logger Logger
	bus    *EventBus

	mu     sync.RWMutex
	global []EventHandler
	typed  map[string][]EventHandler
}

func NewDispatcher(logger Logger, bus *EventBus) *Dispatcher {
	return &Dispatcher{
		logger: logger,
		bus:    bus,
		typed:  make(map[string][]EventHandler),
	}
}

func (d *Dispatcher) Use(handler EventHandler) {
	if handler == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.global = append(d.global, handler)
}

func (d *Dispatcher) On(eventType string, handler EventHandler) {
	if eventType == "" || handler == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.typed[eventType] = append(d.typed[eventType], handler)
}

func (d *Dispatcher) Dispatch(ctx context.Context, event protocol.Event) error {
	var errs []error

	if d.bus != nil {
		if err := d.bus.Publish(ctx, event); err != nil {
			errs = append(errs, err)
		}
	}

	d.mu.RLock()
	globalHandlers := append([]EventHandler(nil), d.global...)
	typeHandlers := append([]EventHandler(nil), d.typed[event.Type]...)
	d.mu.RUnlock()

	for _, handler := range globalHandlers {
		if handler == nil {
			continue
		}
		if err := handler(ctx, event); err != nil {
			errs = append(errs, err)
			if d.logger != nil {
				d.logger.Error(
					"global event handler failed",
					"type", event.Type,
					"target", event.Target,
					"targetKey", event.TargetKey,
					"error", err,
				)
			}
		}
	}

	for _, handler := range typeHandlers {
		if handler == nil {
			continue
		}
		if err := handler(ctx, event); err != nil {
			errs = append(errs, err)
			if d.logger != nil {
				d.logger.Error(
					"typed event handler failed",
					"type", event.Type,
					"target", event.Target,
					"targetKey", event.TargetKey,
					"error", err,
				)
			}
		}
	}

	return errors.Join(errs...)
}
