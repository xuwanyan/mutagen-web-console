package mutagen

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Executor struct {
	mutagenPath string

	// hashCache 按 path 缓存文件哈希与元数据，避免频繁 create/resume 时重复读大文件算 SHA256。
	// 以 (size, mtime) 作为缓存键：文件 size 或 mtime 变化才重新算哈希。
	// 用于双指纹判断（mutagen.exe + mutagen-agents.tar.gz）。
	hashMu    sync.Mutex
	hashCache map[string]hashCacheEntry
}

// hashCacheEntry 单个文件的哈希缓存条目
type hashCacheEntry struct {
	size  int64
	mtime time.Time
	hash  string
}

func NewExecutor() (*Executor, error) {
	path := os.Getenv("MUTAGEN_PATH")
	if path == "" {
		exe, err := os.Executable()
		if err == nil {
			dir := filepath.Dir(exe)
			candidate := filepath.Join(dir, "mutagen.exe")
			if _, err := os.Stat(candidate); err == nil {
				path = candidate
			}
		}
	}
	if path == "" {
		var err error
		path, err = exec.LookPath("mutagen")
		if err != nil {
			return nil, fmt.Errorf("mutagen not found: %w", err)
		}
	}
	return &Executor{mutagenPath: path}, nil
}

func (e *Executor) exec(args ...string) (string, error) {
	cmd := exec.Command(e.mutagenPath, args...)
	output, err := cmd.CombinedOutput()
	return string(output), err
}

func (e *Executor) CreateSync(name, alpha, beta, mode string, ignoreVcs bool, symlinkMode string, ignorePaths []string) (string, error) {
	alpha = strings.ReplaceAll(alpha, "\\", "/")
	beta = strings.ReplaceAll(beta, "\\", "/")
	// 归一化：DB 存的可能是展示名（如 "Two Way Resolved"、"Ignore"），
	// mutagen CLI 只接受小写连字符格式（two-way-resolved、ignore）。
	mode = normalizeMode(mode)
	symlinkMode = normalizeSymlinkMode(symlinkMode)
	args := []string{"sync", "create", "--name=" + name}
	if mode != "" {
		args = append(args, "--mode="+mode)
	}
	if ignoreVcs {
		args = append(args, "--ignore-vcs")
	}
	if symlinkMode != "" {
		args = append(args, "--symlink-mode="+symlinkMode)
	}
	for _, p := range ignorePaths {
		if p != "" {
			args = append(args, "--ignore="+p)
		}
	}
	args = append(args, alpha, beta)
	return e.exec(args...)
}

func (e *Executor) PauseSync(name string) (string, error) {
	return e.exec("sync", "pause", name)
}

func (e *Executor) ResumeSync(name string) (string, error) {
	return e.exec("sync", "resume", name)
}

func (e *Executor) TerminateSync(name string) (string, error) {
	return e.exec("sync", "terminate", name)
}

func (e *Executor) ListSyncs() (string, error) {
	// 使用 -l 长格式输出，获取配置参数（mode、symlinkMode、ignore 等）
	return e.exec("sync", "list", "-l")
}

func (e *Executor) DaemonStart() (string, error) {
	return e.exec("daemon", "start")
}

func (e *Executor) DaemonStop() (string, error) {
	return e.exec("daemon", "stop")
}

func (e *Executor) SyncStatus() (string, error) {
	return e.ListSyncs()
}

func (e *Executor) UpdateGlobalConfig(content string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	path := filepath.Join(home, ".mutagen.yml")
	return os.WriteFile(path, []byte(content), 0644)
}

// UpdateBackupConfig 写本机 mutagen 备份配置 ~/.mutagen/backup.json
// 如果配置中指定了备份目录（dir 字段），会预创建该目录。
func (e *Executor) UpdateBackupConfig(content string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	dir := filepath.Join(home, ".mutagen")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	path := filepath.Join(dir, "backup.json")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return err
	}

	// 预创建备份目录，避免首次备份时才创建
	var cfg struct {
		Dir     string `json:"dir"`
		Enabled *bool  `json:"enabled"`
	}
	if err := json.Unmarshal([]byte(content), &cfg); err == nil && cfg.Dir != "" {
		if err := os.MkdirAll(cfg.Dir, 0755); err != nil {
			log.Printf("warning: create backup dir %s failed: %v", cfg.Dir, err)
		} else {
			log.Printf("backup dir created/verified: %s", cfg.Dir)
		}
	}

	return nil
}

