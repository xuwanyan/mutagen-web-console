# Mutagen Web Console 升级指南

> 适用场景：从老版本（无备份功能）升级到新版（含备份功能 + 双指纹远端 agent 自动更新）
> 涉及组件：Server（1台）+ 4台 Windows Agent + 远端 Linux Agent（自动）

---

## 一、老配置兼容性说明

**老配置无需任何修改，新 agent 和 server 能自动解析。**

| 配置文件 | 位置 | 兼容性 | 说明 |
|----------|------|--------|------|
| `agent-config.json` | `C:\mutagen\` | ✅ 自动兼容 | 新增 `registerKey`、`backup` 字段均带 `omitempty`，老配置缺失时用默认值（空字符串/nil），不影响运行 |
| `data.json` | server 的 `data/` 目录 | ✅ 自动兼容 | 结构（machines/tasks/configs/nextId）未变，新字段不在 data.json 中 |
| `backup.json` | `~/.mutagen/`（两端各一份） | ❌ 需新建 | 老版无此文件，新版需手动创建或通过 Web UI 配置后生效 |

**结论**：agent-config.json 和 data.json 原样保留，不要删除、不要重命名。只需新增 backup.json。

---

## 二、升级前准备

### 2.1 确认构建产物齐全

在开发机 `c:\vscode\mutagen-web-console\build\` 目录下确认以下 6 个产物存在：

| 文件 | 用途 |
|------|------|
| `mutagen-web-server.exe` | Server（Windows） |
| `mutagen-web-server_linux` | Server（Linux，如需） |
| `mutagen-web-agent.exe` | Agent（Windows） |
| `mutagen.exe` | 含备份功能的 CLI |
| `mutagen-agents.tar.gz` | 远端 Linux agent 包 |
| `web/` | 前端静态资源 |

如缺失，在开发机执行全量构建：
```powershell
Set-Location c:\vscode\mutagen-web-console; .\build.ps1
```

### 2.2 打包 Agent 升级包

在开发机执行：
```powershell
$src = "c:\vscode\mutagen-web-console\build"
$dst = "c:\vscode\mutagen-web-console\agent-upgrade"
New-Item -ItemType Directory -Force $dst
Copy-Item "$src\mutagen.exe" $dst\
Copy-Item "$src\mutagen-agents.tar.gz" $dst\
Copy-Item "$src\mutagen-web-agent.exe" $dst\
```

将 `agent-upgrade` 目录拷贝到 4 台 Windows 机器。

### 2.3 确认 Agent 安装方式

在每台 Windows 机器上确认 agent 的启动方式（二选一）：
```powershell
# 方式A：Windows 服务
sc query MutagenWebAgent

# 方式B：计划任务（登录启动）
schtasks /query /tn "MutagenWebAgent"
```

记下每台机器的启动方式，后续停止/启动步骤需对应。

---

## 三、升级步骤

### 升级顺序（必须按此顺序）

1. **先升级 Server**（新版兼容老版 agent 协议，不会中断）
2. **逐台升级 Windows Agent**（不要 4 台同时停，减少同步中断窗口）
3. **远端 Linux Agent 自动更新**（agent 启动时自动检测指纹变化并清理远端）
4. **配置并启用备份功能**

---

### 第 1 步：升级 Server

> 下列 A/B 两种部署方式二选一，按实际 server 所在平台执行。

#### 方式 A：Windows Server（服务或直接运行）

```powershell
# 1. 停止 server
# 如果是服务：sc stop MutagenWebServer
# 如果是直接运行：taskkill /f /im mutagen-web-server.exe

# 2. 备份旧文件
Copy-Item C:\mutagen-web\mutagen-web-server.exe C:\mutagen-web\mutagen-web-server.exe.bak
Copy-Item C:\mutagen-web\web C:\mutagen-web\web.bak -Recurse -Force

# 3. 替换文件
Copy-Item c:\vscode\mutagen-web-console\build\mutagen-web-server.exe C:\mutagen-web\ -Force
Copy-Item c:\vscode\mutagen-web-console\build\web C:\mutagen-web\ -Recurse -Force

# 4. 启动 server
# 如果是服务：sc start MutagenWebServer
# 否则：Start-Process C:\mutagen-web\mutagen-web-server.exe -ArgumentList "-addr :8080"
```

#### 方式 B：Linux Server（Docker 部署，README 主推方式）

```bash
# 1. 在开发机（或 CI）构建产物已就绪：build/mutagen-web-server_linux + build/web/
# 2. 重新构建镜像（Dockerfile 在 scripts/）
docker build -t mutagen-web -f scripts/Dockerfile build/

# 3. 停止并删除旧容器（数据卷 mutagen-data 保留，data.json 不丢）
docker stop mutagen-web && docker rm mutagen-web

# 4. 启动新容器（挂载同一数据卷，端口/参数与原部署一致）
docker run -d \
  --name mutagen-web \
  -p 18080:8080 \
  -v mutagen-data:/app/data \
  mutagen-web
