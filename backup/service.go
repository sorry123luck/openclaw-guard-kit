package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"openclaw-guard-kit/config"
	"openclaw-guard-kit/logging"
)

type SnapshotState int

const (
	SnapshotStateUnknown SnapshotState = iota
	SnapshotStateCandidate
	SnapshotStateHealthy
	SnapshotStateBad
)

type Snapshot struct {
	Name       string        `json:"name"`
	TargetKey  string        `json:"targetKey,omitempty"`
	Kind       string        `json:"kind,omitempty"`
	AgentID    string        `json:"agentId,omitempty"`
	SourcePath string        `json:"sourcePath"`
	BackupPath string        `json:"backupPath"`
	SHA256     string        `json:"sha256"`
	Size       int64         `json:"size"`
	Mode       uint32        `json:"mode"`
	ModTime    time.Time     `json:"modTime"`
	State      SnapshotState `json:"state,omitempty"`
	CreatedAt  time.Time     `json:"createdAt,omitempty"`
}

type Manifest struct {
	Version          int        `json:"version"`
	AgentID          string     `json:"agentId,omitempty"`
	CreatedAt        time.Time  `json:"createdAt"`
	StateFile        string     `json:"-"`
	Targets          []Snapshot `json:"targets,omitempty"` // legacy compatibility: mirrors trustedTargets on save
	TrustedTargets   []Snapshot `json:"trustedTargets,omitempty"`
	CandidateTargets []Snapshot `json:"candidateTargets,omitempty"`
}

type Service struct {
	logger *logging.Logger
}

func NewService(logger *logging.Logger) *Service {
	return &Service{logger: logger}
}

func (s *Service) Prepare(ctx context.Context, cfg config.AppConfig) (Manifest, error) {
	targets, err := cfg.Targets()
	if err != nil {
		return Manifest{}, err
	}

	if err := os.MkdirAll(cfg.BackupDir, 0o755); err != nil {
		return Manifest{}, err
	}
	if err := os.MkdirAll(filepath.Dir(cfg.StateFile), 0o755); err != nil {
		return Manifest{}, err
	}

	manifest := Manifest{
		Version:        3,
		AgentID:        cfg.AgentID,
		CreatedAt:      time.Now().UTC(),
		StateFile:      cfg.StateFile,
		TrustedTargets: make([]Snapshot, 0, len(targets)),
	}

	for _, target := range targets {
		select {
		case <-ctx.Done():
			return Manifest{}, ctx.Err()
		default:
		}

		snapshot, err := s.snapshotTarget(target, cfg.BackupDir)
		if err != nil {
			return Manifest{}, err
		}

		snapshot.State = SnapshotStateHealthy
		snapshot.CreatedAt = time.Now().UTC()
		manifest.TrustedTargets = append(manifest.TrustedTargets, snapshot)
		if s.logger != nil {
			s.logger.Info(
				"baseline snapshot created",
				"target", snapshot.TargetKeyOrName(),
				"source", target.Path,
				"backup", snapshot.BackupPath,
			)
		}
	}

	if err := SaveManifest(cfg.StateFile, manifest); err != nil {
		return Manifest{}, err
	}

	return manifest, nil
}

func LoadManifest(path string) (Manifest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, err
	}

	var manifest Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("parse manifest %s: %w", path, err)
	}

	manifest.StateFile = path
	manifest.normalize()
	return manifest, nil
}

func SaveManifest(path string, manifest Manifest) error {
	copyManifest := manifest
	copyManifest.StateFile = ""
	copyManifest.normalize()
	copyManifest.Targets = append([]Snapshot(nil), copyManifest.TrustedTargets...)

	raw, err := json.MarshalIndent(copyManifest, "", "  ")
	if err != nil {
		return err
	}

	return writeAtomically(path, raw, 0o644)
}

