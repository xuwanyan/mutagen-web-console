# Mutagen 自定义补丁说明

本文件记录本仓库在 **Mutagen 官方 v0.18.1** 基础上所做的自定义改动，方便未来升级官方版本时重新套用。

> ⚠️ 升级提醒：直接用官方新版本源码覆盖会**丢失以下所有改动**。升级后请按本文档逐处重新套用，并执行末尾的验证步骤。

---

## 功能概述：`disableEmptyRootCheck`

给同步会话新增一个安全开关，用于**禁用「某一端根目录被清空时自动暂停」的安全检查**。

- 全局 YAML 配置写法：
  ```yaml
  sync:
    defaults:
      safety:
        disableEmptyRootCheck: true
  ```
- 命令行写法：
  ```
  mutagen sync create --disable-empty-root-safety-check ...
  ```

### 设计要点（重要）

该选项**没有修改 `.proto` 文件、也没有重新生成 protobuf 代码**。而是：

1. 直接在**生成的** `configuration.pb.go` 里手动加了一个字段，并打上 `protobuf:"-"` 标签（表示不参与 protobuf 序列化）。
2. 由于不走 protobuf wire format，跨 gRPC / daemon 传输时会丢失，因此改用 **session labels**（标签 `mutagen.io/disable-empty-root-safety-check`）来传输该布尔值。

这样做的好处是**升级时不需要重新跑 protoc**，坏处是分散在多个文件里，升级时容易漏。

---

## 改动清单（共 6 个文件）

### ① `pkg/synchronization/configuration.proto` —— **未修改**（仅作说明）

官方 proto **保持原样**，字段号最大到 81（compressionAlgorithm）。自定义字段**不在**此文件里。升级时无需改动此文件，但要知道 pb.go 里的自定义字段与它是「脱钩」的。

---

### ② `pkg/synchronization/configuration.pb.go` —— 手动注入字段

在 `Configuration` 结构体末尾（`CompressionAlgorithm` 字段之后、结构体闭合 `}` 之前）加入：

```go
	// DisableEmptyRootSafetyCheck disables the safety check that halts
	// synchronization when one endpoint's root directory is emptied. This is
	// a custom extension (not part of upstream Mutagen). Note: this field is
	// not serialized by protobuf; it is transported via session labels.
	DisableEmptyRootSafetyCheck bool `protobuf:"-" json:"disableEmptyRootSafetyCheck,omitempty"`
```

> 关键：`protobuf:"-"` 标签必须保留，否则会与官方字段号冲突或破坏 wire format。

---

### ③ `pkg/synchronization/configuration.go` —— 标签常量 + equality + merge

**(a) 文件顶部**（`import` 块之后）新增标签常量：

```go
// LabelDisableEmptyRootSafetyCheck is the label key used to transport the
// DisableEmptyRootSafetyCheck configuration option via session labels. This
// is necessary because the field is not part of the protobuf wire format.
const LabelDisableEmptyRootSafetyCheck = "mutagen.io/disable-empty-root-safety-check"
```

**(b) `Equal` 方法**：在最后一个比较条件后追加（注意把上一行结尾的 `&&` 补上）：

```go
		c.CompressionAlgorithm == other.CompressionAlgorithm &&
		c.DisableEmptyRootSafetyCheck == other.DisableEmptyRootSafetyCheck
}
```

**(c) 配置合并逻辑**（`MergeConfigurations` / 相关 merge 函数内，compression 合并之后）追加：

```go
	// Merge the disable empty root safety check flag.
	if higher.DisableEmptyRootSafetyCheck {
		result.DisableEmptyRootSafetyCheck = true
	} else {
		result.DisableEmptyRootSafetyCheck = lower.DisableEmptyRootSafetyCheck
	}
```

---

### ④ `pkg/synchronization/controller.go` —— 实际生效点

在根目录清空安全检查处（原逻辑：`if oneEndpointEmptiedRoot(...) { ... }`），改为先判断开关：

```go
		// This safety check can be disabled via the DisableEmptyRootSafetyCheck
		// configuration option (set at session creation time via CLI flag or
		// global YAML config). The option is transported via session labels
		// since it is not part of the protobuf wire format.
		disableEmptyRootCheck := c.session.Configuration.DisableEmptyRootSafetyCheck ||
			c.session.Labels[LabelDisableEmptyRootSafetyCheck] == "true"
		if !disableEmptyRootCheck && oneEndpointEmptiedRoot(ancestor, αContent, βContent) {
			c.stateLock.Lock()
			c.state.Status = Status_HaltedOnRootEmptied
			c.stateLock.Unlock()
			return errHaltedForSafety
		}
```

> 注意同时支持两种来源：会话内 `Configuration.DisableEmptyRootSafetyCheck` 字段 **或** 标签。

---

### ⑤ `pkg/api/models/synchronization/configuration.go` —— YAML/JSON 配置映射

