package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"openclaw-guard-kit/backup"
	"openclaw-guard-kit/config"
	"openclaw-guard-kit/gateway"
	"openclaw-guard-kit/internal/app"
	"openclaw-guard-kit/internal/bootstrap"
	coordsvc "openclaw-guard-kit/internal/coord"
	"openclaw-guard-kit/internal/protocol"
	"openclaw-guard-kit/internal/robot"
	runtimepkg "openclaw-guard-kit/internal/runtime"
	"openclaw-guard-kit/logging"
	"openclaw-guard-kit/notify"
	"openclaw-guard-kit/process"
	watchsvc "openclaw-guard-kit/watch"
)

type commonFlags struct {
	ConfigPath          string
	RootDir             string
	AgentID             string
	OpenClawPath        string
	AuthProfilesPath    string
	IncludeAuthProfiles bool
	BackupDir           string
	StateFile           string
	PollIntervalSeconds int
	LogFile             string
	AutoPrepare         bool
}

type pipeFlags struct {
	PipeName     string
	AgentID      string
	Target       string
	TargetKey    string
	Kind         string
	Path         string
	ClientID     string
	RequestID    string
	LeaseID      string
	LeaseSeconds int
	WaitSeconds  int
	Mode         string
	Reason       string
	LogFile      string
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	subcommand := os.Args[1]
	switch subcommand {
	case "prepare":
		if err := runPrepare(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "prepare failed: %v\n", err)
			os.Exit(1)
		}
	case "watch":
		if err := runWatch(os.Args[2:]); err != nil {
			if errors.Is(err, gateway.ErrPipeInUse) {
				fmt.Fprintf(os.Stderr,
					"watch 启动失败：pipe %s 已被其他 guard 进程占用。\n可能已有旧的 watch 进程仍在运行，请先停止旧进程后重试。\n",
					gateway.DefaultPipeName,
				)
				os.Exit(2)
			}

			fmt.Fprintf(os.Stderr, "watch 启动失败: %v\n", err)
			os.Exit(1)
		}
	case "run-service":
		if err := runWindowsService(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "run-service failed: %v\n", err)
			os.Exit(1)
		}
	case "status":
		if err := runStatus(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "status failed: %v\n", err)
			os.Exit(1)
		}
	case "stop":
		if err := runStop(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "stop failed: %v\n", err)
			os.Exit(1)
		}
	case "request-write":
		if err := runRequestWrite(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "request-write failed: %v\n", err)
			os.Exit(1)
		}
	case "complete-write":
		if err := runCompleteWrite(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "complete-write failed: %v\n", err)
			os.Exit(1)
		}
	case "fail-write":
		if err := runFailWrite(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "fail-write failed: %v\n", err)
			os.Exit(1)
		}
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n\n", subcommand)
		usage()
		os.Exit(1)
	}
}

func runPrepare(args []string) error {
	flags, fs := parseCommonFlags("prepare")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Resolve(config.Options{
		ConfigPath:             flags.ConfigPath,
		RootDir:                flags.RootDir,
		AgentID:                normalizeAgentID(flags.AgentID),
		OpenClawPath:           flags.OpenClawPath,
		AuthProfilesPath:       flags.AuthProfilesPath,
		IncludeAuthProfiles:    flags.IncludeAuthProfiles,
		IncludeAuthProfilesSet: flagWasSet(fs, "with-auth-profiles"),
		BackupDir:              flags.BackupDir,
		StateFile:              flags.StateFile,
		PollIntervalSeconds:    flags.PollIntervalSeconds,
		LogFile:                flags.LogFile,
	})
	if err != nil {
		return err
	}

	logger, err := logging.New(cfg.LogFile)
	if err != nil {
		return err
	}
	defer logger.Close()

	svc := backup.NewService(logger)
	manifest, err := svc.Prepare(context.Background(), cfg)
	if err != nil {
		return err
	}

	logger.Info(
		"prepare completed",
		"agent", cfg.AgentID,
		"targets", len(manifest.Targets),
		"state", manifest.StateFile,
	)
	return nil
}

