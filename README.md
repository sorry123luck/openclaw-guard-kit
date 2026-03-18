# openclaw-guard-kit (v1 scaffold)

首轮落地：

- 语言：Go
- 主程序：`cmd/guard`
- 已落包：`internal/protocol + config + backup + gateway + process + watch + logging + notify`
- 已实现子命令：`prepare` / `watch`
- 首轮守护目标：
  - 必选：`openclaw.json`
  - 条件性：`auth-profiles.json`（存在时才纳入守护）

## 行为说明

### `guard prepare`

- 解析守护配置。
- 校验 `openclaw.json` 是否存在。
- 若 `auth-profiles.json` 存在，则一并纳入。
- 将基线备份写入：`.guard/backup/*.baseline.json`
- 生成状态清单：`.guard/manifest.json`

### `guard watch`

- 读取 `.guard/manifest.json`
- 若缺失且 `--auto-prepare=true`，自动建立首份基线
- 轮询检测文件漂移：
  - 文件被删除
  - 文件内容被修改
- 发现漂移后按配置恢复基线

## 目录结构

```text
cmd/guard
internal/protocol
config
backup
gateway
process
watch
logging
notify
examples
```

## 默认路径

若只给 `--root C:\OpenClaw`：

- `openclaw.json` → `C:\OpenClaw\openclaw.json`
- `auth-profiles.json` → `C:\OpenClaw\auth-profiles.json`
- 备份目录 → `C:\OpenClaw\.guard\backup`
- 状态文件 → `C:\OpenClaw\.guard\manifest.json`

## 用法

```powershell
guard prepare --root C:\OpenClaw
guard watch --root C:\OpenClaw --interval 2
```

或者：

```powershell
guard watch --config .\examples\guard.example.json
```

## 当前边界（v1）

当前版本故意先做稳：

- 使用轮询，不依赖 `fsnotify`
- `gateway/process/notify` 先保留为可扩展接口与 noop/log 实现
- 只恢复到 `prepare` 阶段形成的静态基线
- 暂未加入 Windows Service、系统托盘、远程上报、热更新基线

## 下一轮建议

1. 增加 `repair` / `status` 子命令
2. 接入 `fsnotify` + 轮询双保险
3. 增加签名校验/白名单写入窗口
4. 将 `gateway` 接到真实 IPC 或本地 socket
5. 将 `process` 接到 OpenClaw 主进程联动
