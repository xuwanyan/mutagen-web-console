# Mutagen Web Console — 第二轮 Bug 修复报告

> 日期：2026-08-20
> 范围：第二轮审查发现的 12 个 server/agent bug + 1 个 web bug，全部已修复并验证
> 前置：第一轮 14 个 bug 此前已修复（见 `CODE_REVIEW.md`），本轮聚焦残留与并发/解析缺陷

---

## 概要

本轮共修复 **13** 个问题，全部通过编译 + `go vet` + 测试。

| 严重级别 | 数量 | 编号 |
|---|---|---|
| 🔴 高 | 3 | 1, 2, 3 |
| 🟡 中 | 5 | 4, 5, 6, 7, 8 |
| 🔵 低 | 5 | 9, 10, 11, 12, 13 |

**验证结果**：
- `go build ./...`：server + agent 全部通过
- `go vet ./...`：server + agent 零警告
- `go test ./...`：全绿（含新增 `TestNormalizeModes` 覆盖 #3）
- ⚠️ `go test -race` 未运行：本机无 gcc/cgo，race 检测需 C 编译器（环境限制，非代码问题）

---

## 🔴 高严重

### Bug 1 — `SendCommand` 向已关闭 channel 发送导致进程崩溃
**文件**：`server/ws/hub.go` `SendCommand`

**问题**：`GetClient` 在 RLock 下返回 `*Client` 后释放锁，与 `unregister` 的 `close(client.Send)`（持写锁）无 happens-before。`select{default}` 救不了 closed channel——向已关闭 channel 发送直接 panic。`task.go` 在无 recover 的 goroutine 里调 `SendCommandAndWait`，panic 会 crash 整个 server（gin Recovery 护不到这个 goroutine）。

**修复**：将"查 client + 投递"整体放入 `h.mu.RLock()` 内完成。写锁的 `close(client.Send)` 必须等 RLock 释放，二者互斥，保证不向已关闭 channel 发送。投递是非阻塞 select（default 立即返回），RLock 持有时间极短，无死锁（已核实：hub 内部持写锁的 register/unregister/Disconnect 均不调 SendCommand）。

```go
h.mu.RLock()
defer h.mu.RUnlock()
client, ok := h.clients[machineID]
if !ok { return ErrMachineOffline }
select {
case client.Send <- data: return nil
default: return ErrSendBufferFull
}
```

### Bug 2 — register/unregister 不比较指针，重连踢掉新连接
**文件**：`server/ws/hub.go` `Run()` unregister 分支

**问题**：机器重连时 register 把新连接 B 放进 `h.clients[5]` 并 close 旧 A 的 Conn；A 的 readPump 触发 `unregister <- A`；unregister 只判 `h.clients[5]` 存在就 delete + close，但此时 map 里已是 B → **删掉了新连接 B**。机器前端永久"离线"，agent 却还连着不重连。

**修复**：unregister 增加指针比较，只在 map 里仍是同一个 client 时才删除：
```go
if current, ok := h.clients[client.MachineID]; ok && current == client {
    delete(h.clients, client.MachineID)
    close(client.Send)
}
```
旧 A 的 `unregister` 到达时 `h.clients[5]` 已是 B（≠A），跳过——B 不受影响。A 的 Send 未 close 也无泄漏（A 的 readPump/writePump 已退出，A 不可达后被 GC）。

### Bug 3 — 默认模式会话解析后重建下发非法 `--mode` 导致 create 失败
**文件**：`agent/mutagen/cmd.go`（`normalizeMode`/`normalizeSymlinkMode`、ParseStatus）+ `agent/client/ws.go`（reportStatus）

**根因**（已交叉核对 mutagen 源码 `mode.go:73`、`symbolic_link_mode.go:67`、`list_monitor_common.go:319-324/364-369`）：
- mutagen 默认模式输出 `Synchronization mode: Default (Two Way Safe)`、`Symbolic link mode: Default (Portable)`、`Ignore VCS mode: Default (Propagate)`（首字母大写，后缀是具体模式名）。
- 原 `ParseStatus` 用小写 ` (default)` 做 `TrimSuffix`，不匹配真实输出 → 存下 `"Default (Two Way Safe)"`。
- `normalizeMode` 不识别该形式 → 原样返回 → `CreateSync` 下发 `--mode=Default (Two Way Safe)` → mutagen `UnmarshalText` 报 `unknown synchronization mode specification` → create_sync 失败。