func runWatch(args []string) error {
	cfg, err := resolveWatchConfig(args)
	if err != nil {
		return err
	}

	logger, err := logging.New(cfg.LogFile)
	if err != nil {
		return err
	}
	defer logger.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	application, err := buildWatchApp(cfg, logger, cancel)
	if err != nil {
		return err
	}

	err = application.Run(runCtx)
	if err != nil && errors.Is(err, gateway.ErrPipeInUse) {
		return gateway.ErrPipeInUse
	}

	return err
}

func resolveWatchConfig(args []string) (config.AppConfig, error) {
	flags, fs := parseCommonFlags("watch")
	restoreOnChange := fs.Bool("restore-on-change", true, "restore baseline when a watched file content changes")
	restoreOnDelete := fs.Bool("restore-on-delete", true, "restore baseline when a watched file is removed")
	if err := fs.Parse(args); err != nil {
		return config.AppConfig{}, err
	}

	cfg, err := config.Resolve(config.Options{
		ConfigPath:             flags.ConfigPath,
		RootDir:                flags.RootDir,
		AgentID:                normalizeAgentID(flags.AgentID),
		OpenClawPath:           flags.OpenClawPath,
		AuthProfilesPath:       flags.AuthProfilesPath,
		IncludeAuthProfiles:    flags.IncludeAuthProfiles,
		IncludeAuthProfilesSet: flagWasSet(fs, "with-auth-profiles"),
		BackupDir:              flags.BackupDir,
		StateFile:              flags.StateFile,
		PollIntervalSeconds:    flags.PollIntervalSeconds,
		LogFile:                flags.LogFile,
	})
	if err != nil {
		return config.AppConfig{}, err
	}

	cfg.RestoreOnChange = *restoreOnChange
	cfg.RestoreOnDelete = *restoreOnDelete
	cfg.AutoPrepare = flags.AutoPrepare

	return cfg, nil
}

func buildWatchApp(cfg config.AppConfig, logger *logging.Logger, stopFunc func()) (*app.App, error) {
	notifier := notify.NewLogNotifier(logger)
	supervisor := process.NewNoopSupervisor(logger)
	backupSvc := backup.NewService(logger)

	robotHub := robot.NewManager(logger)
	robotHub.Register(robot.NewNoopBot())

	eventBus := runtimepkg.NewEventBus(logger, notifier, supervisor, robotHub)
	dispatcher := runtimepkg.NewDispatcher(logger, eventBus)

	if _, err := backup.LoadManifest(cfg.StateFile); err != nil {
		if errors.Is(err, os.ErrNotExist) && cfg.AutoPrepare {
			logger.Info(
				"manifest missing; preparing baseline before watch",
				"root", cfg.RootDir,
				"agent", cfg.AgentID,
				"state", cfg.StateFile,
			)

			if _, err := backupSvc.Prepare(context.Background(), cfg); err != nil {
				return nil, err
			}
		} else {
			return nil, err
		}
	}

	coord := coordsvc.NewCoordinator(logger, dispatcher)
	coord.ConfigureBaselineRefresh(backupSvc, cfg.StateFile)

	watcher := watchsvc.NewService(logger, dispatcher, backupSvc, coord)
	pipeServer := gateway.NewPipeServer(logger, coord, dispatcher, gateway.PipeConfig{
		PipeName: gateway.DefaultPipeName,
		StopFunc: stopFunc,
	})

	watchAdapter := bootstrap.NewWatcherAdapter(logger, "watch-service", func(ctx context.Context) error {
		return watcher.Run(ctx, cfg)
	})

	pipeAdapter := bootstrap.NewPipeServerAdapter(logger, "pipe-server", func(ctx context.Context) error {
		return pipeServer.Run(ctx)
	})

	logger.Info(
		"starting guard watch",
		"root", cfg.RootDir,
		"agent", cfg.AgentID,
		"intervalSeconds", cfg.PollIntervalSeconds,
		"state", cfg.StateFile,
		"pipe", gateway.DefaultPipeName,
	)

	return app.New(cfg, logger, bootstrap.Dependencies{
		PipeServer:   pipeAdapter,
		Watcher:      watchAdapter,
		LeaseManager: coord,
		Notifier:     notifier,
		Supervisor:   supervisor,
		RobotHub:     robotHub,
		EventBus:     eventBus,
		Dispatcher:   dispatcher,
	})
}

