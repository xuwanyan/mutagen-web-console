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