**(a) `Configuration` 结构体**新增 `Safety` 分组（本改动定义了 `sync.defaults.safety.disableEmptyRootCheck` 的解析）：

```go
	// Safety contains parameters related to synchronization safety checks.
	Safety struct {
		// DisableEmptyRootCheck disables the safety check that halts
		// synchronization when one endpoint's root directory is emptied.
		DisableEmptyRootCheck bool `json:"disableEmptyRootCheck,omitempty" yaml:"disableEmptyRootCheck" mapstructure:"disableEmptyRootCheck"`
	} `json:"safety" yaml:"safety" mapstructure:"safety"`
```

**(b) `loadFromInternal` 方法**末尾追加（内部 → 公开模型）：

```go
	// Propagate safety configuration.
	c.Safety.DisableEmptyRootCheck = configuration.DisableEmptyRootSafetyCheck
```

**(c) `ToInternal` 方法**返回的结构体字面量里追加（公开模型 → 内部）：

```go
		DisableEmptyRootSafetyCheck: c.Safety.DisableEmptyRootCheck,
```

---

### ⑥ `cmd/mutagen/sync/create.go` —— CLI 开关

**(a) 命令行传值**：构造 `configuration` 的结构体字面量里追加：

```go
		DisableEmptyRootSafetyCheck: createConfiguration.disableEmptyRootSafetyCheck,
```

**(b) 通过 label 传输**（构造完 `specification` 之后、连接 daemon 之前）：

```go
	// Transport the DisableEmptyRootSafetyCheck option via labels, since this
	// custom field is not part of the protobuf wire format and would otherwise
	// be lost during gRPC serialization.
	if configuration.DisableEmptyRootSafetyCheck {
		if specification.Labels == nil {
			specification.Labels = make(map[string]string)
		}
		specification.Labels[synchronization.LabelDisableEmptyRootSafetyCheck] = "true"
	}
```

**(c) 配置结构体字段**（`createConfiguration` 结构体定义内，compression 字段之后）：

```go
	// disableEmptyRootSafetyCheck specifies whether to disable the safety
	// check that halts synchronization when one endpoint's root directory
	// is emptied.
	disableEmptyRootSafetyCheck bool
```

**(d) 注册命令行 flag**（`init()` 里，safety flags 区域）：

```go
	// Wire up safety flags.
	flags.BoolVar(&createConfiguration.disableEmptyRootSafetyCheck, "disable-empty-root-safety-check", false, "Disable the safety check that halts when one endpoint root is emptied")
```

---

## 功能概述：`落盘前自动备份`（pre-transition backup）

在 mutagen 把变更**写入磁盘之前（`core.Transition` 之前）**，先把即将被覆盖/删除的现有文件或目录树备份到同机的另一目录，并按「增/删/改」标记归档，附带审计清单与按天保留清理。

- **在被修改端本地生效**：Windows 端文件将被改 → 在 Windows 端备份；远端文件将被改 → 在远端备份。备份完成后正常同步传输继续（不做跨机复制）。
- **只有「改/删」有内容可备份**；「增」仅在清单里记录标记（目标端原本无文件）。
- 配置**按机器本地读取**（环境变量优先，其次 `~/.mutagen/backup.json`），因为 endpoint 配置不随 gRPC/网络传输，无法从创建会话参数下发到远端 endpoint。

### 备份目录布局

```
<backupDir>/<yyyy-MM-dd>/<op>/<event>/<相对路径>   # <op> = create|modify|delete，<event> = HHmmss.mmm-<seq>（每次同步事件唯一）
<backupDir>/<yyyy-MM-dd>/manifest.log             # 每行：RFC3339时间\tevent\top\t类型(dir|file)\t绝对路径\t字节数\tsessionId
```

> 先按【操作】再按【事件】归档：同一文件的历史版本全部集中在 `modify/` 下、按事件目录（可按时间排序）依次排列。因为每次 modify 备份的是“改动前”的内容，所以 `modify/` 下**最早的事件目录即该文件的最初原始版本**，`delete/` 下则是删除前的最后内容；同一文件同天多次修改各版本互不覆盖。`manifest.log` 记录每个事件的 `event` 号以便与物理目录对应。

### 配置方式

环境变量：`MUTAGEN_BACKUP_ENABLED` / `MUTAGEN_BACKUP_DIR` / `MUTAGEN_BACKUP_RETENTION_DAYS` / `MUTAGEN_BACKUP_FAIL_OPEN`。

`~/.mutagen/backup.json`：

```json
{ "enabled": true, "dir": "", "retentionDays": 7, "failOpen": true }
```

