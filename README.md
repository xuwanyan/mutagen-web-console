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

## 技术栈

| 层 | 技术 |
|---|---|
| 后端 | Go + Gin + gorilla/websocket |
| 前端 | Vue 3 + Vite |
| 存储 | JSON 文件 |
| 同步引擎 | Mutagen 0.18.1 |
| 部署 | Docker / systemd |