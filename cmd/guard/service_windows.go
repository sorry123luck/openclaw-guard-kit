//go:build windows

package main

import (
	"context"
	"errors"

	"openclaw-guard-kit/logging"

	"golang.org/x/sys/windows/svc"
)

type guardWindowsService struct {
	watchArgs []string
}

func runWindowsService(args []string) error {
	interactive, err := svc.IsAnInteractiveSession()
	if err == nil && interactive {
		return runWatch(args)
	}

	return svc.Run(windowsServiceName, &guardWindowsService{
		watchArgs: args,
	})
}

func (g *guardWindowsService) Execute(_ []string, requests <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	changes <- svc.Status{State: svc.StartPending}

	cfg, err := resolveWatchConfig(g.watchArgs)
	if err != nil {
		return true, 1
	}

	logger, err := logging.New(cfg.LogFile)
	if err != nil {
		return true, 1
	}
	defer logger.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	application, err := buildWatchApp(cfg, logger, cancel)
	if err != nil {
		logger.Error("build service app failed", "error", err)
		return true, 1
	}

	runErrCh := make(chan error, 1)
	go func() {
		runErrCh <- application.Run(ctx)
	}()

	changes <- svc.Status{
		State:   svc.Running,
		Accepts: svc.AcceptStop | svc.AcceptShutdown,
	}

	stopping := false

	for {
		select {
		case req := <-requests:
			switch req.Cmd {
			case svc.Interrogate:
				changes <- req.CurrentStatus

			case svc.Stop, svc.Shutdown:
				if !stopping {
					stopping = true
					changes <- svc.Status{State: svc.StopPending}
					cancel()
				}
			}

		case err := <-runErrCh:
			if err != nil && !errors.Is(err, context.Canceled) {
				logger.Error("windows service exited with error", "error", err)
				return true, 1
			}
			return false, 0
		}
	}
}
