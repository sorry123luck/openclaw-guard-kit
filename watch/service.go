package watch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"openclaw-guard-kit/backup"
	"openclaw-guard-kit/config"
	"openclaw-guard-kit/internal/protocol"
	"openclaw-guard-kit/logging"
)

type LeaseInspector interface {
	HasActiveLease(target, path, agentID string) bool
}

type EventDispatcher interface {
	Dispatch(context.Context, protocol.Event) error
}

type Service struct {
	logger         *logging.Logger
	dispatcher     EventDispatcher
	backupSvc      *backup.Service
	leaseInspector LeaseInspector
}

func NewService(
	logger *logging.Logger,
	dispatcher EventDispatcher,
	backupSvc *backup.Service,
	leaseInspector ...LeaseInspector,
) *Service {
	svc := &Service{
		logger:     logger,
		dispatcher: dispatcher,
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
					"stateFile", cfg.StateFile,
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
					"stateFile", cfg.StateFile,
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

		drift, reason, err := compareToBaseline(snapshot)
		if err != nil {
			return fmt.Errorf("compare target %q at %q: %w", snapshot.Name, snapshot.SourcePath, err)
		}
		if !drift {
			continue
		}

		if s.hasActiveLease(snapshot, effectiveAgentID) {
			if s.logger != nil {
				s.logger.Info(
					"drift detected but skipped due to active lease",
					"agent", effectiveAgentID,
					"target", snapshot.Name,
					"targetKey", snapshot.TargetKey,
					"kind", snapshot.Kind,
					"path", snapshot.SourcePath,
					"reason", reason,
					"message", "active lease exists, restore deferred",
				)
			}
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
				"agentId":      effectiveAgentID,
				"reason":       reason,
				"stateFile":    cfg.StateFile,
				"baselineSha":  snapshot.SHA256,
				"baselineSize": fmt.Sprintf("%d", snapshot.Size),
			},
		}
		s.emit(ctx, cfg, driftEvent)

		shouldRestore := (reason == "deleted" && cfg.RestoreOnDelete) || (reason != "deleted" && cfg.RestoreOnChange)
		if !shouldRestore {
			if s.logger != nil {
				s.logger.Info(
					"drift detected but restore disabled by config",
					"agent", effectiveAgentID,
					"target", snapshot.Name,
					"targetKey", snapshot.TargetKey,
					"kind", snapshot.Kind,
					"path", snapshot.SourcePath,
					"reason", reason,
					"restoreOnDelete", cfg.RestoreOnDelete,
					"restoreOnChange", cfg.RestoreOnChange,
				)
			}
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
					"agentId":   effectiveAgentID,
					"reason":    reason,
					"result":    "failed",
					"error":     err.Error(),
					"stateFile": cfg.StateFile,
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
				"agentId":   effectiveAgentID,
				"reason":    reason,
				"result":    "restored",
				"stateFile": cfg.StateFile,
			},
		})
	}

	return nil
}

func (s *Service) hasActiveLease(snapshot backup.Snapshot, agentID string) bool {
	if s.leaseInspector == nil {
		return false
	}

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
	if _, ok := event.Data["stateFile"]; !ok && cfg.StateFile != "" {
		event.Data["stateFile"] = cfg.StateFile
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
		fields := []any{
			"agent", event.AgentID,
			"target", event.Target,
			"targetKey", event.TargetKey,
			"kind", event.Kind,
			"path", event.Path,
			"message", event.Message,
		}

		if reason := event.Data["reason"]; reason != "" {
			fields = append(fields, "reason", reason)
		}
		if result := event.Data["result"]; result != "" {
			fields = append(fields, "result", result)
		}
		if stateFile := event.Data["stateFile"]; stateFile != "" {
			fields = append(fields, "stateFile", stateFile)
		}
		if baselineSha := event.Data["baselineSha"]; baselineSha != "" {
			fields = append(fields, "baselineSha", baselineSha)
		}
		if baselineSize := event.Data["baselineSize"]; baselineSize != "" {
			fields = append(fields, "baselineSize", baselineSize)
		}
		if errorMessage := event.Data["error"]; errorMessage != "" {
			fields = append(fields, "error", errorMessage)
		}

		s.logger.Info(string(event.Type), fields...)
	}

	if s.dispatcher != nil {
		if err := s.dispatcher.Dispatch(ctx, event); err != nil && s.logger != nil {
			s.logger.Error(
				"dispatch failed",
				"agent", event.AgentID,
				"targetKey", event.TargetKey,
				"kind", event.Kind,
				"path", event.Path,
				"error", err,
			)
		}
	}
}
