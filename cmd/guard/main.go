package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"openclaw-guard-kit/backup"
	"openclaw-guard-kit/config"
	"openclaw-guard-kit/gateway"
	"openclaw-guard-kit/internal/app"
	"openclaw-guard-kit/internal/bootstrap"
	coordsvc "openclaw-guard-kit/internal/coord"
	guardclient "openclaw-guard-kit/internal/guard"
	internalnotify "openclaw-guard-kit/internal/notify"
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
	Agents              string
	OpenClawPath        string
	AuthProfilesPath    string
	IncludeAuthProfiles bool
	IncludeModels       bool
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
type guardedWriteFlags struct {
	common     *commonFlags
	TargetPath string
	SourcePath string
}

type openClawOpFlags struct {
	common      *commonFlags
	OpenClawBin string
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
	case "pause-monitoring":
		if err := runPauseMonitoring(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "pause-monitoring failed: %v\n", err)
			os.Exit(1)
		}
	case "resume-monitoring":
		if err := runResumeMonitoring(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "resume-monitoring failed: %v\n", err)
			os.Exit(1)
		}
	case "monitoring-status":
		if err := runMonitoringStatus(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "monitoring-status failed: %v\n", err)
			os.Exit(1)
		}
	case "candidate-status":
		if err := runCandidateStatus(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "candidate-status failed: %v\n", err)
			os.Exit(1)
		}
	case "promote-candidate":
		if err := runPromoteCandidate(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "promote-candidate failed: %v\n", err)
			os.Exit(1)
		}
	case "discard-candidate":
		if err := runDiscardCandidate(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "discard-candidate failed: %v\n", err)
			os.Exit(1)
		}
	case "mark-bad-candidate":
		if err := runMarkBadCandidate(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "mark-bad-candidate failed: %v\n", err)
			os.Exit(1)
		}
	case "retry-candidate":
		if err := runRetryCandidate(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "retry-candidate failed: %v\n", err)
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
	case "guarded-write":
		if err := runGuardedWrite(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "guarded-write failed: %v\n", err)
			os.Exit(1)
		}
	case "openclaw-op":
		if err := runOpenClawOp(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "openclaw-op failed: %v\n", err)
			os.Exit(1)
		}
	case "complete-telegram-binding":
		if err := runCompleteTelegramBinding(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "complete-telegram-binding failed: %v\n", err)
			os.Exit(1)
		}
	case "save-telegram-credentials":
		if err := runSaveTelegramCredentials(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "save-telegram-credentials failed: %v\n", err)
			os.Exit(1)
		}
	case "unbind-telegram":
		if err := runUnbindTelegram(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "unbind-telegram failed: %v\n", err)
			os.Exit(1)
		}
	case "save-feishu-credentials":
		if err := runSaveFeishuCredentials(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "save-feishu-credentials failed: %v\n", err)
			os.Exit(1)
		}
	case "complete-feishu-binding":
		if err := runCompleteFeishuBinding(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "complete-feishu-binding failed: %v\n", err)
			os.Exit(1)
		}
	case "unbind-feishu":
		if err := runUnbindFeishu(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "unbind-feishu failed: %v\n", err)
			os.Exit(1)
		}
	case "test-feishu-message":
		if err := runTestFeishuMessage(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "test-feishu-message failed: %v\n", err)
			os.Exit(1)
		}
	case "save-wecom-credentials":
		if err := runSaveWecomCredentials(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "save-wecom-credentials failed: %v\n", err)
			os.Exit(1)
		}
	case "test-wecom-connection":
		if err := runTestWecomConnection(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "test-wecom-connection failed: %v\n", err)
			os.Exit(1)
		}
	case "complete-wecom-binding":
		if err := runCompleteWecomBinding(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "complete-wecom-binding failed: %v\n", err)
			os.Exit(1)
		}
	case "unbind-wecom":
		if err := runUnbindWecom(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "unbind-wecom failed: %v\n", err)
			os.Exit(1)
		}
	case "test-wecom-message":
		if err := runTestWecomMessage(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "test-wecom-message failed: %v\n", err)
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
		Agents:                 parseAgentsList(flags.Agents),
		OpenClawPath:           flags.OpenClawPath,
		AuthProfilesPath:       flags.AuthProfilesPath,
		IncludeAuthProfiles:    flags.IncludeAuthProfiles,
		IncludeAuthProfilesSet: flagWasSet(fs, "with-auth-profiles"),
		IncludeModels:          flags.IncludeModels,
		IncludeModelsSet:       flagWasSet(fs, "with-models"),
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
		"targets", len(manifest.TrustedTargets),
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
		Agents:                 parseAgentsList(flags.Agents),
		OpenClawPath:           flags.OpenClawPath,
		AuthProfilesPath:       flags.AuthProfilesPath,
		IncludeAuthProfiles:    flags.IncludeAuthProfiles,
		IncludeAuthProfilesSet: flagWasSet(fs, "with-auth-profiles"),
		IncludeModels:          flags.IncludeModels,
		IncludeModelsSet:       flagWasSet(fs, "with-models"),
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
	// 初始化 notifier 所需的路径和凭证存储
	notify.SetRootDir(cfg.RootDir)
	notify.InitCredentialsStore(cfg.RootDir)

	// 创建聚合通知器：Console + Telegram + Feishu + WeCom
	consoleNotifier := notify.NewLogNotifier(logger)
	telegramNotifier := notify.TelegramNotifier{}
	feishuNotifier := notify.FeishuNotifier{}
	wecomNotifier := notify.WeComNotifier{}

	// 使用 MultiNotifier 聚合所有通知渠道
	notifier := internalnotify.NewMultiNotifier(logger, consoleNotifier, telegramNotifier, feishuNotifier, wecomNotifier)

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
	coord.ConfigureBackupDir(cfg.BackupDir)

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

type bindingFlags struct {
	RootDir     string
	AccountID   string
	Token       string
	AppSecret   string
	BotID       string
	Secret      string
	SenderID    string
	DisplayName string
	Code        string
	Message     string
	LogFile     string
}