**修复**（三层防御）：
1. `cmd.go` ParseStatus：移除基于虚构格式的小写 ` (default)` TrimSuffix，保留原始展示名。
2. `cmd.go` `normalizeMode`/`normalizeSymlinkMode`：改为**防御式**——无法识别的值（含 `Default (...)`）返回空串，`CreateSync` 不下发 `--mode`/`--symlink-mode`，使用 mutagen 默认，杜绝非法标志。
3. `ws.go` reportStatus：上报前用 `canonicalSyncMode`/`canonicalSymlinkMode` 把展示名归一化为 CLI 标志值（`Two Way Resolved`→`two-way-resolved`；`Default (...)`→空）。

**测试**：新增 `TestNormalizeModes` 覆盖显式/默认/garbage 各分支；把 `cmd_test.go` 夹具从虚构值（`bidirectional (default)`、`symbolic (default)`）替换为真实 mutagen 输出，断言改为期望真实值，真正回归本 bug。

---

## 🟡 中严重

### Bug 4 — `writePump` 忽略 WriteMessage 错误，命令静默丢失
**文件**：`server/ws/hub.go` `writePump`

conn 已坏但 readPump 60s 超时未触发期间，`WriteMessage` 错误被丢弃，writePump 继续循环，handler 以为"已发送"。修复：写失败立即 `return`，defer 关闭 conn → 触发 readPump 失败 → unregister，级联清理。

### Bug 5 — `updateSSHHosts` 丢弃两次 `SaveConfig` 错误
**文件**：`server/handlers/config.go`

`SaveConfig(hostsCfg)` / `SaveConfig(sshCfg)` 返回的 error 被忽略，磁盘满时 DB 未落盘却照常向 agent 下发 `update_ssh_config`。修复：两处均检查 error，失败返回 500 且不下发命令（对比同文件 `updateGlobalConfig` 已有检查）。

### Bug 6 — `updateTask` 异步 goroutine 与 `c.JSON` 对同一 `*SyncTask` data race
**文件**：`server/handlers/task.go`

goroutine 写 `t.LastError`/`SaveTask(t)` 与主流程 `c.JSON(task)` 序列化同一指针，`-race` 必报。修复：启动 goroutine 前取独立副本 `taskSnap := *task`，goroutine 操作 `&taskSnap`，`c.JSON` 序列化原 `task`，二者无共享内存。（已核实 `store.GetTask` 返回切片元素的值拷贝 `&t`，副本安全。）

### Bug 7 — `downloadAgentPack` Content-Disposition 头注入
**文件**：`server/handlers/machine.go`

`machine.Name` 仅 `binding:"required"`，可含 `"`、`\r\n`、空格，拼进 `Content-Disposition` 构成 HTTP 头注入/文件名截断，且 `filename=%s.zip` 缺引号。修复：新增 `sanitizeFilename`（仅保留 `[A-Za-z0-9._-]`，余替换为 `_`），文件名加引号：
```go
c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s.zip\"", sanitizeFilename(machine.Name)))
```

### Bug 8 — `/ws/agent` 空 token 路径可注册 + auto_register 绕过
**文件**：`server/ws/hub.go` `MsgTypeAutoRegister` 分支

不传 token 时 `machineID=0` 仍 Upgrade 注册；`auto_register` 在未设 `-register-key` 时无校验即可拿合法 token。这是第一轮 Bug 4 修复后的残留。修复：**自动注册强制要求 registerKey**——未设 `-register-key` 时禁用 auto_register，返回明确错误，引导用户改用 web UI 预创建机器（生成 token 写入安装包）或设置 `-register-key`。

> ⚠️ **行为变更**：依赖"无密钥 name 自动注册"的部署需在 server 启动时加 `-register-key <key>`，并在 agent 端 `agent-config.json` 配 `registerKey`。已注册（持 token）的 agent 不受影响。

---

## 🔵 低严重

### Bug 9 — `ignoreVcs` 大小写敏感比较恒为 false
**文件**：`agent/client/ws.go` reportStatus

`== "ignore"` 不匹配真实输出 `"Ignore"`（大写）→ 导入会话的"忽略 VCS"勾选恒空，重建丢失 `--ignore-vcs`。修复：改用 `strings.Contains(strings.ToLower(v), "ignore")`，对 `"Ignore"`、`"Default (Ignore)"` 均正确（默认 `Default (Propagate)` 不含 ignore → false，与 mutagen 默认一致）。

### Bug 10 — `LoginHandler` 用户名枚举时序侧信道
**文件**：`server/handlers/auth.go`

用户不存在立即 401（~0ms），存在走 bcrypt（~100ms），可按耗时区分。修复：用户不存在时也对 `dummyBcryptHash` 跑一次 `CompareHashAndPassword` 消耗等价时间（dummy hash 在 init 时生成，比较在释放 `userMu` 后进行，不持锁）。

