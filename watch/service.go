package watch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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

type EventDispatcher interface {
	Dispatch(context.Context, protocol.Event) error
}

type Service struct {
	logger       *logging.Logger
	dispatcher   EventDispatcher
	backupSvc    *backup.Service
	driftPending map[string]*pendingDrift
}

type pendingDrift struct {
	Reason             string
	Signature          string
	FirstSeen          time.Time
	LastChanged        time.Time
	CandidateSignature string
}

type startupProtectFile struct {
	Until time.Time `json:"until"`
}

func NewService(
	logger *logging.Logger,
	dispatcher EventDispatcher,
	backupSvc *backup.Service,
	_ ...interface{},
) *Service {
	svc := &Service{
		logger:       logger,
		dispatcher:   dispatcher,
		backupSvc:    backupSvc,
		driftPending: make(map[string]*pendingDrift),
	}

	return svc
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
func driftKey(snapshot backup.Snapshot) string {
	if key := strings.TrimSpace(snapshot.TargetKey); key != "" {
		return key
	}
	if name := strings.TrimSpace(snapshot.Name); name != "" {
		return name
	}
	return strings.TrimSpace(snapshot.SourcePath)
}

func (s *Service) clearPendingDrift(snapshot backup.Snapshot) {
	if s.driftPending == nil {
		return
	}
	delete(s.driftPending, driftKey(snapshot))
}

func (s *Service) upsertPendingDrift(snapshot backup.Snapshot, reason, signature string, now time.Time) (*pendingDrift, bool) {
	if s.driftPending == nil {
		s.driftPending = make(map[string]*pendingDrift)
	}

	key := driftKey(snapshot)
	state, ok := s.driftPending[key]
	if !ok {
		state = &pendingDrift{
			Reason:      reason,
			Signature:   signature,
			FirstSeen:   now,
			LastChanged: now,
		}
		s.driftPending[key] = state
		return state, true
	}

	changed := state.Signature != signature || state.Reason != reason
	if changed {
		state.Reason = reason
		state.Signature = signature
		state.LastChanged = now
		state.CandidateSignature = ""
	}

	return state, changed
}

func hashBytes(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func currentProtectedSignature(snapshot backup.Snapshot) (string, error) {
	raw, err := os.ReadFile(snapshot.SourcePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "deleted", nil
		}
		return "", err
	}

	switch {
	case isAuthProfilesSnapshot(snapshot):
		protected, err := normalizeAuthProfilesProtectedBytes(raw)
		if err == nil {
			return "auth:" + hashBytes(protected), nil
		}
		return "auth-raw:" + hashBytes(raw), nil

	case isOpenClawSnapshot(snapshot):
		protected, err := normalizeOpenClawProtectedBytes(raw)
		if err == nil {
			return "openclaw:" + hashBytes(protected), nil
		}
		return "openclaw-raw:" + hashBytes(raw), nil

	default:
		return fmt.Sprintf("file:%s:%d", hashBytes(raw), len(raw)), nil
	}
}

func findCandidateSnapshot(manifest backup.Manifest, snapshot backup.Snapshot) (backup.Snapshot, bool) {
	targetKey := strings.TrimSpace(snapshot.TargetKey)
	targetName := strings.TrimSpace(snapshot.Name)

	for _, candidate := range manifest.CandidateTargets {
		if candidate.State == backup.SnapshotStateBad {
			continue
		}

		if targetKey != "" && strings.EqualFold(strings.TrimSpace(candidate.TargetKey), targetKey) {
			return candidate, true
		}
		if targetName != "" && strings.EqualFold(strings.TrimSpace(candidate.Name), targetName) {
			return candidate, true
		}
	}

	return backup.Snapshot{}, false
}

func currentMatchesCandidate(manifest backup.Manifest, snapshot backup.Snapshot) (bool, error) {
	candidate, ok := findCandidateSnapshot(manifest, snapshot)
	if !ok {
		return false, nil
	}

	drift, _, err := compareToBaseline(candidate)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}

	return !drift, nil
}

func (s *Service) createCandidateFromCurrent(ctx context.Context, cfg config.AppConfig, snapshot backup.Snapshot, agentID, reason string) error {
	targetName := snapshot.TargetKeyOrName()

	candidate, err := s.backupSvc.CreateCandidateSnapshot(ctx, config.FileTarget{
		Name: snapshot.Name,
		Path: snapshot.SourcePath,
	}, filepath.Dir(snapshot.BackupPath))
	if err != nil {
		return fmt.Errorf("create candidate snapshot for %s failed: %w", targetName, err)
	}

	if candidate.TargetKey == "" {
		candidate.TargetKey = snapshot.TargetKey
	}
	if candidate.Kind == "" {
		candidate.Kind = snapshot.Kind
	}
	if candidate.AgentID == "" {
		candidate.AgentID = snapshot.AgentID
	}

	if err := s.backupSvc.UpsertCandidateSnapshot(cfg.StateFile, candidate); err != nil {
		return fmt.Errorf("persist candidate snapshot for %s failed: %w", targetName, err)
	}

	s.emit(ctx, cfg, protocol.Event{
		Type:      protocol.EventCandidateCreated,
		AgentID:   agentID,
		Target:    snapshot.Name,
		TargetKey: snapshot.TargetKey,
		Kind:      snapshot.Kind,
		Path:      snapshot.SourcePath,
		Message:   "drift stabilized; candidate snapshot created, awaiting verification",
		At:        time.Now().UTC(),
		Data: map[string]string{
			"agentId":        agentID,
			"reason":         reason,
			"stage":          "candidate",
			"stateFile":      cfg.StateFile,
			"candidatePath":  candidate.BackupPath,
			"stableSeconds":  fmt.Sprintf("%d", cfg.DriftStableSeconds),
			"candidateState": "pending",
		},
	})

	return nil
}

