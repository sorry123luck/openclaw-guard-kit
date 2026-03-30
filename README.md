# OpenClaw Guard Kit

OpenClaw Guard Kit 是一个运行在 Windows 上的 **OpenClaw 外部守护与恢复工具**。

它**不会修改 OpenClaw 源码**，而是以独立程序的方式运行，主要解决下面几件事：

- 检测 OpenClaw 当前是否在线
- 在 OpenClaw 在线时自动拉起守护程序和控制面板
- 监控关键配置文件是否发生异常变更
- 在发现可疑配置变动后执行审查、诊断、恢复或回退
- 通过 Telegram、飞书、企业微信发送通知
- 允许已绑定用户远程启动或重启 OpenClaw

---

# 项目定位

本项目当前主线定位为：

- **guard-detector.exe**：负责生命周期监控
- **guard.exe**：负责配置保护、候选审查、doctor 诊断、恢复/回退
- **guard-ui.exe**：负责本地控制面板、托盘入口、机器人绑定
- **机器人通道**：负责通知与远程恢复

它是一个**外部守护工具**，不是对 OpenClaw 的深度内部改造。

---

# 当前主线能力

当前版本已经包含以下主线功能：

## 1. 生命周期监控
detector 会持续检测 OpenClaw 是否在线。

当 OpenClaw 在线时：
- 自动确保 `guard.exe` 正常运行
- 自动确保 `guard-ui.exe` 正常运行

当 OpenClaw 离线达到确认条件后：
- 自动停止 `guard-ui.exe`
- 自动停止 `guard.exe`
- 发送离线确认通知

此外，detector 还支持：
- 启动保护窗口
- 过渡保护窗口
- 远程启动后的快速探测窗口
- detector 自身退出状态
- gateway 端口自动发现
- gateway 端口本地缓存复用

---

## 2. 外部配置保护
guard 会监控 OpenClaw 关键配置文件。

当检测到配置变化时，流程大致为：
1. 等待文件变更稳定
2. 生成 candidate snapshot
3. 执行 review / health / doctor 流程
4. 判断是否可信、是否需要恢复、是否需要回退、是否需要标记 bad

也就是说，guard 不只是简单“发现文件变了”，而是会继续判断：
- 这是正常修改
- 这是可疑修改
- 这是破坏性修改
- 是否需要自动恢复

---

## 3. 通知通道
当前支持以下机器人通道：

- Telegram
- 飞书
- 企业微信

每个已绑定通道都可以分别控制：
- 是否接收通知
- 是否允许远程命令

---

## 4. 远程恢复
已绑定且开启远程命令的用户，可以通过机器人发送命令，例如：

- `启动openclaw`
- `重启openclaw`

detector 收到后会：
- 校验发送者是否已绑定
- 校验该绑定是否允许远程命令
- 执行启动或重启
- 进入快速探测窗口
- 更快确认 OpenClaw 是否恢复在线
- 返回成功、失败或建议重试的信息

如果首次冷启动失败，系统会提示可再次尝试启动。

---

# 程序结构

## detector
目录：`cmd/guard-detector`

职责：
- 检测 OpenClaw 在线 / 离线状态
- 自动发现 gateway 端口并缓存
- 拉起 / 停止 guard 和 UI
- 发送生命周期通知
- 处理远程启动与重启命令
- 输出 detector 状态文件

---

## guard
目录：`cmd/guard`

职责：
- 准备 trusted baseline
- 监控受保护目标
- 生成 candidate snapshot
- 执行 review / doctor / restore / rollback 流程
- 提供 CLI 操作入口
- 提供 UI 所需的绑定与凭证命令

---

## UI
目录：`cmd/guard-ui`

职责：
- 托盘入口
- 显示 detector / guard / gateway 状态
- 管理 Telegram / 飞书 / 企业微信凭证
- 管理机器人绑定
- 保存通知开关与远程命令开关
- 发送测试消息

当前 UI 是**轻量控制面板**，不是复杂的后台管理系统。

---

## review
目录：`internal/review`

职责：
- 健康检查
- doctor 输出分类
- rollback / self-heal / ignore 决策
- review 状态输出

---

## notify
目录：`notify`

职责：
- 凭证存储
- 绑定存储
- 通道消息发送
- pairing 监听
- 事件文案生成

---

# 运行时文件

程序运行后，会在 OpenClaw 根目录下写入运行状态文件：

```text
<OpenClawRoot>\.guard-state\