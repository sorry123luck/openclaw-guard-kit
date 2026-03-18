//go:build windows

package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"time"

	"openclaw-guard-kit/internal/protocol"
	"openclaw-guard-kit/logging"

	"github.com/Microsoft/go-winio"
)

type PipeServer struct {
	logger  *logging.Logger
	handler RequestHandler
	cfg     PipeConfig
}

func NewPipeServer(logger *logging.Logger, handler RequestHandler, cfg PipeConfig) *PipeServer {
	return &PipeServer{
		logger:  logger,
		handler: handler,
		cfg:     cfg,
	}
}

func (s *PipeServer) Run(ctx context.Context) error {
	pipeName := s.cfg.ResolvePipeName()

	listener, err := winio.ListenPipe(pipeName, &winio.PipeConfig{
		MessageMode:      true,
		InputBufferSize:  64 * 1024,
		OutputBufferSize: 64 * 1024,
	})
	if err != nil {
		return err
	}
	defer listener.Close()

	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()

	s.logger.Info("pipe server started", "pipe", pipeName)
	defer s.logger.Info("pipe server stopped", "pipe", pipeName)

	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil || isClosedListenerError(err) {
				return nil
			}

			s.logger.Error("pipe accept failed", "pipe", pipeName, "error", err)
			continue
		}

		go s.handleConn(ctx, conn)
	}
}

func (s *PipeServer) handleConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()

	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("pipe connection panic", "panic", r)
		}
	}()

	_ = conn.SetDeadline(time.Now().Add(5 * time.Minute))

	var msg protocol.Message
	if err := json.NewDecoder(conn).Decode(&msg); err != nil {
		s.logger.Error("pipe decode failed", "error", err)

		_ = json.NewEncoder(conn).Encode(protocol.Message{
			Type:    protocol.MessageError,
			Status:  protocol.StatusError,
			Message: "invalid request payload",
			At:      time.Now().UTC(),
		})
		return
	}

	if msg.TargetKey == "" && msg.Target != "" {
		msg.TargetKey = msg.Target
	}
	if msg.Mode == "" {
		msg.Mode = protocol.WriteModeReject
	}

	s.logger.Debug(
		"pipe request received",
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

	resp, err := s.handler.HandleMessage(ctx, msg)
	if err != nil {
		s.logger.Error(
			"pipe request handling failed",
			"type", msg.Type,
			"agent", msg.AgentID,
			"target", msg.Target,
			"targetKey", msg.TargetKey,
			"kind", msg.Kind,
			"path", msg.Path,
			"requestId", msg.RequestID,
			"leaseId", msg.LeaseID,
			"clientId", msg.ClientID,
			"error", err,
		)

		resp = protocol.Message{
			Type:      protocol.MessageError,
			Status:    protocol.StatusError,
			RequestID: msg.RequestID,
			LeaseID:   msg.LeaseID,
			ClientID:  msg.ClientID,
			AgentID:   msg.AgentID,
			Target:    msg.Target,
			TargetKey: msg.TargetKey,
			Kind:      msg.Kind,
			Path:      msg.Path,
			Message:   err.Error(),
			At:        time.Now().UTC(),
		}
	}

	if resp.At.IsZero() {
		resp.At = time.Now().UTC()
	}
	if resp.TargetKey == "" && resp.Target != "" {
		resp.TargetKey = resp.Target
	}

	if err := json.NewEncoder(conn).Encode(resp); err != nil {
		if !errors.Is(err, io.EOF) {
			s.logger.Error("pipe response write failed", "error", err)
		}
		return
	}

	s.logger.Debug(
		"pipe response sent",
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
}

func isClosedListenerError(err error) bool {
	if err == nil {
		return false
	}

	text := strings.ToLower(err.Error())
	return strings.Contains(text, "use of closed network connection") ||
		strings.Contains(text, "file has already been closed") ||
		strings.Contains(text, "pipe has been ended")
}