func (s *Service) Restore(snapshot Snapshot) error {
	src, err := os.Open(snapshot.BackupPath)
	if err != nil {
		return err
	}
	defer src.Close()

	if err := os.MkdirAll(filepath.Dir(snapshot.SourcePath), 0o755); err != nil {
		return err
	}

	return copyReaderToFile(src, snapshot.SourcePath, os.FileMode(snapshot.Mode))
}

func (s *Service) RefreshBaseline(manifestPath, targetName string) (Manifest, error) {
	manifest, err := LoadManifest(manifestPath)
	if err != nil {
		return Manifest{}, err
	}

	index := manifest.findTrustedTargetIndex(targetName)
	if index < 0 {
		return Manifest{}, fmt.Errorf("trusted target not found in manifest: %s", targetName)
	}

	oldSnapshot := manifest.TrustedTargets[index]
	newSnapshot, err := s.snapshotTarget(config.FileTarget{
		Name: oldSnapshot.Name,
		Path: oldSnapshot.SourcePath,
	}, filepath.Dir(oldSnapshot.BackupPath))
	if err != nil {
		return Manifest{}, err
	}

	if newSnapshot.TargetKey == "" {
		newSnapshot.TargetKey = oldSnapshot.TargetKey
	}
	if newSnapshot.Kind == "" {
		newSnapshot.Kind = oldSnapshot.Kind
	}
	if newSnapshot.AgentID == "" {
		newSnapshot.AgentID = oldSnapshot.AgentID
	}
	newSnapshot.State = SnapshotStateHealthy
	newSnapshot.CreatedAt = time.Now().UTC()

	manifest.TrustedTargets[index] = newSnapshot
	manifest.removeCandidate(targetName)
	if err := SaveManifest(manifestPath, manifest); err != nil {
		return Manifest{}, err
	}

	if s.logger != nil {
		s.logger.Info(
			"trusted baseline refreshed",
			"target", newSnapshot.TargetKeyOrName(),
			"source", newSnapshot.SourcePath,
			"backup", newSnapshot.BackupPath,
		)
	}

	return manifest, nil
}

// Stage 3: Health-based promotion/marking on manifest (persistent)
func (s *Service) PromoteSnapshotToHealthy(manifestPath string, targetName string) error {
	manifest, err := LoadManifest(manifestPath)
	if err != nil {
		return err
	}

	idx := manifest.findCandidateTargetIndex(targetName)
	if idx < 0 {
		return fmt.Errorf("candidate target not found in manifest: %s", targetName)
	}

	snapshot := manifest.CandidateTargets[idx]
	snapshot.State = SnapshotStateHealthy
	snapshot.CreatedAt = time.Now().UTC()
	snapshot.BackupPath = strings.TrimSuffix(snapshot.BackupPath, ".candidate.json") + ".baseline.json"

	raw, err := os.ReadFile(manifest.CandidateTargets[idx].BackupPath)
	if err != nil {
		return err
	}
	if err := writeAtomically(snapshot.BackupPath, raw, os.FileMode(snapshot.Mode)); err != nil {
		return err
	}

	trustedIdx := manifest.findTrustedTargetIndex(targetName)
	if trustedIdx >= 0 {
		manifest.TrustedTargets[trustedIdx] = snapshot
	} else {
		manifest.TrustedTargets = append(manifest.TrustedTargets, snapshot)
	}

	manifest.CandidateTargets = append(manifest.CandidateTargets[:idx], manifest.CandidateTargets[idx+1:]...)
	return SaveManifest(manifestPath, manifest)
}

