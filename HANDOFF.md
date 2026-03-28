# openclaw-guard-kit 项目交接说明

## 一、项目当前状态（已冻结）

当前项目已经完成并验证通过的内容：

### 1. 核心守护闭环已完成
项目已经具备以下能力：

- 对 `openclaw.json` 进行守护
- 对 `auth-profiles.json` 进行守护
- 未授权修改会被检测并自动恢复
- 合法修改必须先申请写租约（lease）
- `complete-write` 会刷新 baseline
- `fail-write` 会释放 lease，但不刷新 baseline，后续会恢复旧 baseline

---

### 2. 已验证通过的场景

#### openclaw.json
- 未授权修改后自动恢复：通过
- `request-write -> 修改 -> complete-write`：通过
- `request-write -> 修改 -> fail-write`：通过

#### auth-profiles.json
- 未授权修改后自动恢复：通过
- `request-write -> 修改 -> complete-write`：通过
- `request-write -> 修改 -> fail-write`：通过

---

### 3. fail-write 语义已冻结
正式语义如下：

- `fail-write` = 释放 lease
- 不刷新 baseline
- 如果文件内容已经偏离 baseline，watch 后续应恢复旧 baseline

这个语义已经实测通过，后续不要随意改。

---

### 4. 日志可观测性已补齐
当前已确认以下日志能正常出现：

- `drift detected but skipped due to active lease`
- `drift.detected`
- `restore.completed`
- `write completed`
- `baseline refreshed after write complete`

---

## 二、项目目录结构（当前核心文件）

```text
cmd/guard/main.go
config/config.go
backup/service.go
gateway/pipe_client.go
gateway/pipe_server.go
gateway/service.go
internal/coord/coordinator.go
internal/protocol/types.go
logging/logger.go
notify/manager.go
notify/notifier.go
notify/store.go
process/manager.go
process/service.go
watch/service.go
```

---

## 三、当前启动与单实例边界

### 1. 当前程序的正式运行入口
```
guard watch
```

### 2. 当前单实例治理方式
- 依赖固定 named pipe（`\\.\pipe\openclaw-guard`）
- 如果已有旧的 watch 进程仍在运行，第二个 watch 会启动失败
- CLI 会直接提示 pipe 已被占用
- 日志会记录：
  - `watch.starting`
  - `watch.started`
  - `watch.start.failed`（`reason=pipe_in_use`）

### 3. 当前阶段明确不做的事
- 尚未引入 Windows Service
- 尚未引入 mutex
- 尚未引入 pid file
- 尚未提供 stop / service 子命令

### 4. 未来服务化边界
- 未来如需做 Windows Service，服务模式应复用现有 watch 主循环
- 不应重新发明第二套守护逻辑
- service 只是外层托管壳，不改变当前 watcher / pipe / lease / baseline 语义

---

## 四、当前日志边界

### 1. 启动边界日志
- `watch.starting`
- `watch.started`
- `watch.start.failed`

### 2. 运行期核心日志（已冻结，不在本轮改动）
- `drift.detected`
- `restore.completed`
- `write completed`
- `baseline refreshed after write complete`

### 3. 单实例占用错误识别
- `reason=pipe_in_use` 表示已有旧 watch 占用 pipe
- `reason=pipe_listen_error` 表示其他 pipe 监听失败

当前项目进度更新如下：

openclaw-guard-kit 已完成本轮事件总线接入改造，并通过完整回归测试。测试结果表明，项目在编译层、运行层、写入授权链路和事件链路上均已正常工作。具体包括：go build ./... 编译通过，guard.exe 主程序编译通过，watch 可正常启动，status 可正确返回守护状态，openclaw 的 request-write -> complete-write 链路通过，auth-profiles 的 request-write -> fail-write 链路通过，stop 后 status 可正确返回未运行状态。

同时，从运行日志可以确认，service、guard、pipe、write、watch、restore、baseline refresh 等关键事件已经统一进入事件链，说明本次事件总线接入并非停留在代码层，而是已经在运行时真实生效。

因此，目前可以确认：项目已完成阶段四骨架接入后的事件总线主链路打通，核心守护框架、写入授权机制、漂移检测与恢复机制、baseline 刷新机制和统一事件分发机制均已跑通。项目当前进入服务化收口、长期稳定性验证与上层联动完善阶段。