# OpenClaw Guard Kit 命令参考

## 核心命令

### guard prepare

初始化守护，生成配置文件基线。

```powershell
guard prepare --root <OPENCLAW_ROOT> [--agent main]
```

**参数：**
- `--root` — OpenClaw 根目录（必需）
- `--agent` — 要守护的 agent ID（默认 main）

**示例：**
```powershell
guard prepare --root C:\Users\Administrator\.openclaw
```

---

### guard watch

启动守护进程，监控受保护文件的变更。

```powershell
guard watch --root <OPENCLAW_ROOT> [--interval 2] [--auto-prepare]
```

**参数：**
- `--root` — OpenClaw 根目录（必需）
- `--interval` — 轮询间隔秒数（默认 2）
- `--auto-prepare` — 缺失基线时自动创建

**示例：**
```powershell
guard watch --root C:\Users\Administrator\.openclaw --interval 2
```

---

### guard status

检查 guard 守护进程是否正在运行。

```powershell
guard status [--pipe \\.\pipe\openclaw-guard]
```

---

### guard stop

请求 guard 守护进程优雅停止。

```powershell
guard stop
```

---

## 受保护写入

### guard guarded-write

受保护的配置文件写入（需要先申请租约）。

```powershell
guard guarded-write --root <OPENCLAW_ROOT> --path <TARGET_FILE> --from <SOURCE_FILE>
```

**参数：**
- `--root` — OpenClaw 根目录
- `--path` — 目标文件路径
- `--from` — 源文件路径（包含新内容的文件）

**示例：**
```powershell
guard guarded-write --root C:\Users\Administrator\.openclaw --path C:\Users\Administrator\.openclaw\openclaw.json --from C:\temp\new_config.json
```

---

### guard openclaw-op

在暂停监控的情况下执行 OpenClaw 原生命令。

```powershell
guard openclaw-op --root <OPENCLAW_ROOT> -- <OPENCLAW_COMMAND>
```

**示例：**
```powershell
guard openclaw-op --root C:\Users\Administrator\.openclaw -- config set agents.defaults.model.primary minimax/MiniMax-M2.7
```

---

## 监控管理

### guard pause-monitoring

暂停文件变更监控（用于手动修改配置前）。

```powershell
guard pause-monitoring --root <OPENCLAW_ROOT>
```

---

### guard resume-monitoring

恢复文件变更监控。

```powershell
guard resume-monitoring --root <OPENCLAW_ROOT>
```

---

### guard monitoring-status

查看当前监控状态。

```powershell
guard monitoring-status --root <OPENCLAW_ROOT>
```

---

### guard candidate-status

查看可信基线（Baseline）和候选快照（Candidate）的状态。

```powershell
guard candidate-status --root <OPENCLAW_ROOT>
```

---

### guard promote-candidate

将候选快照提升为可信基线。

```powershell
guard promote-candidate --root <OPENCLAW_ROOT> --target <TARGET_NAME>
```

**示例：**
```powershell
guard promote-candidate --root C:\Users\Administrator\.openclaw --target auth:main
```

---

### guard discard-candidate

丢弃候选快照。

```powershell
guard discard-candidate --root <OPENCLAW_ROOT> --target <TARGET_NAME>
```

---

### guard mark-bad-candidate

标记候选快照为坏快照（停止自动验证）。

```powershell
guard mark-bad-candidate --root <OPENCLAW_ROOT> --target <TARGET_NAME>
```

---

### guard retry-candidate

重试坏快照（重新验证）。

```powershell
guard retry-candidate --root <OPENCLAW_ROOT> --target <TARGET_NAME>
```

---

## 通知通道绑定

### Telegram

```powershell
# 保存凭证
guard save-telegram-credentials --root <OPENCLAW_ROOT> --token "<BOT_TOKEN>"

# 完成绑定（获取配对码后）
guard complete-telegram-binding --root <OPENCLAW_ROOT> --account-id "<BOT_ID>" --sender-id "<CHAT_ID>" --display-name "<NAME>" --code "<CODE>"

# 解除绑定
guard unbind-telegram --root <OPENCLAW_ROOT>
```

---

### 飞书

```powershell
# 保存凭证
guard save-feishu-credentials --root <OPENCLAW_ROOT> --account-id "<APP_ID>" --app-secret "<APP_SECRET>"

# 完成绑定
guard complete-feishu-binding --root <OPENCLAW_ROOT> --account-id "<APP_ID>" --sender-id "<OPEN_ID>" --display-name "<NAME>" --code "<CODE>"

# 测试消息
guard test-feishu-message --root <OPENCLAW_ROOT> --message "测试消息内容"

# 解除绑定
guard unbind-feishu --root <OPENCLAW_ROOT>
```

---

### 企业微信

```powershell
# 保存凭证
guard save-wecom-credentials --root <OPENCLAW_ROOT> --bot-id "<BOT_ID>" --secret "<SECRET>"

# 测试连接
guard test-wecom-connection --root <OPENCLAW_ROOT>

# 完成绑定
guard complete-wecom-binding --root <OPENCLAW_ROOT> --account-id "<BOT_ID>" --sender-id "<USER_ID>" --display-name "<NAME>" --code "<CODE>"

# 测试消息
guard test-wecom-message --root <OPENCLAW_ROOT> --message "测试消息内容"

# 解除绑定
guard unbind-wecom --root <OPENCLAW_ROOT>
```

---

## 安装与更新

### 安装

```powershell
git clone https://github.com/sorry123luck/openclaw-guard-kit.git
cd openclaw-guard-kit
.\installer\install.ps1
```

### 强制重新编译

```powershell
.\install.ps1 -ForceRebuild
```

### 更新

```powershell
guard.exe update
```

或手动运行 `installer\update.ps1`

### 卸载

```powershell
installer\uninstall.ps1
```

---

## 状态文件说明

守护进程在 OpenClaw 根目录下创建 `.guard-state` 目录：

```
~/.openclaw/.guard-state/
├── manifest.json              # 守护状态清单
├── backup/                    # 基线和候选快照
│   ├── openclaw.baseline.json
│   └── auth.main.baseline.json
└── pending_robot_bindings.json  # 待绑定机器人
```

---

## 故障排除

### 守护进程启动失败

```
watch 启动失败：pipe 已被其他 guard 进程占用
```

解决：先运行 `guard stop` 停止旧进程，再重新启动。

### 配置文件被恢复

如果文件被意外修改，guard 会自动从基线恢复。如需保留修改，运行 `guard promote-candidate` 将修改提升为新基线。
