package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type FileTarget struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	Optional bool   `json:"optional,omitempty"`
}

type AppConfig struct {
	RootDir             string `json:"rootDir"`
	AgentID             string `json:"agentId"`
	OpenClawPath        string `json:"openclawPath"`
	AuthProfilesPath    string `json:"authProfilesPath,omitempty"`
	IncludeAuthProfiles bool   `json:"includeAuthProfiles"`
	BackupDir           string `json:"backupDir"`
	StateFile           string `json:"stateFile"`
	PollIntervalSeconds int    `json:"pollIntervalSeconds"`
	RestoreOnChange     bool   `json:"restoreOnChange"`
	RestoreOnDelete     bool   `json:"restoreOnDelete"`
	AutoPrepare         bool   `json:"autoPrepare"`
	LogFile             string `json:"logFile,omitempty"`
}

type Options struct {
	ConfigPath             string
	RootDir                string
	AgentID                string
	OpenClawPath           string
	AuthProfilesPath       string
	IncludeAuthProfiles    bool
	IncludeAuthProfilesSet bool
	BackupDir              string
	StateFile              string
	PollIntervalSeconds    int
	LogFile                string
}

type partialConfig struct {
	RootDir             *string `json:"rootDir"`
	AgentID             *string `json:"agentId"`
	OpenClawPath        *string `json:"openclawPath"`
	AuthProfilesPath    *string `json:"authProfilesPath"`
	IncludeAuthProfiles *bool   `json:"includeAuthProfiles"`
	BackupDir           *string `json:"backupDir"`
	StateFile           *string `json:"stateFile"`
	PollIntervalSeconds *int    `json:"pollIntervalSeconds"`
	RestoreOnChange     *bool   `json:"restoreOnChange"`
	RestoreOnDelete     *bool   `json:"restoreOnDelete"`
	AutoPrepare         *bool   `json:"autoPrepare"`
	LogFile             *string `json:"logFile"`
}

func Resolve(opts Options) (AppConfig, error) {
	cfg := defaultConfig()

	if opts.ConfigPath != "" {
		loaded, err := loadFromFile(opts.ConfigPath)
		if err != nil {
			return AppConfig{}, err
		}
		cfg = merge(cfg, loaded)
	}

	if opts.RootDir != "" {
		cfg.RootDir = opts.RootDir
	}
	if strings.TrimSpace(opts.AgentID) != "" {
		cfg.AgentID = strings.TrimSpace(opts.AgentID)
	}
	if opts.OpenClawPath != "" {
		cfg.OpenClawPath = opts.OpenClawPath
	}
	if opts.AuthProfilesPath != "" {
		cfg.AuthProfilesPath = opts.AuthProfilesPath
	}
	if opts.BackupDir != "" {
		cfg.BackupDir = opts.BackupDir
	}
	if opts.StateFile != "" {
		cfg.StateFile = opts.StateFile
	}
	if opts.PollIntervalSeconds > 0 {
		cfg.PollIntervalSeconds = opts.PollIntervalSeconds
	}
	if opts.LogFile != "" {
		cfg.LogFile = opts.LogFile
	}
	if opts.IncludeAuthProfilesSet {
		cfg.IncludeAuthProfiles = opts.IncludeAuthProfiles
	}

	if cfg.RootDir == "" {
		cfg.RootDir = defaultRootDir()
	}
	if strings.TrimSpace(cfg.AgentID) == "" {
		cfg.AgentID = "main"
	}

	rootAbs, err := filepath.Abs(cfg.RootDir)
	if err != nil {
		return AppConfig{}, err
	}
	cfg.RootDir = filepath.Clean(rootAbs)

	if cfg.OpenClawPath == "" {
		cfg.OpenClawPath = filepath.Join(cfg.RootDir, "openclaw.json")
	}
	cfg.OpenClawPath, err = absFromRoot(cfg.RootDir, cfg.OpenClawPath)
	if err != nil {
		return AppConfig{}, fmt.Errorf("resolve openclaw path: %w", err)
	}

	if cfg.AuthProfilesPath == "" {
		cfg.AuthProfilesPath = filepath.Join(cfg.RootDir, "agents", cfg.AgentID, "agent", "auth-profiles.json")
	}
	cfg.AuthProfilesPath, err = absFromRoot(cfg.RootDir, cfg.AuthProfilesPath)
	if err != nil {
		return AppConfig{}, fmt.Errorf("resolve auth-profiles path: %w", err)
	}

	if cfg.BackupDir == "" {
		cfg.BackupDir = filepath.Join(cfg.RootDir, ".guard-state", "backup")
	}
	cfg.BackupDir, err = absFromRoot(cfg.RootDir, cfg.BackupDir)
	if err != nil {
		return AppConfig{}, fmt.Errorf("resolve backup dir: %w", err)
	}

	if cfg.StateFile == "" {
		cfg.StateFile = filepath.Join(cfg.RootDir, ".guard-state", "manifest.json")
	}
	cfg.StateFile, err = absFromRoot(cfg.RootDir, cfg.StateFile)
	if err != nil {
		return AppConfig{}, fmt.Errorf("resolve state file: %w", err)
	}

	if cfg.PollIntervalSeconds <= 0 {
		cfg.PollIntervalSeconds = 2
	}

	if cfg.LogFile != "" {
		cfg.LogFile, err = absFromRoot(cfg.RootDir, cfg.LogFile)
		if err != nil {
			return AppConfig{}, fmt.Errorf("resolve log file: %w", err)
		}
	}

	return cfg, nil
}