func runCompleteTelegramBinding(args []string) error {
	flags, fs := parseBindingFlags("complete-telegram-binding")
	if err := fs.Parse(args); err != nil {
		return err
	}

	// 验证必要参数
	if flags.RootDir == "" {
		return fmt.Errorf("--root is required")
	}
	if flags.AccountID == "" {
		return fmt.Errorf("account-id is required")
	}
	if flags.SenderID == "" {
		return fmt.Errorf("sender-id is required")
	}
	if flags.Code == "" {
		return fmt.Errorf("code is required")
	}

	logger, err := logging.New(flags.LogFile)
	if err != nil {
		return err
	}
	defer logger.Close()

	// 初始化 store，使用 root 绝对路径
	notify.SetRootDir(flags.RootDir)
	storePath := notify.BindingsPath(flags.RootDir)
	s, err := notify.NewStore(storePath)
	if err != nil {
		return fmt.Errorf("failed to open store: %v", err)
	}

	// 校验 pending：查找配对码是否存在且未过期
	pending, found := s.FindPendingByCode("telegram", flags.AccountID, flags.Code)
	if !found {
		return fmt.Errorf("pairing code not found or expired")
	}
	// 校验 senderID 是否匹配
	if pending.SenderID != flags.SenderID {
		return fmt.Errorf("senderID mismatch")
	}

	// 调用 store.MarkBound（软件通知绑定）- 注意：MarkBound 内部已清理 pending
	record, err := s.MarkBound(
		"telegram",
		flags.AccountID,
		flags.SenderID,
		flags.DisplayName,
		flags.Code,
	)
	if err != nil {
		return fmt.Errorf("failed to mark bound: %v", err)
	}

	// 自动发送确认消息（绑定成功后）
	notify.InitCredentialsStore(flags.RootDir)
	token := notify.GetTelegramToken()
	chatID, err := strconv.ParseInt(flags.SenderID, 10, 64)
	confirmationStatus := "unknown"
	if err != nil {
		confirmationStatus = "failed"
		fmt.Printf("binding completed, but failed to parse chatID for confirmation: %v\n", err)
	} else {
		success, errMsg := notify.SendTelegramMessage(token, chatID, "🔔 OpenClaw Guard 绑定成功！您现在会收到来自 OpenClaw 的通知。")
		if !success {
			// 绑定已保存成功，但确认消息发送失败，不回滚绑定
			confirmationStatus = "failed"
			fmt.Printf("binding completed, but confirmation message failed: %s\n", errMsg)
			// 持久化失败结果
			_ = s.UpdateBindingTestResult("telegram", flags.AccountID, flags.SenderID, errMsg, "failed")
		} else {
			confirmationStatus = "ok"
			// 持久化成功结果
			_ = s.UpdateBindingTestResult("telegram", flags.AccountID, flags.SenderID, "连接成功", "ok")
		}
	}

	fmt.Printf("binding completed successfully\n")
	fmt.Printf("id: %s\n", record.ID)
	fmt.Printf("channel: %s\n", record.Channel)
	fmt.Printf("accountId: %s\n", record.AccountID)
	fmt.Printf("senderId: %s\n", record.SenderID)
	fmt.Printf("displayName: %s\n", record.DisplayName)
	fmt.Printf("pairingCode: %s\n", record.PairingCode)
	fmt.Printf("status: %s\n", record.Status)
	fmt.Printf("confirmationSent: %s\n", confirmationStatus)

	return nil
}

func parseBindingFlags(name string) (*bindingFlags, *flag.FlagSet) {
	cfg := &bindingFlags{}
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.StringVar(&cfg.RootDir, "root", "", "root directory (OpenClaw data directory)")
	fs.StringVar(&cfg.AccountID, "account-id", "", "account id (telegram bot id / feishu app id / wecom bot id)")
	fs.StringVar(&cfg.Token, "token", "", "telegram bot token")
	fs.StringVar(&cfg.AppSecret, "app-secret", "", "feishu app secret")
	fs.StringVar(&cfg.BotID, "bot-id", "", "wecom bot id")
	fs.StringVar(&cfg.Secret, "secret", "", "wecom bot secret")
	fs.StringVar(&cfg.SenderID, "sender-id", "", "sender id (telegram chat id / feishu open_id / wecom user_id)")
	fs.StringVar(&cfg.DisplayName, "display-name", "", "display name")
	fs.StringVar(&cfg.Code, "code", "", "pairing code")
	fs.StringVar(&cfg.Message, "message", "", "test message content")
	fs.StringVar(&cfg.LogFile, "log-file", "", "optional log file path")
	return cfg, fs
}