- `dir` 为空 → 默认取同步根的兄弟目录 `<父目录>/<根名>.mutagen-backup`（保证在同步根之外，不会被再次同步）；若备份目录落在同步根内部则**拒绝启用**并记录错误。
- `dir` 非空（显式指定）→ 同一台机器上所有任务共用这个基目录，会在其下**自动拼一层短子目录** `<vol>/<basename>_<hash8>`（vol 为卷标去冒号，basename 为同步根末段，hash8 为整条同步根路径的 FNV-1a 哈希前 8 位）以隔离多任务。例：基目录 `D:\backups`、同步根 `D:\FTP\Impath\Acmp\InBox` → 实际备份根 `D:\backups\D\InBox_<hash8>`；POSIX 同步根 `/srv/ftp/SW/sw1/Acmp/InBox` → `<base>/InBox_<hash8>`。该短布局替代了早期把完整路径展开成多层目录的实现——后者叠加 `day/op/<event>/<relPath>` 后在深层目录树上极易超 Windows MAX_PATH(260)，导致备份静默失败、而 `failOpen=true` 时同步照常进行、数据无保护却无感知。
- **失败冷却**：某绝对路径备份失败后，`backupFailCooldown`（60s）窗口内不再重复尝试拷贝，避免永久失败路径（文件被锁 / 路径过长）在每个 scan 周期都做无用 IO 并刷屏 problem。冷却期内 `fail-closed` 仍跳过该 transition（旧内容受保护），只是省去拷贝尝试；一旦备份成功即清除该路径的失败记录、恢复即时重试。
- 默认值：`enabled=false`（opt-in）、`retentionDays=7`、`failOpen=true`。
- `failOpen=false` 时，备份失败的那条 transition 会被跳过（保留旧内容 + 产生一条 `core.Problem`），保护数据不被覆盖。

### 改动清单（共 3 个文件，均在 `pkg/synchronization/endpoint/local/`）

#### ⑦ `backup.go` —— **新增文件**（备份引擎）

包含 `backupConfig`、`loadBackupConfig`、`backupBeforeTransition`、`backupCopyPath`、`copyFile`、`backupSymlink`、`appendManifest`、`pruneOldBackups`、`classifyChange` 等。整文件均为自定义代码，升级时原样保留即可（无需改动，除非官方改了 `core.Change` / `core.Transition` 签名）。

#### ⑧ `backup_test.go` —— **新增文件**（单元测试）

覆盖增删改布局、manifest 内容、目录递归备份、fail-closed 跳过、按天保留清理、配置加载。

#### ⑨ `endpoint.go` —— 结构体字段 + 构造 + Transition 钩子

**(a)** `endpoint` 结构体末尾（`stager` 字段之后）新增：

```go
	// [CUSTOM PATCH]
	backup backupConfig
```

**(b)** `NewEndpoint` 里 `endpoint := &endpoint{` 之前新增：

```go
	backupCfg := loadBackupConfig(root, logger)
	backupCfg.sessionIdentifier = sessionIdentifier
```

并在结构体字面量里（`stager:` 之后）追加 `backup: backupCfg,`。

**(c)** `Transition` 方法里，`e.unlockScanLock()` 之后、`core.Transition(...)` 调用处，插入备份钩子：备份 `transitions`，`failOpen=false` 时过滤掉备份失败的 transition（改用 `effectiveTransitions` 传给 `core.Transition`），调用后再把结果按原始下标展开并合并 `backupProblems`。搜索标记 `[CUSTOM PATCH]` 可定位全部三处。

---

## 升级官方版本后的套用步骤

1. 用官方新版本替换 mutagen 源码（`cmd/`、`pkg/`、`scripts/` 等）。
2. 按上面 ②～⑥ 逐处重新套用（① 无需改动）。
   - 重点检查 `configuration.pb.go` 是否因官方重新生成而覆盖，需要重新注入自定义字段。
   - 检查官方是否新增了 protobuf 字段号，避免与自定义逻辑冲突（自定义字段用 `protobuf:"-"`，一般不会冲突）。
3. 编译并验证。

### 验证步骤

```powershell
# 1. 编译 mutagen（在仓库根目录）
cd c:\vscode\mutagen-0.18.1
go run scripts\build.go

# 2. 确认字段与逻辑都在（应能搜到多处命中）
#    可用编辑器全局搜索：DisableEmptyRootSafetyCheck / disableEmptyRootCheck

# 3. 功能验证：CLI flag 存在
build\mutagen.exe sync create --help   # 应能看到 --disable-empty-root-safety-check

# 4. 功能验证：全局 YAML 配置可解析（写入含 safety.disableEmptyRootCheck 的配置后创建会话）
```

---

## 与 web-console 的关系

`web-console` 是独立 Go 模块（`mutagen-web/*`），通过子进程调用编译好的 `mutagen.exe`，**不 import 也不修改** mutagen 源码。前端 `web/src/App.vue` 的全局配置输入框已把该 YAML 片段作为 placeholder 示例。因此本补丁只与 mutagen 本体有关，与 web-console 的整合互不影响。
