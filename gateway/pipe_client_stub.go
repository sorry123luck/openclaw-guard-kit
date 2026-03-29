//go:build !windows

package gateway

import (
	"context"
	"fmt"

	"openclaw-guard-kit/internal/protocol"
	"openclaw-guard-kit/logging"
)

type PipeClient struct {
	logger *logging.Logger
	cfg    PipeConfig
}

func NewPipeClient(logger *logging.Logger, cfg PipeConfig) *PipeClient {
	return &PipeClient{
		logger: logger,
		cfg:    cfg,
	}
}

func (c *PipeClient) Request(context.Context, protocol.Message) (protocol.Message, error) {
	return protocol.Message{}, fmt.Errorf("named pipe client is only supported on Windows")
}

func (c *PipeClient) Status(ctx context.Context) (protocol.Message, error) {
	return c.Request(ctx, protocol.Message{
		Type: protocol.TypeGuardStatusRequest,
	})
}

func (c *PipeClient) Stop(ctx context.Context) (protocol.Message, error) {
	return c.Request(ctx, protocol.Message{
		Type: protocol.TypeGuardStopRequest,
	})
}
