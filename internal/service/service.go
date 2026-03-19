package service

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"openclaw-guard-kit/internal/protocol"
	"openclaw-guard-kit/internal/runtime"
)

type coordinatorLifecycle interface {
	Start(context.Context) error
	Stop(context.Context) error
}

type Service struct {
	mu    sync.RWMutex
	rt    *runtime.Runtime
	state State
}

func New(rt *runtime.Runtime) (*Service, error) {
	if rt == nil {
		return nil, errors.New("service runtime is nil")
	}
	if err := rt.Validate(); err != nil {
		return nil, fmt.Errorf("service runtime validate failed: %w", err)
	}

	return &Service{
		rt:    rt,
		state: StateStopped,
	}, nil
}

func (s *Service) State() State {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state
}

func (s *Service) setState(state State) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = state
}

func (s *Service) emit(ctx context.Context, event protocol.Event) {
	if s == nil || s.rt == nil {
		return
	}
	if err := s.rt.Emit(ctx, event); err != nil && s.rt.Logger != nil {
		s.rt.Logger.Error(
			"service emit failed",
			"type", event.Type,
			"target", event.Target,
			"targetKey", event.TargetKey,
			"error", err,
		)
	}
}

func (s *Service) Start(ctx context.Context) error {
	switch s.State() {
	case StateRunning, StateInitializing:
		return nil
	}

	s.setState(StateInitializing)

	if s.rt.Logger != nil {
		s.rt.Logger.Info("service starting")
	}

	s.emit(ctx, protocol.Event{
		Type:    protocol.EventServiceStarting,
		Message: "service starting",
	})

	started := make([]func(context.Context) error, 0, 4)

	if s.rt.PipeServer != nil {
		if err := s.rt.PipeServer.Start(ctx); err != nil {
			s.setState(StateStopped)
			return fmt.Errorf("start pipe server failed: %w", err)
		}
		started = append(started, func(stopCtx context.Context) error {
			return s.rt.PipeServer.Stop(stopCtx)
		})
	}

	if s.rt.Watcher != nil {
		if err := s.rt.Watcher.Start(ctx); err != nil {
			_ = s.stopStarted(ctx, started)
			s.setState(StateStopped)
			return fmt.Errorf("start watcher failed: %w", err)
		}
		started = append(started, func(stopCtx context.Context) error {
			return s.rt.Watcher.Stop(stopCtx)
		})
	}

	if s.rt.RobotHub != nil {
		if err := s.rt.RobotHub.StartAll(ctx); err != nil {
			_ = s.stopStarted(ctx, started)
			s.setState(StateStopped)
			return fmt.Errorf("start robot hub failed: %w", err)
		}
		started = append(started, func(stopCtx context.Context) error {
			return s.rt.RobotHub.StopAll(stopCtx)
		})
	}

	if lifecycle, ok := s.rt.GuardCoordinator.(coordinatorLifecycle); ok {
		if err := lifecycle.Start(ctx); err != nil {
			_ = s.stopStarted(ctx, started)
			s.setState(StateStopped)
			return fmt.Errorf("start guard coordinator failed: %w", err)
		}
		started = append(started, func(stopCtx context.Context) error {
			return lifecycle.Stop(stopCtx)
		})
	}

	s.setState(StateRunning)

	if s.rt.Logger != nil {
		s.rt.Logger.Info("service started")
	}

	s.emit(ctx, protocol.Event{
		Type:    protocol.EventServiceStarted,
		Message: "service started",
	})

	return nil
}

func (s *Service) Run(ctx context.Context) error {
	if err := s.Start(ctx); err != nil {
		return err
	}

	<-ctx.Done()

	if s.rt.Logger != nil {
		s.rt.Logger.Info("service shutting down", "reason", ctx.Err())
	}

	stopCtx := context.Background()
	if err := s.Stop(stopCtx); err != nil {
		return err
	}

	return nil
}

func (s *Service) Stop(ctx context.Context) error {
	switch s.State() {
	case StateStopped, StateStopping:
		return nil
	}

	s.setState(StateStopping)

	if s.rt.Logger != nil {
		s.rt.Logger.Info("service stopping")
	}

	s.emit(ctx, protocol.Event{
		Type:    protocol.EventServiceStopping,
		Message: "service stopping",
	})

	var errs []error

	if lifecycle, ok := s.rt.GuardCoordinator.(coordinatorLifecycle); ok {
		if err := lifecycle.Stop(ctx); err != nil {
			errs = append(errs, fmt.Errorf("stop guard coordinator failed: %w", err))
		}
	}

	if s.rt.RobotHub != nil {
		if err := s.rt.RobotHub.StopAll(ctx); err != nil {
			errs = append(errs, fmt.Errorf("stop robot hub failed: %w", err))
		}
	}

	if s.rt.Watcher != nil {
		if err := s.rt.Watcher.Stop(ctx); err != nil {
			errs = append(errs, fmt.Errorf("stop watcher failed: %w", err))
		}
	}

	if s.rt.PipeServer != nil {
		if err := s.rt.PipeServer.Stop(ctx); err != nil {
			errs = append(errs, fmt.Errorf("stop pipe server failed: %w", err))
		}
	}

	s.setState(StateStopped)

	if s.rt.Logger != nil {
		if len(errs) > 0 {
			s.rt.Logger.Error("service stopped with errors", "count", len(errs))
		} else {
			s.rt.Logger.Info("service stopped")
		}
	}

	s.emit(ctx, protocol.Event{
		Type:    protocol.EventServiceStopped,
		Message: "service stopped",
	})

	return errors.Join(errs...)
}

func (s *Service) stopStarted(ctx context.Context, started []func(context.Context) error) error {
	var errs []error

	for i := len(started) - 1; i >= 0; i-- {
		if err := started[i](ctx); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}