// PushRemoteBackupConfig 通过 SSH 将 backup.json 推送到远端主机的 ~/.mutagen/backup.json。
// host 为本机 ~/.ssh/config 中的主机别名，依赖 mutagen 同步同款的免密登录。
// 带 30s 执行超时，防止远端 hang 住阻塞 cmdMu 串行队列。
//
// 多 Win 共用同一 Linux 的场景：
//   - 远端不存在 backup.json 时，正常写入（首次推送），返回 "BACKUP_WRITTEN"。
//   - 远端已存在 backup.json 时，不覆盖，返回 "BACKUP_EXISTS:" 前缀 + 远端现有 backup.json 内容。
//     此时该 Linux 上的所有同步任务（无论来自哪个 Win）都共用现有的备份配置。
//     备份子目录由 rootBackupSubdir 按 sync root 自动区分，数据不会冲突。
//   - 如需强制覆盖，先通过 verify_remote_backup_config 查看远端内容，
//     再手动到 Linux 上删除 ~/.mutagen/backup.json 后重新推送。
func (e *Executor) PushRemoteBackupConfig(host, content string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// 远端已存在则不覆盖，并回读现有内容供上层回传给前端（用于锁定 B 的配置）。
	// 走 else 分支时 cat 从 stdin 读取 content 写入文件；走 if 分支时不读 stdin，SSH 连接关闭后丢弃。
	script := `if [ -f ~/.mutagen/backup.json ]; then echo "BACKUP_EXISTS:"; cat ~/.mutagen/backup.json; else mkdir -p ~/.mutagen && cat > ~/.mutagen/backup.json && echo "BACKUP_WRITTEN"; fi`
	cmd := exec.CommandContext(ctx, "ssh",
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=15",
		host,
		script)
	cmd.Stdin = strings.NewReader(content)
	output, err := cmd.CombinedOutput()
	return string(output), err
}

// ReadRemoteBackupConfig 通过 SSH 读取远端主机的 ~/.mutagen/backup.json 内容，用于一致性校验。
// 带 30s 执行超时，防止远端 hang 住阻塞 cmdMu 串行队列。
func (e *Executor) ReadRemoteBackupConfig(host string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ssh",
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=15",
		host,
		"cat ~/.mutagen/backup.json")
	output, err := cmd.CombinedOutput()
	return string(output), err
}

func (e *Executor) UpdateSSHConfig(content string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0700); err != nil {
		return err
	}
	path := filepath.Join(sshDir, "config")
	if runtime.GOOS == "windows" {
		content = strings.ReplaceAll(content, "\n", "\r\n")
	}
	return os.WriteFile(path, []byte(content), 0600)
}

// ReadSSHConfig 读取本机 ~/.ssh/config 原文。文件不存在时返回空串且不报错，
// 让上层区分"空配置"和"读取失败"。
func (e *Executor) ReadSSHConfig() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	path := filepath.Join(home, ".ssh", "config")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	// 统一去 CRLF，避免解析时因换行差异出问题
	return strings.ReplaceAll(string(data), "\r\n", "\n"), nil
}

