# guard 命令使用说明

## watch 使用说明

### 启动命令
```
guard watch
```

### 当前用途
- 启动本地守护
- 监听 named pipe 请求
- 监控受保护文件并执行恢复/刷新逻辑

### 启动成功时可观察到的日志
- `watch.starting`
- `watch.started`

---

## watch 启动占用说明

1. guard watch 启动时会监听固定 named pipe（`\\.\pipe\openclaw-guard`）
2. 若已有旧的 watch 进程仍在运行，第二个 watch 会启动失败
3. 失败时 stderr 会提示 pipe 已被其他 guard 进程占用
4. 日志会记录 `watch.start.failed` 且 `reason=pipe_in_use`
5. 处理方式：先停止旧 watch，再重新启动

---

## 当前服务化状态

1. 当前版本尚未正式提供 Windows Service 模式
2. 当前推荐运行方式仍是前台执行 `guard watch`
3. 未来若增加 Service，应复用当前 watch 逻辑，而不是另写一套守护主链
