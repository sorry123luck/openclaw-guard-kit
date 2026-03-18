package watch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"openclaw-guard-kit/backup"
	"openclaw-guard-kit/config"
	"openclaw-guard-kit/gateway"
	"openclaw-guard-kit/internal/protocol"
	"openclaw-guard-kit/logging"
	"openclaw-guard-kit/notify"
	"openclaw-guard-kit/process"
)

type LeaseInspector interface {
	HasActiveLease(target, path, agentID string) bool
}

type Service struct {
	logger         *logging.Logger
	notifier       notify.Notifier
	publisher      gateway.Publisher
	supervisor     process.Supervisor
	backupSvc      *backup.Service
	leaseInspector LeaseInspector
}

func NewService(
	logger *logging.Logger,
	notifier notify.Notifier,
	publisher gateway.Publisher,
	supervisor process.Supervisor,
	backupSvc *backup.Service,
	leaseInspector ...LeaseInspector,
) *Service {
	svc := &Service{
		logger:     logger,
		notifier:   notifier,
		publisher:  publisher,
		supervisor: supervisor,
		backupSvc:  backupSvc,
	}

	if len(leaseInspector) > 0 {
		svc.leaseInspector = leaseInspector[0]
	}

	return svc
}

func (s *Service) SetLeaseInspector(inspector LeaseInspector) {
	s.leaseInspector = inspector
}

func (s *Service) Run(ctx context.Context, cfg config.AppConfig) error {
	if s.backupSvc == nil {
		return errors.New("backup service is nil")
	}

	manifest, err := backup.LoadManifest(cfg.StateFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) && cfg.AutoPrepare {
			if s.logger != nil {
				s.logger.Info(
					"manifest missing; auto prepare baseline",
					"agent", cfg.AgentID,
					"state", cfg.StateFile,
				)
			}
			manifest, err = s.backupSvc.Prepare(ctx, cfg)
		}
		if err != nil {
			return err
		}
	}

	startEvent := protocol.Event{
		Type:    protocol.EventWatchStarted,
		AgentID: cfg.AgentID,
		Message: "watch loop started",
		At:      time.Now().UTC(),
		Data: map[string]string{
			"agentId":         cfg.AgentID,
			"intervalSeconds": fmt.Sprintf("%d", cfg.PollIntervalSeconds),
			"targets":         fmt.Sprintf("%d", len(manifest.Targets)),
			"stateFile":       cfg.StateFile,
		},
	}
	s.emit(ctx, cfg, startEvent)

	ticker := time.NewTicker(time.Duration(cfg.PollIntervalSeconds) * time.Second)
	defer ticker.Stop()

	defer s.emit(ctx, cfg, protocol.Event{
		Type:    protocol.EventWatchStopped,
		AgentID: cfg.AgentID,
		Message: "watch loop stopped",
		At:      time.Now().UTC(),
		Data: map[string]string{
			"agentId":   cfg.AgentID,
			"stateFile": cfg.StateFile,
		},
	})

	for {
		if err := s.scanOnce(ctx, cfg); err != nil {
			if s.logger != nil {
				s.logger.Error(
					"watch scan failed",
					"agent", cfg.AgentID,
					"error", err,
				)
			}
		}

		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (s *Service) scanOnce(ctx context.Context, cfg config.AppConfig) error {
	manifest, err := backup.LoadManifest(cfg.StateFile)
	if err != nil {
		return err
	}

	for _, snapshot := range manifest.Targets {
		effectiveAgentID := snapshot.AgentID
		if effectiveAgentID == "" {
			effectiveAgentID = cfg.AgentID
		}

		if s.hasActiveLease(snapshot, effectiveAgentID) {
			continue
		}

		drift, reason, err := compareToBaseline(snapshot)
		if err != nil {
			return err
		}
		if !drift {
			continue
		}

		driftEvent := protocol.Event{
			Type:      protocol.EventDriftDetected,
			AgentID:   effectiveAgentID,
			Target:    snapshot.Name,
			TargetKey: snapshot.TargetKey,
			Kind:      snapshot.Kind,
			Path:      snapshot.SourcePath,
			Message:   reason,
			At:        time.Now().UTC(),
			Data: map[string]string{
				"agentId": effectiveAgentID,
			},
		}
		s.emit(ctx, cfg, driftEvent)

		shouldRestore := (reason == "deleted" && cfg.RestoreOnDelete) || (reason != "deleted" && cfg.RestoreOnChange)
		if !shouldRestore {
			continue
		}

		if err := s.backupSvc.Restore(snapshot); err != nil {
			s.emit(ctx, cfg, protocol.Event{
				Type:      protocol.EventRestoreFailed,
				AgentID:   effectiveAgentID,
				Target:    snapshot.Name,
				TargetKey: snapshot.TargetKey,
				Kind:      snapshot.Kind,
				Path:      snapshot.SourcePath,
				Message:   err.Error(),
				At:        time.Now().UTC(),
				Data: map[string]string{
					"agentId": effectiveAgentID,
				},
			})
			continue
		}

		s.emit(ctx, cfg, protocol.Event{
			Type:      protocol.EventRestoreCompleted,
			AgentID:   effectiveAgentID,
			Target:    snapshot.Name,
			TargetKey: snapshot.TargetKey,
			Kind:      snapshot.Kind,
			Path:      snapshot.SourcePath,
			Message:   "baseline restored",
			At:        time.Now().UTC(),
			Data: map[string]string{
				"agentId": effectiveAgentID,
			},
		})
	}

	return nil
}

func (s *Service) hasActiveLease(snapshot backup.Snapshot, agentID string) bool {
	if s.leaseInspector == nil {
		return false
	}

	// 优先走 path 推断，最准确。
	if snapshot.SourcePath != "" && s.leaseInspector.HasActiveLease("", snapshot.SourcePath, agentID) {
		return true
	}

	if snapshot.TargetKey != "" && s.leaseInspector.HasActiveLease(snapshot.TargetKey, "", agentID) {
		return true
	}

	if snapshot.Name != "" && s.leaseInspector.HasActiveLease(snapshot.Name, "", agentID) {
		return true
	}

	return false
}

func compareToBaseline(snapshot backup.Snapshot) (bool, string, error) {
	sha, size, _, _, err := backup.Fingerprint(snapshot.SourcePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return true, "deleted", nil
		}
		return false, "", err
	}

	if sha != snapshot.SHA256 || size != snapshot.Size {
		return true, "content changed", nil
	}

	return false, "", nil
}