// ParseStatus 解析 mutagen sync list -l 输出，提取状态、路径和配置参数。
// mutagen 以整行连字符（cmd.DelimiterLine，79/80 个 '-'）分隔各会话，
// 输出中不含空行，因此必须按分隔线切块——不能按空行，否则所有会话会被
// 粘连成一块、后者覆盖前者，只剩最后 1 个。
func (e *Executor) ParseStatus(output string) []map[string]string {
	var tasks []map[string]string
	var current map[string]string
	var section string
	var configSection bool
	var collectingIgnores bool
	// nestedConfig 标记当前进入的是子级 Configuration（Alpha:/Beta: 下的子段，带缩进），
	// 用来区分根级 Configuration，避免后者被错误退出。
	var nestedConfig bool
	var collectingTransitionProblems bool

	flush := func() {
		if current != nil {
			// 仅收录真正含会话信息的块（有 identifier 或 name），
			// 避免把前导/尾部分隔线之间的空块计入。
			if current["identifier"] != "" || current["name"] != "" {
				tasks = append(tasks, current)
			}
			current = nil
			section = ""
			configSection = false
			collectingIgnores = false
			nestedConfig = false
			collectingTransitionProblems = false
		}
	}

	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		// 分隔线：整行均为连字符，作为会话块边界。
		if isDelimiterLine(trimmed) {
			flush()
			continue
		}
		if trimmed == "" {
			continue
		}
		if current == nil {
			current = make(map[string]string)
			section = ""
			configSection = false
			collectingIgnores = false
			nestedConfig = false
		}

		// =============================================
		// 兜底 1：无条件优先解析 Status / Last error。
		// 不依赖任何状态机（configSection / nestedConfig），
		// 只要行以 Status: 或 Last error: 开头就必取，
		// 彻底避免被任何 continue/continue 分支跳过。
		// =============================================
		if strings.HasPrefix(trimmed, "Status:") {
			v := strings.TrimSpace(strings.TrimPrefix(trimmed, "Status:"))
			if current["status"] == "" {
				current["status"] = v
			}
		} else if strings.HasPrefix(trimmed, "Last error:") {
			v := strings.TrimSpace(strings.TrimPrefix(trimmed, "Last error:"))
			if current["last error"] == "" {
				current["last error"] = v
			}
		}
		// 注意：这里不 continue，下方通用解析仍可能写入同名 key（幂等覆盖，值相同）。

		lower := strings.ToLower(trimmed)

		// =============================================
		// Transition problems 解析。
		// -l 长格式输出：
		//   Transition problems:
		//       path: error message
		//       path: error message
		// 短格式（无 -l）：
		//   Transition problems: 2
		// =============================================
		if strings.HasPrefix(lower, "transition problems:") {
			v := strings.TrimSpace(trimmed[len("transition problems:"):])
			if v == "" {
				// 长格式：后续 \t\t 缩进行为问题详情
				collectingTransitionProblems = true
			} else {
				// 短格式：只有数量
				current["transition_problems_count"] = v
			}
			continue
		}
		if collectingTransitionProblems {
			if strings.HasPrefix(line, "\t\t") {
				inner := strings.TrimSpace(line)
				if idx := strings.Index(inner, ":"); idx > 0 {
					p := strings.TrimSpace(inner[:idx])
					e := strings.TrimSpace(inner[idx+1:])
					problem := p + "|" + e
					if existing := current["transition_problems"]; existing != "" {
						current["transition_problems"] = existing + ";" + problem
					} else {
						current["transition_problems"] = problem
					}
				}
				continue
			}
			// 非问题行，结束收集，继续正常解析
			collectingTransitionProblems = false
		}

		// 退出 Configuration 章节
		if lower == "alpha:" || lower == "beta:" || lower == "status:" {
			configSection = false
			collectingIgnores = false
			nestedConfig = false
		}
		if lower == "alpha:" || lower == "beta:" {
			section = lower[:len(lower)-1]
			continue
		}

		// 进入 Configuration 章节（-l 长格式）。
		// 区分根级（非缩进行）与子级（Alpha/Beta 下，带 \t 前缀）。
		if trimmed == "Configuration:" {
			configSection = true
			collectingIgnores = false
			nestedConfig = strings.HasPrefix(line, "\t")
			continue
		}

		// 兜底 2a：configSection=true 但遇到完全无缩进的顶层行
		// （如 Name: / Identifier: / Status: 等），必退出。
		if configSection && !strings.HasPrefix(line, "\t") {
			configSection = false
			collectingIgnores = false
			nestedConfig = false
		}
		// 兜底 2b：子级 Configuration 下遇到一层缩进（\t 但非 \t\t），
		// 说明子段已结束（如回到 \tConnected: / \tSynchronizable contents:），
		// 退出 configSection 让这些行正常归位。
		if configSection && nestedConfig &&
			strings.HasPrefix(line, "\t") && !strings.HasPrefix(line, "\t\t") {
			configSection = false
			collectingIgnores = false
			nestedConfig = false
		}

		// 解析 Configuration 章节内的配置行
		if configSection {
			if strings.HasPrefix(line, "\t\t") && collectingIgnores {
				// 收集 Ignores 中的路径
				path := strings.TrimSpace(line)
				if path != "" {
					existing := current["config_ignores"]
					if existing != "" {
						existing += ";"
					}
					current["config_ignores"] = existing + path
				}
				continue
			}
			// 子级 Configuration（\tConfiguration:）只消费两层缩进行（\t\txxx）。
			// 根级 Configuration 消费一层缩进行（\txxx）。
			wantPrefix := "\t"
			if nestedConfig {
				wantPrefix = "\t\t"
			}
			if strings.HasPrefix(line, wantPrefix) {
				collectingIgnores = false
				inner := strings.TrimSpace(line)
				if idx := strings.Index(inner, ":"); idx > 0 {
					key := strings.TrimSpace(inner[:idx])
					value := strings.TrimSpace(inner[idx+1:])
					configKey := strings.ToLower(strings.ReplaceAll(key, " ", "_"))
					if configKey == "ignores" && value == "" {
						// Ignores: 后跟缩进路径列表
						collectingIgnores = true
					} else if configKey == "ignores" && strings.ToLower(value) == "none" {
						current["config_ignores"] = ""
					} else {
						// mutagen 默认模式输出形如 "Default (Two Way Safe)"，显式模式输出
						// "Two Way Resolved"。此处保留原始展示名，归一化在 reportStatus/
						// normalizeMode 完成（默认形式归一化为空，重建时不下发 --mode）。
						// 嵌套子级配置不覆盖根级同名 key（子级值通常更具体或相同，
						// 覆盖会把根级 Synchronization mode 等被 Beta 的 Compression 冲走）。
						if nestedConfig {
							// 子级 key 写入独立命名空间，避免与根级冲突
							nestedKey := section + "_config_" + configKey
							current[nestedKey] = value
						} else {
							current["config_"+configKey] = value
						}
					}
				}
			}
			continue
		}

		if idx := strings.Index(trimmed, ":"); idx > 0 {
			key := strings.TrimSpace(trimmed[:idx])
			value := strings.TrimSpace(trimmed[idx+1:])
			if section != "" && key == "URL" {
				current[section+"_url"] = value
			} else {
				current[strings.ToLower(key)] = value
			}
		}
	}
	flush()
	return tasks
}

