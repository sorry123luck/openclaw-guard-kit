# OpenClaw Guard Kit

面向 Windows 的 OpenClaw 守护与恢复工具集。

## 核心能力

- **漂移监控**：持续轮询受保护配置文件，与基线比对检测未授权修改
- **基线备份与恢复**：自动建立受保护文件的基线快照，支持一键恢复到受信任状态
- **Candidate 机制**：漂移稳定 5 秒后自动进入 candidate 状态，由 doctor 流程验证并通过 promote/rollback 处置
- **多通道通知**：支持 **Telegram、飞书、企业微信** 推送 candidate、异常、恢复等关键事件
- **轻量托盘控制面板**：Windows 系统托盘程序，支持机器人绑定配置和状态查看

## 主要组件

| 组件 | 说明 |
|------|------|
| `guard` | 核心守护进程，负责漂移监控、基线恢复、candidate 生命周期管理 |
| `guard-detector` | 检测 OpenClaw 运行状态，并管理 guard 生命周期 |
| `guard-ui` | 轻量级 Windows 控制面板 / 托盘程序 |
| `tools/wecom-bridge` | 企业微信桥接辅助工具 |

## 系统要求

- Windows
- 已正确安装并可运行的 OpenClaw
- PowerShell 5.1 或更高版本
- 能访问 GitHub Releases
- Go 仅在本地源码开发和发布打包时需要

## 安装

### 推荐方式：基于 Release 的安装

```powershell
powershell -ExecutionPolicy Bypass -File .\installer\install.ps1
```

**默认流程：**

1. 从 GitHub Releases 下载最新发布包
2. 解压到临时目录
3. 调用 `installer/install-package.ps1`
4. 安装程序文件到本地安装目录
5. 注册并启动 guard-detector

**默认安装目录：** `%USERPROFILE%\.openclaw-guard-kit`

### 自定义安装目录

```powershell
powershell -ExecutionPolicy Bypass -File .\installer\install.ps1 `
  -InstallDir "$env:USERPROFILE\.openclaw-guard-kit-test" `
  -OpenClawRoot "$env:USERPROFILE\.openclaw"
```

## 升级

运行已安装目录中的升级脚本：

```powershell
powershell -ExecutionPolicy Bypass -File "$env:USERPROFILE\.openclaw-guard-kit\installer\update.ps1"
```

**默认流程：**

1. 从 GitHub Releases 下载最新发布包
2. 解压到临时目录
3. 调用 `installer/update-from-dir.ps1`
4. 刷新安装目录中的资源
5. 重启 detector

## 本地开发

本地源码开发时可执行：

```powershell
go build ./...
powershell -ExecutionPolicy Bypass -File .\packaging\package.ps1 -Version v0.1.0-test
```

打包脚本会生成用于 GitHub Releases 的发布 zip。

## 核心工作流程

```
watch (轮询) → drift detected → stable 5s → candidate → doctor → promote / rollback
```

Guard 不介入 OpenClaw 的操作流程，只监控受保护文件的变化并提供基线恢复能力。

## 受保护文件

- `~/.openclaw/openclaw.json`
- `~/.openclaw/agents/<agentId>/agent/auth-profiles.json`
- `~/.openclaw/agents/<agentId>/agent/models.json`

## 文档

- [COMMANDS.md](COMMANDS.md)
- [docs/install.md](docs/install.md)
- [docs/update.md](docs/update.md)
- [docs/uninstall.md](docs/uninstall.md)
- [docs/troubleshoot.md](docs/troubleshoot.md)
- [docs/releasing.md](docs/releasing.md)

## 许可证

MIT