func runStatus(args []string) error {
	flags, fs := parsePipeFlags("status")
	if err := fs.Parse(args); err != nil {
		return err
	}

	logger, err := logging.New(flags.LogFile)
	if err != nil {
		return err
	}
	defer logger.Close()

	client := gateway.NewPipeClient(logger, gateway.PipeConfig{
		PipeName: flags.PipeName,
	})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	resp, err := client.Status(ctx)
	if err != nil {
		if isPipeNotRunningError(err) {
			fmt.Println("guard is not running")
			return nil
		}
		return err
	}

	fmt.Println("guard is running")
	if strings.TrimSpace(resp.PipeName) != "" {
		fmt.Printf("pipe: %s\n", resp.PipeName)
	} else {
		fmt.Printf("pipe: %s\n", flags.PipeName)
	}
	return nil
}

func runStop(args []string) error {
	flags, fs := parsePipeFlags("stop")
	if err := fs.Parse(args); err != nil {
		return err
	}

	logger, err := logging.New(flags.LogFile)
	if err != nil {
		return err
	}
	defer logger.Close()

	client := gateway.NewPipeClient(logger, gateway.PipeConfig{
		PipeName: flags.PipeName,
	})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	resp, err := client.Stop(ctx)
	if err != nil {
		if isPipeNotRunningError(err) {
			fmt.Println("guard is not running")
			return nil
		}
		return err
	}

	if strings.TrimSpace(resp.Message) != "" {
		fmt.Println(resp.Message)
	} else {
		fmt.Println("guard stop requested")
	}
	return nil
}

func runRequestWrite(args []string) error {
	flags, fs := parsePipeFlags("request-write")
	if err := fs.Parse(args); err != nil {
		return err
	}

	logger, err := logging.New(flags.LogFile)
	if err != nil {
		return err
	}
	defer logger.Close()

	client := gateway.NewPipeClient(logger, gateway.PipeConfig{
		PipeName: flags.PipeName,
	})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	target := strings.TrimSpace(flags.Target)
	targetKey := strings.TrimSpace(flags.TargetKey)
	path := strings.TrimSpace(flags.Path)

	// 只有 request-write 在完全未指定目标时，默认走 openclaw。
	if target == "" && targetKey == "" && path == "" {
		target = protocol.TargetOpenClaw
	}

	resp, err := client.RequestWrite(ctx, protocol.Message{
		RequestID:    strings.TrimSpace(flags.RequestID),
		ClientID:     normalizeClientID(flags.ClientID),
		AgentID:      normalizeAgentID(flags.AgentID),
		Target:       target,
		TargetKey:    targetKey,
		Kind:         strings.TrimSpace(flags.Kind),
		Path:         path,
		LeaseSeconds: flags.LeaseSeconds,
		WaitSeconds:  flags.WaitSeconds,
		Mode:         normalizeWriteMode(flags.Mode),
		Reason:       strings.TrimSpace(flags.Reason),
	})
	if err != nil {
		return err
	}

	printMessage(resp)
	return nil
}

func runCompleteWrite(args []string) error {
	flags, fs := parsePipeFlags("complete-write")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if err := ensureReleaseTargetSpecified("complete-write", flags); err != nil {
		return err
	}

	logger, err := logging.New(flags.LogFile)
	if err != nil {
		return err
	}
	defer logger.Close()

	client := gateway.NewPipeClient(logger, gateway.PipeConfig{
		PipeName: flags.PipeName,
	})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	resp, err := client.CompleteWrite(ctx, protocol.Message{
		RequestID: strings.TrimSpace(flags.RequestID),
		LeaseID:   strings.TrimSpace(flags.LeaseID),
		ClientID:  normalizeClientID(flags.ClientID),
		AgentID:   normalizeAgentID(flags.AgentID),
		Target:    strings.TrimSpace(flags.Target),
		TargetKey: strings.TrimSpace(flags.TargetKey),
		Kind:      strings.TrimSpace(flags.Kind),
		Path:      strings.TrimSpace(flags.Path),
		Reason:    strings.TrimSpace(flags.Reason),
	})
	if err != nil {
		return err
	}

	printMessage(resp)
	return nil
}

