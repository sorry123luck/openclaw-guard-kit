//go:build windows

package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

type UIConfig struct {
	GuardExePath string
	RootDir      string
	AgentID      string
	ConfigPath   string
	PollSeconds  int
	StartHidden  bool
}

var errUIAlreadyRunning = errors.New("guard-ui already running")

type singleInstanceLock struct {
	handle windows.Handle
}

var (
	kernel32         = windows.NewLazySystemDLL("kernel32.dll")
	procCreateMutexW = kernel32.NewProc("CreateMutexW")
)

func main() {
	cfg, err := parseFlags()
	if err != nil {
		fmt.Fprintf(os.Stderr, "guard-ui 启动失败: %v\n", err)
		os.Exit(1)
	}

	lock, err := acquireSingleInstanceLock("OpenClawGuardUI.Singleton")
	if err != nil {
		if errors.Is(err, errUIAlreadyRunning) {
			os.Exit(0)
		}
		fmt.Fprintf(os.Stderr, "guard-ui 单实例初始化失败: %v\n", err)
		os.Exit(1)
	}
	defer lock.Close()

	if err := Run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "guard-ui 运行失败: %v\n", err)
		os.Exit(1)
	}
}

func parseFlags() (UIConfig, error) {
	exePath, err := os.Executable()
	if err != nil {
		return UIConfig{}, err
	}
	exeDir := filepath.Dir(exePath)

	homeDir, _ := os.UserHomeDir()
	defaultRoot := filepath.Join(homeDir, ".openclaw")

	cfg := UIConfig{}
	fs := flag.NewFlagSet("guard-ui", flag.ContinueOnError)
	fs.StringVar(&cfg.GuardExePath, "guard-exe", filepath.Join(exeDir, "guard.exe"), "guard.exe 路径")
	fs.StringVar(&cfg.RootDir, "root", defaultRoot, "OpenClaw 根目录")
	fs.StringVar(&cfg.AgentID, "agent", "main", "当前 agent")
	fs.StringVar(&cfg.ConfigPath, "config", filepath.Join(defaultRoot, "openclaw.json"), "配置文件路径")
	fs.IntVar(&cfg.PollSeconds, "poll", 15, "状态轮询秒数")
	fs.BoolVar(&cfg.StartHidden, "start-hidden", false, "启动时最小化到托盘")

	if err := fs.Parse(os.Args[1:]); err != nil {
		return UIConfig{}, err
	}

	if cfg.PollSeconds <= 0 {
		cfg.PollSeconds = 15
	}
	if cfg.GuardExePath == "" {
		return UIConfig{}, fmt.Errorf("guard.exe 路径不能为空")
	}

	return cfg, nil
}

func acquireSingleInstanceLock(name string) (*singleInstanceLock, error) {
	namePtr, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}

	r1, _, lastErr := procCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(namePtr)))
	if r1 == 0 {
		if lastErr != windows.ERROR_SUCCESS && lastErr != nil {
			return nil, lastErr
		}
		return nil, fmt.Errorf("CreateMutexW failed")
	}

	if errno, ok := lastErr.(windows.Errno); ok && errno == windows.ERROR_ALREADY_EXISTS {
		_ = windows.CloseHandle(windows.Handle(r1))
		return nil, errUIAlreadyRunning
	}

	return &singleInstanceLock{handle: windows.Handle(r1)}, nil
}

func (l *singleInstanceLock) Close() {
	if l == nil || l.handle == 0 {
		return
	}
	_ = windows.CloseHandle(l.handle)
	l.handle = 0
}
