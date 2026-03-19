//go:build windows

package service

import (
	"fmt"
	"time"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

type InstallOptions struct {
	Name           string
	DisplayName    string
	ExecutablePath string
	Arguments      []string
	Automatic      bool
}

func Install(opts InstallOptions) error {
	if opts.Name == "" {
		return fmt.Errorf("service name is required")
	}
	if opts.ExecutablePath == "" {
		return fmt.Errorf("executable path is required")
	}

	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect service manager: %w", err)
	}
	defer m.Disconnect()

	if existing, err := m.OpenService(opts.Name); err == nil {
		existing.Close()
		return fmt.Errorf("service %q already exists", opts.Name)
	}

	var startType uint32 = mgr.StartManual
	if opts.Automatic {
		startType = mgr.StartAutomatic
	}

	s, err := m.CreateService(
		opts.Name,
		opts.ExecutablePath,
		mgr.Config{
			DisplayName: opts.DisplayName,
			StartType:   startType,
		},
		opts.Arguments...,
	)
	if err != nil {
		return fmt.Errorf("create service %q: %w", opts.Name, err)
	}
	defer s.Close()

	return nil
}

func Uninstall(name string) error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect service manager: %w", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(name)
	if err != nil {
		return fmt.Errorf("open service %q: %w", name, err)
	}
	defer s.Close()

	status, err := s.Query()
	if err == nil && status.State != svc.Stopped {
		_, _ = s.Control(svc.Stop)

		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			status, err = s.Query()
			if err == nil && status.State == svc.Stopped {
				break
			}
			time.Sleep(300 * time.Millisecond)
		}
	}

	if err := s.Delete(); err != nil {
		return fmt.Errorf("delete service %q: %w", name, err)
	}

	return nil
}

func Start(name string) error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect service manager: %w", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(name)
	if err != nil {
		return fmt.Errorf("open service %q: %w", name, err)
	}
	defer s.Close()

	status, err := s.Query()
	if err == nil && status.State == svc.Running {
		return nil
	}

	if err := s.Start(); err != nil {
		return fmt.Errorf("start service %q: %w", name, err)
	}

	return nil
}

func Stop(name string, timeout time.Duration) error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect service manager: %w", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(name)
	if err != nil {
		return fmt.Errorf("open service %q: %w", name, err)
	}
	defer s.Close()

	status, err := s.Query()
	if err != nil {
		return fmt.Errorf("query service %q: %w", name, err)
	}
	if status.State == svc.Stopped {
		return nil
	}

	status, err = s.Control(svc.Stop)
	if err != nil {
		return fmt.Errorf("stop service %q: %w", name, err)
	}

	deadline := time.Now().Add(timeout)
	for status.State != svc.Stopped {
		if time.Now().After(deadline) {
			return fmt.Errorf("stop service %q: timeout after %s", name, timeout)
		}

		time.Sleep(300 * time.Millisecond)

		status, err = s.Query()
		if err != nil {
			return fmt.Errorf("query service %q while stopping: %w", name, err)
		}
	}

	return nil
}

func Query(name string) (svc.State, error) {
	m, err := mgr.Connect()
	if err != nil {
		return 0, fmt.Errorf("connect service manager: %w", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(name)
	if err != nil {
		return 0, fmt.Errorf("open service %q: %w", name, err)
	}
	defer s.Close()

	status, err := s.Query()
	if err != nil {
		return 0, fmt.Errorf("query service %q: %w", name, err)
	}

	return status.State, nil
}

func StateString(state svc.State) string {
	switch state {
	case svc.Stopped:
		return "stopped"
	case svc.StartPending:
		return "start-pending"
	case svc.StopPending:
		return "stop-pending"
	case svc.Running:
		return "running"
	case svc.ContinuePending:
		return "continue-pending"
	case svc.PausePending:
		return "pause-pending"
	case svc.Paused:
		return "paused"
	default:
		return fmt.Sprintf("unknown(%d)", state)
	}
}
