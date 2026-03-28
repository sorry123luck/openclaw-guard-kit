package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

type PipeServerAdapter struct {
	mu      sync.Mutex
	name    string
	logger  adapterLogger
	runFn   func(context.Context) error
	cancel  context.CancelFunc
	doneCh  chan error
	started bool
}

func NewPipeServerAdapter(logger adapterLogger, name string, runFn func(context.Context) error) *PipeServerAdapter {
	if name == "" {
		name = "pipe-server"
	}
	return &PipeServerAdapter{
		name:   name,
		logger: logger,
		runFn:  runFn,
	}
}

func (a *PipeServerAdapter) Start(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.started {
		return nil
	}
	if a.runFn == nil {
		return fmt.Errorf("%s run function is nil", a.name)
	}

	runCtx, cancel := context.WithCancel(ctx)
	doneCh := make(chan error, 1)

	a.cancel = cancel
	a.doneCh = doneCh
	a.started = true

	go func() {
		err := a.runFn(runCtx)
		doneCh <- err
		close(doneCh)
	}()

	select {
	case err, ok := <-doneCh:
		a.cancel = nil
		a.doneCh = nil
		a.started = false

		if !ok || err == nil || errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	case <-time.After(80 * time.Millisecond):
		if a.logger != nil {
			a.logger.Info("pipe adapter started", "name", a.name)
		}
		return nil
	case <-ctx.Done():
		a.cancel = nil
		a.doneCh = nil
		a.started = false
		cancel()
		return ctx.Err()
	}
}

func (a *PipeServerAdapter) Stop(ctx context.Context) error {
	a.mu.Lock()
	if !a.started {
		a.mu.Unlock()
		return nil
	}

	cancel := a.cancel
	doneCh := a.doneCh

	a.cancel = nil
	a.doneCh = nil
	a.started = false
	a.mu.Unlock()

	if cancel != nil {
		cancel()
	}

	select {
	case err, ok := <-doneCh:
		if !ok || err == nil || errors.Is(err, context.Canceled) {
			if a.logger != nil {
				a.logger.Info("pipe adapter stopped", "name", a.name)
			}
			return nil
		}
		if a.logger != nil {
			a.logger.Error("pipe adapter stopped with error", "name", a.name, "error", err)
		}
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}
