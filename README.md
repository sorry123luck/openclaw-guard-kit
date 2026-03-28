# OpenClaw Guard Kit

配置文件守护工具，为 OpenClaw 提供受保护的写入机制、配置文件备份恢复、以及独立的消息通知通道。

## 核心功能

### 1. 受保护的写入（Guarded Write）

修改 OpenClaw 配置文件前必须申请临时租约（Lease），写入完成后确认或放弃。整个过程受 guard 监控，防止配置被意外或恶意修改导致系统故障。

### 2. 配置文件备份与恢复

自动记录配置文件的可信基线（Baseline），检测到文件被修改时可根据策略自动恢复或通知用户。

### 3. 独立消息通知

即使 OpenClaw 主程序崩溃，guard 仍可通过 Telegram、飞书、企业微信独立向用户发送通知，确保关键信息不漏失。

### 4. 进程守护

guard-detector 监控 OpenClaw 运行状态，在线时确保 guard 正常运作，离线时执行对应策略。

## 系统要求

- Windows 操作系统
- OpenClaw 已安装并正常运行
- Go 1.21+（如需从源码编译）

## 安装

### 一键安装（推荐）

```powershell
git clone https://github.com/sorry123luck/openclaw-guard-kit.git
cd openclaw-guard-kit
.\installer\install.ps1
```

安装过程会自动：
1. 编译 Go 程序（如无可用二进制）
2. 创建安装目录
3. 安装共享技能到 OpenClaw
4. 配置自启动

### 手动安装

1. 下载本仓库
2. 运行 `guard prepare --root <OPENCLAW_ROOT>` 初始化基线
3. 运行 `guard watch --root <OPENCLAW_ROOT>` 启动守护

## 快速开始

### 1. 初始化

```powershell
guard prepare --root C:\Users\Administrator\.openclaw
```

### 2. 启动守护

```powershell
guard watch --root C:\Users\Administrator\.openclaw --interval 2
```

### 3. 配置通知通道（可选）

**Telegram：**
```powershell
guard save-telegram-credentials --root ~/.openclaw --token "YOUR_BOT_TOKEN"
guard complete-telegram-binding --root ~/.openclaw --account-id "BOT_ID" --sender-id "YOUR_CHAT_ID" --display-name "My Guard" --code "PAIRING_CODE"
```

**飞书：**
```powershell
guard save-feishu-credentials --root ~/.openclaw --account-id "APP_ID" --app-secret "APP_SECRET"
guard complete-feishu-binding --root ~/.openclaw --account-id "APP_ID" --sender-id "OPEN_ID" --display-name "My Guard" --code "PAIRING_CODE"
```

**企业微信：**
```powershell
guard save-wecom-credentials --root ~/.openclaw --bot-id "BOT_ID" --secret "SECRET"
guard complete-wecom-binding --root ~/.openclaw --account-id "BOT_ID" --sender-id "USER_ID" --display-name "My Guard" --code "PAIRING_CODE"
```

## 命令参考

详见 [COMMANDS.md](COMMANDS.md)

## 工作流程

```
用户修改配置 → guard request-write 申请租约 → guard 记录当前状态 → 用户执行写入 → guard complete-write 确认写入 → 文件稳定后更新基线
```

## 架构

- `guard` — 核心守护进程，处理受保护的写入请求
- `guard-detector` — OpenClaw 状态探测器，负责自动启停 guard
- `guard-ui` — 轻量控制面板
- `tools/wecom-bridge` — 企业微信通知桥接

## 许可证

MIT License
