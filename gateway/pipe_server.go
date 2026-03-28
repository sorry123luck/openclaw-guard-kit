//go:build windows

package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"openclaw-guard-kit/internal/protocol"
	"openclaw-guard-kit/logging"

	"github.com/Microsoft/go-winio"
)

func wrapPipeListenError(pipeName string, err error) error {
	if err == nil {
		return nil
	}
	if isPipeInUseError(err) {
		return fmt.Errorf("%w: %s: %v", ErrPipeInUse, pipeName, err)
	}
	return fmt.Errorf("listen pipe %s: %w", pipeName, err)
}

func isPipeInUseError(err error) bool {
	if err == nil {
		return false
	}

	s := strings.ToLower(err.Error())
	return strings.Contains(s, "access is denied") ||
		strings.Contains(s, "all pipe instances are busy") ||
		strings.Contains(s, "pipe busy")
}

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

func (s *PipeServer) Run(ctx context.Context) error {
	pipeName := s.cfg.ResolvePipeName()

	s.logger.Info("watch.starting", "pipe", pipeName)

	listener, err := winio.ListenPipe(pipeName, &winio.PipeConfig{
		MessageMode:      true,
		InputBufferSize:  64 * 1024,
		OutputBufferSize: 64 * 1024,
	})
	if err != nil {
		wrappedErr := wrapPipeListenError(pipeName, err)
		if isPipeInUseError(err) {
			s.logger.Error("watch.start.failed", "reason", "pipe_in_use", "pipe", pipeName, "err", err)
		} else {
			s.logger.Error("watch.start.failed", "reason", "pipe_listen_error", "pipe", pipeName, "err", err)
		}
		return wrappedErr
	}
	defer listener.Close()

	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()

	s.logger.Info("watch.started", "pipe", pipeName)
	defer s.logger.Info("watch.stopped", "pipe", pipeName)

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

	switch msg.Type {
	case protocol.TypeGuardStatusRequest:
		s.emit(ctx, protocol.Event{
			Type:    protocol.TypeGuardStatusRequest,
			AgentID: msg.AgentID,
			Message: "guard status requested",
			At:      time.Now().UTC(),
			Data: map[string]string{
				"requestId": msg.RequestID,
				"clientId":  msg.ClientID,
				"pipeName":  s.cfg.ResolvePipeName(),
			},
		})

		resp := protocol.Message{
			Type:      protocol.TypeGuardStatusResponse,
			Status:    protocol.StatusCompleted,
			RequestID: msg.RequestID,
			ClientID:  msg.ClientID,
			AgentID:   msg.AgentID,
			Message:   "guard is running",
			PipeName:  s.cfg.ResolvePipeName(),
			At:        time.Now().UTC(),
		}

		if err := json.NewEncoder(conn).Encode(resp); err != nil {
			if !errors.Is(err, io.EOF) {
				s.logger.Error("pipe response write failed", "error", err)
			}
			return
		}

		s.emit(ctx, protocol.Event{
			Type:    protocol.TypeGuardStatusResponse,
			AgentID: resp.AgentID,
			Message: resp.Message,
			At:      resp.At,
			Data: map[string]string{
				"requestId": resp.RequestID,
				"clientId":  resp.ClientID,
				"status":    resp.Status,
				"pipeName":  resp.PipeName,
			},
		})

		s.logger.Debug(
			"pipe response sent",
			"type", resp.Type,
			"status", resp.Status,
			"agent", resp.AgentID,
			"requestId", resp.RequestID,
			"clientId", resp.ClientID,
		)
		return

	case protocol.TypeGuardStopRequest:
		s.emit(ctx, protocol.Event{
			Type:    protocol.TypeGuardStopRequest,
			AgentID: msg.AgentID,
			Message: "guard stop requested",
			At:      time.Now().UTC(),
			Data: map[string]string{
				"requestId": msg.RequestID,
				"clientId":  msg.ClientID,
			},
		})

		resp := protocol.Message{
			Type:      protocol.TypeGuardStopResponse,
			Status:    protocol.StatusCompleted,
			RequestID: msg.RequestID,
			ClientID:  msg.ClientID,
			AgentID:   msg.AgentID,
			Message:   "guard stop requested",
			At:        time.Now().UTC(),
		}

		if err := json.NewEncoder(conn).Encode(resp); err != nil {
			if !errors.Is(err, io.EOF) {
				s.logger.Error("pipe response write failed", "error", err)
			}
			return
		}

		s.emit(ctx, protocol.Event{
			Type:    protocol.TypeGuardStopResponse,
			AgentID: resp.AgentID,
			Message: resp.Message,
			At:      resp.At,
			Data: map[string]string{
				"requestId": resp.RequestID,
				"clientId":  resp.ClientID,
				"status":    resp.Status,
			},
		})

		s.logger.Debug(
			"pipe response sent",
			"type", resp.Type,
			"status", resp.Status,
			"agent", resp.AgentID,
			"requestId", resp.RequestID,
			"clientId", resp.ClientID,
		)

		s.logger.Info("watch.stop.requested", "source", "pipe")

		if s.stopFunc != nil {
			go s.stopFunc()
		}
		return
	}

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

func (s *PipeServer) emit(ctx context.Context, event protocol.Event) {
	if s.dispatcher == nil {
		return
	}
	if err := s.dispatcher.Dispatch(ctx, event); err != nil && s.logger != nil {
		s.logger.Error(
			"pipe event dispatch failed",
			"type", event.Type,
			"target", event.Target,
			"targetKey", event.TargetKey,
			"error", err,
		)
	}
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