func (s *Service) MarkSnapshotAsBad(manifestPath string, targetName string) error {
	manifest, err := LoadManifest(manifestPath)
	if err != nil {
		return err
	}
	idx := manifest.findCandidateTargetIndex(targetName)
	if idx < 0 {
		return fmt.Errorf("candidate target not found in manifest: %s", targetName)
	}
	manifest.CandidateTargets[idx].State = SnapshotStateBad
	manifest.CandidateTargets[idx].CreatedAt = time.Now().UTC()
	return SaveManifest(manifestPath, manifest)
}
func (s *Service) DiscardCandidate(manifestPath string, targetName string) error {
	manifest, err := LoadManifest(manifestPath)
	if err != nil {
		return err
	}

	idx := manifest.findCandidateTargetIndex(targetName)
	if idx < 0 {
		return fmt.Errorf("candidate target not found in manifest: %s", targetName)
	}

	manifest.CandidateTargets = append(manifest.CandidateTargets[:idx], manifest.CandidateTargets[idx+1:]...)
	return SaveManifest(manifestPath, manifest)
}
func (s *Service) RollbackCandidatesToTrusted(manifestPath string, targets []string) error {
	manifest, err := LoadManifest(manifestPath)
	if err != nil {
		return err
	}

	seen := make(map[string]struct{}, len(targets))
	for _, rawTarget := range targets {
		targetName := strings.TrimSpace(rawTarget)
		if targetName == "" {
			continue
		}
		if _, ok := seen[targetName]; ok {
			continue
		}
		seen[targetName] = struct{}{}

		trustedIdx := manifest.findTrustedTargetIndex(targetName)
		if trustedIdx < 0 {
			return fmt.Errorf("trusted target not found in manifest: %s", targetName)
		}
		if err := s.Restore(manifest.TrustedTargets[trustedIdx]); err != nil {
			return fmt.Errorf("restore trusted target %s: %w", targetName, err)
		}
		manifest.removeCandidate(targetName)
	}

	return SaveManifest(manifestPath, manifest)
}
func (s *Service) ArchiveCandidateTargetsAsBad(manifestPath string, targets []string) ([]string, error) {
	manifest, err := LoadManifest(manifestPath)
	if err != nil {
		return nil, err
	}

	seen := make(map[string]struct{}, len(targets))
	archived := make([]string, 0, len(targets))
	var firstErr error

	for _, rawTarget := range targets {
		targetName := strings.TrimSpace(rawTarget)
		if targetName == "" {
			continue
		}
		if _, ok := seen[targetName]; ok {
			continue
		}
		seen[targetName] = struct{}{}

		idx := manifest.findCandidateTargetIndex(targetName)
		if idx < 0 {
			if firstErr == nil {
				firstErr = fmt.Errorf("candidate target not found in manifest: %s", targetName)
			}
			continue
		}

		path, err := archiveSnapshotAsBad(manifest.CandidateTargets[idx])
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("archive bad candidate %s: %w", targetName, err)
			}
			continue
		}
		archived = append(archived, path)
	}

	return archived, firstErr
}
func (s *Service) RetryCandidate(manifestPath string, targetName string) error {
	manifest, err := LoadManifest(manifestPath)
	if err != nil {
		return err
	}

	idx := manifest.findCandidateTargetIndex(targetName)
	if idx < 0 {
		return fmt.Errorf("candidate target not found in manifest: %s", targetName)
	}

	manifest.CandidateTargets[idx].State = SnapshotStateCandidate
	manifest.CandidateTargets[idx].CreatedAt = time.Now().UTC()
	return SaveManifest(manifestPath, manifest)
}
func Fingerprint(path string) (sha string, size int64, mode uint32, modTime time.Time, err error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", 0, 0, time.Time{}, err
	}

	f, err := os.Open(path)
	if err != nil {
		return "", 0, 0, time.Time{}, err
	}
	defer f.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, f); err != nil {
		return "", 0, 0, time.Time{}, err
	}

	return hex.EncodeToString(hash.Sum(nil)), info.Size(), uint32(info.Mode()), info.ModTime().UTC(), nil
}