// isDelimiterLine 判断整行是否为 mutagen 会话分隔线（全部由 '-' 组成）。
func isDelimiterLine(s string) bool {
	if len(s) < 3 {
		return false
	}
	for _, r := range s {
		if r != '-' {
			return false
		}
	}
	return true
}

func OS() string {
	return runtime.GOOS
}

// remoteCleanupMarkerFile 远端每个主机上的清理标记文件路径（相对于 $HOME）。
// 内容为最近一次清理时使用的双指纹，避免多台 Win 升级到同一版本时重复清理同一远端。
const remoteCleanupMarkerFile = ".mutagen/.agent_cleanup_fingerprint"

// mutagenVersionRegexp 匹配 mutagen 语义化版本号，支持 "0.18.1" / "0.18.1-dev" / "0.18.1-beta.1" 等格式，
// 用于从 mutagen version 输出中稳健提取版本号，避免直接取最后一个字段被 build 信息干扰。
var mutagenVersionRegexp = regexp.MustCompile(`\b(\d+\.\d+\.\d+(?:-[A-Za-z0-9._-]+)?)\b`)

// getMutagenVersion 调用本地 mutagen version 并解析出版本号（如 "0.18.1"），
// 用于精确删除远端对应版本的 agent 子目录，避免误删其他版本号的 agent。
func (e *Executor) getMutagenVersion() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, e.mutagenPath, "version").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("run mutagen version: %w, output: %s", err, strings.TrimSpace(string(out)))
	}
	raw := strings.TrimSpace(string(out))
	if raw == "" {
		return "", fmt.Errorf("empty mutagen version output")
	}
	// 用正则优先匹配语义化版本号，兼容 "mutagen version 0.18.1" / "mutagen version 0.18.1 (built xxx)" 等多种输出
	if m := mutagenVersionRegexp.FindStringSubmatch(raw); m != nil && m[1] != "" {
		return m[1], nil
	}
	// 正则兜底：取最后一段再校验一次，防止极端输出格式
	fields := strings.Fields(raw)
	last := fields[len(fields)-1]
	if m := mutagenVersionRegexp.FindStringSubmatch(last); m != nil && m[1] != "" {
		return m[1], nil
	}
	return "", fmt.Errorf("unable to parse mutagen version from output: %q", raw)
}

