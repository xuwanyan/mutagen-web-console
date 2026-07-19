# 交接说明（mutagen-web 控制台重构）

> 给接手的 AI：先读完本文件，再读计划 `C:\Users\xuyan\AppData\Roaming\Qoder\SharedClientCache\cache\plans\任务与全局配置重构_0e6b55c0.md`。本文件是权威现状，计划文件是原始需求。

## 0. ⚠️ 最重要的工具陷阱（务必先看，能省大量 token 和翻车）
本代码库里，**Read / Grep 工具会返回损坏或捏造的内容**（全角空格、行号跳变、重复行、吞掉 Go 的 gorm tag、甚至伪造不存在的函数签名）。因此：
- **读真实内容**：用 PowerShell `Get-Content -LiteralPath ... -Encoding UTF8` 或 `Select-String -LiteralPath ... -Pattern ...`（直接读磁盘字节）。
- **验证编辑真的落盘**：改完用 `Select-String -Pattern` 复查磁盘上确实有改动（SearchReplace 可能"假成功"——报成功但没写进去，尤其当 original_text 来自损坏内容时）。
- **验证编译**：`go build ./...`，`EXIT=0` 才算数。
- 环境：Windows PowerShell，**不支持 `&&`，用 `;` 分隔**。

## 1. 项目架构
- 路径根：`c:\vscode\mutagen-0.18.1\web-console`
- 三层：**Server**（管理面，Go/Gin，`server/`）→ **Agent**（管控代理，Go，WebSocket 反向连 server，`agent/`）→ **mutagen daemon**（独立进程执行同步）。
- 前端：Vue 3 + Vite，`web/`，构建产物 `web/dist`，被 server 以 `../web/dist` 静态托管。
- **存储是纯 JSON 文件**（`server/store/store.go` 用 `json.MarshalIndent`+`os.WriteFile`），用**切片**存 machines/tasks/configs，**不是 gorm/sqlite**（model 上的 gorm tag 是遗留装饰、不生效）。
- MachineConfig 按 `machineID + type` 存，type 有：`global` / `ssh` / `ssh_hosts`。

## 2. 本次已完成（全部已落盘 + go build EXIT=0 + 本机冒烟通过）
| 文件 | 改动 |
|---|---|
| `server/models/models.go` | SyncTask 加 `IgnorePaths []string` |
| `server/store/store.go` | `DeleteTasksByMachine` / `DeleteConfigsByMachine`（切片过滤级联删） |
| `server/ws/hub.go` | sync_status 分支：`FindTaskByName==nil` 时 `CreateTask` 自动接管补建 |
| `server/handlers/task.go` | CreateTaskRequest 加 ignorePaths；下发 Params 加 ignorePaths；新增 `POST /api/machines/:id/refresh-status` + `refreshStatus` handler |
| `server/handlers/config.go` | 新增 `GET/PUT /api/machines/:id/config/ssh-hosts`、`SSHHost` 结构、`buildSSHConfigText` |
| `server/handlers/machine.go` | `deleteMachine` 加 hub 参数、`terminate` 模式、级联清理 |
| `agent/mutagen/cmd.go` | `CreateSync` 加 `ignorePaths []string`，每项拼 `--ignore=` |
| `agent/client/ws.go` | create_sync 分支解析 ignorePaths + 新增 `report_status` case + `getStringSlice` helper |
| `web/src/api/client.js` | delete 加 terminate 参数、refreshStatus、getSSHHosts/updateSSHHosts |
| `web/src/App.vue` | 六块 UI 改造（见计划一~六） |

产物：`server/server.exe`、`agent/agent.exe` 均已编译。

