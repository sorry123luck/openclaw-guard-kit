package watch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
type startupProtectFile struct {
	Until time.Time `json:"until"`
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
func monitoringPauseFile(cfg config.AppConfig) string {
	return filepath.Join(filepath.Dir(cfg.StateFile), "monitor.paused")
}

func isMonitoringPaused(cfg config.AppConfig) bool {
	_, err := os.Stat(monitoringPauseFile(cfg))
	return err == nil
}
func startupProtectFilePath(cfg config.AppConfig) string {
	return filepath.Join(filepath.Dir(cfg.StateFile), "startup-protect.json")
}

func loadStartupProtectionUntil(cfg config.AppConfig) time.Time {
	raw, err := os.ReadFile(startupProtectFilePath(cfg))
	if err != nil {
		return time.Time{}
	}

	var payload startupProtectFile
	if err := json.Unmarshal(raw, &payload); err != nil {
		return time.Time{}
	}
	return payload.Until.UTC()
}
func candidateTargetSet(manifest backup.Manifest) map[string]struct{} {
	targets := make(map[string]struct{})

	for _, snapshot := range manifest.CandidateTargets {
		if snapshot.State == backup.SnapshotStateBad {
			continue
		}

		if key := strings.TrimSpace(snapshot.TargetKey); key != "" {
			targets[key] = struct{}{}
		}
		if name := strings.TrimSpace(snapshot.Name); name != "" {
			targets[name] = struct{}{}
		}
	}

	return targets
}

func snapshotMatchesCandidate(snapshot backup.Snapshot, candidateTargets map[string]struct{}) bool {
	if len(candidateTargets) == 0 {
		return false
	}

	if key := strings.TrimSpace(snapshot.TargetKey); key != "" {
		if _, ok := candidateTargets[key]; ok {
			return true
		}
	}

	if name := strings.TrimSpace(snapshot.Name); name != "" {
		if _, ok := candidateTargets[name]; ok {
			return true
		}
	}

	return false
}
func startupRestoreSuppressed(until time.Time, snapshot backup.Snapshot) bool {
	if until.IsZero() {
		return false
	}
	if !time.Now().UTC().Before(until) {
		return false
	}
	return isOpenClawSnapshot(snapshot)
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
			"targets":         fmt.Sprintf("%d", len(manifest.TrustedTargets)),
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
	if isMonitoringPaused(cfg) {
		return nil
	}

	manifest, err := backup.LoadManifest(cfg.StateFile)
	if err != nil {
		return err
	}

	candidateTargets := candidateTargetSet(manifest)
	startupProtectUntil := loadStartupProtectionUntil(cfg)

	for _, snapshot := range manifest.TrustedTargets {
		effectiveAgentID := snapshot.AgentID
		if effectiveAgentID == "" {
			effectiveAgentID = cfg.AgentID
		}

		if snapshotMatchesCandidate(snapshot, candidateTargets) {
			if s.logger != nil {
				s.logger.Info(
					"restore deferred due to candidate snapshot",
					"agent", effectiveAgentID,
					"target", snapshot.Name,
					"targetKey", snapshot.TargetKey,
					"kind", snapshot.Kind,
					"path", snapshot.SourcePath,
					"message", "candidate exists for target, drift restore temporarily deferred",
				)
			}
			continue
		}

		if startupRestoreSuppressed(startupProtectUntil, snapshot) {
			if s.logger != nil {
				s.logger.Info(
					"restore deferred due to startup protection window",
					"agent", effectiveAgentID,
					"target", snapshot.Name,
					"targetKey", snapshot.TargetKey,
					"kind", snapshot.Kind,
					"path", snapshot.SourcePath,
					"until", startupProtectUntil.Format(time.RFC3339),
					"message", "OpenClaw startup protection window active, restore temporarily deferred",
				)
			}
			continue
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
	switch {
	case isAuthProfilesSnapshot(snapshot):
		return compareAuthProfilesToBaseline(snapshot)
	case isOpenClawSnapshot(snapshot):
		return compareOpenClawToBaseline(snapshot)
	default:
		return compareFileFingerprintToBaseline(snapshot)
	}
}

func isAuthProfilesSnapshot(snapshot backup.Snapshot) bool {
	kind := strings.TrimSpace(snapshot.Kind)
	targetKey := strings.TrimSpace(snapshot.TargetKey)
	name := strings.TrimSpace(snapshot.Name)
	base := filepath.Base(strings.TrimSpace(snapshot.SourcePath))

	return strings.EqualFold(kind, protocol.KindAuthProfile) ||
		strings.EqualFold(targetKey, "auth") ||
		strings.HasPrefix(strings.ToLower(targetKey), "auth:") ||
		strings.EqualFold(name, protocol.TargetAuthProfile) ||
		strings.EqualFold(base, "auth-profiles.json")
}

func isOpenClawSnapshot(snapshot backup.Snapshot) bool {
	kind := strings.TrimSpace(snapshot.Kind)
	targetKey := strings.TrimSpace(snapshot.TargetKey)
	name := strings.TrimSpace(snapshot.Name)
	base := filepath.Base(strings.TrimSpace(snapshot.SourcePath))

	return strings.EqualFold(kind, protocol.KindOpenClaw) ||
		strings.EqualFold(targetKey, protocol.TargetOpenClaw) ||
		strings.EqualFold(name, protocol.TargetOpenClaw) ||
		strings.EqualFold(base, "openclaw.json")
}

func compareFileFingerprintToBaseline(snapshot backup.Snapshot) (bool, string, error) {
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

func compareOpenClawToBaseline(snapshot backup.Snapshot) (bool, string, error) {
	currentRaw, err := os.ReadFile(snapshot.SourcePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return true, "deleted", nil
		}
		return false, "", err
	}

	baselineRaw, err := os.ReadFile(snapshot.BackupPath)
	if err != nil {
		return false, "", err
	}

	currentProtected, err := normalizeOpenClawProtectedBytes(currentRaw)
	if err != nil {
		return true, "protected openclaw content changed", nil
	}

	baselineProtected, err := normalizeOpenClawProtectedBytes(baselineRaw)
	if err != nil {
		return false, "", fmt.Errorf("normalize openclaw baseline failed: %w", err)
	}

	if string(currentProtected) != string(baselineProtected) {
		return true, "protected openclaw content changed", nil
	}

	return false, "", nil
}

func compareAuthProfilesToBaseline(snapshot backup.Snapshot) (bool, string, error) {
	currentRaw, err := os.ReadFile(snapshot.SourcePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return true, "deleted", nil
		}
		return false, "", err
	}

	baselineRaw, err := os.ReadFile(snapshot.BackupPath)
	if err != nil {
		return false, "", err
	}

	currentProtected, err := normalizeAuthProfilesProtectedBytes(currentRaw)
	if err != nil {
		// 当前文件如果已经坏掉/格式异常，也应该视为受保护内容变化
		return true, "protected auth content changed", nil
	}

	baselineProtected, err := normalizeAuthProfilesProtectedBytes(baselineRaw)
	if err != nil {
		return false, "", fmt.Errorf("normalize auth baseline failed: %w", err)
	}

	if string(currentProtected) != string(baselineProtected) {
		return true, "protected auth content changed", nil
	}

	return false, "", nil
}

func normalizeAuthProfilesProtectedBytes(raw []byte) ([]byte, error) {
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}

	protected := map[string]any{}

	if v, ok := doc["version"]; ok {
		protected["version"] = v
	}
	if v, ok := doc["profiles"]; ok {
		protected["profiles"] = v
	}

	return json.Marshal(protected)
}
func normalizeOpenClawProtectedBytes(raw []byte) ([]byte, error) {
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}

	// 1. wizard 整块忽略
	delete(doc, "wizard")

	// 2. meta.lastTouchedAt / lastTouchedVersion 忽略
	if metaAny, ok := doc["meta"]; ok {
		if meta, ok := metaAny.(map[string]any); ok {
			delete(meta, "lastTouchedAt")
			delete(meta, "lastTouchedVersion")
			if len(meta) == 0 {
				delete(doc, "meta")
			} else {
				doc["meta"] = meta
			}
		}
	}

	// 3. 根级 installs.*.resolvedAt / installedAt 忽略
	if installsAny, ok := doc["installs"]; ok {
		if installs, ok := installsAny.(map[string]any); ok {
			stripInstallTimestampFields(installs)
			if len(installs) == 0 {
				delete(doc, "installs")
			} else {
				doc["installs"] = installs
			}
		}
	}

	// 4. plugins.installs.*.resolvedAt / installedAt 忽略
	if pluginsAny, ok := doc["plugins"]; ok {
		if plugins, ok := pluginsAny.(map[string]any); ok {
			if installsAny, ok := plugins["installs"]; ok {
				if installs, ok := installsAny.(map[string]any); ok {
					stripInstallTimestampFields(installs)
					if len(installs) == 0 {
						delete(plugins, "installs")
					} else {
						plugins["installs"] = installs
					}
				}
			}
			if len(plugins) == 0 {
				delete(doc, "plugins")
			} else {
				doc["plugins"] = plugins
			}
		}
	}

	return json.Marshal(doc)
}

func stripInstallTimestampFields(installs map[string]any) {
	for name, itemAny := range installs {
		item, ok := itemAny.(map[string]any)
		if !ok {
			continue
		}

		delete(item, "resolvedAt")
		delete(item, "installedAt")

		if len(item) == 0 {
			delete(installs, name)
		} else {
			installs[name] = item
		}
	}
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