func (s *Service) CreateCandidateSnapshot(ctx context.Context, target config.FileTarget, backupDir string) (Snapshot, error) {
	snapshot, err := s.snapshotTargetWithSuffix(target, backupDir, ".candidate.json")
	if err != nil {
		return Snapshot{}, err
	}
	snapshot.State = SnapshotStateCandidate
	snapshot.CreatedAt = time.Now().UTC()
	return snapshot, nil
}

func (s *Service) UpsertCandidateSnapshot(manifestPath string, snapshot Snapshot) error {
	manifest, err := LoadManifest(manifestPath)
	if err != nil {
		return err
	}
	manifest.upsertCandidate(snapshot)
	return SaveManifest(manifestPath, manifest)
}

func (s *Service) snapshotTarget(target config.FileTarget, backupDir string) (Snapshot, error) {
	return s.snapshotTargetWithSuffix(target, backupDir, ".baseline.json")
}

func (s *Service) snapshotTargetWithSuffix(target config.FileTarget, backupDir, suffix string) (Snapshot, error) {
	sha, size, mode, modTime, err := Fingerprint(target.Path)
	if err != nil {
		return Snapshot{}, err
	}

	targetKey, kind, agentID := deriveTargetMetadata(target.Name, target.Path)
	backupBase := targetKey
	if strings.TrimSpace(backupBase) == "" {
		backupBase = target.Name
	}
	if strings.TrimSpace(backupBase) == "" {
		backupBase = filepath.Base(target.Path)
	}

	backupPath := filepath.Join(backupDir, sanitizeBackupName(backupBase)+suffix)
	raw, err := os.ReadFile(target.Path)
	if err != nil {
		return Snapshot{}, err
	}

	if err := writeAtomically(backupPath, raw, os.FileMode(mode)); err != nil {
		return Snapshot{}, err
	}

	return Snapshot{
		Name:       target.Name,
		TargetKey:  targetKey,
		Kind:       kind,
		AgentID:    agentID,
		SourcePath: target.Path,
		BackupPath: backupPath,
		SHA256:     sha,
		Size:       size,
		Mode:       mode,
		ModTime:    modTime,
	}, nil
}
func archiveSnapshotAsBad(snapshot Snapshot) (string, error) {
	raw, err := os.ReadFile(snapshot.BackupPath)
	if err != nil {
		return "", err
	}

	targetName := sanitizeBackupName(snapshot.TargetKeyOrName())
	if strings.TrimSpace(targetName) == "" {
		targetName = sanitizeBackupName(snapshot.Name)
	}
	if strings.TrimSpace(targetName) == "" {
		targetName = "unknown"
	}

	backupRoot := filepath.Dir(snapshot.BackupPath)
	archiveDir := filepath.Join(backupRoot, "bad", targetName)

	ext := strings.TrimSpace(filepath.Ext(snapshot.SourcePath))
	if ext == "" {
		ext = strings.TrimSpace(filepath.Ext(snapshot.BackupPath))
	}
	if ext == "" {
		ext = ".bak"
	}

	stamp := time.Now().UTC().Format("20060102-150405-000")
	archivePath := uniqueArchivePath(filepath.Join(archiveDir, "bad-"+stamp+ext))
	if err := writeAtomically(archivePath, raw, os.FileMode(snapshot.Mode)); err != nil {
		return "", err
	}
	return archivePath, nil
}

