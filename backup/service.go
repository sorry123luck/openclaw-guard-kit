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

type Snapshot struct {
	Name       string    `json:"name"`
	TargetKey  string    `json:"targetKey,omitempty"`
	Kind       string    `json:"kind,omitempty"`
	AgentID    string    `json:"agentId,omitempty"`
	SourcePath string    `json:"sourcePath"`
	BackupPath string    `json:"backupPath"`
	SHA256     string    `json:"sha256"`
	Size       int64     `json:"size"`
	Mode       uint32    `json:"mode"`
	ModTime    time.Time `json:"modTime"`
}

type Manifest struct {
	Version   int        `json:"version"`
	AgentID   string     `json:"agentId,omitempty"`
	CreatedAt time.Time  `json:"createdAt"`
	StateFile string     `json:"-"`
	Targets   []Snapshot `json:"targets"`
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
		Version:   2,
		AgentID:   cfg.AgentID,
		CreatedAt: time.Now().UTC(),
		StateFile: cfg.StateFile,
		Targets:   make([]Snapshot, 0, len(targets)),
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

		manifest.Targets = append(manifest.Targets, snapshot)
		s.logger.Info(
			"baseline snapshot created",
			"target", snapshot.TargetKeyOrName(),
			"source", target.Path,
			"backup", snapshot.BackupPath,
		)
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
	return manifest, nil
}

func SaveManifest(path string, manifest Manifest) error {
	copyManifest := manifest
	copyManifest.StateFile = ""

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

	index := -1
	for i, snapshot := range manifest.Targets {
		if snapshot.Name == targetName || snapshot.TargetKey == targetName {
			index = i
			break
		}
	}
	if index < 0 {
		return Manifest{}, fmt.Errorf("target not found in manifest: %s", targetName)
	}

	oldSnapshot := manifest.Targets[index]
	newSnapshot, err := s.snapshotTarget(config.FileTarget{
		Name: oldSnapshot.Name,
		Path: oldSnapshot.SourcePath,
	}, filepath.Dir(oldSnapshot.BackupPath))
	if err != nil {
		return Manifest{}, err
	}

	// 尽量保留已有元信息；新推导优先
	if newSnapshot.TargetKey == "" {
		newSnapshot.TargetKey = oldSnapshot.TargetKey
	}
	if newSnapshot.Kind == "" {
		newSnapshot.Kind = oldSnapshot.Kind
	}
	if newSnapshot.AgentID == "" {
		newSnapshot.AgentID = oldSnapshot.AgentID
	}

	manifest.Targets[index] = newSnapshot
	if err := SaveManifest(manifestPath, manifest); err != nil {
		return Manifest{}, err
	}

	s.logger.Info(
		"baseline refreshed",
		"target", newSnapshot.TargetKeyOrName(),
		"source", newSnapshot.SourcePath,
		"backup", newSnapshot.BackupPath,
	)

	return manifest, nil
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

func (s *Service) snapshotTarget(target config.FileTarget, backupDir string) (Snapshot, error) {
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

	backupPath := filepath.Join(backupDir, sanitizeBackupName(backupBase)+".baseline.json")
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

func (s Snapshot) TargetKeyOrName() string {
	if strings.TrimSpace(s.TargetKey) != "" {
		return s.TargetKey
	}
	return s.Name
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