## 3. 与计划有出入的 3 处（有意为之，非遗漏；接手者可按需补齐）
1. **删除机器时未主动断开 agent 连接**：`Hub` 无 `Disconnect` 方法。效果不受影响——删除后旧 token 失效，agent 重连会被 `invalid token` 拒。若要主动踢线：给 `server/ws/hub.go` 的 `Hub` 加 `Disconnect(machineID)`（关 conn + 从 clients map 删），在 `machine.go` deleteMachine 里调。
2. **token「复制」按钮未做**：只实现了显示/隐藏（`toggleReveal`）。
3. **自动接管补建的任务 alpha/beta 留空**：agent 的 `SyncStatusPayload.TaskStatus` 只有 name/status/error，无路径字段，无法解析。若要填：需扩展 agent 上报 payload 带上 alpha/beta（改 `agent/client/message.go` 的 TaskStatus + `reportStatus()` 解析 `mutagen sync list`）。

微小差异：刷新延迟用 1200ms（计划写 ~800ms）；全局 YAML 里 permissions 段在 safety 段前（YAML 无顺序要求，不影响）。

## 4. 构建 & 本机冒烟命令
```powershell
# 前端
cd c:\vscode\mutagen-0.18.1\web-console\web ; npm run build
# 两个 exe
cd c:\vscode\mutagen-0.18.1\web-console\server ; go build -o server.exe .
cd c:\vscode\mutagen-0.18.1\web-console\agent  ; go build -o agent.exe .
# 冒烟（临时库，跑完删掉 smoke_test.json）
cd c:\vscode\mutagen-0.18.1\web-console\server
Start-Process .\server.exe -ArgumentList '-addr',':8099','-db','.\smoke_test.json' -WindowStyle Hidden
# 然后 Invoke-WebRequest 打 http://127.0.0.1:8099/api/... 各接口
```
冒烟已验证：health、建/列/删机器、ssh-hosts 存取+生成 ssh config 文本、global 存取往返、建任务 ignorePaths/beta 持久化、refresh-status 路由、删除 terminate=true 级联清空 tasks/configs。
> 无 agent 连接时，"下发命令"类接口返回 `503 machine offline`（且 `saved:true`）是**预期行为**，数据已持久化。

## 5. 尚未做、需真机验证的（下一步）
把 `agent.exe` 部署到真实目标机（Windows，连上 server 的 `/ws/agent`），实测：
1. 全局开关+权限保存 → 查客户端 `~/.mutagen.yml`。
2. 加 SSH 主机保存 → 查客户端 `~/.ssh/config`。
3. 任务下拉选主机 + 多条 ignore 创建 → `mutagen sync list` 核对 ignore/权限/beta（对照用户原始命令）。
4. 点「刷新状态」即时更新。
5. 删除模式1 → 任务记录清空但客户端仍同步，agent 重连后任务自动重现（接管）。
6. 删除模式2 → 客户端同步被 terminate 终止。

用户的原始基准命令：
```
mutagen sync create --name=test-ali --mode=two-way-resolved --ignore-vcs --symlink-mode=ignore --ignore="*.crdownload" --ignore="*.part" --ignore="*.tmp" --ignore="*.log.bak" --default-file-mode=0666 --default-directory-mode=0777 C:\ftp_test test-ali:/hmgdata/ftp_test
```

## 6. 关键真实事实（防止接手者被损坏工具误导）
- store 用切片非 map；方法签名：`DeleteTask(machineID, id)`、`GetTask(machineID, id)`、`ListTasks(machineID)`、`GetConfig(machineID, configType)`、`SaveConfig(cfg)`、`FindTaskByName(machineID, name)`。
- task.go 路由用 `r.Group()` 闭包，`ws.MachineIDParam(c)`、`ws.GenerateToken()`。
- CreateTaskRequest 字段 json 名：`name/alpha/beta(required)/mode/ignoreVcs/symlinkMode/ignorePaths`。
- config.go 有 `getOrCreateConfig(machineID, configType)` 辅助函数。
- server 入口 `server/main.go`：`-addr`（默认 `:8080`）、`-db`（默认空=用户目录）。