func uniqueArchivePath(path string) string {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return path
	}

	ext := filepath.Ext(path)
	base := strings.TrimSuffix(path, ext)
	for i := 1; ; i++ {
		candidate := fmt.Sprintf("%s-%d%s", base, i, ext)
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			return candidate
		}
	}
}
func (s Snapshot) TargetKeyOrName() string {
	if strings.TrimSpace(s.TargetKey) != "" {
		return s.TargetKey
	}
	return s.Name
}
func (m *Manifest) normalize() {
	if len(m.TrustedTargets) == 0 && len(m.CandidateTargets) == 0 && len(m.Targets) > 0 {
		for _, snap := range m.Targets {
			if snap.State == SnapshotStateCandidate || snap.State == SnapshotStateBad {
				if snap.State == SnapshotStateUnknown {
					snap.State = SnapshotStateCandidate
				}
				m.CandidateTargets = append(m.CandidateTargets, snap)
				continue
			}
			if snap.State == SnapshotStateUnknown {
				snap.State = SnapshotStateHealthy
			}
			m.TrustedTargets = append(m.TrustedTargets, snap)
		}
	}

	for i := range m.TrustedTargets {
		if m.TrustedTargets[i].State != SnapshotStateHealthy {
			m.TrustedTargets[i].State = SnapshotStateHealthy
		}
	}
	for i := range m.CandidateTargets {
		if m.CandidateTargets[i].State != SnapshotStateCandidate && m.CandidateTargets[i].State != SnapshotStateBad {
			m.CandidateTargets[i].State = SnapshotStateCandidate
		}
	}
	m.Targets = nil
}

func (m Manifest) findTrustedTargetIndex(targetName string) int {
	for i, snapshot := range m.TrustedTargets {
		if snapshot.Name == targetName || snapshot.TargetKey == targetName {
			return i
		}
	}
	return -1
}

func (m Manifest) findCandidateTargetIndex(targetName string) int {
	for i, snapshot := range m.CandidateTargets {
		if snapshot.Name == targetName || snapshot.TargetKey == targetName {
			return i
		}
	}
	return -1
}

func (m *Manifest) upsertCandidate(snapshot Snapshot) {
	idx := m.findCandidateTargetIndex(snapshot.TargetKeyOrName())
	if idx >= 0 {
		m.CandidateTargets[idx] = snapshot
		return
	}
	m.CandidateTargets = append(m.CandidateTargets, snapshot)
}

func (m *Manifest) removeCandidate(targetName string) {
	idx := m.findCandidateTargetIndex(targetName)
	if idx < 0 {
		return
	}
	m.CandidateTargets = append(m.CandidateTargets[:idx], m.CandidateTargets[idx+1:]...)
}
func deriveTargetMetadata(name, path string) (targetKey, kind, agentID string) {
	clean := filepath.Clean(strings.TrimSpace(path))
	lowerBase := strings.ToLower(filepath.Base(clean))

	if lowerBase == "openclaw.json" {
		return "openclaw", "openclaw", ""
	}

	slash := filepath.ToSlash(clean)
	parts := strings.Split(slash, "/")
	if len(parts) >= 4 {
		n := len(parts)
		last := strings.ToLower(parts[n-1])
		prev := strings.ToLower(parts[n-2])
		agentDir := parts[n-3]
		agentsMarker := strings.ToLower(parts[n-4])

		if agentsMarker == "agents" && prev == "agent" && strings.TrimSpace(agentDir) != "" {
			switch last {
			case "auth-profiles.json":
				return "auth:" + strings.ToLower(agentDir), "auth-profiles", agentDir
			case "models.json":
				return "models:" + strings.ToLower(agentDir), "models", agentDir
			}
		}
	}

	trimmedName := strings.TrimSpace(name)
	if trimmedName != "" {
		return strings.ToLower(trimmedName), trimmedName, ""
	}

	return "", "", ""
}

func sanitizeBackupName(name string) string {
	replacer := strings.NewReplacer(
		":", ".",
		"/", ".",
		"\\", ".",
		" ", "_",
		"|", ".",
		"*", ".",
		"?", ".",
		"\"", ".",
		"<", ".",
		">", ".",
	)
	return replacer.Replace(strings.TrimSpace(name))
}

func writeAtomically(path string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), ".guard-tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	_ = os.Remove(path)
	return os.Rename(tmpName, path)
}

func copyReaderToFile(r io.Reader, path string, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), ".guard-restore-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := io.Copy(tmp, r); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	_ = os.Remove(path)
	return os.Rename(tmpName, path)
}
