package app

import (
	"context"
	"fmt"

	"openclaw-guard-kit/internal/bootstrap"
	"openclaw-guard-kit/internal/runtime"
	"openclaw-guard-kit/internal/service"
	"openclaw-guard-kit/logging"
)

type App struct {
	logger  *logging.Logger
	runtime *runtime.Runtime
	service *service.Service
}

func New(cfg any, logger *logging.Logger, deps bootstrap.Dependencies) (*App, error) {
	if logger == nil {
		return nil, fmt.Errorf("app logger is nil")
	}

	rt, err := bootstrap.BuildRuntime(cfg, logger, deps)
	if err != nil {
		return nil, fmt.Errorf("build runtime failed: %w", err)
	}

	svc, err := service.New(rt)
	if err != nil {
		return nil, fmt.Errorf("create service failed: %w", err)
	}

	return &App{
		logger:  logger,
		runtime: rt,
		service: svc,
	}, nil
}

func (a *App) Run(ctx context.Context) error {
	if a == nil {
		return fmt.Errorf("app is nil")
	}
	if a.service == nil {
		return fmt.Errorf("app service is nil")
	}

	if a.logger != nil {
		a.logger.Info("app running")
	}

	return a.service.Run(ctx)
}

func (a *App) Runtime() *runtime.Runtime {
	if a == nil {
		return nil
	}
	return a.runtime
}

func (a *App) Service() *service.Service {
	if a == nil {
		return nil
	}
	return a.service
}