// readRemoteMarker SSH 到指定主机读取清理标记文件，返回 (内容, nil)；文件不存在或读取失败返回 ("", nil)。
func (e *Executor) readRemoteMarker(host string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ssh",
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=10",
		host,
		// 用 $HOME 代替 ~（~ 仅在无引号时展开，$HOME 在任何位置都展开），
		// cat + 2>/dev/null + || true：文件不存在时返回空串且不报错。
		"cat \"$HOME/"+remoteCleanupMarkerFile+"\" 2>/dev/null || true")
	out, err := cmd.CombinedOutput()
	if err != nil {
		// SSH 连不上才是真错误；cat 文件不存在的错误已经被 || true 吃掉
		return "", fmt.Errorf("ssh read marker on %s: %w", host, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// writeRemoteMarker SSH 到指定主机把当前指纹写入清理标记文件。
func (e *Executor) writeRemoteMarker(host, fingerprint string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	// 关键说明：
	// 1) 用 $HOME 代替 ~，避免波浪号在引号内不展开的问题；
	// 2) fingerprint 仅由 hex([0-9a-f]) 和 "|" 构成，不含 shell 特殊字符（无 $、'、空格等），
	//    所以用 shell 单引号包裹是安全的（相比 Go 的 %q 会额外写入双引号到文件里，导致后续对比失败）。
	// 先 mkdir -p 保证 .mutagen 目录存在，再用 printf '%s' 写（避免 echo 的换行/转义问题）。
	writeCmd := fmt.Sprintf(
		"mkdir -p \"$HOME/.mutagen\" && printf '%%s' '%s' > \"$HOME/%s\"",
		fingerprint, remoteCleanupMarkerFile)
	cmd := exec.CommandContext(ctx, "ssh",
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=10",
		host, writeCmd)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("write marker on %s: %s | %w",
			host, strings.TrimSpace(string(out)), err)
	}
	return nil
}

// EnsureRemoteAgentsFresh 检测本地 mutagen.exe 与 mutagen-agents.tar.gz 是否更新过。
// 若指纹变化（或 force=true 强制检查），则按以下规则处理每个远端主机：
//   1. 读取远端 ~/.mutagen/.agent_cleanup_fingerprint，若和当前指纹一致→已被其他Win清理过，跳过
//   2. 否则只删除 ~/.mutagen/agents/<当前版本号>/ 子目录（不影响其他版本号的 agent），
//      触发 mutagen 下次连接时自动重装当前版本的 agent
//   3. 清理完成后写入远端标记文件，避免同版本的其他 Win 升级时重复清理
//
// force=true 时跳过"本地指纹未变"的快路径，用于 Web UI 的"刷新远端 agents"按钮主动触发检查。
//
// 性能说明：热路径（两个文件都未改动 + force=false）只做 4 次 stat（exe + tar.gz + fingerprint 文件）
// + 内存读，开销 ~微秒级；只有文件 mtime/size 变化或 force=true 时才真正打 SSH。
func (e *Executor) EnsureRemoteAgentsFresh(force bool) error {
	// 1. 计算（或从缓存取）当前双指纹：mutagen.exe + mutagen-agents.tar.gz
	exeHash, err := e.cachedFileHash(e.mutagenPath)
	if err != nil {
		return fmt.Errorf("compute mutagen.exe hash: %w", err)
	}
	tarPath := filepath.Join(filepath.Dir(e.mutagenPath), "mutagen-agents.tar.gz")
	tarHash, err := e.cachedFileHash(tarPath)
	if err != nil {
		// tar.gz 不存在不算致命错误，只记日志，降级为只看 exe
		log.Printf("warning: unable to hash %s: %v (falling back to exe-only fingerprint)", tarPath, err)
		tarHash = ""
	}
	currentFingerprint := exeHash + "|" + tarHash

	// 2. 读取本地上次存储的指纹：未更新→直接返回（本地快路径，99% 情况命中；force=true时绕过）
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	mutagenDir := filepath.Join(home, ".mutagen")
	fingerprintPath := filepath.Join(mutagenDir, "agent_fingerprint")
	lastHash, _ := os.ReadFile(fingerprintPath)
	lastHashStr := strings.TrimSpace(string(lastHash))
	if !force && currentFingerprint == lastHashStr {
		return nil
	}
	if force {
		log.Printf("force refresh remote agents triggered (current fingerprint %s)",
			formatFingerprint(currentFingerprint))
	} else {
		log.Printf("mutagen fingerprint changed (%s -> %s), checking remote agents",
			formatFingerprint(lastHashStr), formatFingerprint(currentFingerprint))
	}

	// 3. 获取本地 mutagen 版本号，用于精确指定要删除的子目录
	version, err := e.getMutagenVersion()
	if err != nil {
		log.Printf("warning: unable to get mutagen version, falling back to full agents dir cleanup: %v", err)
		version = "" // 空串表示降级为删整个 agents/（兼容老版本）
	}

	// 4. 解析 ~/.ssh/config 获取所有远端主机别名（已按 HostName 去重，避免同机多别名重复清理）
	hosts := e.listSSHHosts()
	if len(hosts) == 0 {
		log.Printf("no SSH hosts found in ~/.ssh/config, skipping remote agent cleanup")
	}

	// 5. 对每个远端主机并发处理：先读远端标记→已清理过则跳过，否则只删当前版本子目录并写标记
	//    计数器用 atomic 保护；多台主机 15s ConnectTimeout 串行会卡死，并发整体约等于最慢的一台。
	var (
		cleanedCount int32
		skippedCount int32
		failedCount  int32
		wg           sync.WaitGroup
	)
	processHost := func(host string) {
		defer wg.Done()
		// 5a. 读远端标记：若标记与当前指纹一致 → 同版本的其他机器已经清过+重装过，跳过
		remoteMarker, rerr := e.readRemoteMarker(host)
		if rerr != nil {
			// 单独计数 SSH 失败（可能是离线/配置错误），与"marker 匹配跳过"区分开
			atomic.AddInt32(&failedCount, 1)
			log.Printf("warning: skip %s (ssh/parse failed): %v", host, rerr)
			return
		}
		if remoteMarker == currentFingerprint {
			atomic.AddInt32(&skippedCount, 1)
			log.Printf("skip %s: already cleaned by same fingerprint", host)
			return
		}

		// 5b. 构造清理命令：
		//     - 已知版本号 → 只删 agents/<version>/ 子目录（$HOME/.mutagen/agents/<version>/），
		//       不影响其他版本号 agent 共存；
		//     - 未知版本号 → 降级为删整个 agents/（最坏情况，但比删错好）。
		//     全部使用 $HOME 代替 ~，避免波浪号路径展开的壳兼容性问题；
		//     version 已被正则校验（仅数字、点号、可选预发布标签），直接拼接无注入风险。
		var cleanCmd string
		var logCleanPath string // 仅用于日志展示，不进 shell
		if version != "" {
			cleanCmd = fmt.Sprintf(
				`mkdir -p "$HOME/.mutagen" && rm -rf "$HOME/.mutagen/agents/%s/"`,
				version)
			logCleanPath = fmt.Sprintf("$HOME/.mutagen/agents/%s/", version)
		} else {
			cleanCmd = `mkdir -p "$HOME/.mutagen" && rm -rf "$HOME/.mutagen/agents/"`
			logCleanPath = "$HOME/.mutagen/agents/"
		}

		// 5c. SSH 执行清理
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		cmd := exec.CommandContext(ctx, "ssh",
			"-o", "BatchMode=yes",
			"-o", "ConnectTimeout=15",
			host, cleanCmd)
		if out, cerr := cmd.CombinedOutput(); cerr != nil {
			cancel()
			atomic.AddInt32(&failedCount, 1)
			log.Printf("failed to clean agents on %s (%s): %s | %v",
				host, logCleanPath, strings.TrimSpace(string(out)), cerr)
			return
		}
		cancel()

		// 5d. 写远端标记文件，告诉其他同版本 Win：我已经清过了，你们别再清了
		if werr := e.writeRemoteMarker(host, currentFingerprint); werr != nil {
			log.Printf("warning: cleaned %s but failed to write marker: %v", host, werr)
		} else {
			log.Printf("cleaned remote agents on %s (removed %s)", host, logCleanPath)
		}
		atomic.AddInt32(&cleanedCount, 1)
	}
	for _, host := range hosts {
		wg.Add(1)
		go processHost(host)
	}
	wg.Wait()
	if len(hosts) > 0 {
		log.Printf("remote agent cleanup done: %d cleaned, %d skipped (marker matched), %d failed (ssh/error)",
			atomic.LoadInt32(&cleanedCount),
			atomic.LoadInt32(&skippedCount),
			atomic.LoadInt32(&failedCount))
	}

	// 6. 保存当前指纹到本地（即使无 SSH 主机也要保存，避免下次重复扫；force=true 时也写入，
	//    避免"点了刷新但本地指纹没存，下次启动还以为指纹变了"的重复判断）。
	if err := os.MkdirAll(mutagenDir, 0755); err != nil {
		return err
	}
	return os.WriteFile(fingerprintPath, []byte(currentFingerprint), 0644)
}

// cachedFileHash 带内存缓存的文件哈希：以 (size, mtime) 为缓存键。
// 绝大多数调用（文件未变动）走 stat + 内存命中，不读文件内容。
// 缓存按 path 区分，支持多文件并存（如 mutagen.exe 和 mutagen-agents.tar.gz）。
func (e *Executor) cachedFileHash(path string) (string, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	size := fi.Size()
	mtime := fi.ModTime()

	e.hashMu.Lock()
	if e.hashCache == nil {
		e.hashCache = make(map[string]hashCacheEntry)
	}
	if entry, ok := e.hashCache[path]; ok && entry.size == size && entry.mtime.Equal(mtime) && entry.hash != "" {
		cached := entry.hash
		e.hashMu.Unlock()
		return cached, nil
	}
	e.hashMu.Unlock()

	// 缓存未命中，算哈希
	h, err := computeFileHash(path)
	if err != nil {
		return "", err
	}
	e.hashMu.Lock()
	e.hashCache[path] = hashCacheEntry{size: size, mtime: mtime, hash: h}
	e.hashMu.Unlock()
	return h, nil
}

// listSSHHosts 解析 ~/.ssh/config，返回所有 Host 别名（跳过通配符 *? 条目）。
// 只识别行首 "Host " 前缀（不区分大小写），支持一行多别名。
// 额外做 HostName 级去重：如果多个 Host 别名指向同一个 HostName（或 IP），
// 则只返回第一个别名，避免同一台物理机被重复清理多次。
func (e *Executor) listSSHHosts() []string {
	content, err := e.ReadSSHConfig()
	if err != nil || content == "" {
		return nil
	}

	// 先按块解析：每个 Host 行开启一个新块，直到下一个 Host 行；
	// 在每块内部找 HostName 字段，用于别名归并去重。
	type hostBlock struct {
		aliases  []string // 本块的 Host 别名（已剔通配符）
		hostName string   // 本块内找到的 HostName，空串表示未指定（默认=别名本身）
	}
	var blocks []hostBlock
	var current *hostBlock

	// 辅助：解析行，返回 (keyLower, valueTrimmed, ok)
	parseKV := func(line string) (string, string, bool) {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			return "", "", false
		}
		// ssh_config 允许 "key value" 或 "key = value"
		var key, val string
		if idx := strings.IndexAny(trimmed, " \t="); idx > 0 {
			key = trimmed[:idx]
			val = strings.TrimSpace(trimmed[idx:])
			val = strings.TrimLeft(val, "=")
			val = strings.TrimSpace(val)
		} else {
			key = trimmed
			val = ""
		}
		// 剥离行内注释（value 内部的 #）
		if i := strings.Index(val, "#"); i >= 0 {
			val = strings.TrimSpace(val[:i])
		}
		return strings.ToLower(key), val, true
	}

	for _, line := range strings.Split(content, "\n") {
		key, val, ok := parseKV(line)
		if !ok {
			continue
		}
		if key == "host" {
			// 新块：收齐上一块的别名（过滤通配符）
			var validAliases []string
			if current != nil {
				for _, a := range current.aliases {
					if !strings.ContainsAny(a, "*?") {
						validAliases = append(validAliases, a)
					}
				}
				if len(validAliases) > 0 {
					blocks = append(blocks, hostBlock{aliases: validAliases, hostName: current.hostName})
				}
			}
			current = &hostBlock{aliases: strings.Fields(val)}
			continue
		}
		// 非 Host 行：只处理 HostName
		if current != nil && key == "hostname" && val != "" && current.hostName == "" {
			current.hostName = val
		}
	}
	// 收最后一块
	if current != nil {
		var validAliases []string
		for _, a := range current.aliases {
			if !strings.ContainsAny(a, "*?") {
				validAliases = append(validAliases, a)
			}
		}
		if len(validAliases) > 0 {
			blocks = append(blocks, hostBlock{aliases: validAliases, hostName: current.hostName})
		}
	}

	// 按 HostName（或 IP）去重：
	// - 块内指定了 HostName 的，用 HostName 做归并键
	// - 没指定 HostName 的，用别名本身做归并键（ssh 默认 HostName=别名）
	// 同一个归并键只保留第一个遇到的别名。
	var hosts []string
	seenDedupKey := map[string]bool{}
	seenAlias := map[string]bool{}
	for _, b := range blocks {
		if len(b.aliases) == 0 {
			continue
		}
		dedupKey := b.hostName
		// 没指定 HostName 时，归并键就是别名本身（这样相同别名不会重复出现）
		for _, alias := range b.aliases {
			key := dedupKey
			if key == "" {
				key = alias
			}
			if seenDedupKey[key] {
				continue // 同机其他别名，跳过
			}
			if seenAlias[alias] {
				continue
			}
			seenDedupKey[key] = true
			seenAlias[alias] = true
			hosts = append(hosts, alias)
		}
	}
	return hosts
}

