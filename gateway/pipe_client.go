//go:build windows

package gateway

import (
	"context"
	"encoding/json"
	"time"

	"openclaw-guard-kit/internal/protocol"
	"openclaw-guard-kit/logging"

	"github.com/Microsoft/go-winio"
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

func (c *PipeClient) Request(ctx context.Context, msg protocol.Message) (protocol.Message, error) {
	pipeName := c.cfg.ResolvePipeName()

	conn, err := winio.DialPipeContext(ctx, pipeName)
	if err != nil {
		return protocol.Message{}, err
	}
	defer conn.Close()

	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))

	if msg.At.IsZero() {
		msg.At = time.Now().UTC()
	}
	if msg.TargetKey == "" && msg.Target != "" {
		msg.TargetKey = msg.Target
	}
	if msg.Mode == "" {
		msg.Mode = protocol.WriteModeReject
	}

	c.logger.Debug(
		"pipe request sending",
		"pipe", pipeName,
		"type", msg.Type,
		"agent", msg.AgentID,
		"target", msg.Target,
		"targetKey", msg.TargetKey,
		"kind", msg.Kind,
		"path", msg.Path,
		"requestId", msg.RequestID,
		"leaseId", msg.LeaseID,
		"clientId", msg.ClientID,
		"mode", msg.Mode,
	)

	if err := json.NewEncoder(conn).Encode(msg); err != nil {
		return protocol.Message{}, err
	}

	var resp protocol.Message
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return protocol.Message{}, err
	}

	if resp.At.IsZero() {
		resp.At = time.Now().UTC()
	}
	if resp.TargetKey == "" && resp.Target != "" {
		resp.TargetKey = resp.Target
	}

	c.logger.Debug(
		"pipe response received",
		"pipe", pipeName,
		"type", resp.Type,
		"status", resp.Status,
		"agent", resp.AgentID,
		"target", resp.Target,
		"targetKey", resp.TargetKey,
		"kind", resp.Kind,
		"path", resp.Path,
		"requestId", resp.RequestID,
		"leaseId", resp.LeaseID,
		"clientId", resp.ClientID,
		"queuePosition", resp.QueuePosition,
	)

	return resp, nil
}

func (c *PipeClient) RequestWrite(ctx context.Context, msg protocol.Message) (protocol.Message, error) {
	msg.Type = protocol.MessageWriteRequest
	if msg.Mode == "" {
		msg.Mode = protocol.WriteModeReject
	}
	return c.Request(ctx, msg)
}

func (c *PipeClient) CompleteWrite(ctx context.Context, msg protocol.Message) (protocol.Message, error) {
	msg.Type = protocol.MessageWriteCompleted
	return c.Request(ctx, msg)
}

func (c *PipeClient) FailWrite(ctx context.Context, msg protocol.Message) (protocol.Message, error) {
	msg.Type = protocol.MessageWriteFailed
	return c.Request(ctx, msg)
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
