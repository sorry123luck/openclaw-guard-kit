//go:build !windows

package gateway

import (
	"context"
	"fmt"

	"openclaw-guard-kit/internal/protocol"
	"openclaw-guard-kit/logging"
)

type EventDispatcher interface {
	Dispatch(context.Context, protocol.Event) error
}

type PipeServer struct {
	logger     *logging.Logger
	handler    RequestHandler
	dispatcher EventDispatcher
	cfg        PipeConfig
	stopFunc   func()
}

func NewPipeServer(
	logger *logging.Logger,
	handler RequestHandler,
	dispatcher EventDispatcher,
	cfg PipeConfig,
) *PipeServer {
	return &PipeServer{
		logger:     logger,
		handler:    handler,
		dispatcher: dispatcher,
		cfg:        cfg,
		stopFunc:   cfg.StopFunc,
	}
}

func (s *PipeServer) Run(context.Context) error {
	return fmt.Errorf("named pipe server is only supported on Windows")
}
