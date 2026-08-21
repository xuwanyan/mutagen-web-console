# Mutagen Web Console

远程文件同步管理面板。基于 [Mutagen](https://mutagen.io/) 的 Web 管理界面，支持多机管理、SSH 主机配置、文件同步任务创建与监控、落盘前自动备份。

## 架构

```
浏览器 ──→ Server（管理面）── WebSocket ──→ Agent ──调用──→ Mutagen Daemon
                                                              │
                                                   本地目录 ↔ 远程服务器目录
                                                      （文件同步持续运行）
```

| 组件 | 说明 | 部署平台 |
|---|---|---|
| **Server** | Go/Gin，管理面，提供 Web UI + API + WebSocket | **Linux**（Docker） |
| **Agent** | 部署在目标机器上，通过 WebSocket 反向连接 Server | **Windows** |
| **Mutagen** | 文件同步引擎，实际执行同步任务 | Windows |

## 目录结构

```
mutagen-web-console/
├── server/                 # Server 端 Go 代码
├── agent/                  # Agent 端 Go 代码
├── web/                    # Vue 3 前端
├── mutagen/                # Mutagen 源码（含定制补丁）
├── scripts/                # 部署与构建脚本
│   ├── Dockerfile
│   ├── docker-build.sh     # Linux 完整构建 + Docker 镜像
│   ├── docker-compose.yml
│   ├── install.bat         # Windows agent 安装脚本
│   └── README.txt
├── build.ps1               # Windows 构建脚本（增量交叉编译）
└── build/                  # 构建产物（gitignore）
```

---

## 构建

### 前置环境

| 工具 | 版本 | 用途 | Windows | Linux |
|---|---|---|---|---|
| Go | 1.22+ | 编译 server / agent / mutagen | ✓ | ✓ |
| Node.js | 18+ | 构建前端 | ✓ | 不需要（用 Docker 容器构建） |
| npm | 9+ | 前端依赖管理 | ✓ | 不需要 |
| Docker | 20+ | 构建 Linux 镜像 | — | ✓ |

> Linux 上不需要装 Node.js，`docker-build.sh` 会自动用 `node:20` 容器构建前端。

### 方式一：Windows 上构建（推荐，开发机用）

```powershell
cd C:\vscode\mutagen-web-console
.\build.ps1
```

增量构建 6 个产物到 `build/`：
1. `mutagen-web-server.exe` — Windows server
2. `mutagen-web-server_linux` — Linux server (amd64)
3. `mutagen-web-agent.exe` — Windows agent
4. `mutagen.exe` — Windows mutagen CLI (-tags mutagencli)
5. `mutagen-agents.tar.gz` — Mutagen 跨平台 agent 包
6. `web/` — 前端静态文件

构建后会自动复制 `scripts/` 到 `build/scripts/`，并运行 agent + server 测试。

### 方式二：Linux 上完整构建 + Docker 镜像

```bash
git clone -b v1.0.2 https://github.com/xuwanyan/mutagen-web-console.git
cd mutagen-web-console

# 一键构建所有产物 + 运行测试 + 构建 Docker 镜像
bash scripts/docker-build.sh
```

`docker-build.sh` 会在 Linux 上：
1. 检查环境（go / docker，**不需要 Node**）
2. 交叉编译所有产物（Linux server 原生编译，Windows agent/mutagen 交叉编译）
3. 构建 mutagen-agents.tar.gz
4. 构建前端：**通过 Docker 容器**（`node:20` 镜像）跑 `npm install && npm run build`，兼容宿主机 glibc 过老的情况
5. 复制 scripts/
6. 运行 agent + server 测试
7. 构建 Docker 镜像 `mutagen-web`

增量构建：只重新编译有变更的产物，未变更的跳过。

### 方式三：Windows 构建后传到 Linux

```powershell
# 1. Windows 上构建
.\build.ps1

# 2. 打包 build/ 目录
tar -czf build.tar.gz -C build .

# 3. 传到 Linux
scp build.tar.gz root@<linux-ip>:/tmp/
```

```bash
# 4. Linux 上解压并构建镜像
mkdir -p /opt/mutagen-web
cd /opt/mutagen-web
tar -xzf /tmp/build.tar.gz
docker build -t mutagen-web -f scripts/Dockerfile .
```

---

## 服务端部署（Linux）

### Docker 部署

```bash
# 构建镜像（见上方构建说明）
docker build -t mutagen-web -f scripts/Dockerfile build/

# 运行容器
docker run -d \
  --name mutagen-web \
  --restart unless-stopped \
  -p 8080:8080 \
  -v /opt/mutagen-web/data:/app/data \
  mutagen-web
```

- Web UI：`http://<linux-ip>:8080`
- 数据持久化：`/app/data` 挂载到宿主机 `/opt/mutagen-web/data`

### 登录认证

启动时通过 `-auth` 参数指定登录凭证（明文），或用 `-print-hash` 生成 bcrypt 哈希：

```bash
# 生成哈希
docker exec mutagen-web ./server -print-hash yourpassword

# 用哈希启动
docker run -d ... mutagen-web -auth 'admin:$2a$10$...'
```

### 参数说明

| 参数 | 默认值 | 说明 |
|---|---|---|
| `-addr` | `:8080` | 监听地址 |
| `-db` | `/app/data/data.json` | 数据库文件路径 |
| `-agent-bin` | `/app/tools/mutagen-web-agent.exe` | Agent 安装包路径（供 Web 下载） |
| `-auth` | — | 登录凭证（`username:password` 或 `username:bcrypt-hash`） |
| `-register-key` | — | Agent 注册密钥（空=开放注册） |
| `-log` | stdout | 日志文件路径 |
| `-print-hash` | — | 生成 bcrypt 密码哈希后退出 |

---

## 客户端部署（Windows）

### 前置条件

1. Windows 10/11 目标机
2. 管理员权限
3. 能访问 Server 的 8080 端口

### 从网页下载安装包（推荐）

```
① 打开 http://<server-ip>:8080
② 登录
③ 机器管理 → 添加机器 → 输入名称
④ 点「下载安装包」
⑤ 把 ZIP 传到目标机
⑥ 解压到 C:\mutagen
⑦ 右键 install.bat → 以管理员身份运行
⑧ 自动完成：复制文件 → 注册开机自启服务 → 启动 agent
```

### 安装包内容

```
agent-pack-xxx.zip
├── mutagen.exe               - Mutagen 同步引擎
├── mutagen-agents.tar.gz     - SSH agent 包
├── mutagen-web-agent.exe     - Agent 二进制
├── agent-config.json         - Agent 配置（server/token/machineId）
├── install.bat               - 安装脚本
└── README.txt                - 安装说明
```

### 管理命令

```cmd
启动:  sc start MutagenWebAgent
停止:  sc stop MutagenWebAgent
状态:  sc query MutagenWebAgent
日志:  type C:\mutagen\agent.log
```

### 升级 Agent

1. 停止 agent 服务：`sc stop MutagenWebAgent`
2. 停止 mutagen daemon：`C:\mutagen\mutagen.exe daemon stop`
3. 替换 `mutagen-web-agent.exe`（保留 `agent-config.json`）
4. 启动 daemon：`C:\mutagen\mutagen.exe daemon start`
5. 启动 agent：`sc start MutagenWebAgent`

> Agent 升级需逐台进行以减少同步中断。Server 应先于 agent 升级。

---

## 配置文件清单

### 客户端本地（Windows Agent 端）

| 文件路径 | 来源 | 用途 |
|---|---|---|
| `C:\mutagen\agent-config.json` | 自动注册后 agent 写回 / 手动编辑 | Agent 主配置：`server`/`token`/`machineId`/`name`/`backup` 段 |
| `~/.mutagen.yml` | Server 通过 `update_global_config` 推送 | Mutagen 全局配置（mode、symlink-mode、ignore 等） |
| `~/.ssh/config` | Agent 启动时自动上报 / Server 推送 | SSH 客户端配置，供 mutagen 连接远端 |
| `~/.mutagen/backup.json` | Server 通过 `update_backup_config` 推送 | 本机落盘前备份配置 |

> `agent-config.json` 是 Agent 自定义文件，必须保留（含 token/machineId）。其他三个分别是 Mutagen / OpenSSH / 定制版 mutagen 的标准读取文件。

### 远端（Linux Beta 端）

| 文件路径 | 来源 | 用途 |
|---|---|---|
| `~/.mutagen/backup.json` | Agent 通过 SSH（`PushRemoteBackupConfig`）推送 | 远端落盘前备份配置 |

> 远端仅需 `~/.mutagen/backup.json` 一个配置文件；不需要 agent-config.json、.mutagen.yml、ssh config。

---

## 落盘前自动备份（pre-transition backup）

定制版 mutagen 支持在把变更**写入磁盘之前**，先把即将被覆盖/删除的旧文件备份到同机另一目录。

### 备份目录结构

```
<备份根目录>/                          ← backup.json 的 dir 字段
└── <同步根路径扁平化>_<FNV hash>/     ← rootBackupSubdir（按同步根路径区分任务）
    ├── 2026-08-21/                   ← 日期目录
    │   ├── modify/                   ← 操作类型
    │   │   └── <事件时间戳>/         ← 单次 sync 事件
    │   │       └── <相对路径文件>    ← 被覆盖前的旧版本
    │   └── delete/
    │       └── <事件时间戳>/
    │           └── <相对路径文件>    ← 删除前的内容
    └── manifest.log                  ← 清单：时间·事件号·操作·类型·路径·字节数·会话ID
```

### backup.json 字段

```json
{ "enabled": true, "dir": "/hmgdata/sync_backup", "retentionDays": 7, "failOpen": true }
```

| 字段 | 默认 | 说明 |
|---|---|---|
| `enabled` | `false` | 是否启用落盘前备份 |
| `dir` | 空 | 备份根目录；为空时默认取同步根兄弟目录 `<父目录>/<根名>.mutagen-backup`。显式指定时自动按同步根路径分子目录 |
| `retentionDays` | `7` | 保留天数，超过则自动清理（≤0 不清理）。每日每目录最多清理一次 |
| `failOpen` | `true` | 备份失败时：`true`=记录错误并继续落盘；`false`=跳过该文件以保护旧内容 |

### 保留策略（retentionDays=7 的行为）

- 每次备份后触发清理（每日每目录最多一次）
- cutoff = 今天 00:00 减去 7 天
- 日期目录名早于 cutoff 的整个目录被删除（含所有 modify/delete 事件）
- 保留范围：今天 + 过去 7 天（共 8 天）

### 远端 backup.json 配置

通过 Web UI「远端 Linux 备份配置」页面保存时，Agent 通过 SSH 推送到远端 `~/.mutagen/backup.json`：

- **首次推送**：远端无文件 → 正常写入
- **远端已存在**（其他 Windows 客户端已推过）：不覆盖，回传远端现有配置并**锁定本页字段**，弹窗提示「已将现有配置回传」
- **解锁方式**：在远端执行 `rm ~/.mutagen/backup.json` 后重新保存即可

> 多台 Windows 同步到同一 Linux 时共用同一份远端备份配置。备份子目录按同步根路径自动区分（`rootBackupSubdir`），数据不会冲突。

---

## SSH 配置自动上报

Agent 连接 Server 成功后，自动读取本机 `~/.ssh/config` 并上报给 Server，无需手动点击「从本机导入」。

- Agent 端：WebSocket 连接建立后通过 goroutine 读取并上报
- Server 端：通过 `SSHConfigHandler` 回调接收，复用 `SaveSSHParsedConfig` 解析入库

---

## 技术栈

| 层 | 技术 |
|---|---|
| 后端 | Go + Gin + gorilla/websocket |
| 前端 | Vue 3 + Vite |
| 存储 | JSON 文件 |
| 同步引擎 | Mutagen 0.18.1（含定制备份补丁） |
| 部署 | Docker |