func (c AppConfig) Targets() ([]FileTarget, error) {
	if _, err := os.Stat(c.OpenClawPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("required target missing: %s", c.OpenClawPath)
		}
		return nil, err
	}

	targets := []FileTarget{
		{
			Name: "openclaw",
			Path: c.OpenClawPath,
		},
	}

	if c.IncludeAuthProfiles {
		if _, err := os.Stat(c.AuthProfilesPath); err == nil {
			targets = append(targets, FileTarget{
				Name:     "auth:" + strings.ToLower(strings.TrimSpace(c.AgentID)),
				Path:     c.AuthProfilesPath,
				Optional: true,
			})
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}

	return targets, nil
}

func defaultConfig() AppConfig {
	return AppConfig{
		RootDir:             defaultRootDir(),
		AgentID:             "main",
		IncludeAuthProfiles: true,
		PollIntervalSeconds: 2,
		RestoreOnChange:     true,
		RestoreOnDelete:     true,
		AutoPrepare:         true,
	}
}

func defaultRootDir() string {
	home, err := os.UserHomeDir()
	if err == nil && home != "" {
		return filepath.Join(home, ".openclaw")
	}

	wd, err := os.Getwd()
	if err == nil && wd != "" {
		return filepath.Join(wd, ".openclaw")
	}

	return ".openclaw"
}

func loadFromFile(path string) (partialConfig, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return partialConfig{}, err
	}

	var cfg partialConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return partialConfig{}, fmt.Errorf("parse config %s: %w", path, err)
	}
	return cfg, nil
}

func merge(base AppConfig, override partialConfig) AppConfig {
	if override.RootDir != nil {
		base.RootDir = *override.RootDir
	}
	if override.AgentID != nil && strings.TrimSpace(*override.AgentID) != "" {
		base.AgentID = strings.TrimSpace(*override.AgentID)
	}
	if override.OpenClawPath != nil {
		base.OpenClawPath = *override.OpenClawPath
	}
	if override.AuthProfilesPath != nil {
		base.AuthProfilesPath = *override.AuthProfilesPath
	}
	if override.IncludeAuthProfiles != nil {
		base.IncludeAuthProfiles = *override.IncludeAuthProfiles
	}
	if override.BackupDir != nil {
		base.BackupDir = *override.BackupDir
	}
	if override.StateFile != nil {
		base.StateFile = *override.StateFile
	}
	if override.PollIntervalSeconds != nil && *override.PollIntervalSeconds > 0 {
		base.PollIntervalSeconds = *override.PollIntervalSeconds
	}
	if override.RestoreOnChange != nil {
		base.RestoreOnChange = *override.RestoreOnChange
	}
	if override.RestoreOnDelete != nil {
		base.RestoreOnDelete = *override.RestoreOnDelete
	}
	if override.AutoPrepare != nil {
		base.AutoPrepare = *override.AutoPrepare
	}
	if override.LogFile != nil {
		base.LogFile = *override.LogFile
	}
	return base
}

func absFromRoot(root, path string) (string, error) {
	if filepath.IsAbs(path) {
		return filepath.Clean(path), nil
	}
	return filepath.Abs(filepath.Join(root, path))
}