func (s *Service) restoreDeletedTarget(ctx context.Context, cfg config.AppConfig, snapshot backup.Snapshot, agentID, reason string) error {
	if !cfg.RestoreOnDelete {
		return nil
	}

	if err := s.backupSvc.Restore(snapshot); err != nil {
		s.emit(ctx, cfg, protocol.Event{
			Type:      protocol.EventRestoreFailed,
			AgentID:   agentID,
			Target:    snapshot.Name,
			TargetKey: snapshot.TargetKey,
			Kind:      snapshot.Kind,
			Path:      snapshot.SourcePath,
			Message:   err.Error(),
			At:        time.Now().UTC(),
			Data: map[string]string{
				"agentId":   agentID,
				"reason":    reason,
				"result":    "failed",
				"error":     err.Error(),
				"stateFile": cfg.StateFile,
			},
		})
		return err
	}

	s.emit(ctx, cfg, protocol.Event{
		Type:      protocol.EventRestoreCompleted,
		AgentID:   agentID,
		Target:    snapshot.Name,
		TargetKey: snapshot.TargetKey,
		Kind:      snapshot.Kind,
		Path:      snapshot.SourcePath,
		Message:   "trusted baseline restored after stable delete drift",
		At:        time.Now().UTC(),
		Data: map[string]string{
			"agentId":   agentID,
			"reason":    reason,
			"result":    "restored",
			"stateFile": cfg.StateFile,
		},
	})

	return nil
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

	manifest, err := backup.LoadManifest(cfg.StateFile)
	if err != nil {
		return err
	}

	startupProtectUntil := loadStartupProtectionUntil(cfg)
	now := time.Now().UTC()

	for _, snapshot := range manifest.TrustedTargets {
		effectiveAgentID := snapshot.AgentID
		if effectiveAgentID == "" {
			effectiveAgentID = cfg.AgentID
		}

		if startupRestoreSuppressed(startupProtectUntil, snapshot) {
			s.clearPendingDrift(snapshot)
			if s.logger != nil {
				s.logger.Info(
					"candidate creation deferred due to startup protection window",
					"agent", effectiveAgentID,
					"target", snapshot.Name,
					"targetKey", snapshot.TargetKey,
					"kind", snapshot.Kind,
					"path", snapshot.SourcePath,
					"until", startupProtectUntil.Format(time.RFC3339),
					"message", "OpenClaw startup protection window active, drift verification temporarily deferred",
				)
			}
			continue
		}

		drift, reason, err := compareToBaseline(snapshot)
		if err != nil {
			return fmt.Errorf("compare target %q at %q: %w", snapshot.Name, snapshot.SourcePath, err)
		}
		if !drift {
			s.clearPendingDrift(snapshot)
			continue
		}

		signature, err := currentProtectedSignature(snapshot)
		if err != nil {
			return fmt.Errorf("calculate drift signature for %q at %q: %w", snapshot.Name, snapshot.SourcePath, err)
		}

		pending, changed := s.upsertPendingDrift(snapshot, reason, signature, now)
		stableUntil := pending.LastChanged.Add(time.Duration(cfg.DriftStableSeconds) * time.Second)

		if changed {
			s.emit(ctx, cfg, protocol.Event{
				Type:      protocol.EventDriftDetected,
				AgentID:   effectiveAgentID,
				Target:    snapshot.Name,
				TargetKey: snapshot.TargetKey,
				Kind:      snapshot.Kind,
				Path:      snapshot.SourcePath,
				Message:   fmt.Sprintf("protected file changed; waiting %d seconds for stabilization before candidate verification", cfg.DriftStableSeconds),
				At:        now,
				Data: map[string]string{
					"agentId":       effectiveAgentID,
					"reason":        reason,
					"stateFile":     cfg.StateFile,
					"baselineSha":   snapshot.SHA256,
					"baselineSize":  fmt.Sprintf("%d", snapshot.Size),
					"stage":         "stabilizing",
					"stableSeconds": fmt.Sprintf("%d", cfg.DriftStableSeconds),
					"stableUntil":   stableUntil.Format(time.RFC3339Nano),
				},
			})
		}

		if now.Before(stableUntil) {
			continue
		}

		if pending.CandidateSignature == signature {
			continue
		}

		if reason == "deleted" {
			if err := s.restoreDeletedTarget(ctx, cfg, snapshot, effectiveAgentID, reason); err == nil {
				pending.CandidateSignature = signature
			}
			continue
		}

		matchesCandidate, err := currentMatchesCandidate(manifest, snapshot)
		if err != nil {
			return fmt.Errorf("compare current file with candidate for %q failed: %w", snapshot.TargetKeyOrName(), err)
		}
		if matchesCandidate {
			pending.CandidateSignature = signature
			continue
		}

		if err := s.createCandidateFromCurrent(ctx, cfg, snapshot, effectiveAgentID, reason); err != nil {
			return err
		}

		pending.CandidateSignature = signature
	}

	return nil
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