func runFailWrite(args []string) error {
	flags, fs := parsePipeFlags("fail-write")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if err := ensureReleaseTargetSpecified("fail-write", flags); err != nil {
		return err
	}

	logger, err := logging.New(flags.LogFile)
	if err != nil {
		return err
	}
	defer logger.Close()

	client := gateway.NewPipeClient(logger, gateway.PipeConfig{
		PipeName: flags.PipeName,
	})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	resp, err := client.FailWrite(ctx, protocol.Message{
		RequestID: strings.TrimSpace(flags.RequestID),
		LeaseID:   strings.TrimSpace(flags.LeaseID),
		ClientID:  normalizeClientID(flags.ClientID),
		AgentID:   normalizeAgentID(flags.AgentID),
		Target:    strings.TrimSpace(flags.Target),
		TargetKey: strings.TrimSpace(flags.TargetKey),
		Kind:      strings.TrimSpace(flags.Kind),
		Path:      strings.TrimSpace(flags.Path),
		Reason:    strings.TrimSpace(flags.Reason),
	})
	if err != nil {
		return err
	}

	printMessage(resp)
	return nil
}

func parseCommonFlags(name string) (*commonFlags, *flag.FlagSet) {
	cfg := &commonFlags{}
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.StringVar(&cfg.ConfigPath, "config", "", "optional JSON config file")
	fs.StringVar(&cfg.RootDir, "root", "", "root directory of the guarded files")
	fs.StringVar(&cfg.AgentID, "agent", "main", "agent id whose auth-profiles.json should be guarded")
	fs.StringVar(&cfg.OpenClawPath, "openclaw", "", "path to openclaw.json")
	fs.StringVar(&cfg.AuthProfilesPath, "auth-profiles", "", "path to auth-profiles.json")
	fs.BoolVar(&cfg.IncludeAuthProfiles, "with-auth-profiles", true, "include auth-profiles.json when it exists")
	fs.StringVar(&cfg.BackupDir, "backup-dir", "", "directory storing baseline backups")
	fs.StringVar(&cfg.StateFile, "state-file", "", "manifest file path")
	fs.IntVar(&cfg.PollIntervalSeconds, "interval", 2, "polling interval in seconds (watch only)")
	fs.StringVar(&cfg.LogFile, "log-file", "", "optional log file path")
	fs.BoolVar(&cfg.AutoPrepare, "auto-prepare", true, "auto-bootstrap baseline if no manifest exists (watch only)")
	return cfg, fs
}

func parsePipeFlags(name string) (*pipeFlags, *flag.FlagSet) {
	cfg := &pipeFlags{}
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.StringVar(&cfg.PipeName, "pipe", gateway.DefaultPipeName, "named pipe path")
	fs.StringVar(&cfg.AgentID, "agent", "main", "agent id")
	fs.StringVar(&cfg.Target, "target", "", "legacy target name")
	fs.StringVar(&cfg.TargetKey, "target-key", "", "resolved target key, e.g. openclaw / auth:main / models:tester")
	fs.StringVar(&cfg.Kind, "kind", "", "target kind, e.g. openclaw / auth-profiles / models")
	fs.StringVar(&cfg.Path, "path", "", "optional direct path")
	fs.StringVar(&cfg.ClientID, "client", "guard-cli", "client id")
	fs.StringVar(&cfg.RequestID, "request", "", "request id")
	fs.StringVar(&cfg.LeaseID, "lease-id", "", "lease id returned by request-write")
	fs.IntVar(&cfg.LeaseSeconds, "lease", 60, "lease seconds (request-write only)")
	fs.IntVar(&cfg.WaitSeconds, "wait", 0, "wait seconds for queued request (request-write only)")
	fs.StringVar(&cfg.Mode, "mode", protocol.WriteModeReject, "request mode: reject or block")
	fs.StringVar(&cfg.Reason, "reason", "", "optional reason")
	fs.StringVar(&cfg.LogFile, "log-file", "", "optional log file path")
	return cfg, fs
}