// computeFileHash 计算文件 SHA256 哈希（小写十六进制）
func computeFileHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// shortHash 返回哈希前 8 位用于日志可读性；空串则返回 "<none>"
func shortHash(h string) string {
	if h == "" {
		return "<none>"
	}
	if len(h) <= 8 {
		return h
	}
	return h[:8]
}

// formatFingerprint 把 "exeHash|tarHash" 格式化成日志友好的 "exe: xxx, tar: xxx"，
// 每段取前 8 位。旧指纹如果是单哈希（无 |）则兼容为 "exe: xxx, tar: <none>"。
func formatFingerprint(fp string) string {
	if fp == "" {
		return "exe: <none>, tar: <none>"
	}
	parts := strings.SplitN(fp, "|", 2)
	if len(parts) == 1 {
		return "exe: " + shortHash(parts[0]) + ", tar: <none>"
	}
	return "exe: " + shortHash(parts[0]) + ", tar: " + shortHash(parts[1])
}

// normalizeMode 将展示名（如 "Two Way Resolved"）归一化为 mutagen CLI 接受的
// 小写连字符格式（two-way-resolved）。无法识别的值（含 mutagen 默认模式的
// "Default (Two Way Safe)"）返回空串，让 CreateSync 不下发 --mode，
// 使用 mutagen 默认，避免下发非法标志导致 create 失败。
func normalizeMode(m string) string {
	if m == "" {
		return ""
	}
	canon := strings.ToLower(strings.ReplaceAll(m, " ", "-"))
	switch canon {
	case "two-way-safe", "two-way-resolved", "one-way-safe", "one-way-replica":
		return canon
	}
	return ""
}

// normalizeSymlinkMode 同上，针对 symlink-mode 字段：portable | ignore | posix-raw。
// 无法识别的值（含 "Default (Portable)"）返回空串，不下发 --symlink-mode。
func normalizeSymlinkMode(m string) string {
	if m == "" {
		return ""
	}
	canon := strings.ToLower(strings.ReplaceAll(m, " ", "-"))
	switch canon {
	case "portable", "ignore", "posix-raw":
		return canon
	}
	return ""
}
