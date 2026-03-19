//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	guardsvc "openclaw-guard-kit/internal/service"
)

const (
	windowsServiceName        = "openclaw-guard"
	windowsServiceDisplayName = "OpenClaw Guard"
)

func handleServiceCommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: guard.exe service <install|uninstall|start|stop|status|run>")
	}

	switch args[0] {
	case "install":
		flags, fs := parseCommonFlags("service install")
		restoreOnChange := fs.Bool("restore-on-change", true, "restore baseline when a watched file content changes")
		restoreOnDelete := fs.Bool("restore-on-delete", true, "restore baseline when a watched file is removed")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}

		exe, err := os.Executable()
		if err != nil {
			return fmt.Errorf("resolve executable: %w", err)
		}

		exe, err = filepath.Abs(exe)
		if err != nil {
			return fmt.Errorf("resolve absolute executable path: %w", err)
		}

		serviceArgs := append([]string{"service", "run"}, buildServiceRunArgs(flags, *restoreOnChange, *restoreOnDelete)...)

		if err := guardsvc.Install(guardsvc.InstallOptions{
			Name:           windowsServiceName,
			DisplayName:    windowsServiceDisplayName,
			ExecutablePath: exe,
			Arguments:      serviceArgs,
			Automatic:      true,
		}); err != nil {
			return err
		}

		fmt.Println("service installed")
		return nil

	case "uninstall":
		if err := guardsvc.Uninstall(windowsServiceName); err != nil {
			return err
		}
		fmt.Println("service uninstalled")
		return nil

	case "start":
		if err := guardsvc.Start(windowsServiceName); err != nil {
			return err
		}
		fmt.Println("service start requested")
		return nil

	case "stop":
		if err := guardsvc.Stop(windowsServiceName, 20*time.Second); err != nil {
			return err
		}
		fmt.Println("service stopped")
		return nil

	case "status":
		state, err := guardsvc.Query(windowsServiceName)
		if err != nil {
			return err
		}
		fmt.Printf("service state: %s\n", guardsvc.StateString(state))
		return nil

	case "run":
		return runWindowsService(args[1:])

	default:
		return fmt.Errorf("unknown service subcommand %q", args[0])
	}
}

func buildServiceRunArgs(flags *commonFlags, restoreOnChange, restoreOnDelete bool) []string {
	args := make([]string, 0, 24)

	args = append(args, "--agent", normalizeAgentID(flags.AgentID))
	args = append(args, "--interval", strconv.Itoa(flags.PollIntervalSeconds))
	args = append(args, "--with-auth-profiles="+strconv.FormatBool(flags.IncludeAuthProfiles))
	args = append(args, "--auto-prepare="+strconv.FormatBool(flags.AutoPrepare))
	args = append(args, "--restore-on-change="+strconv.FormatBool(restoreOnChange))
	args = append(args, "--restore-on-delete="+strconv.FormatBool(restoreOnDelete))

	args = appendIfValue(args, "--config", flags.ConfigPath)
	args = appendIfValue(args, "--root", flags.RootDir)
	args = appendIfValue(args, "--openclaw", flags.OpenClawPath)
	args = appendIfValue(args, "--auth-profiles", flags.AuthProfilesPath)
	args = appendIfValue(args, "--backup-dir", flags.BackupDir)
	args = appendIfValue(args, "--state-file", flags.StateFile)
	args = appendIfValue(args, "--log-file", flags.LogFile)

	return args
}

func appendIfValue(args []string, name, value string) []string {
	if value == "" {
		return args
	}
	return append(args, name, value)
}
