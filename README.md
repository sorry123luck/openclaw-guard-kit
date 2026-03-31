# OpenClaw Guard Kit

> 本文档由 **openclaw-大笨蛋** 总结上传。

[![Platform](https://img.shields.io/badge/platform-Windows-blue?style=flat-square)](#)
[![License](https://img.shields.io/badge/license-MIT-green?style=flat-square)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.25+-blue?style=flat-square)](#开发)

---

## 目录（中文）

- [快速开始](#快速开始)
- [功能概览](#功能概览)
- [组件架构](#组件架构)
- [命令行接口](#命令行接口)
- [远程安装与升级](#远程安装与升级)
- [配置说明](#配置说明)
- [运行时文件](#运行时文件)
- [开发](#开发)

> 🚀 English documentation: [Jump to English](#english-documentation)

---

## 快速开始

### 前置要求

- Windows 操作系统
- 已安装 [OpenClaw](https://github.com/openclaw/openclaw)
- PowerShell 5.0+

### 一键安装

从 Gitee（国内镜像）自动下载并安装最新版本：

```powershell
irm https://gitee.com/sorry123luck/openclaw-guard-kit/raw/main/scripts/install.ps1 | iex
```

> 国际用户可使用 GitHub（国内访问慢）：
> `irm https://github.com/sorry123luck/openclaw-guard-kit/releases/latest/download/install.ps1 | iex`

- 安装路径：`~/.openclaw-guard-kit/`
- OpenClaw 路径：`~/.openclaw/`

安装完成后：
- `guard-detector.exe` 自动注册为开机自启动
- `guard-ui.exe` 在系统托盘运行
- 检测到 OpenClaw 上线后自动拉起守护程序

### 升级

```powershell
powershell -ExecutionPolicy Bypass -File "$env:USERPROFILE\.openclaw-guard-kit\installer\update.ps1"
```

### 卸载

```powershell
powershell -ExecutionPolicy Bypass -File "$env:USERPROFILE\.openclaw-guard-kit\installer\uninstall.ps1"
```

加 `-RemoveInstallDir` 可同时删除安装目录本身。

---

## 功能概览

### 1. 生命周期监控

`guard-detector.exe` 持续检测 OpenClaw 在线状态：

| OpenClaw 状态 | 行为 |
|---------------|------|
| 在线 | 自动确保 `guard.exe` 和 `guard-ui.exe` 运行 |
| 离线确认 | 自动停止 guard 和 UI，发送离线通知 |
| 远程启动命令 | 进入 30 秒快速探测窗口，每秒探测一次 |

### 2. 配置文件保护

`guard.exe` 监控 OpenClaw 关键配置文件：

```
文件变化 → 稳定等待（5s）→ 创建候选快照 → 健康检查 → Doctor 诊断 → 判定处理方式
```

| 判定结果 | 处理 |
|----------|------|
| 配置正常 | 候选升格为可信基线 |
| 运行时异常 | 等待自愈，不回滚 |
| 硬故障 | 自动回滚到上一个可信版本 |

**受保护文件：**

- `openclaw.json`（归一化比较，忽略运行时字段）
- `auth-profiles.json`（仅比较 version + profiles）
- `models.json`（可选）

### 3. 通知与远程命令

三个平台通道，每个通道独立控制通知开关和远程命令权限：

| 平台 | 凭证 | 绑定方式 |
|------|------|----------|
| Telegram | Bot Token | 机器人会话绑定 |
| 飞书 | App ID + App Secret | 应用消息绑定 |
| 企业微信 | Corp ID + Agent ID + Secret | 企业应用会话绑定 |

已绑定用户可发送远程命令：

- `启动openclaw`
- `重启openclaw`

---

## 组件架构

```
┌─────────────────────────────────────────────────────┐
│                  用户 / 机器人                         │
│              (Telegram · 飞书 · 企业微信)            │
└──────────────────────┬────────────────────────────────┘
                       │ 远程命令 / 通知
┌──────────────────────▼────────────────────────────────┐
│                 guard-detector.exe                     │
│  ┌──────────────┐ ┌──────────────┐ ┌───────────────┐ │
│  │  生命周期监控  │ │  远程命令处理  │ │  快速探测窗口  │ │
│  └──────────────┘ └──────────────┘ └───────────────┘ │
│         │                │                  │          │
│    OpenClaw 在线     绑定校验          30s/1s 探测     │
└─────────┬──────────────────────────────────┬────────────┘
          │ 拉起 / 停止                      │ TCP 探测
┌─────────▼──────────────────────────────────▼────────────┐
│                      guard.exe                          │
│  ┌──────────────┐ ┌──────────────┐ ┌───────────────┐  │
│  │   文件监控    │ │ ReviewWorker │ │ Backup Service │  │
│  └──────────────┘ └──────────────┘ └───────────────┘  │
└─────────────────────────────────────────────────────────┘
          │
┌─────────▼──────────────────────────────────────────────────┐
│                 guard-ui.exe（托盘）                        │
│   detector / guard / gateway 三层状态 · 凭证管理 · 绑定管理   │
└────────────────────────────────────────────────────────────┘
```

---

## 命令行接口

> 以下列出 `guard.exe` 的常用子命令；`guard-detector.exe` 通常由安装器/自启动流程管理，普通用户一般无需手动调用。

### 核心命令

| 命令 | 说明 |
|------|------|
| `guard.exe watch` | 启动配置文件监控循环 |
| `guard.exe prepare` | 生成可信基线快照（manifest.json） |
| `guard.exe status` | 查看当前 guard 运行状态 |
| `guard.exe stop` | 停止 watch 循环 |

### 候选管理命令

| 命令 | 说明 |
|------|------|
| `guard.exe candidate-status` | 查看当前候选状态 |
| `guard.exe promote-candidate` | 将候选升格为可信 |
| `guard.exe discard-candidate` | 丢弃当前候选 |
| `guard.exe mark-bad-candidate` | 标记候选为坏并归档 |
| `guard.exe retry-candidate` | 重新审查当前候选 |

### Telegram 凭证与绑定

```bash
guard.exe save-telegram-credentials --token <bot-token>
guard.exe complete-telegram-binding --chat-id <chatId>
guard.exe unbind-telegram
```

### 飞书凭证与绑定

```bash
guard.exe save-feishu-credentials --app-id <appId> --app-secret <secret>
guard.exe complete-feishu-binding --open-id <openId>
guard.exe unbind-feishu
guard.exe test-feishu-message --open-id <openId> --content <text>
```

### 企业微信凭证与绑定

```bash
guard.exe save-wecom-credentials --corp-id <corpId> --agent-id <agentId> --secret <secret>
guard.exe test-wecom-connection
guard.exe complete-wecom-binding --user-id <userId>
guard.exe unbind-wecom
guard.exe test-wecom-message --user-id <userId> --content <text>
```

---

## 远程安装与升级

> 安装器默认优先从 **Gitee** 下载，Gitee 不可用时自动切换到 **GitHub**，无需手动干预。

### 远程安装（本地执行）

**国内用户（一键安装）：**
```powershell
irm https://gitee.com/sorry123luck/openclaw-guard-kit/raw/main/scripts/install.ps1 | iex
```

**国际用户：**
```powershell
irm https://github.com/sorry123luck/openclaw-guard-kit/releases/latest/download/install.ps1 | iex
```

**参数：**

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `-InstallDir` | `~/.openclaw-guard-kit/` | 安装目录 |
| `-OpenClawRoot` | `~/.openclaw/` | OpenClaw 根目录 |
| `-PrimarySource` | `gitee` | 首选源，可选 `github` |

### 远程升级

升级脚本同样支持双源自动回退：
```powershell
powershell -ExecutionPolicy Bypass -File "$env:USERPROFILE\.openclaw-guard-kit\installer\update.ps1"
```

支持参数：`-InstallDir` / `-PrimarySource gitee|github`

### 手动下载安装

适用于无法执行远程 PowerShell 脚本的场景：

```powershell
# 1. 下载最新 zip
# https://github.com/sorry123luck/openclaw-guard-kit/releases/latest/download/openclaw-guard-kit-windows-x64.zip

# 2. 解压后本地安装
powershell -ExecutionPolicy Bypass -File ".\installer\install.ps1"
```

---

## 配置说明

### guard-detector 主要参数

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `--openclaw-path` | 系统 PATH 中的 openclaw | openclaw.exe 路径 |
| `--root` | `~/.openclaw/` | OpenClaw 根目录 |
| `--agent` | `main` | Agent ID |
| `--gateway-port` | 0（自动发现） | 固定 gateway 端口，0=自动 |
| `--probe-interval` | 5 秒 | 探测间隔 |
| `--startup-protect` | 20 秒 | 启动保护窗口 |
| `--offline-grace` | 90 秒 | 离线宽限期 |
| `--restart-cooldown` | 45 秒 | 重启冷却时间 |
| `--healthy-confirm` | 2 次 | 在线确认次数 |
| `--unhealthy-confirm` | 3 次 | 离线确认次数 |

### 通道权限控制

每个已绑定通道独立控制：

| 开关 | 说明 |
|------|------|
| `notifyEnabled` | 是否接收通知 |
| `remoteCommandEnabled` | 是否允许远程命令 |

---

## 运行时文件

程序运行后在 OpenClaw 根目录下生成：

```
<OpenClawRoot>\
├── .guard-state\
│   ├── manifest.json           # 快照清单（Trusted + Candidate）
│   ├── detector-status.json    # detector 状态
│   ├── gateway-port-cache.json # gateway 端口缓存
│   ├── startup-protect.json    # 启动保护窗口
│   └── logs\
│       └── doctor-*.log        # Doctor 诊断日志
└── .offline                    # 离线标记（手动放置可强制离线）
```

---

## 开发

### 项目结构

```
openclaw-guard-kit/
├── cmd/
│   ├── guard/                  # guard 主程序
│   ├── guard-detector/         # detector 主程序
│   └── guard-ui/               # UI 程序（托盘）
├── internal/
│   ├── review/                 # 候选审查 Worker
│   ├── protocol/               # 协议类型定义
│   └── ...
├── notify/                    # 通知通道（Telegram / 飞书 / 企业微信）
├── backup/                    # 快照管理
├── watch/                     # 文件监控服务
├── gateway/                   # Named Pipe IPC
├── config/                    # 配置解析
└── dist/.../installer/        # 打包后的安装脚本
```

### 技术栈

- Go 1.25+
- `github.com/Microsoft/go-winio` — Windows Named Pipe
- `github.com/larksuite/oapi-sdk-go/v3` — 飞书 SDK
- `github.com/lxn/walk` — Windows GUI

---

> 👆 [返回中文目录](#目录中文)

---

## English Documentation

> 本章节由 openclaw-大笨蛋 整理。

### Overview

OpenClaw Guard Kit is a **Windows-only** external guardian and recovery tool for OpenClaw. It runs as independent processes and does not modify OpenClaw's source code.

**Core capabilities:**

- Detects OpenClaw online/offline status and auto-spawns/stops guardian processes
- Monitors critical config files with drift detection and candidate review workflow
- Sends notifications and accepts remote commands via Telegram, Feishu, and WeCom
- Supports automatic rollback on config hard failures

### Quick Install

> The installer automatically falls back to GitHub if Gitee is unavailable.

**Recommended (Gitee — fast in China):**
```powershell
irm https://gitee.com/sorry123luck/openclaw-guard-kit/raw/main/scripts/install.ps1 | iex
```

**International (GitHub):**
```powershell
irm https://github.com/sorry123luck/openclaw-guard-kit/releases/latest/download/install.ps1 | iex
```

- Install dir: `~/.openclaw-guard-kit/`
- OpenClaw root: `~/.openclaw/`

### Upgrade

The updater also supports dual-source fallback:
```powershell
powershell -ExecutionPolicy Bypass -File "$env:USERPROFILE\.openclaw-guard-kit\installer\update.ps1"
```

### Uninstall

```powershell
powershell -ExecutionPolicy Bypass -File "$env:USERPROFILE\.openclaw-guard-kit\installer\uninstall.ps1"
```

### Components

| Executable | Role |
|------------|------|
| `guard-detector.exe` | Lifecycle monitor: detects OpenClaw online/offline, spawns/stops guard and UI |
| `guard.exe` | File guardian: monitors config files, runs candidate review workflow |
| `guard-ui.exe` | System tray control panel: status display, credential & binding management |

### CLI Subcommands

**Core:**
- `guard.exe watch` — Start file monitoring loop
- `guard.exe prepare` — Generate trusted baseline snapshots
- `guard.exe status` — Show current guard status
- `guard.exe stop` — Stop watch loop

**Candidate management:**
- `candidate-status` / `promote-candidate` / `discard-candidate` / `mark-bad-candidate` / `retry-candidate`

**Platform credentials & binding:**
- Telegram: `save-telegram-credentials`, `complete-telegram-binding`, `unbind-telegram`
- Feishu: `save-feishu-credentials`, `complete-feishu-binding`, `unbind-feishu`, `test-feishu-message`
- WeCom: `save-wecom-credentials`, `test-wecom-connection`, `complete-wecom-binding`, `unbind-wecom`, `test-wecom-message`

### Remote Install Options

**One-liner — Gitee (China, recommended):**
```powershell
irm https://gitee.com/sorry123luck/openclaw-guard-kit/raw/main/scripts/install.ps1 | iex
```

**One-liner — GitHub (international):**
```powershell
irm https://github.com/sorry123luck/openclaw-guard-kit/releases/latest/download/install.ps1 | iex
```

Both installers automatically fall back to the other source if the primary is unavailable.

**With custom paths:**
```powershell
irm https://gitee.com/sorry123luck/openclaw-guard-kit/raw/main/scripts/install.ps1 | iex `
  -InstallDir "D:\guard-kit" `
  -OpenClawRoot "D:\openclaw"
```

**Manual download:**
```powershell
# Gitee:
# https://gitee.com/sorry123luck/openclaw-guard-kit/releases/download/v.1.0.0/openclaw-guard-kit-windows-x64.zip
# GitHub:
# https://github.com/sorry123luck/openclaw-guard-kit/releases/download/v.1.0.0/openclaw-guard-kit-windows-x64.zip

powershell -ExecutionPolicy Bypass -File ".\installer\install.ps1"
```

### Runtime Files

```
<OpenClawRoot>\.guard-state\
├── manifest.json           # Snapshot manifest (Trusted + Candidate)
├── detector-status.json    # Detector state
├── gateway-port-cache.json # Cached gateway port
├── startup-protect.json   # Startup protection window
└── logs\
    └── doctor-*.log        # Doctor diagnosis logs
```

### License

MIT
