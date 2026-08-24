# Mutagen Web Console — 代码审查报告

> 审查日期：2026-08-18
> 项目：远程文件同步管理面板（Server / Agent / Web 三层）
> 技术栈：Go (Gin + gorilla/websocket) + Vue 3 + JSON 文件存储

---

## 概要

共发现 **14** 个问题：

| 严重级别 | 数量 | 问题编号 |
|---|---|---|
| 🔴 高 | 6 | 1, 2, 3, 4, 5, 6 |
| 🟡 中 | 5 | 7, 8, 9, 10, 11 |
| 🔵 低 | 3 | 12, 13, 14 |

**建议优先修复**：Bug 1（数据丢失）、Bug 2 / Bug 4（认证绕过）会在真实生产环境造成严重事故；Bug 3 / Bug 5（data race）在多机并发下必现。

---

## 🔴 高严重级别

### Bug 1：`save()` 忽略写错误，数据文件被永久删除（数据丢失）

- **文件**：`server/store/store.go:94-118`
- **类型**：数据完整性

```go
func (s *Store) save() error {
    ...
    tmpPath := s.path + ".tmp"
    os.WriteFile(tmpPath, data, 0644)   // err 被丢弃
    os.Remove(s.path)                     // 无检查地删除原文件
    return os.Rename(tmpPath, s.path)
}
```

**问题**：`os.WriteFile` 返回的 error 完全被丢弃。当磁盘满（ENOSPC）、权限错误或临时路径不可写时，写入失败，但代码继续执行 `os.Remove(s.path)` 删除原始数据文件，再用不存在的 tmp 文件 `Rename`——原始数据永久丢失。

`save()` 被 `CreateMachine` / `SaveMachine` / `DeleteMachine` / `CreateTask` / `SaveTask` / `DeleteTask` / `SaveConfig` / `NextID` / `UpsertSyncTaskStatus` / `BulkUpsertSyncTaskStatus` 等十余处调用，任何写操作都可能触发。

**复现**：磁盘空间耗尽时执行任意写操作（如 `CreateMachine`）。

**修复建议**：

```go
tmpPath := s.path + ".tmp"
if err := os.WriteFile(tmpPath, data, 0644); err != nil {
    os.Remove(tmpPath) // 清理可能残留的临时文件
    return err
}
os.Remove(s.path)
return os.Rename(tmpPath, s.path)
```

---

### Bug 2：`-auth` 命令行参数提供凭据但无 `auth.json` 时认证系统完全失效（认证绕过）

- **文件**：`server/main.go:56-78`
- **类型**：安全漏洞

```go
handlers.InitAuth(*auth)          // 凭据正确加载到 userPasswords
hasAuth := true
if *auth == "" {                  // 只检查文件是否存在
    if _, err := os.Stat(...); err != nil {
        hasAuth = false
    }
}
if hasAuth {                      // hasAuth=false 时不注册登录路由和认证中间件
    r.POST("/api/login", handlers.LoginHandler)
    r.Use(...)
}
```

**问题**：当用户通过 `-auth admin:secret` 提供凭据、但 `~/.mutagen-web/auth.json` 文件不存在时，`InitAuth` 正常加载了凭据，但 `hasAuth` 因文件不存在被判为 false，登录路由和 auth 中间件均不注册——**所有 `/api/*` 路由无认证保护**。

**复现**：
```bash
./mutagen-web -addr :8080 -auth admin:secret
# 无 auth.json 时，GET /api/machines 直接返回 200
```

**修复建议**：`hasAuth` 应以 `userPasswords` 是否非空为准，让 `InitAuth` 返回布尔值表示是否实际加载了凭据。

---

### Bug 3：`GetMachine` / `GetTask` / `GetConfig` 返回切片元素指针导致 data race

- **文件**：`server/store/store.go:141-160`（GetMachine/GetMachineByToken）
- **文件**：`server/store/store.go:239-248`（GetTask）
- **文件**：`server/store/store.go:487-496`（GetConfig）
- **类型**：并发安全（data race）

```go
func (s *Store) GetMachine(id uint) *models.Machine {
    s.mu.RLock()
    defer s.mu.RUnlock()
    for i := range s.machines {
        if s.machines[i].ID == id {
            return &s.machines[i]   // 返回切片元素指针
        }
    }
    return nil
}
```

**问题**：读锁在函数返回时即释放，但返回的指针直接指向内部切片元素。调用方在锁外修改这些字段时，与持写锁的 `Save*`/`Create*`/`Delete*` 操作构成 data race（`go run -race` 必报）。

**已确认的锁外修改点**：

| 位置 | 修改内容 |
|---|---|
| `ws/hub.go:322` MsgTypeRegister | `m.LastSeenAt`, `m.AgentVersion`, `m.OS` |
| `ws/hub.go:369` MsgTypeHeartbeat | `m.LastSeenAt` |
| `handlers/task.go:167` retryTask | `task.LastError = ""` |
| `handlers/task.go:220` updateTask | `task.Name/Alpha/Beta/Mode/...` |
| `handlers/task.go:294` 异步 goroutine | `t.LastError` |
| `handlers/task.go:418` deleteTask | `task.LastError` |
| `handlers/config.go:40` getOrCreateConfig | `cfg.Content`, `cfg.UpdatedAt` |