```

> 若是 systemd 直接运行二进制（非 Docker）：停服务 → 备份并替换 `mutagen-web-server_linux` 与 `web/` 目录 → `systemctl start mutagen-web`。

**验证**：浏览器打开 Web UI，能登录、能看到 4 台机器（此时 agent 还是老版，但应显示在线）。

---

### 第 2 步：逐台升级 Windows Agent（重复 4 次）

**关键：先停 daemon 再替换文件，否则 mutagen.exe 被占用无法覆盖。**

```powershell
# ===== 1. 停止 =====
# 1.1 停 agent（对应 2.3 确认的安装方式）
# 方式A：Windows 服务
sc stop MutagenWebAgent
# 方式B：计划任务
taskkill /f /im mutagen-web-agent.exe

# 1.2 停 mutagen daemon（释放 mutagen.exe 文件锁）
C:\mutagen\mutagen.exe daemon stop

# ===== 2. 备份（保留 agent-config.json，不要删！）=====
Copy-Item C:\mutagen\mutagen.exe C:\mutagen\mutagen.exe.bak
Copy-Item C:\mutagen\mutagen-web-agent.exe C:\mutagen\mutagen-web-agent.exe.bak
# agent-config.json 不要动，token/machineId 不变，重装后自动重连

# ===== 3. 替换文件 =====
Copy-Item <升级包路径>\mutagen.exe C:\mutagen\ -Force
Copy-Item <升级包路径>\mutagen-agents.tar.gz C:\mutagen\ -Force
Copy-Item <升级包路径>\mutagen-web-agent.exe C:\mutagen\ -Force

# ===== 4. 启动 =====
# 4.1 启动 mutagen daemon
C:\mutagen\mutagen.exe daemon start

# 4.2 启动 agent（对应 2.3 确认的安装方式）
# 方式A：Windows 服务
sc start MutagenWebAgent
# 方式B：计划任务（重新登录即触发，或手动启动）
Start-Process C:\mutagen\mutagen-web-agent.exe -ArgumentList "-config C:\mutagen\agent-config.json -log C:\mutagen\agent.log"
```

**验证**：
- Web UI 机器列表该机器恢复"在线"状态
- 查看 `C:\mutagen\agent.log`，应出现：
  ```
  startup ensure remote agents fresh completed
  ```
  或指纹变化的日志：
  ```
  mutagen fingerprint changed (exe: <none>, tar: <none> -> exe: abcd1234, tar: 5678ef90), cleaning remote agents
  cleaned remote agents on <host>
  ```

---

### 第 3 步：远端 Linux Agent 自动更新（无需手动操作）

每台 Windows agent 启动后自动执行：
1. 计算新 `mutagen.exe` + `mutagen-agents.tar.gz` 的双 SHA256 指纹
2. 和存储的指纹对比，发现变化
3. SSH 到所有 `~/.ssh/config` 中的远端主机，删除 `~/.mutagen/agents/` 目录
4. 下次 mutagen 同步连接时，自动推送新 agent 到远端重装

**验证**：在远端 Linux 主机上执行：
```bash
ls -la ~/.mutagen/agents/
# 应看到新的版本目录（如 0.18.1/）
```

---

### 第 4 步：配置并启用备份功能

#### 4.1 配置本机 Windows 备份

在 Web UI → 全局配置 → 备份配置区域：
- 设置 enabled=true、retentionDays=7、failOpen=true
- dir 留空（默认在同步根兄弟位置创建 `<root>.mutagen-backup`）或填指定路径
- 点击"**保存本机备份配置**"
- 点击"**一键校验本机 backup.json**"确认写入成功

或手动创建 `C:\Users\<用户>\.mutagen\backup.json`：
```json
{
  "enabled": true,
  "dir": "",
  "retentionDays": 7,
  "failOpen": true
}
```

#### 4.2 配置远端 Linux 备份

在 Web UI → 全局配置 → 远端 Linux 备份配置区域：
- 设置相同的备份参数
- 点击"**保存远端Linux备份配置**"（SSH 推送到所有远端主机）
- 点击"**一键校验远端 backup.json**"确认推送成功

#### 4.3 Pause + Resume 所有同步任务（关键！）

> backup.json 在 endpoint 初始化时读取一次（`NewEndpoint` 函数内），运行中的会话不会自动重读。
> **必须 pause + resume 才能让两端 endpoint 重新加载 backup.json，备份配置才生效。**

在 Web UI 操作：
1. 点击"**暂停全部**"
2. 等待所有任务状态变为"已暂停"
3. 点击"**恢复全部**"

或在 agent 机器上用 CLI 逐个操作：
```powershell
C:\mutagen\mutagen.exe sync pause <task-name>
C:\mutagen\mutagen.exe sync resume <task-name>
```

#### 4.4 验证备份生效

1. 在同步目录里**覆盖一个已存在的文件**（新建文件是 create，无旧内容可备份）：
   ```powershell
   echo "old" > D:\sync-dir\test.txt   # 先确保 test.txt 已存在
   echo "new" > D:\sync-dir\test.txt   # 覆盖 → 触发 modify 备份
   ```

2. 等待同步完成（10秒内 agent 上报周期）

3. 检查备份目录是否出现被覆盖的旧文件：
   ```powershell
   # dir 留空时，默认备份目录在同步根的兄弟位置
   # 例如 D:\sync-dir → D:\sync-dir.mutagen-backup\<日期>\<操作>\<时间戳>\test.txt
   dir D:\sync-dir.mutagen-backup\ -Recurse
   ```

4. 在远端 Linux 上同理验证（默认备份目录是同步根的**兄弟目录**，不在 home 下）：
   ```bash
   # <同步根父目录>/<同步根名>.mutagen-backup，例如同步根 /srv/ftp/InBox → /srv/ftp/InBox.mutagen-backup
   ssh <host> "find /srv/ftp/InBox.mutagen-backup -type f 2>/dev/null"
   # 或直接用 Web UI 的「一键校验远端 backup.json」
   ```

备份目录结构：
```
<备份根目录>/
└── <同步根名>.mutagen-backup/      ← dir 留空时自动生成
    └── 2026-08-19/                 ← 按日期
        └── modify/                 ← 操作类型：create / modify / delete
            └── 143022.000-1/       ← 事件戳（HHMMSS.毫秒-序号，同毫秒事件不撞）
                └── <相对路径>/     ← 原文件路径
                    └── test.txt    ← 被覆盖前的旧内容
