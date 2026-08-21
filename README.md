# Mutagen Web Console

远程文件同步管理面板。基于 [Mutagen](https://mutagen.io/) 的 Web 管理界面，支持多机管理、SSH 主机配置、文件同步任务创建与监控。

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
web-console/
├── server/          # Server 端 Go 代码
├── agent/           # Agent 端 Go 代码
├── agent-setup/     # Windows 安装器（弃用，改用 install.bat）
├── web/             # Vue 3 前端
├── scripts/         # 部署脚本
│   ├── Dockerfile
│   ├── docker-compose.yml
│   ├── install.bat
│   └── manage.bat
├── build.ps1        # Windows 构建脚本（交叉编译）
├── build.sh         # Linux 构建脚本
└── build/           # 构建产物（gitignore）
```

---

## 服务端部署（Linux）

### 方式一：Docker 部署（推荐，Linux 上构建）

```bash
# 1. 克隆代码
git clone https://github.com/xuwanyan/mutagen-web-console.git
cd mutagen-web-console

# 2. 构建
chmod +x build.sh
./build.sh

# 3. 构建 Docker 镜像
docker build -t mutagen-web -f scripts/Dockerfile build/

# 4. 运行
docker run -d \
  --name mutagen-web \
  -p 18080:18080 \
  -v mutagen-data:/app/data \
  mutagen-web
```

### 方式二：Docker 部署（Windows 交叉编译）

```bash
# 1. 在 Windows 上构建
.\build.ps1

# 2. 把 build/ 目录传到 Linux 服务器

# 3. 构建镜像
docker build -t mutagen-web -f scripts/Dockerfile build/

# 4. 运行
docker run -d -p 18080:18080 -v mutagen-data:/app/data --name mutagen-web mutagen-web
```

### 方式三：直接运行

```bash
# 需要 Go 1.22+ 和 Node.js 18+
cd server
go build -o ../build/mutagen-web-server_linux .

# 前端
cd ../web
npm install && npm run build
cp -r dist/* ../build/web/

# 运行
cd ../build
chmod +x mutagen-web-server_linux
./mutagen-web-server_linux -addr :18080
```

### 登录认证

创建 `auth.json`（密码用 bcrypt 哈希）：

```json
{"admin": "$2a$10$..."}
```

生成哈希：

```bash
./mutagen-web-server_linux -print-hash admin123
```

### 参数说明

| 参数 | 默认值 | 说明 |
|---|---|---|
| `-addr` | `:8080` | 监听地址 |
| `-db` | `~/.mutagen-web/data.json` | 数据库文件路径 |
| `-auth` | — | 登录凭证（username:password） |
| `-log` | stdout | 日志文件路径 |
| `-print-hash` | — | 生成 bcrypt 密码哈希后退出 |

---

## 客户端部署（Windows）

### 前置条件

1. Windows 10/11 目标机
2. 管理员权限
3. 能访问 Server 的 WebSocket 端口（默认 18080）

### 安装步骤

#### 方式一：从网页下载安装包（推荐）

```
① 打开 http://你的ServerIP:18080
② 登录
③ 机器管理 → 添加机器 → 输入名称
④ 点「下载安装包」
⑤ 把 ZIP 传到目标机
⑥ 解压到任意目录
⑦ 右键 install.bat → 以管理员身份运行
⑧ 自动完成：复制文件 → 注册开机自启任务
```

#### 方式二：手动注册服务

```cmd
# 复制文件到 C:\mutagen
# 以管理员身份运行：

sc create MutagenWebAgent binPath= "C:\mutagen\mutagen-web-agent.exe --config C:\mutagen\agent-config.json -log C:\mutagen\agent.log" start= auto obj= ".\%USERNAME%" password= "你的密码"

# 启动服务
sc start MutagenWebAgent
```

### 安装包内容

```
agent-pack-xxx.zip
├── mutagen.exe               - Mutagen 同步引擎
├── mutagen-agents.tar.gz     - SSH agent 包
├── mutagen-web-agent.exe     - Agent 二进制
├── agent-config.json         - Agent 配置（server/token）
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

---

## 构建

### Linux 上构建

```bash
chmod +x build.sh
./build.sh
```

### Windows 上构建

```powershell
.\build.ps1
```

### 单独构建

```bash
# Server（Linux）
cd server && GOOS=linux GOARCH=amd64 go build -o ../build/mutagen-web-server_linux .

# Agent（Windows）
cd agent && GOOS=windows GOARCH=amd64 go build -o ../build/mutagen-web-agent.exe .

# 前端
cd web && npm run build && cp -r dist/* ../build/web/
```

### 构建产物

```
build/
  ├── mutagen-web-server_linux   - Linux server（部署到服务器）
  ├── mutagen-web-agent.exe      - Windows agent
  └── web/                       - 前端静态文件
```

---

## 配置文件清单

### 客户端本地（Windows Agent 端）

| 文件路径 | 来源 | 用途 |
|---|---|---|
| `<exe同目录>/agent-config.json` | 手动编辑或自动注册后 agent 写回 | Agent 主配置：`server`/`token`/`machineId`/`name`/`backup` 段 |
| `~/.mutagen.yml` | Server 通过 `update_global_config` 命令推送 | Mutagen 全局配置（Mutagen 标准文件） |
| `~/.ssh/config` | Server 通过 `update_ssh_config` 命令推送 | SSH 客户端配置，供 mutagen 连接远端 beta endpoint（OpenSSH 标准文件，Windows 下换行会被自动转成 CRLF） |
| `~/.mutagen/backup.json` | Agent 启动时从 `agent-config.json` 的 `backup` 段自动写入，或 Server 通过 `update_backup_config` 命令推送 | 定制版 mutagen 的落盘前备份配置 |

> `agent-config.json` 是 Agent 自定义文件，其他三个分别是 Mutagen / OpenSSH / 定制版 mutagen 的标准读取文件，路径不可更改。

### 远端（Linux Beta 端）

| 文件路径 | 来源 | 用途 |
|---|---|---|
| `~/.mutagen/backup.json` | Agent 通过 SSH（`PushRemoteBackupConfig`）推送 | 与客户端本地相同的备份配置，供远端 mutagen daemon 执行落盘前备份 |

> 远端作为 SSH 被动方，仅需 `~/.mutagen/backup.json` 一个配置文件；不需要 `agent-config.json`、`~/.mutagen.yml`、`~/.ssh/config`。

---

## 落盘前自动备份（pre-transition backup）

定制版 mutagen 支持在把变更**写入磁盘之前**，先把即将被覆盖/删除的旧文件备份到同机另一目录，按日期 → 「增/删/改」 → **每次同步事件** 归档（`<备份根>/<日期>/<op>/<事件号>/<相对路径>`）。备份**在被修改端本地发生**（Windows 端被改就备 Windows、远端被改就备远端），备份完成后同步正常继续。同一文件的历史版本集中在 `modify/` 下按事件目录排列；因每次 modify 备的是“改前”内容，**最早的事件目录即最初原始版本**，`delete/` 下为删除前的最后内容，各版本互不覆盖；同机 `<备份根>/<日期>/manifest.log` 逐条记录「时间·事件号·操作·类型(dir/file)·绝对路径·字节数·会话ID」。

> 配置按机器本地读取（endpoint 配置不随网络传输）：环境变量优先，其次 `~/.mutagen/backup.json`。默认关闭（opt-in）。

### backup.json 字段

```json
{ "enabled": true, "dir": "", "retentionDays": 7, "failOpen": true }
```

| 字段 | 默认 | 说明 |
|---|---|---|
| `enabled` | `false` | 是否启用落盘前备份 |
| `dir` | 空 | 备份目录；为空时默认取同步根兄弟目录 `<父目录>/<根名>.mutagen-backup`（必须在同步根之外）。**显式指定时**，因全机任务共用此目录，会自动在其下按同步根路径分子目录（如 `D:\backups\D\FTP\Impath\Acmp\InBox`），避免多任务同名文件撞车 |
| `retentionDays` | `7` | 保留天数，超过则自动清理（≤0 不清理） |
| `failOpen` | `true` | 备份失败时：`true`=记录错误并继续落盘；`false`=跳过该文件以保护旧内容 |

也可用环境变量覆盖：`MUTAGEN_BACKUP_ENABLED` / `MUTAGEN_BACKUP_DIR` / `MUTAGEN_BACKUP_RETENTION_DAYS` / `MUTAGEN_BACKUP_FAIL_OPEN`。

### Windows 端（agent）

在 `agent-config.json` 新增 `backup` 段，agent 启动时会自动写入本机 `~/.mutagen/backup.json`：

```json
{
  "server": "ws://...",
  "token": "...",
  "machineId": "...",
  "backup": { "enabled": true, "retentionDays": 7 }
}
```

### 远端 (beta) Linux

远端 daemon 由 SSH 拉起、无法注入环境变量，采用**一次性部署** `~/.mutagen/backup.json`（默认目录=兄弟目录，故只需开启）：

```bash
mkdir -p ~/.mutagen
cat > ~/.mutagen/backup.json <<'EOF'
{ "enabled": true, "retentionDays": 7 }
EOF
```

> 备份目录若落在同步根内部会被自动拒绝启用（避免备份被再次同步）。备份引擎的源码改动详见 `mutagen/CUSTOM-PATCHES.md`。

---

## 技术栈

| 层 | 技术 |
|---|---|
| 后端 | Go + Gin + gorilla/websocket |
| 前端 | Vue 3 + Vite |
| 存储 | JSON 文件 |
| 同步引擎 | Mutagen 0.18.1 |
| 部署 | Docker / systemd |