同文件 `FindTaskByName` / `FindTaskByIdentifier` 已正确返回拷贝，设计不一致。

**修复建议**：`GetMachine` / `GetMachineByToken` / `GetTask` / `GetConfig` 改为返回拷贝：

```go
for i := range s.machines {
    if s.machines[i].ID == id {
        m := s.machines[i]
        return &m
    }
}
```

---

### Bug 4：`/ws/agent` 端点无认证保护，可绕过登录系统注册机器

- **文件**：`server/main.go:85-88`、`server/ws/hub.go:20-26`
- **类型**：安全漏洞

```go
r.GET("/ws/agent", func(c *gin.Context) {
    hub.HandleAgentWebSocket(c)
})
```

**问题**：
1. `/ws/agent` 路由未应用任何认证中间件（auth 中间件只覆盖 `/api/*`）。
2. `websocket.Upgrader.CheckOrigin` 返回 `true`，允许任意来源跨域 WebSocket 连接。
3. 连接后可发 `auto_register` 消息，**无需任何凭据**即可创建新机器、获取 token。

攻击者可从任意网页跨域连接并注册机器，完全绕过登录认证。

**复现**：
```javascript
const ws = new WebSocket("http://target:8080/ws/agent");
ws.onopen = () => ws.send(JSON.stringify({
    type: "auto_register",
    payload: { name: "pwned", agentVersion: "1.0", os: "linux" }
}));
```

**修复建议**：对 `/ws/agent` 添加认证中间件；或将 `auto_register` 限制为仅回环地址可用；或为 WebSocket 端点使用非猜测路径（长随机前缀）。

---

### Bug 5：Agent `signalDisconnect()` 中 `reconnecting` 无同步保护导致 data race

- **文件**：`agent/client/ws.go:138-145`
- **类型**：并发安全（data race）

```go
func (a *Agent) signalDisconnect() {
    if !a.reconnecting {          // 无保护的读
        a.reconnecting = true     // 无保护的写
        a.cleanup()
        close(a.done)
    }
}
```

**问题**：`readLoop` 和 `writePump` 的 defer 都会调用 `signalDisconnect()`。TCP 断线时两者几乎同时退出，两个 goroutine 并发读写裸 `bool a.reconnecting`——data race。此外 `a.done` 可能被重复 close 导致 panic（service 停止路径与 readLoop 退出竞态）。

**修复建议**：使用 `sync.Once` 或 `atomic.Bool`：

```go
disconnectOnce sync.Once
func (a *Agent) signalDisconnect() {
    a.disconnectOnce.Do(func() { a.cleanup() })
    close(a.done)
}
```

---

### Bug 6：前端 `downloadAgentPack` 将 token 拼入 URL 查询参数（敏感信息泄露）

- **文件**：`web/src/App.vue:1222-1224`、`web/src/api/client.js:42`
- **类型**：安全漏洞

```js
// App.vue:1223
window.open(`/api/machines/${m.id}/agent-pack?token=${localStorage.getItem("auth_token")}`, "_blank")
```

**问题**：
1. 登录 token 明文出现在浏览器地址栏、历史记录、服务器访问日志、Referer 头中。
2. `client.js:42` 已有正确的 axios blob 版 `machineApi.downloadPack`（带 Authorization 头），但界面用的是 `window.open`——两种实现并存。
3. 如果后端只校验 `Authorization` 头（常见做法），`window.open` 方式会收到 401 而非下载文件。

**修复建议**：改用 axios blob 方式：

```js
async function downloadAgentPack(id) {
    const res = await machineApi.downloadPack(id)
    const url = URL.createObjectURL(new Blob([res.data]))
    const a = document.createElement('a')
    a.href = url
    a.download = `mutagen-agent-${id}.zip`
    a.click()
    URL.revokeObjectURL(url)
}
```

---

## 🟡 中严重级别

### Bug 7：`stop_agent` 用 `os.Exit(0)`，跳过 defer 且结果消息可能丢失

- **文件**：`agent/client/ws.go:732-740`
- **类型**：可靠性

```go
case "stop_agent":
    a.enqueue(msg)
    time.Sleep(500 * time.Millisecond)
    a.Close()
    os.Exit(0)
```

**问题**：`os.Exit(0)` 跳过所有 defer（包括 readLoop/writePump/heartbeatLoop 的 defer），这些 goroutine 对已关闭 connection 的操作可能 panic。`enqueue` 通过 `select` 的 `<-a.done` 分支可能被静默丢弃，Server 端收不到 "agent stopping" 结果。

**修复建议**：改用正常退出路径（`a.Close()` 触发 `signalDisconnect`），不要用 `os.Exit`。

---

### Bug 8：SSH 命令无执行超时，一个卡死阻塞全部 agent 命令