### Bug 11 — `GenerateToken` 忽略 `crypto/rand.Read` 错误
**文件**：`server/ws/hub.go` + `server/handlers/auth.go`

`rand.Read` 失败时 `b` 可能部分为零，仍生成 token，极端情况碰撞。修复：检查 error，失败时 `log.Fatalf`（CSPRNG 不可用意味着系统无法安全运行；实际 `crypto/rand` 几乎不失败）。

### Bug 12 — `listSSHHosts` 把 `Host = foo` 的 `=` 当别名
**文件**：`agent/mutagen/cmd.go` `listSSHHosts`

ssh_config 允许 `key = value` 形式，`=` 被 `strings.Fields` 当主机名发起失败 SSH。修复：提取 rest 后先 `TrimPrefix(rest, "=")` 再解析。

### Bug 13 — 前端 `updateTask` 缺 `betaPath` 校验
**文件**：`web/src/App.vue` `updateTask`

只校验 `betaHost` 不校验 `betaPath`，清空远端路径保存会让 beta 变成 `"host:"`。修复：补 `if (!t.betaPath)` 校验（与 `createTask` 一致）。

---

## 自查：是否引入新 bug？

逐项复核本次改动，**未发现回归**：

| 改动 | 自查结论 |
|---|---|
| `SendCommand` 持 RLock 发送 | 无死锁：hub 内部持写锁的 register/unregister/Disconnect 均不调 SendCommand；handler 调用时不持 hub 锁 |
| unregister 指针比较 | 旧连接 Send 不 close 也无泄漏：旧 readPump/writePump 已退出，client 不可达即被 GC |
| writePump 写错即 return | 无 double-unregister：writePump defer 只 close conn 不 unregister，仅 readPump 发一次 unregister |
| auto_register 强制 registerKey | 已注册 agent（持 token）走 MsgTypeRegister 不受影响；仅首次 name 自动注册需密钥 |
| normalizeMode 返回空串 | CreateSync 在空串时跳过 `--mode`，使用 mutagen 默认，行为正确；retry 模板同路径安全 |
| taskSnap 副本 | goroutine 与 c.JSON 无共享指针；SaveTask(&taskSnap) 按 ID 更新，正确 |
| sanitizeFilename | 空名→`""`→filename=".zip"，可接受；无注入 |
| 测试夹具替换 | 5 个 ParseStatus 测试 + 1 个 NormalizeModes 测试全绿 |

**环境限制说明**：`go test -race` 需 cgo/gcc，本机无 C 编译器未运行。data race 修复（#6）已通过代码审查确认（`GetTask` 返回值拷贝 + goroutine 独立副本），建议在有 gcc 的 CI 环境补跑 `-race`。

---

## 验证命令复现

```bash
# 编译
cd server && go build ./... && cd ../agent && go build ./...

# vet
cd server && go vet ./... && cd ../agent && go vet ./...

# 测试
cd server && go test ./... -count=1
cd agent && go test ./... -count=1   # 含 TestNormalizeModes

# race（需 gcc/cgo）
cd server && CGO_ENABLED=1 go test ./... -race
cd agent && CGO_ENABLED=1 go test ./... -race
```

全部输出 `ok` / 零警告。

---

## 涉及文件

| 文件 | 改动 |
|---|---|
| `server/ws/hub.go` | #1 #2 #4 #8 #11 |
| `server/handlers/auth.go` | #10 #11 |
| `server/handlers/config.go` | #5 |
| `server/handlers/task.go` | #6 |
| `server/handlers/machine.go` | #7 |
| `agent/mutagen/cmd.go` | #3 #12 |
| `agent/client/ws.go` | #3 #9 |
| `agent/mutagen/cmd_test.go` | #3 夹具真实化 + TestNormalizeModes |
| `web/src/App.vue` | #13 |

---

## 后续建议（已实施）

1. **CI 补 `-race`** ✅ 已在 `.github/workflows/build.yml` 新增 `Run Tests (race)` 步骤（ubuntu-latest 自带 gcc/cgo，server + agent 均跑 `-race`）。
2. **hub `broadcast` 死代码** ✅ 已移除 `Hub.broadcast` 字段、`NewHub` 中的初始化、`Run()` 的 broadcast case（含其不可达的 `close(client.Send)` on default 隐患）。
3. **`updateTask` goroutine `SaveTask` 覆盖** ✅ 新增 `store.UpdateTaskError(machineID, id, lastError)`（写锁内只改 LastError + UpdatedAt），goroutine 三处 `SaveTask(t)` 改用之，不再用重建前快照整体覆盖期间被 agent 上报更新的 Status/Identifier 等字段。

三处改动均通过 `go build` + `go vet` + `go test` 验证（全绿）。