```

> `create`（新建文件）只在同目录的 `manifest.log` 记一条，无旧内容可备份，不产生文件副本；只有 `modify`/`delete` 才落盘旧文件。同一文件多次修改按事件目录并排保留，最早的事件即最初原始版本。

---

## 四、升级后验证清单

| 检查项 | 方法 | 预期结果 |
|--------|------|----------|
| Server 在线 | Web UI 能登录 | ✅ 正常 |
| 4台 Agent 在线 | Web UI 机器列表 | ✅ 全部在线 |
| 任务状态正常 | `mutagen sync list -l`（任一 agent 机器） | ✅ 所有会话可见 |
| 远端 agent 更新 | agent 日志 `C:\mutagen\agent.log` | ✅ 出现 fingerprint changed + cleaned remote agents |
| backup.json 本机 | Web UI "一键校验本机" | ✅ 校验通过 |
| backup.json 远端 | Web UI "一键校验远端" | ✅ 校验通过 |
| 备份功能生效 | 修改文件触发同步后检查备份目录 | ✅ 出现被覆盖的旧文件 |

---

## 五、回滚方案（如升级失败）

### 5.1 Agent 回滚

```powershell
# 1. 停 agent + 停 daemon
sc stop MutagenWebAgent  # 或 taskkill /f /im mutagen-web-agent.exe
C:\mutagen\mutagen.exe daemon stop

# 2. 恢复备份文件
Copy-Item C:\mutagen\mutagen.exe.bak C:\mutagen\mutagen.exe -Force
Copy-Item C:\mutagen\mutagen-web-agent.exe.bak C:\mutagen\mutagen-web-agent.exe -Force

# 3. 启动 daemon + agent
C:\mutagen\mutagen.exe daemon start
sc start MutagenWebAgent  # 或 Start-Process ...
```

### 5.2 Server 回滚

#### Windows Server

```powershell
# 1. 停 server
sc stop MutagenWebServer  # 或 taskkill /f /im mutagen-web-server.exe

# 2. 恢复备份
Copy-Item C:\mutagen-web\mutagen-web-server.exe.bak C:\mutagen-web\mutagen-web-server.exe -Force
Copy-Item C:\mutagen-web\web.bak C:\mutagen-web\web -Recurse -Force

# 3. 启动 server
sc start MutagenWebServer  # 或 Start-Process ...
```

#### Linux Server（Docker）

```bash
# 重新用旧镜像启动（若旧镜像已删除，用 .bak 的二进制/web 重新 docker build 一个旧版镜像）
docker stop mutagen-web && docker rm mutagen-web
docker run -d --name mutagen-web -p 18080:8080 -v mutagen-data:/app/data <旧镜像名或重新构建>
```

---

## 六、注意事项

1. **agent-config.json 不要删**——里面有 token 和 machineId，删了会重新注册新机器，老机器记录变"离线"
2. **data.json 保留**——server 的 `data/data.json` 不动，任务记录不丢
3. **逐台升级**——不要 4 台 agent 同时停，一台一台来，减少同步中断窗口
4. **升级期间同步会中断**——agent 停止期间文件不会同步，重启后自动恢复
5. **pause + resume 期间同步会短暂中断**（几秒），但不会丢数据
6. **远端也要配 backup.json**——只配本机的话，远端被覆盖的文件不会备份
7. **retentionDays 到期后旧备份自动清理**，不用手动管
8. **failOpen=true**（默认）：备份失败时不阻塞同步（只记日志）；设为 false 则备份失败时跳过该文件覆盖
