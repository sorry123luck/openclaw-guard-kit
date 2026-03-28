package bootstrap

import (
	"openclaw-guard-kit/internal/guard"
	"openclaw-guard-kit/internal/robot"
	"openclaw-guard-kit/internal/runtime"
	"openclaw-guard-kit/logging"
	"openclaw-guard-kit/notify"
	"openclaw-guard-kit/process"
)

type Dependencies struct {
	PipeServer       runtime.PipeServer
	Watcher          runtime.Watcher
	LeaseManager     runtime.LeaseManager
	Notifier         runtime.Notifier
	Supervisor       runtime.Supervisor
	RobotHub         runtime.RobotHub
	EventBus         *runtime.EventBus
	Dispatcher       *runtime.Dispatcher
	GuardCoordinator runtime.GuardCoordinator
}

func BuildRuntime(cfg any, logger *logging.Logger, deps Dependencies) (*runtime.Runtime, error) {
	rt := runtime.New()
	rt.SetConfig(cfg)
	rt.SetLogger(logger)

	rt.PipeServer = deps.PipeServer
	rt.Watcher = deps.Watcher
	rt.LeaseManager = deps.LeaseManager

	if deps.Notifier != nil {
		rt.Notifier = deps.Notifier
	} else {
		rt.Notifier = notify.NewLogNotifier(logger)
	}

	if deps.Supervisor != nil {
		rt.Supervisor = deps.Supervisor
	} else {
		rt.Supervisor = process.NewNoopSupervisor(logger)
	}

	if deps.RobotHub != nil {
		rt.RobotHub = deps.RobotHub
	} else {
		hub := robot.NewManager(logger)
		hub.Register(robot.NewNoopBot())
		rt.RobotHub = hub
	}

	if deps.EventBus != nil {
		rt.SetEventBus(deps.EventBus)
	} else {
		rt.SetEventBus(
			runtime.NewEventBus(
				logger,
				rt.Notifier,
				rt.Supervisor,
				rt.RobotHub,
			),
		)
	}

	if deps.Dispatcher != nil {
		rt.SetDispatcher(deps.Dispatcher)
	} else {
		rt.SetDispatcher(runtime.NewDispatcher(logger, rt.EventBus))
	}

	if deps.GuardCoordinator != nil {
		rt.GuardCoordinator = deps.GuardCoordinator
	} else {
		rt.GuardCoordinator = guard.NewCoordinator(logger, rt.Dispatcher)
	}

	if err := rt.Validate(); err != nil {
		return nil, err
	}

	return rt, nil
}
