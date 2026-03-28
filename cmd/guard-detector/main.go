//go:build windows

package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
)

func normalizeDetectorArgs(args []string) []string {
	if len(args) == 0 {
		return args
	}

	// 兼容旧调用方式：
	// guard-detector.exe watch --root ...
	// 这里把开头的 watch 吃掉，后面 flag 才能正常解析。
	if args[0] == "watch" {
		return args[1:]
	}

	return args
}

func defaultOpenClawRoot() string {
	homeDir, err := os.UserHomeDir()
	if err != nil || homeDir == "" {
		cwd, _ := os.Getwd()
		return cwd
	}
	return filepath.Join(homeDir, ".openclaw")
}

func main() {
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)

	rootDir := fs.String("root", "", "根目录路径")
	agentID := fs.String("agent", "main", "agent ID")
	probeInterval := fs.Int("probe-interval", 5, "探测间隔秒数")
	candidateStable := fs.Int("candidate-stable", 120, "候选快照连续稳定秒数")
	healthCheckInterval := fs.Int("health-interval", 30, "候选健康检查间隔秒数")
	healthTimeout := fs.Int("health-timeout", 20, "单次健康检查命令超时秒数")
	doctorTimeout := fs.Int("doctor-timeout", 60, "doctor 诊断命令超时秒数")
	doctorDeep := fs.Bool("doctor-deep", false, "候选失败时是否启用 doctor --deep")
	startupProtect := fs.Int("startup-protect", 20, "OpenClaw 刚上线时的启动保护秒数")
	transitionGrace := fs.Int("transition-grace", 45, "过渡保护秒数")
	offlineGrace := fs.Int("offline-grace", 90, "持续离线确认秒数")
	guardExePath := fs.String("guard-exe", "", "guard.exe 路径")
	guardUIExePath := fs.String("guard-ui-exe", "", "guard-ui.exe 路径")
	openclawPath := fs.String("openclaw", "", "openclaw.exe 路径")
	gatewayHost := fs.String("gateway-host", "127.0.0.1", "gateway host")
	gatewayPort := fs.Int("gateway-port", 0, "gateway port (0 = use CLI fallback)")
	logLevel := fs.String("log-level", "info", "log level")

	args := normalizeDetectorArgs(os.Args[1:])
	if err := fs.Parse(args); err != nil {
		log.Fatalf("parse args failed: %v", err)
	}

	cwd, _ := os.Getwd()
	exePath, _ := os.Executable()
	exeDir := filepath.Dir(exePath)

	if *rootDir == "" {
		*rootDir = defaultOpenClawRoot()
	}

	if *guardExePath == "" {
		candidate := filepath.Join(exeDir, "guard.exe")
		if _, err := os.Stat(candidate); err == nil {
			*guardExePath = candidate
		} else {
			*guardExePath = filepath.Join(cwd, "guard.exe")
		}
	}

	if *guardUIExePath == "" {
		candidate := filepath.Join(exeDir, "guard-ui.exe")
		if _, err := os.Stat(candidate); err == nil {
			*guardUIExePath = candidate
		} else {
			*guardUIExePath = filepath.Join(cwd, "guard-ui.exe")
		}
	}

	cfg := DetectorConfig{
		OpenClawPath:            *openclawPath,
		RootDir:                 *rootDir,
		AgentID:                 *agentID,
		ProbeIntervalSeconds:    *probeInterval,
		CandidateStableSeconds:  *candidateStable,
		HealthCheckIntervalSec:  *healthCheckInterval,
		HealthCommandTimeoutSec: *healthTimeout,
		DoctorCommandTimeoutSec: *doctorTimeout,
		DoctorDeep:              *doctorDeep,
		StartupProtectSeconds:   *startupProtect,
		RestartCooldownSeconds:  *transitionGrace,
		OfflineGraceSeconds:     *offlineGrace,
		HealthyConfirmCount:     2,
		UnhealthyConfirmCount:   1,
		LogLevel:                *logLevel,
		GatewayHost:             *gatewayHost,
		GatewayPort:             *gatewayPort,
	}

	logger := log.New(os.Stdout, "detector ", log.LstdFlags)
	logger.Printf("detector starting with config:")
	logger.Printf("  root: %s", cfg.RootDir)
	logger.Printf("  agent: %s", cfg.AgentID)
	logger.Printf("  probe-interval: %d seconds", cfg.ProbeIntervalSeconds)
	logger.Printf("  candidate-stable: %d seconds", cfg.CandidateStableSeconds)
	logger.Printf("  health-interval: %d seconds", cfg.HealthCheckIntervalSec)
	logger.Printf("  health-timeout: %d seconds", cfg.HealthCommandTimeoutSec)
	logger.Printf("  doctor-timeout: %d seconds", cfg.DoctorCommandTimeoutSec)
	logger.Printf("  doctor-deep: %t", cfg.DoctorDeep)
	logger.Printf("  startup-protect: %d seconds", cfg.StartupProtectSeconds)
	logger.Printf("  transition-grace: %d seconds", cfg.RestartCooldownSeconds)
	logger.Printf("  offline-grace: %d seconds", cfg.OfflineGraceSeconds)
	logger.Printf("  healthy-confirm: %d times", cfg.HealthyConfirmCount)
	logger.Printf("  unhealthy-confirm: %d times", cfg.UnhealthyConfirmCount)
	logger.Printf("  log-level: %s", cfg.LogLevel)
	logger.Printf("  guard-exe: %s", *guardExePath)
	logger.Printf("  guard-ui-exe: %s", *guardUIExePath)
	logger.Printf("  openclaw: %s", cfg.OpenClawPath)
	logger.Printf("  gateway-host: %s", cfg.GatewayHost)
	logger.Printf("  gateway-port: %d", cfg.GatewayPort)

	detector := NewDetector(logger, cfg, *guardExePath, *guardUIExePath)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		logger.Printf("received exit signal, shutting down...")
		cancel()
	}()

	logger.Printf("detector running (press Ctrl+C to exit)...")
	if err := detector.Run(ctx); err != nil {
		logger.Printf("detector exited: %v", err)
	}
}