func (s *Service) emit(ctx context.Context, cfg config.AppConfig, event protocol.Event) {
	if event.AgentID == "" {
		event.AgentID = cfg.AgentID
	}

	if event.Data == nil {
		event.Data = map[string]string{}
	}
	if _, ok := event.Data["agentId"]; !ok {
		event.Data["agentId"] = event.AgentID
	}
	if event.TargetKey != "" {
		if _, ok := event.Data["targetKey"]; !ok {
			event.Data["targetKey"] = event.TargetKey
		}
	}
	if event.Kind != "" {
		if _, ok := event.Data["kind"]; !ok {
			event.Data["kind"] = event.Kind
		}
	}

	if s.logger != nil {
		s.logger.Info(
			event.Type,
			"agent", event.AgentID,
			"target", event.Target,
			"targetKey", event.TargetKey,
			"kind", event.Kind,
			"path", event.Path,
			"message", event.Message,
		)
	}

	if s.notifier != nil {
		if err := s.notifier.Notify(ctx, event); err != nil && s.logger != nil {
			s.logger.Error(
				"notify failed",
				"agent", event.AgentID,
				"error", err,
			)
		}
	}

	if s.publisher != nil {
		if err := s.publisher.Publish(ctx, event); err != nil && s.logger != nil {
			s.logger.Error(
				"publish failed",
				"agent", event.AgentID,
				"error", err,
			)
		}
	}

	if s.supervisor != nil {
		if err := s.supervisor.OnEvent(ctx, event); err != nil && s.logger != nil {
			s.logger.Error(
				"process hook failed",
				"agent", event.AgentID,
				"error", err,
			)
		}
	}
}