- **文件**：`agent/mutagen/cmd.go:143-163`（Push/ReadRemoteBackupConfig）、`agent/mutagen/cmd.go:428-440`（EnsureRemoteAgentsFresh）
- **类型**：可靠性

```go
cmd := exec.Command("ssh",
    "-o", "BatchMode=yes",
    "-o", "ConnectTimeout=15",
    host, "rm -rf ~/.mutagen/agents/")
```

**问题**：只设了 `ConnectTimeout=15`（连接超时），没设命令执行超时。远端 `cat` / `rm -rf` 因磁盘 IO hang 卡死时，因 `handleCommand` 受 `cmdMu` 串行保护，**所有后续命令一起阻塞**，前端超时。

**修复建议**：

```go
ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
defer cancel()
cmd := exec.CommandContext(ctx, "ssh", ...)
```

---

### Bug 9：`listSSHHosts` 未剥离 Host 行内联注释，别名提取污染

- **文件**：`agent/mutagen/cmd.go:501-502`
- **类型**：逻辑错误

```go
for _, h := range strings.Fields(rest) {
    if strings.ContainsAny(h, "*?") { continue }
    ...
}
```

**问题**：`Host myserver # production server` 会被解析成 `["myserver", "#", "production", "server"]`，后续 `EnsureRemoteAgentsFresh` 对这些无效别名执行 SSH，产生大量失败日志。

**修复建议**：

```go
rest = strings.Split(rest, "#")[0]
```

---

### Bug 10：`persistBackupToAgentConfig` 与 `configWatchLoop` 对同一文件无同步读改写

- **文件**：`agent/client/ws.go:810-827`、`agent/client/ws.go:414-483`
- **类型**：可靠性

**问题**：`update_backup_config` 路径中 `persistBackupToAgentConfig` 执行 `ReadFile` → 改 → `WriteFile`（非原子），而 `configWatchLoop` 的定时器回调同时也在 `ReadFile` 读同一个文件。若恰好在写入中间读到，得到损坏 JSON，`configWatchLoop` 静默 `continue` 跳过本次重载。

**修复建议**：用临时文件 + `os.Rename`（Windows 上原子）；解析失败记 warning。

---

### Bug 11：前端 `setInterval` 无保存引用且可重复叠加（轮询倍速）

- **文件**：`web/src/App.vue:497`、`web/src/App.vue:1230-1234`、`web/src/App.vue:1239`
- **类型**：可靠性

**问题**：
- `doLogin` 登录成功时 `setInterval(loadMachines, 30000)`（第 497 行）
- `onMounted` 又调一次 `setInterval(loadMachines, 30000)`（第 1239 行）
- 任务轮询定时器（第 1230-1234 行）同样无引用、无清理

多次登录或页面重载后定时器叠加，`loadMachines` 以倍数频率执行。所有定时器都无保存引用，`onUnmounted` 无法清理。

**修复建议**：存 `ref`，启动前先 `clearInterval`，`onUnmounted` 清理。

---

## 🔵 低严重级别

### Bug 12：`signal.Notify` 在重连循环体内重复调用

- **文件**：`agent/client/ws.go:78`

`signal.Notify(a.interrupt, os.Interrupt)` 放在 `for` 循环内，每次重连都重复注册。虽语义安全但资源浪费。移至循环之前只调一次即可。

### Bug 13：`authRedirecting` 标志永远无法重置 + 100ms 延迟 reload

- **文件**：`web/src/api/client.js:17-29`

首次 401 后 `authRedirecting = true` 永不重置；若 `reload()` 因浏览器策略被阻止，后续所有 401 静默吞掉。100ms 的 `setTimeout` 也增加了竞态窗口。

### Bug 14：`vite.config.js` 配置了 `/ws` 代理但前端无任何 WebSocket 客户端

- **文件**：`web/vite.config.js:12-15`

代理形同虚设，所有前端代码无 `new WebSocket()` 调用。要么实现 WebSocket 连接逻辑，要么删除代理配置避免混淆。

---

## 线索核实结论

| 用户线索 | 结论 | 对应 Bug |
|---|---|---|
| `r.Use` 注册顺序问题 | 不是 bug，Gin 全局中间件与注册先后无关 | — |
| `hasAuth` 判断与 `InitAuth` 关系 | 确认是 bug | Bug 2 |
| `GetMachine` 等返回切片元素指针 | 确认是 bug（含 GetTask/GetConfig） | Bug 3 |
| `save()` 忽略 `WriteFile` err | 确认是 bug | Bug 1 |

---

## 修复优先级建议

1. **Bug 1**（数据丢失）— 改 `save()`，3 行改动
2. **Bug 2**（认证绕过）— 改 `hasAuth` 判断逻辑
3. **Bug 4**（认证绕过）— 给 `/ws/agent` 加保护
4. **Bug 3**（data race）— 4 个方法返回拷贝
5. **Bug 5**（data race）— `signalDisconnect` 加同步保护
6. **Bug 6**（安全泄露）— 改用 axios 下载
7. 其余按可靠性加固处理