func parseCommonFlags(name string) (*commonFlags, *flag.FlagSet) {
	cfg := &commonFlags{}
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.StringVar(&cfg.ConfigPath, "config", "", "optional JSON config file")
	fs.StringVar(&cfg.RootDir, "root", "", "root directory of the guarded files")
	fs.StringVar(&cfg.AgentID, "agent", "main", "agent id (default context for operations)")
	fs.StringVar(&cfg.Agents, "agents", "", "comma-separated list of agents to protect (default: auto-detect all)")
	fs.StringVar(&cfg.OpenClawPath, "openclaw", "", "path to openclaw.json")
	fs.StringVar(&cfg.AuthProfilesPath, "auth-profiles", "", "path to auth-profiles.json")
	fs.BoolVar(&cfg.IncludeAuthProfiles, "with-auth-profiles", true, "include auth-profiles.json when it exists")
	fs.BoolVar(&cfg.IncludeModels, "with-models", true, "include models.json when it exists")
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
func parseGuardedWriteFlags(name string) (*guardedWriteFlags, *flag.FlagSet) {
	common, fs := parseCommonFlags(name)
	cfg := &guardedWriteFlags{common: common}
	fs.StringVar(&cfg.TargetPath, "path", "", "target protected file path")
	fs.StringVar(&cfg.SourcePath, "from", "", "source file containing new content")
	return cfg, fs
}

func parseOpenClawOpFlags(name string) (*openClawOpFlags, *flag.FlagSet) {
	common, fs := parseCommonFlags(name)
	cfg := &openClawOpFlags{common: common}
	fs.StringVar(&cfg.OpenClawBin, "openclaw-bin", "", "optional OpenClaw executable path")
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
func runGuardedWrite(args []string) error {
	flags, fs := parseGuardedWriteFlags("guarded-write")
	if err := fs.Parse(args); err != nil {
		return err
	}

	targetPath := strings.TrimSpace(flags.TargetPath)
	if targetPath == "" {
		return fmt.Errorf("guarded-write requires --path")
	}
	sourcePath := strings.TrimSpace(flags.SourcePath)
	if sourcePath == "" {
		return fmt.Errorf("guarded-write requires --from")
	}

	cfg, err := resolveCommonConfig(flags.common, fs)
	if err != nil {
		return err
	}

	data, err := os.ReadFile(sourcePath)
	if err != nil {
		return fmt.Errorf("read --from file failed: %w", err)
	}

	guardExe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve guard executable failed: %w", err)
	}

	client := guardclient.NewClient(guardExe, cfg.RootDir, normalizeAgentID(flags.common.AgentID))
	if err := client.WriteFile(context.Background(), targetPath, data); err != nil {
		return err
	}

	resolvedTargetPath := targetPath
	if !filepath.IsAbs(resolvedTargetPath) {
		resolvedTargetPath = filepath.Join(cfg.RootDir, resolvedTargetPath)
	}
	resolvedSourcePath, err := filepath.Abs(sourcePath)
	if err != nil {
		resolvedSourcePath = sourcePath
	}

	fmt.Println("operation: guarded-write")
	fmt.Println("status: ok")
	fmt.Printf("agentId: %s\n", normalizeAgentID(flags.common.AgentID))
	fmt.Printf("path: %s\n", resolvedTargetPath)
	fmt.Printf("from: %s\n", resolvedSourcePath)
	fmt.Printf("bytes: %d\n", len(data))
	return nil
}

func runOpenClawOp(args []string) error {
	flags, fs := parseOpenClawOpFlags("openclaw-op")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := resolveCommonConfig(flags.common, fs)
	if err != nil {
		return err
	}

	commandArgs := fs.Args()
	if len(commandArgs) == 0 {
		return fmt.Errorf("openclaw-op requires an OpenClaw command after --")
	}

	monitoringWasPaused, err := isMonitoringPaused(cfg)
	if err != nil {
		return err
	}

	pausedByThisCommand := false
	if !monitoringWasPaused {
		if err := pauseMonitoring(cfg); err != nil {
			return err
		}
		pausedByThisCommand = true
	}

	output, cmdErr := runOpenClawCommand(commandArgs, strings.TrimSpace(flags.OpenClawBin))

	resumedByThisCommand := false
	var resumeErr error
	if pausedByThisCommand {
		resumeErr = resumeMonitoring(cfg)
		if resumeErr == nil {
			resumedByThisCommand = true
		}
	}

	fmt.Println("operation: openclaw-op")
	fmt.Println("mode: compatibility")
	fmt.Println("flow: pause-monitoring -> openclaw-command -> resume-monitoring")
	if cmdErr == nil && resumeErr == nil {
		fmt.Println("status: ok")
	} else {
		fmt.Println("status: failed")
	}
	fmt.Printf("monitoringWasPaused: %t\n", monitoringWasPaused)
	fmt.Printf("pausedByThisCommand: %t\n", pausedByThisCommand)
	fmt.Printf("resumedByThisCommand: %t\n", resumedByThisCommand)
	fmt.Printf("command: %s\n", strings.Join(commandArgs, " "))
	if output != "" {
		fmt.Println("commandOutput:")
		fmt.Println(output)
	}

	if cmdErr != nil && resumeErr != nil {
		return fmt.Errorf("OpenClaw command failed: %v; resume-monitoring also failed: %w", cmdErr, resumeErr)
	}
	if cmdErr != nil {
		return fmt.Errorf("OpenClaw command failed: %w", cmdErr)
	}
	if resumeErr != nil {
		return fmt.Errorf("OpenClaw command succeeded but resume-monitoring failed: %w", resumeErr)
	}
	return nil
}

func isMonitoringPaused(cfg config.AppConfig) (bool, error) {
	path := monitoringPauseFilePath(cfg)
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func pauseMonitoring(cfg config.AppConfig) error {
	path := monitoringPauseFilePath(cfg)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte("paused=true\n"), 0644)
}

func resumeMonitoring(cfg config.AppConfig) error {
	logger, err := logging.New(cfg.LogFile)
	if err != nil {
		return err
	}
	defer logger.Close()

	backupSvc := backup.NewService(logger)
	if err := createMonitoringCandidatesFromCurrent(context.Background(), backupSvc, cfg); err != nil {
		return err
	}

	path := monitoringPauseFilePath(cfg)
	err = os.Remove(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func runOpenClawCommand(commandArgs []string, explicitBin string) (string, error) {
	cmdArgs := append([]string(nil), commandArgs...)
	if len(cmdArgs) == 0 {
		return "", fmt.Errorf("empty command")
	}

	commandPath := strings.TrimSpace(explicitBin)
	if commandPath == "" {
		first := strings.TrimSpace(cmdArgs[0])
		if strings.EqualFold(first, "openclaw") || strings.EqualFold(first, "openclaw.exe") || strings.EqualFold(first, "openclaw.cmd") {
			commandPath = first
			cmdArgs = cmdArgs[1:]
		} else {
			commandPath = "openclaw"
		}
	}

	cmd := exec.Command(commandPath, cmdArgs...)
	output, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(output))
	if err != nil {
		if text != "" {
			return text, fmt.Errorf("%s", text)
		}
		return text, err
	}
	return text, nil
}
func resolveCommonConfig(flags *commonFlags, fs *flag.FlagSet) (config.AppConfig, error) {
	return config.Resolve(config.Options{
		ConfigPath:             flags.ConfigPath,
		RootDir:                flags.RootDir,
		AgentID:                normalizeAgentID(flags.AgentID),
		Agents:                 parseAgentsList(flags.Agents),
		OpenClawPath:           flags.OpenClawPath,
		AuthProfilesPath:       flags.AuthProfilesPath,
		IncludeAuthProfiles:    flags.IncludeAuthProfiles,
		IncludeAuthProfilesSet: flagWasSet(fs, "with-auth-profiles"),
		IncludeModels:          flags.IncludeModels,
		IncludeModelsSet:       flagWasSet(fs, "with-models"),
		BackupDir:              flags.BackupDir,
		StateFile:              flags.StateFile,
		PollIntervalSeconds:    flags.PollIntervalSeconds,
		LogFile:                flags.LogFile,
	})
}
func resolveMonitoringConfig(args []string, name string) (config.AppConfig, error) {
	flags, fs := parseCommonFlags(name)
	if err := fs.Parse(args); err != nil {
		return config.AppConfig{}, err
	}

	cfg, err := resolveCommonConfig(flags, fs)
	if err != nil {
		return config.AppConfig{}, err
	}

	return cfg, nil
}

func monitoringPauseFilePath(cfg config.AppConfig) string {
	return filepath.Join(filepath.Dir(cfg.StateFile), "monitor.paused")
}

func runPauseMonitoring(args []string) error {
	cfg, err := resolveMonitoringConfig(args, "pause-monitoring")
	if err != nil {
		return err
	}

	path := monitoringPauseFilePath(cfg)
	if err := pauseMonitoring(cfg); err != nil {
		return err
	}

	fmt.Printf("monitoring paused\n")
	fmt.Printf("pauseFile: %s\n", path)
	return nil
}

func runResumeMonitoring(args []string) error {
	cfg, err := resolveMonitoringConfig(args, "resume-monitoring")
	if err != nil {
		return err
	}

	path := monitoringPauseFilePath(cfg)
	if err := resumeMonitoring(cfg); err != nil {
		return err
	}

	fmt.Printf("monitoring resumed\n")
	fmt.Printf("candidate snapshots created from current files\n")
	fmt.Printf("pauseFile: %s\n", path)
	return nil
}

func createMonitoringCandidatesFromCurrent(ctx context.Context, backupSvc *backup.Service, cfg config.AppConfig) error {
	manifest, err := backup.LoadManifest(cfg.StateFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			_, err := backupSvc.Prepare(ctx, cfg)
			return err
		}
		return err
	}

	if len(manifest.TrustedTargets) == 0 {
		_, err := backupSvc.Prepare(ctx, cfg)
		return err
	}

	for _, snapshot := range manifest.TrustedTargets {
		targetName := strings.TrimSpace(snapshot.TargetKeyOrName())
		if targetName == "" {
			targetName = strings.TrimSpace(snapshot.Name)
		}
		if targetName == "" {
			continue
		}

		candidate, err := backupSvc.CreateCandidateSnapshot(ctx, config.FileTarget{
			Name: targetName,
			Path: snapshot.SourcePath,
		}, cfg.BackupDir)
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

		if err := backupSvc.UpsertCandidateSnapshot(cfg.StateFile, candidate); err != nil {
			return fmt.Errorf("persist candidate snapshot for %s failed: %w", targetName, err)
		}
	}

	return nil
}

func parseCandidateFlags(name string) (*commonFlags, *flag.FlagSet, *string) {
	flags, fs := parseCommonFlags(name)
	target := fs.String("target", "", "candidate target key, e.g. openclaw / auth:main / models:tester")
	return flags, fs, target
}

func runCandidateStatus(args []string) error {
	flags, fs, _ := parseCandidateFlags("candidate-status")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Resolve(config.Options{
		ConfigPath:             flags.ConfigPath,
		RootDir:                flags.RootDir,
		AgentID:                normalizeAgentID(flags.AgentID),
		Agents:                 parseAgentsList(flags.Agents),
		OpenClawPath:           flags.OpenClawPath,
		AuthProfilesPath:       flags.AuthProfilesPath,
		IncludeAuthProfiles:    flags.IncludeAuthProfiles,
		IncludeAuthProfilesSet: flagWasSet(fs, "with-auth-profiles"),
		IncludeModels:          flags.IncludeModels,
		IncludeModelsSet:       flagWasSet(fs, "with-models"),
		BackupDir:              flags.BackupDir,
		StateFile:              flags.StateFile,
		PollIntervalSeconds:    flags.PollIntervalSeconds,
		LogFile:                flags.LogFile,
	})
	if err != nil {
		return err
	}

	manifest, err := backup.LoadManifest(cfg.StateFile)
	if err != nil {
		return err
	}

	fmt.Printf("trustedTargets: %d\n", len(manifest.TrustedTargets))
	for _, snapshot := range manifest.TrustedTargets {
		fmt.Printf("trusted: %s | %s | %s\n", snapshot.TargetKeyOrName(), snapshot.SourcePath, snapshot.BackupPath)
	}

	fmt.Printf("candidateTargets: %d\n", len(manifest.CandidateTargets))
	for _, snapshot := range manifest.CandidateTargets {
		fmt.Printf("candidate: %s | state=%s | source=%s | backup=%s\n",
			snapshot.TargetKeyOrName(),
			candidateStateText(snapshot.State),
			snapshot.SourcePath,
			snapshot.BackupPath,
		)
	}

	return nil
}
func candidateStateText(state backup.SnapshotState) string {
	switch state {
	case backup.SnapshotStateCandidate:
		return "candidate"
	case backup.SnapshotStateHealthy:
		return "healthy"
	case backup.SnapshotStateBad:
		return "bad"
	default:
		return "unknown"
	}
}
func runPromoteCandidate(args []string) error {
	flags, fs, target := parseCandidateFlags("promote-candidate")
	if err := fs.Parse(args); err != nil {
		return err
	}

	targetName := strings.TrimSpace(*target)
	if targetName == "" {
		return errors.New("promote-candidate requires --target")
	}

	cfg, err := config.Resolve(config.Options{
		ConfigPath:             flags.ConfigPath,
		RootDir:                flags.RootDir,
		AgentID:                normalizeAgentID(flags.AgentID),
		Agents:                 parseAgentsList(flags.Agents),
		OpenClawPath:           flags.OpenClawPath,
		AuthProfilesPath:       flags.AuthProfilesPath,
		IncludeAuthProfiles:    flags.IncludeAuthProfiles,
		IncludeAuthProfilesSet: flagWasSet(fs, "with-auth-profiles"),
		IncludeModels:          flags.IncludeModels,
		IncludeModelsSet:       flagWasSet(fs, "with-models"),
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

	backupSvc := backup.NewService(logger)
	if err := backupSvc.PromoteSnapshotToHealthy(cfg.StateFile, targetName); err != nil {
		return err
	}

	fmt.Printf("candidate promoted\n")
	fmt.Printf("target: %s\n", targetName)
	return nil
}

func runDiscardCandidate(args []string) error {
	flags, fs, target := parseCandidateFlags("discard-candidate")
	if err := fs.Parse(args); err != nil {
		return err
	}

	targetName := strings.TrimSpace(*target)
	if targetName == "" {
		return errors.New("discard-candidate requires --target")
	}

	cfg, err := config.Resolve(config.Options{
		ConfigPath:             flags.ConfigPath,
		RootDir:                flags.RootDir,
		AgentID:                normalizeAgentID(flags.AgentID),
		Agents:                 parseAgentsList(flags.Agents),
		OpenClawPath:           flags.OpenClawPath,
		AuthProfilesPath:       flags.AuthProfilesPath,
		IncludeAuthProfiles:    flags.IncludeAuthProfiles,
		IncludeAuthProfilesSet: flagWasSet(fs, "with-auth-profiles"),
		IncludeModels:          flags.IncludeModels,
		IncludeModelsSet:       flagWasSet(fs, "with-models"),
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

	backupSvc := backup.NewService(logger)
	if err := backupSvc.DiscardCandidate(cfg.StateFile, targetName); err != nil {
		return err
	}

	fmt.Printf("candidate discarded\n")
	fmt.Printf("target: %s\n", targetName)
	return nil
}
func runMarkBadCandidate(args []string) error {
	flags, fs, target := parseCandidateFlags("mark-bad-candidate")
	if err := fs.Parse(args); err != nil {
		return err
	}

	targetName := strings.TrimSpace(*target)
	if targetName == "" {
		return errors.New("mark-bad-candidate requires --target")
	}

	cfg, err := config.Resolve(config.Options{
		ConfigPath:             flags.ConfigPath,
		RootDir:                flags.RootDir,
		AgentID:                normalizeAgentID(flags.AgentID),
		Agents:                 parseAgentsList(flags.Agents),
		OpenClawPath:           flags.OpenClawPath,
		AuthProfilesPath:       flags.AuthProfilesPath,
		IncludeAuthProfiles:    flags.IncludeAuthProfiles,
		IncludeAuthProfilesSet: flagWasSet(fs, "with-auth-profiles"),
		IncludeModels:          flags.IncludeModels,
		IncludeModelsSet:       flagWasSet(fs, "with-models"),
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

	backupSvc := backup.NewService(logger)
	if err := backupSvc.MarkSnapshotAsBad(cfg.StateFile, targetName); err != nil {
		return err
	}

	fmt.Printf("candidate marked bad\n")
	fmt.Printf("target: %s\n", targetName)
	return nil
}

func runRetryCandidate(args []string) error {
	flags, fs, target := parseCandidateFlags("retry-candidate")
	if err := fs.Parse(args); err != nil {
		return err
	}

	targetName := strings.TrimSpace(*target)
	if targetName == "" {
		return errors.New("retry-candidate requires --target")
	}

	cfg, err := config.Resolve(config.Options{
		ConfigPath:             flags.ConfigPath,
		RootDir:                flags.RootDir,
		AgentID:                normalizeAgentID(flags.AgentID),
		Agents:                 parseAgentsList(flags.Agents),
		OpenClawPath:           flags.OpenClawPath,
		AuthProfilesPath:       flags.AuthProfilesPath,
		IncludeAuthProfiles:    flags.IncludeAuthProfiles,
		IncludeAuthProfilesSet: flagWasSet(fs, "with-auth-profiles"),
		IncludeModels:          flags.IncludeModels,
		IncludeModelsSet:       flagWasSet(fs, "with-models"),
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

	backupSvc := backup.NewService(logger)
	if err := backupSvc.RetryCandidate(cfg.StateFile, targetName); err != nil {
		return err
	}

	fmt.Printf("candidate retry requested\n")
	fmt.Printf("target: %s\n", targetName)
	return nil
}
func runMonitoringStatus(args []string) error {
	cfg, err := resolveMonitoringConfig(args, "monitoring-status")
	if err != nil {
		return err
	}

	path := monitoringPauseFilePath(cfg)
	_, err = os.Stat(path)
	if err == nil {
		fmt.Println("monitoring: paused")
		fmt.Printf("pauseFile: %s\n", path)
		return nil
	}
	if !os.IsNotExist(err) {
		return err
	}

	fmt.Println("monitoring: active")
	fmt.Printf("pauseFile: %s\n", path)
	return nil
}
func usage() {
	text := `openclaw-guard-kit / guard.exe (v2)

Usage:
  guard prepare [flags]
  guard watch   [flags]
  guard status  [flags]
  guard stop    [flags]
  guard run-service [flags]
  guard pause-monitoring  [flags]
  guard resume-monitoring [flags]
  guard monitoring-status [flags]
  guard candidate-status [flags]
  guard promote-candidate [flags]
  guard discard-candidate [flags]
  guard guarded-write [flags]
  guard openclaw-op [flags] -- <openclaw args>

  guard save-telegram-credentials [flags]
  guard complete-telegram-binding [flags]
  guard unbind-telegram [flags]

  guard save-feishu-credentials [flags]
  guard complete-feishu-binding [flags]
  guard unbind-feishu [flags]
  guard test-feishu-message [flags]
  guard save-wecom-credentials [flags]
  guard test-wecom-connection [flags]
  guard complete-wecom-binding [flags]
  guard unbind-wecom [flags]
  guard test-wecom-message [flags]

Testing commands:
  guard request-write  [flags]
  guard complete-write [flags]
  guard fail-write     [flags]

Implemented in v2:
  - prepare: baseline backup for guarded targets
  - watch:   daemon watch + restore from baseline + named pipe server
    - status:  check whether guard watch is running
  - stop:    request running guard watch to stop gracefully
  - pause-monitoring:  pause drift restore by creating monitor.paused
  - resume-monitoring: resume drift restore by removing monitor.paused
  - monitoring-status: show whether monitoring is paused
  - candidate-status: show trusted/candidate snapshot summary
  - promote-candidate: manually promote one candidate target to trusted
  - discard-candidate: discard one blocked/unwanted candidate target
  - mark-bad-candidate: mark one candidate target as bad and stop auto verification
  - retry-candidate: reset one bad candidate target back to candidate for re-verification
  - guarded-write: primary protected config write path (lease + atomic local write + complete/fail)
  - openclaw-op: compatibility path only (pause monitoring -> run OpenClaw command -> resume monitoring)
  - pipe:    dynamic write request / complete / fail testing
  - run-service: internal Windows service entrypoint

Examples:
  guard prepare --root C:\Users\Administrator\.openclaw --agent main
  guard watch --root C:\Users\Administrator\.openclaw --agent main --interval 2
  guard status
  guard stop
  guard pause-monitoring --root C:\Users\Administrator\.openclaw
  guard resume-monitoring --root C:\Users\Administrator\.openclaw
  guard monitoring-status --root C:\Users\Administrator\.openclaw
  guard candidate-status --root C:\Users\Administrator\.openclaw
  guard promote-candidate --root C:\Users\Administrator\.openclaw --target auth:main
  guard discard-candidate --root C:\Users\Administrator\.openclaw --target auth:main

  guard request-write --agent main --target-key openclaw --kind openclaw --client test-cli --request req-1 --lease 180
  guard request-write --agent main --kind auth-profiles --path C:\Users\Administrator\.openclaw\agents\main\agent\auth-profiles.json --client test-cli --request req-2 --lease 180 --mode block --wait 30

  guard complete-write --lease-id lease-123456789 --target-key openclaw --kind openclaw
  guard fail-write --lease-id lease-123456789 --target-key openclaw --kind openclaw --reason manual-test
  guard guarded-write --root C:\Users\Administrator\.openclaw --agent main --path C:\Users\Administrator\.openclaw\openclaw.json --from C:\temp\openclaw.json
  guard guarded-write --root C:\Users\Administrator\.openclaw --agent main --path C:\Users\Administrator\.openclaw\agents\main\agent\auth-profiles.json --from C:\temp\auth-profiles.json
  guard openclaw-op --root C:\Users\Administrator\.openclaw --agent main -- <OpenClaw native operation requiring compatibility flow>

  guard complete-write --lease-id lease-123456789 --agent main --target-key auth:main --kind auth-profiles --client test-cli --request req-auth-complete
  guard fail-write --lease-id lease-123456789 --agent main --target-key auth:main --kind auth-profiles --client test-cli --request req-auth-fail
  guard save-feishu-credentials --root C:\Users\Administrator\.openclaw --account-id cli_xxx --app-secret yyy
  guard complete-feishu-binding --root C:\Users\Administrator\.openclaw --account-id cli_xxx --sender-id ou_xxx --display-name 张三 --code ABC123
  guard unbind-feishu --root C:\Users\Administrator\.openclaw
  guard test-feishu-message --root C:\Users\Administrator\.openclaw
  guard save-wecom-credentials --root C:\Users\Administrator\.openclaw --bot-id xxx --secret yyy
  guard test-wecom-connection --root C:\Users\Administrator\.openclaw
  guard complete-wecom-binding --root C:\Users\Administrator\.openclaw --account-id ww_xxx --sender-id zhangsan --display-name 张三 --code ABC123
  guard unbind-wecom --root C:\Users\Administrator\.openclaw
  guard test-wecom-message --root C:\Users\Administrator\.openclaw
  guard mark-bad-candidate --root C:\Users\Administrator\.openclaw --target auth:main
  guard retry-candidate --root C:\Users\Administrator\.openclaw --target auth:main
`
	fmt.Fprintln(os.Stdout, strings.TrimSpace(text))
}

var errNoTargets = errors.New("no watched targets were resolved")

// parseAgentsList parses a comma-separated string of agent IDs into a slice.
// It trims whitespace, removes duplicates, and returns nil for empty input.
func parseAgentsList(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	seen := make(map[string]bool)
	var result []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" && !seen[p] {
			seen[p] = true
			result = append(result, p)
		}
	}
	return result
}

// runSaveTelegramCredentials 保存 Telegram 凭证到 credentials store
func runSaveTelegramCredentials(args []string) error {
	flags, fs := parseBindingFlags("save-telegram-credentials")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if flags.RootDir == "" {
		return fmt.Errorf("--root is required")
	}
	if flags.Token == "" {
		return fmt.Errorf("--token is required")
	}

	// 初始化 credentials store
	notify.InitCredentialsStore(flags.RootDir)

	// 保存凭证
	store, err := notify.NewCredentialsStore(notify.CredentialsPath(flags.RootDir))
	if err != nil {
		return fmt.Errorf("failed to open credentials store: %v", err)
	}

	if err := store.SetTelegramCredentials(flags.Token); err != nil {
		return fmt.Errorf("failed to save credentials: %v", err)
	}

	fmt.Println("Telegram credentials saved successfully")
	return nil
}

// runUnbindTelegram 解除 Telegram 绑定
func runUnbindTelegram(args []string) error {
	flags, fs := parseBindingFlags("unbind-telegram")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if flags.RootDir == "" {
		return fmt.Errorf("--root is required")
	}

	// 初始化 store
	notify.SetRootDir(flags.RootDir)
	storePath := notify.BindingsPath(flags.RootDir)
	s, err := notify.NewStore(storePath)
	if err != nil {
		return fmt.Errorf("failed to open store: %v", err)
	}

	// 查找 Telegram 绑定
	bindings := s.ListBindings()
	var targetBinding notify.BindingRecord
	for _, b := range bindings {
		if b.Channel == "telegram" && b.Status == notify.BindingStatusBound {
			targetBinding = b
			break
		}
	}

	if targetBinding.ID == "" {
		return fmt.Errorf("no active Telegram binding found")
	}

	// 撤销绑定
	if err := s.RevokeBinding("telegram", targetBinding.AccountID, targetBinding.SenderID); err != nil {
		return fmt.Errorf("failed to revoke binding: %v", err)
	}

	fmt.Println("Telegram binding revoked successfully")
	fmt.Printf("id: %s\n", targetBinding.ID)
	fmt.Printf("accountId: %s\n", targetBinding.AccountID)
	fmt.Printf("senderId: %s\n", targetBinding.SenderID)

	return nil
}

// runSaveFeishuCredentials 保存 Feishu 凭证到 credentials store
func runSaveFeishuCredentials(args []string) error {
	flags, fs := parseBindingFlags("save-feishu-credentials")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if flags.RootDir == "" {
		return fmt.Errorf("--root is required")
	}
	if flags.AccountID == "" {
		return fmt.Errorf("--account-id is required")
	}
	if flags.AppSecret == "" {
		return fmt.Errorf("--app-secret is required")
	}

	if err := notify.VerifyFeishuCredentials(flags.AccountID, flags.AppSecret); err != nil {
		return fmt.Errorf("invalid feishu credentials: %v", err)
	}

	notify.InitCredentialsStore(flags.RootDir)

	store, err := notify.NewCredentialsStore(notify.CredentialsPath(flags.RootDir))
	if err != nil {
		return fmt.Errorf("failed to open credentials store: %v", err)
	}

	if err := store.SetFeishuCredentials(flags.AccountID, flags.AppSecret); err != nil {
		return fmt.Errorf("failed to save feishu credentials: %v", err)
	}

	fmt.Println("Feishu credentials saved successfully")
	fmt.Printf("accountId: %s\n", flags.AccountID)
	return nil
}

// runCompleteFeishuBinding 完成 Feishu 绑定
func runCompleteFeishuBinding(args []string) error {
	flags, fs := parseBindingFlags("complete-feishu-binding")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if flags.RootDir == "" {
		return fmt.Errorf("--root is required")
	}
	if flags.AccountID == "" {
		return fmt.Errorf("--account-id is required")
	}
	if flags.SenderID == "" {
		return fmt.Errorf("--sender-id is required")
	}
	if flags.Code == "" {
		return fmt.Errorf("--code is required")
	}

	logger, err := logging.New(flags.LogFile)
	if err != nil {
		return err
	}
	defer logger.Close()

	notify.SetRootDir(flags.RootDir)
	storePath := notify.BindingsPath(flags.RootDir)
	s, err := notify.NewStore(storePath)
	if err != nil {
		return fmt.Errorf("failed to open store: %v", err)
	}

	pending, found := s.FindPendingByCode("feishu", flags.AccountID, flags.Code)
	if !found {
		return fmt.Errorf("pairing code not found or expired")
	}
	if pending.SenderID != flags.SenderID {
		return fmt.Errorf("senderID mismatch")
	}

	record, err := s.MarkBound(
		"feishu",
		flags.AccountID,
		flags.SenderID,
		flags.DisplayName,
		flags.Code,
	)
	if err != nil {
		return fmt.Errorf("failed to mark bound: %v", err)
	}

	notify.InitCredentialsStore(flags.RootDir)
	appID, appSecret := notify.GetFeishuCredentials()
	if strings.TrimSpace(appID) == "" {
		appID = strings.TrimSpace(flags.AccountID)
	}
	if strings.TrimSpace(appSecret) == "" {
		appSecret = strings.TrimSpace(flags.AppSecret)
	}

	confirmationStatus := "unknown"
	if strings.TrimSpace(appID) == "" || strings.TrimSpace(appSecret) == "" {
		confirmationStatus = "failed"
		fmt.Printf("binding completed, but feishu credentials not found for confirmation\n")
		_ = s.UpdateBindingTestResult("feishu", flags.AccountID, flags.SenderID, "feishu credentials not found", "failed")
	} else {
		success, errMsg := notify.SendFeishuMessage(
			appID,
			appSecret,
			flags.SenderID,
			"🔔 OpenClaw Guard 绑定成功！您现在会收到来自 OpenClaw 的通知。",
		)
		if !success {
			confirmationStatus = "failed"
			fmt.Printf("binding completed, but confirmation message failed: %s\n", errMsg)
			_ = s.UpdateBindingTestResult("feishu", flags.AccountID, flags.SenderID, errMsg, "failed")
		} else {
			confirmationStatus = "ok"
			_ = s.UpdateBindingTestResult("feishu", flags.AccountID, flags.SenderID, "连接成功", "ok")
		}
	}

	fmt.Printf("binding completed successfully\n")
	fmt.Printf("id: %s\n", record.ID)
	fmt.Printf("channel: %s\n", record.Channel)
	fmt.Printf("accountId: %s\n", record.AccountID)
	fmt.Printf("senderId: %s\n", record.SenderID)
	fmt.Printf("displayName: %s\n", record.DisplayName)
	fmt.Printf("pairingCode: %s\n", record.PairingCode)
	fmt.Printf("status: %s\n", record.Status)
	fmt.Printf("confirmationSent: %s\n", confirmationStatus)

	return nil
}

// runUnbindFeishu 解除 Feishu 绑定
func runUnbindFeishu(args []string) error {
	flags, fs := parseBindingFlags("unbind-feishu")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if flags.RootDir == "" {
		return fmt.Errorf("--root is required")
	}

	notify.SetRootDir(flags.RootDir)
	storePath := notify.BindingsPath(flags.RootDir)
	s, err := notify.NewStore(storePath)
	if err != nil {
		return fmt.Errorf("failed to open store: %v", err)
	}

	bindings := s.ListBindings()
	var targetBinding notify.BindingRecord
	for _, b := range bindings {
		if b.Channel == "feishu" && b.Status == notify.BindingStatusBound {
			targetBinding = b
			break
		}
	}

	if targetBinding.ID == "" {
		return fmt.Errorf("no active Feishu binding found")
	}

	if err := s.RevokeBinding("feishu", targetBinding.AccountID, targetBinding.SenderID); err != nil {
		return fmt.Errorf("failed to revoke binding: %v", err)
	}

	fmt.Println("Feishu binding revoked successfully")
	fmt.Printf("id: %s\n", targetBinding.ID)
	fmt.Printf("accountId: %s\n", targetBinding.AccountID)
	fmt.Printf("senderId: %s\n", targetBinding.SenderID)

	return nil
}

// runTestFeishuMessage 发送 Feishu 测试消息
func runTestFeishuMessage(args []string) error {
	flags, fs := parseBindingFlags("test-feishu-message")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if flags.RootDir == "" {
		return fmt.Errorf("--root is required")
	}

	notify.InitCredentialsStore(flags.RootDir)
	appID, appSecret := notify.GetFeishuCredentials()
	if strings.TrimSpace(appID) == "" {
		appID = strings.TrimSpace(flags.AccountID)
	}
	if strings.TrimSpace(appSecret) == "" {
		appSecret = strings.TrimSpace(flags.AppSecret)
	}

	if strings.TrimSpace(appID) == "" {
		return fmt.Errorf("feishu app id not found")
	}
	if strings.TrimSpace(appSecret) == "" {
		return fmt.Errorf("feishu app secret not found")
	}

	notify.SetRootDir(flags.RootDir)
	storePath := notify.BindingsPath(flags.RootDir)
	s, err := notify.NewStore(storePath)
	if err != nil {
		return fmt.Errorf("failed to open store: %v", err)
	}

	bindings := s.ListBindings()
	var targetBinding notify.BindingRecord
	for _, b := range bindings {
		if b.Channel == "feishu" && b.Status == notify.BindingStatusBound {
			targetBinding = b
			break
		}
	}

	if targetBinding.ID == "" {
		return fmt.Errorf("no active Feishu binding found")
	}

	message := strings.TrimSpace(flags.Message)
	if message == "" {
		message = "✅ OpenClaw Guard Feishu 测试消息：连接正常。"
	}

	success, errMsg := notify.SendFeishuMessage(appID, appSecret, targetBinding.SenderID, message)
	if !success {
		_ = s.UpdateBindingTestResult("feishu", targetBinding.AccountID, targetBinding.SenderID, errMsg, "failed")
		return fmt.Errorf("failed to send feishu test message: %s", errMsg)
	}

	_ = s.UpdateBindingTestResult("feishu", targetBinding.AccountID, targetBinding.SenderID, "连接成功", "ok")

	fmt.Println("Feishu test message sent successfully")
	fmt.Printf("accountId: %s\n", targetBinding.AccountID)
	fmt.Printf("senderId: %s\n", targetBinding.SenderID)
	fmt.Printf("displayName: %s\n", targetBinding.DisplayName)
	return nil
}

// runSaveWecomCredentials 保存企业微信凭证到 credentials store
func runSaveWecomCredentials(args []string) error {
	flags, fs := parseBindingFlags("save-wecom-credentials")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if flags.RootDir == "" {
		return fmt.Errorf("--root is required")
	}
	if strings.TrimSpace(flags.BotID) == "" {
		return fmt.Errorf("--bot-id is required")
	}
	if strings.TrimSpace(flags.Secret) == "" {
		return fmt.Errorf("--secret is required")
	}

	if err := notify.VerifyWecomCredentials(flags.BotID, flags.Secret); err != nil {
		return err
	}

	notify.InitCredentialsStore(flags.RootDir)

	store, err := notify.NewCredentialsStore(notify.CredentialsPath(flags.RootDir))
	if err != nil {
		return fmt.Errorf("failed to open credentials store: %v", err)
	}

	if err := store.SetWecomCredentials(flags.BotID, flags.Secret); err != nil {
		return fmt.Errorf("failed to save wecom credentials: %v", err)
	}

	fmt.Println("WeCom credentials saved successfully")
	fmt.Printf("botId: %s\n", flags.BotID)
	return nil
}

// runTestWecomConnection 测试企业微信 bridge 是否可启动
func runTestWecomConnection(args []string) error {
	flags, fs := parseBindingFlags("test-wecom-connection")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if flags.RootDir == "" {
		return fmt.Errorf("--root is required")
	}

	notify.InitCredentialsStore(flags.RootDir)

	botID, secret := notify.GetWecomCredentials()
	if strings.TrimSpace(botID) == "" {
		botID = strings.TrimSpace(flags.BotID)
	}
	if strings.TrimSpace(secret) == "" {
		secret = strings.TrimSpace(flags.Secret)
	}

	if strings.TrimSpace(botID) == "" {
		return fmt.Errorf("wecom bot id not found")
	}
	if strings.TrimSpace(secret) == "" {
		return fmt.Errorf("wecom secret not found")
	}

	if err := notify.EnsureWecomBridge(botID, secret); err != nil {
		return err
	}

	fmt.Println("wecom bridge ready")
	fmt.Printf("botId: %s\n", botID)
	return nil
}

// runCompleteWecomBinding 完成企业微信绑定
func runCompleteWecomBinding(args []string) error {
	flags, fs := parseBindingFlags("complete-wecom-binding")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if flags.RootDir == "" {
		return fmt.Errorf("--root is required")
	}
	if strings.TrimSpace(flags.AccountID) == "" {
		return fmt.Errorf("--account-id is required")
	}
	if strings.TrimSpace(flags.SenderID) == "" {
		return fmt.Errorf("--sender-id is required")
	}
	if strings.TrimSpace(flags.Code) == "" {
		return fmt.Errorf("--code is required")
	}

	logger, err := logging.New(flags.LogFile)
	if err != nil {
		return err
	}
	defer logger.Close()

	notify.SetRootDir(flags.RootDir)
	storePath := notify.BindingsPath(flags.RootDir)
	s, err := notify.NewStore(storePath)
	if err != nil {
		return fmt.Errorf("failed to open store: %v", err)
	}

	pending, found := s.FindPendingByCode("wecom", flags.AccountID, flags.Code)
	if !found {
		return fmt.Errorf("pairing code not found or expired")
	}
	if pending.SenderID != flags.SenderID {
		return fmt.Errorf("senderID mismatch")
	}

	record, err := s.MarkBound(
		"wecom",
		flags.AccountID,
		flags.SenderID,
		flags.DisplayName,
		flags.Code,
	)
	if err != nil {
		return fmt.Errorf("failed to mark bound: %v", err)
	}

	notify.InitCredentialsStore(flags.RootDir)
	botID, secret := notify.GetWecomCredentials()
	if strings.TrimSpace(botID) == "" {
		botID = strings.TrimSpace(flags.AccountID)
	}
	if strings.TrimSpace(secret) == "" {
		secret = strings.TrimSpace(flags.Secret)
	}

	confirmationStatus := "unknown"
	if strings.TrimSpace(botID) == "" || strings.TrimSpace(secret) == "" {
		confirmationStatus = "failed"
		fmt.Printf("binding completed, but wecom credentials not found for confirmation\n")
		_ = s.UpdateBindingTestResult("wecom", flags.AccountID, flags.SenderID, "wecom credentials not found", "failed")
	} else {
		success, errMsg := notify.SendWecomMessage(
			botID,
			secret,
			flags.SenderID,
			"🔔 OpenClaw Guard 绑定成功！您现在会收到来自 OpenClaw 的通知。",
		)
		if !success {
			confirmationStatus = "failed"
			fmt.Printf("binding completed, but confirmation message failed: %s\n", errMsg)
			_ = s.UpdateBindingTestResult("wecom", flags.AccountID, flags.SenderID, errMsg, "failed")
		} else {
			confirmationStatus = "ok"
			_ = s.UpdateBindingTestResult("wecom", flags.AccountID, flags.SenderID, "连接成功", "ok")
		}
	}

	fmt.Printf("binding completed successfully\n")
	fmt.Printf("id: %s\n", record.ID)
	fmt.Printf("channel: %s\n", record.Channel)
	fmt.Printf("accountId: %s\n", record.AccountID)
	fmt.Printf("senderId: %s\n", record.SenderID)
	fmt.Printf("displayName: %s\n", record.DisplayName)
	fmt.Printf("pairingCode: %s\n", record.PairingCode)
	fmt.Printf("status: %s\n", record.Status)
	fmt.Printf("confirmationSent: %s\n", confirmationStatus)

	return nil
}

// runUnbindWecom 解除企业微信绑定
func runUnbindWecom(args []string) error {
	flags, fs := parseBindingFlags("unbind-wecom")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if flags.RootDir == "" {
		return fmt.Errorf("--root is required")
	}

	notify.SetRootDir(flags.RootDir)
	storePath := notify.BindingsPath(flags.RootDir)
	s, err := notify.NewStore(storePath)
	if err != nil {
		return fmt.Errorf("failed to open store: %v", err)
	}

	bindings := s.ListBindings()
	var targetBinding notify.BindingRecord
	for _, b := range bindings {
		if b.Channel == "wecom" && b.Status == notify.BindingStatusBound {
			targetBinding = b
			break
		}
	}

	if targetBinding.ID == "" {
		return fmt.Errorf("no active WeCom binding found")
	}

	if err := s.RevokeBinding("wecom", targetBinding.AccountID, targetBinding.SenderID); err != nil {
		return fmt.Errorf("failed to revoke binding: %v", err)
	}

	fmt.Println("WeCom binding revoked successfully")
	fmt.Printf("id: %s\n", targetBinding.ID)
	fmt.Printf("accountId: %s\n", targetBinding.AccountID)
	fmt.Printf("senderId: %s\n", targetBinding.SenderID)

	return nil
}

// runTestWecomMessage 发送企业微信测试消息
func runTestWecomMessage(args []string) error {
	flags, fs := parseBindingFlags("test-wecom-message")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if flags.RootDir == "" {
		return fmt.Errorf("--root is required")
	}

	notify.InitCredentialsStore(flags.RootDir)
	botID, secret := notify.GetWecomCredentials()

	if strings.TrimSpace(botID) == "" {
		botID = strings.TrimSpace(flags.AccountID)
	}
	if strings.TrimSpace(secret) == "" {
		secret = strings.TrimSpace(flags.Secret)
	}

	if strings.TrimSpace(botID) == "" {
		return fmt.Errorf("wecom bot id not found")
	}
	if strings.TrimSpace(secret) == "" {
		return fmt.Errorf("wecom secret not found")
	}

	notify.SetRootDir(flags.RootDir)
	storePath := notify.BindingsPath(flags.RootDir)
	s, err := notify.NewStore(storePath)
	if err != nil {
		return fmt.Errorf("failed to open store: %v", err)
	}

	bindings := s.ListBindings()
	var targetBinding notify.BindingRecord
	for _, b := range bindings {
		if b.Channel == "wecom" && b.Status == notify.BindingStatusBound {
			targetBinding = b
			break
		}
	}

	if targetBinding.ID == "" {
		return fmt.Errorf("no active WeCom binding found")
	}

	message := strings.TrimSpace(flags.Message)
	if message == "" {
		message = "✅ OpenClaw Guard 企业微信测试消息：连接正常。"
	}

	success, errMsg := notify.SendWecomMessage(botID, secret, targetBinding.SenderID, message)
	if !success {
		_ = s.UpdateBindingTestResult("wecom", targetBinding.AccountID, targetBinding.SenderID, errMsg, "failed")
		return fmt.Errorf("failed to send wecom test message: %s", errMsg)
	}

	_ = s.UpdateBindingTestResult("wecom", targetBinding.AccountID, targetBinding.SenderID, "连接成功", "ok")

	fmt.Println("WeCom test message sent successfully")
	fmt.Printf("accountId: %s\n", targetBinding.AccountID)
	fmt.Printf("senderId: %s\n", targetBinding.SenderID)
	fmt.Printf("displayName: %s\n", targetBinding.DisplayName)
	return nil
}
