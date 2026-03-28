# OpenClaw Guard Kit

面向 Windows 的 OpenClaw 守护与恢复工具集。

## 核心能力

- 对关键配置文件进行**受保护写入**
- 为关键配置建立**基线备份**与恢复能力
- 通过 detector 管理 guard **生命周期**
- 支持 **Telegram、飞书、企业微信** 等通知通道
- 提供轻量级 **Windows 托盘 / 控制面板**

## 主要组件

| 组件 | 说明 |
|------|------|
| `guard` | 核心守护进程，负责 lease、受保护写入、基线刷新、漂移恢复 |
| `guard-detector` | 检测 OpenClaw 运行状态，并管理 guard 生命周期 |
| `guard-ui` | 轻量级 Windows 控制面板 / 托盘程序 |
| `tools/wecom-bridge` | 企业微信桥接辅助工具 |
| `skills/openclaw-guard-kit` | 安装到 OpenClaw 的共享技能 |

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
5. 安装共享技能到 OpenClaw
6. 更新 workspace 规则
7. 注册并启动 guard-detector

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
5. 刷新共享技能与 workspace 规则
6. 重启 detector

## 本地开发

本地源码开发时可执行：

```powershell
go build ./...
powershell -ExecutionPolicy Bypass -File .\packaging\package.ps1 -Version v0.1.0-test
```

打包脚本会生成用于 GitHub Releases 的发布 zip。

## 受保护写入流程

```
request-write → lease granted → modify → complete-write / fail-write
```

## 文档

- [COMMANDS.md](COMMANDS.md)
- [docs/install.md](docs/install.md)
- [docs/update.md](docs/update.md)
- [docs/uninstall.md](docs/uninstall.md)
- [docs/troubleshoot.md](docs/troubleshoot.md)
- [docs/releasing.md](docs/releasing.md)

## 许可证

MIT