func printMessage(msg protocol.Message) {
	fmt.Printf("type: %s\n", msg.Type)
	fmt.Printf("status: %s\n", msg.Status)
	fmt.Printf("requestId: %s\n", msg.RequestID)
	fmt.Printf("leaseId: %s\n", msg.LeaseID)
	fmt.Printf("clientId: %s\n", msg.ClientID)
	fmt.Printf("agentId: %s\n", msg.AgentID)
	fmt.Printf("target: %s\n", msg.Target)
	fmt.Printf("targetKey: %s\n", msg.TargetKey)
	fmt.Printf("kind: %s\n", msg.Kind)
	fmt.Printf("path: %s\n", msg.Path)
	fmt.Printf("leaseSeconds: %d\n", msg.LeaseSeconds)
	fmt.Printf("waitSeconds: %d\n", msg.WaitSeconds)
	fmt.Printf("queuePosition: %d\n", msg.QueuePosition)
	fmt.Printf("mode: %s\n", msg.Mode)
	fmt.Printf("reason: %s\n", msg.Reason)
	if !msg.ExpiresAt.IsZero() {
		fmt.Printf("expiresAt: %s\n", msg.ExpiresAt.Format("2006-01-02 15:04:05Z07:00"))
	} else {
		fmt.Printf("expiresAt: \n")
	}
	fmt.Printf("message: %s\n", msg.Message)
	fmt.Printf("at: %s\n", msg.At.Format("2006-01-02 15:04:05Z07:00"))
}

func flagWasSet(fs *flag.FlagSet, name string) bool {
	set := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			set = true
		}
	})
	return set
}

func normalizeAgentID(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return "main"
	}
	return v
}

func normalizeClientID(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return "guard-cli"
	}
	return v
}

func isPipeNotRunningError(err error) bool {
	if err == nil {
		return false
	}

	if isPipeFileNotFoundErrno(err) {
		return true
	}

	s := strings.ToLower(err.Error())
	return strings.Contains(s, "cannot find the file specified") ||
		strings.Contains(s, "the system cannot find the file specified") ||
		strings.Contains(s, "file not found")
}

func normalizeWriteMode(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	switch v {
	case protocol.WriteModeBlock:
		return protocol.WriteModeBlock
	case protocol.WriteModeReject, "":
		return protocol.WriteModeReject
	default:
		return protocol.WriteModeReject
	}
}

func ensureReleaseTargetSpecified(cmd string, flags *pipeFlags) error {
	target := strings.TrimSpace(flags.Target)
	targetKey := strings.TrimSpace(flags.TargetKey)
	path := strings.TrimSpace(flags.Path)

	if target != "" || targetKey != "" || path != "" {
		return nil
	}

	return fmt.Errorf(
		"%s requires --target-key, --target, or --path; for auth-profiles use --lease-id <id> --agent main --target-key auth:main --kind auth-profiles --client <client> --request <request>",
		cmd,
	)
}

func usage() {
	text := `openclaw-guard-kit / guard.exe (v2)

Usage:
  guard prepare [flags]
  guard watch   [flags]
  guard status  [flags]
  guard stop    [flags]
  guard run-service [flags]

Testing commands:
  guard request-write  [flags]
  guard complete-write [flags]
  guard fail-write     [flags]

Implemented in v2:
  - prepare: baseline backup for guarded targets
  - watch:   daemon watch + restore from baseline + named pipe server
  - status:  check whether guard watch is running
  - stop:    request running guard watch to stop gracefully
  - pipe:    dynamic write request / complete / fail testing
  - run-service: internal Windows service entrypoint

Examples:
  guard prepare --root C:\Users\Administrator\.openclaw --agent main
  guard watch --root C:\Users\Administrator\.openclaw --agent main --interval 2
  guard status
  guard stop

  guard request-write --agent main --target-key openclaw --kind openclaw --client test-cli --request req-1 --lease 180
  guard request-write --agent main --kind auth-profiles --path C:\Users\Administrator\.openclaw\agents\main\agent\auth-profiles.json --client test-cli --request req-2 --lease 180 --mode block --wait 30

  guard complete-write --lease-id lease-123456789 --target-key openclaw --kind openclaw
  guard fail-write --lease-id lease-123456789 --target-key openclaw --kind openclaw --reason manual-test

  guard complete-write --lease-id lease-123456789 --agent main --target-key auth:main --kind auth-profiles --client test-cli --request req-auth-complete
  guard fail-write --lease-id lease-123456789 --agent main --target-key auth:main --kind auth-profiles --client test-cli --request req-auth-fail
`
	fmt.Fprintln(os.Stdout, strings.TrimSpace(text))
}

var errNoTargets = errors.New("no watched targets were resolved")
