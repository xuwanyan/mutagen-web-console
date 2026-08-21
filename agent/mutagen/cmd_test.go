package mutagen

import "testing"

// realListOutput 取自真实 `mutagen sync list -l` 输出（3 个会话），
// 包含 Configuration 章节。会话之间仅用一整行连字符分隔，全程无空行。
const realListOutput = "" +
	"-------------------------------------------------------------------------------\n" +
	"Name: SHA-ACMP-InBox-SW1\n" +
	"Identifier: sync_sv6tpP5S36rQEDih7fcx505W30XSpvpq0g0jB0wlxuU\n" +
	"Configuration:\n" +
	"\tSynchronization mode: Default (Two Way Safe)\n" +
	"\tSymbolic link mode: Default (Portable)\n" +
	"\tIgnores: None\n" +
	"\tIgnore VCS mode: Default (Propagate)\n" +
	"Alpha:\n" +
	"\tURL: D:\\FTP\\Impath\\Acmp\\InBox\n" +
	"\tConnected: Yes\n" +
	"\tSynchronizable contents:\n" +
	"\t\t1 directory\n" +
	"\t\t243 files (96 kB)\n" +
	"\t\t0 symbolic links\n" +
	"Beta:\n" +
	"\tURL: dms-ftp:/srv/ftp/SW/sw1/Acmp/InBox\n" +
	"\tConnected: Yes\n" +
	"\tSynchronizable contents:\n" +
	"\t\t1 directory\n" +
	"\t\t243 files (96 kB)\n" +
	"\t\t0 symbolic links\n" +
	"Status: Watching for changes\n" +
	"-------------------------------------------------------------------------------\n" +
	"Name: SHA-ACMP-OutBox-SW1\n" +
	"Identifier: sync_kfvGYsWDzp4zYExZATWS5EnFnW6GwEkfBwf6kLLZTR1\n" +
	"Configuration:\n" +
	"\tSynchronization mode: Default (Two Way Safe)\n" +
	"\tSymbolic link mode: Default (Portable)\n" +
	"\tIgnores: None\n" +
	"\tIgnore VCS mode: Default (Propagate)\n" +
	"Alpha:\n" +
	"\tURL: D:\\FTP\\Impath\\Acmp\\OutBox\n" +
	"\tConnected: Yes\n" +
	"\tSynchronizable contents:\n" +
	"\t\t1 directory\n" +
	"\t\t0 files (0 B)\n" +
	"\t\t0 symbolic links\n" +
	"Beta:\n" +
	"\tURL: dms-ftp:/srv/ftp/SW/sw1/Acmp/OutBox\n" +
	"\tConnected: Yes\n" +
	"\tSynchronizable contents:\n" +
	"\t\t1 directory\n" +
	"\t\t0 files (0 B)\n" +
	"\t\t0 symbolic links\n" +
	"Status: Watching for changes\n" +
	"-------------------------------------------------------------------------------\n" +
	"Name: SHA-Decciq-InBox-SW1\n" +
	"Identifier: sync_OyMu9VGjiFRNsyNloT51RHPLwAS6YBnmEXy1QZXEoJ5\n" +
	"Configuration:\n" +
	"\tSynchronization mode: Default (Two Way Safe)\n" +
	"\tSymbolic link mode: Default (Portable)\n" +
	"\tIgnores: None\n" +
	"\tIgnore VCS mode: Default (Propagate)\n" +
	"Alpha:\n" +
	"\tURL: D:\\FTP\\Impath\\Decciq001\\InBox\n" +
	"\tConnected: Yes\n" +
	"\tSynchronizable contents:\n" +
	"\t\t1 directory\n" +
	"\t\t2474 files (841 kB)\n" +
	"\t\t0 symbolic links\n" +
	"Beta:\n" +
	"\tURL: dms-ftp:/srv/ftp/SW/sw1/Decciq001/InBox\n" +
	"\tConnected: Yes\n" +
	"\tSynchronizable contents:\n" +
	"\t\t1 directory\n" +
	"\t\t2474 files (841 kB)\n" +
	"\t\t0 symbolic links\n" +
	"Status: Watching for changes\n" +
	"-------------------------------------------------------------------------------\n"

func TestParseStatusMultiSession(t *testing.T) {
	e := &Executor{}
	tasks := e.ParseStatus(realListOutput)

	if len(tasks) != 3 {
		t.Fatalf("expected 3 sessions parsed, got %d: %+v", len(tasks), tasks)
	}

	wantNames := []string{"SHA-ACMP-InBox-SW1", "SHA-ACMP-OutBox-SW1", "SHA-Decciq-InBox-SW1"}
	wantIDs := []string{
		"sync_sv6tpP5S36rQEDih7fcx505W30XSpvpq0g0jB0wlxuU",
		"sync_kfvGYsWDzp4zYExZATWS5EnFnW6GwEkfBwf6kLLZTR1",
		"sync_OyMu9VGjiFRNsyNloT51RHPLwAS6YBnmEXy1QZXEoJ5",
	}
	for i, task := range tasks {
		if task["name"] != wantNames[i] {
			t.Errorf("session %d: name = %q, want %q", i, task["name"], wantNames[i])
		}
		if task["identifier"] != wantIDs[i] {
			t.Errorf("session %d: identifier = %q, want %q", i, task["identifier"], wantIDs[i])
		}
		if task["status"] != "Watching for changes" {
			t.Errorf("session %d: status = %q, want %q", i, task["status"], "Watching for changes")
		}
		// 验证 Configuration 章节解析
		if task["config_synchronization_mode"] != "Default (Two Way Safe)" {
			t.Errorf("session %d: config_synchronization_mode = %q, want %q", i, task["config_synchronization_mode"], "Default (Two Way Safe)")
		}
		if task["config_symbolic_link_mode"] != "Default (Portable)" {
			t.Errorf("session %d: config_symbolic_link_mode = %q, want %q", i, task["config_symbolic_link_mode"], "Default (Portable)")
		}
		if task["config_ignore_vcs_mode"] != "Default (Propagate)" {
			t.Errorf("session %d: config_ignore_vcs_mode = %q, want %q", i, task["config_ignore_vcs_mode"], "Default (Propagate)")
		}
		// Ignores: None 时 config_ignores 应为空字符串
		if _, ok := task["config_ignores"]; !ok {
			t.Errorf("session %d: config_ignores key missing", i)
		}
	}

	// alpha/beta URL 需正确归属（beta URL 含冒号，考验解析）
	if tasks[0]["alpha_url"] != "D:\\FTP\\Impath\\Acmp\\InBox" {
		t.Errorf("session 0: alpha_url = %q", tasks[0]["alpha_url"])
	}
	if tasks[0]["beta_url"] != "dms-ftp:/srv/ftp/SW/sw1/Acmp/InBox" {
		t.Errorf("session 0: beta_url = %q", tasks[0]["beta_url"])
	}
}

// realListOutputWithIgnores 测试 Ignores 含路径列表的长格式输出
const realListOutputWithIgnores = "" +
	"-------------------------------------------------------------------------------\n" +
	"Name: Test-Session\n" +
	"Identifier: sync_test_id\n" +
	"Configuration:\n" +
	"\tSynchronization mode: Two Way Resolved\n" +
	"\tSymbolic link mode: Portable\n" +
	"\tIgnores:\n" +
	"\t\t*.crdownload\n" +
	"\t\t*.part\n" +
	"\t\t*.tmp\n" +
	"\tIgnore VCS mode: Ignore\n" +
	"Alpha:\n" +
	"\tURL: C:\\test\\alpha\n" +
	"\tConnected: Yes\n" +
	"\tSynchronizable contents:\n" +
	"\t\t1 directory\n" +
	"\t\t0 files (0 B)\n" +
	"\t\t0 symbolic links\n" +
	"Beta:\n" +
	"\tURL: test-remote:/srv/test/beta\n" +
	"\tConnected: Yes\n" +
	"\tSynchronizable contents:\n" +
	"\t\t1 directory\n" +
	"\t\t0 files (0 B)\n" +
	"\t\t0 symbolic links\n" +
	"Status: Watching for changes\n" +
	"-------------------------------------------------------------------------------\n"

func TestParseStatusIgnores(t *testing.T) {
	e := &Executor{}
	tasks := e.ParseStatus(realListOutputWithIgnores)

	if len(tasks) != 1 {
		t.Fatalf("expected 1 session, got %d", len(tasks))
	}

	task := tasks[0]
	if task["name"] != "Test-Session" {
		t.Errorf("name = %q, want %q", task["name"], "Test-Session")
	}
	if task["config_synchronization_mode"] != "Two Way Resolved" {
		t.Errorf("config_synchronization_mode = %q", task["config_synchronization_mode"])
	}
	if task["config_symbolic_link_mode"] != "Portable" {
		t.Errorf("config_symbolic_link_mode = %q", task["config_symbolic_link_mode"])
	}
	if task["config_ignore_vcs_mode"] != "Ignore" {
		t.Errorf("config_ignore_vcs_mode = %q", task["config_ignore_vcs_mode"])
	}
	// 验证 Ignores 路径收集
	ignores := task["config_ignores"]
	want := "*.crdownload;*.part;*.tmp"
	if ignores != want {
		t.Errorf("config_ignores = %q, want %q", ignores, want)
	}
}

// realListOutputWithNestedConfig 取自真实输出，Alpha/Beta 下各含子级 Configuration 段。
// 此前因 configSection 未正确退出，Status 和 Last error 行被跳过，导致前端显示"未知"。
const realListOutputWithNestedConfig = "" +
	"-------------------------------------------------------------------------------\n" +
	"Name: test-ali\n" +
	"Identifier: sync_ukoUvDbPVuOR6lXMfomXbGCayFkewnJDInL8uan9C3U\n" +
	"Configuration:\n" +
	"\tSynchronization mode: Two Way Resolved\n" +
	"\tHashing algorithm: Default (SHA-1)\n" +
	"\tSymbolic link mode: Ignore\n" +
	"\tIgnores:\n" +
	"\t\t*.crdownload\n" +
	"\t\t*.part\n" +
	"\t\t*.tmp\n" +
	"\t\t*.log.bak\n" +
	"\tIgnore VCS mode: Ignore\n" +
	"Alpha:\n" +
	"\tURL: C:\\ftp_test\n" +
	"\tConfiguration:\n" +
	"\t\tWatch mode: Default (Portable)\n" +
	"\t\tWatch polling interval: Default (10 seconds)\n" +
	"\t\tProbe mode: Default (Probe)\n" +
	"\t\tScan mode: Default (Accelerated)\n" +
	"\t\tStage mode: Default (Mutagen Data Directory)\n" +
	"\t\tFile mode: 0666\n" +
	"\t\tDirectory mode: 0777\n" +
	"\t\tDefault file/directory owner: Default\n" +
	"\t\tDefault file/directory group: Default\n" +
	"\tConnected: Yes\n" +
	"\tSynchronizable contents:\n" +
	"\t\t1 directory\n" +
	"\t\t0 files (0 B)\n" +
	"\t\t0 symbolic links\n" +
	"Beta:\n" +
	"\tURL: test-ali:/hmgdata/ftp_test\n" +
	"\tConfiguration:\n" +
	"\t\tWatch mode: Default (Portable)\n" +
	"\t\tWatch polling interval: Default (10 seconds)\n" +
	"\t\tProbe mode: Default (Probe)\n" +
	"\t\tScan mode: Default (Accelerated)\n" +
	"\t\tStage mode: Default (Mutagen Data Directory)\n" +
	"\t\tFile mode: 0666\n" +
	"\t\tDirectory mode: 0777\n" +
	"\t\tDefault file/directory owner: Default\n" +
	"\t\tDefault file/directory group: Default\n" +
	"\t\tCompression: Default (DEFLATE)\n" +
	"\tConnected: Yes\n" +
	"\tSynchronizable contents:\n" +
	"\t\t1 directory\n" +
	"\t\t0 files (0 B)\n" +
	"\t\t0 symbolic links\n" +
	"Status: Watching for changes\n" +
	"-------------------------------------------------------------------------------\n"

func TestParseStatusNestedConfig(t *testing.T) {
	e := &Executor{}
	tasks := e.ParseStatus(realListOutputWithNestedConfig)

	if len(tasks) != 1 {
		t.Fatalf("expected 1 session, got %d: %+v", len(tasks), tasks)
	}

	task := tasks[0]
	if task["name"] != "test-ali" {
		t.Errorf("name = %q, want %q", task["name"], "test-ali")
	}
	if task["status"] != "Watching for changes" {
		t.Errorf("status = %q, want %q", task["status"], "Watching for changes")
	}
	if task["alpha_url"] != "C:\\ftp_test" {
		t.Errorf("alpha_url = %q", task["alpha_url"])
	}
	if task["beta_url"] != "test-ali:/hmgdata/ftp_test" {
		t.Errorf("beta_url = %q", task["beta_url"])
	}
	// 根级 Configuration 的配置项不应被子级覆盖
	if task["config_synchronization_mode"] != "Two Way Resolved" {
		t.Errorf("config_synchronization_mode = %q", task["config_synchronization_mode"])
	}
	if task["config_symbolic_link_mode"] != "Ignore" {
		t.Errorf("config_symbolic_link_mode = %q", task["config_symbolic_link_mode"])
	}
	// Ignores 路径列表应完整收集
	ignores := task["config_ignores"]
	wantIgnores := "*.crdownload;*.part;*.tmp;*.log.bak"
	if ignores != wantIgnores {
		t.Errorf("config_ignores = %q, want %q", ignores, wantIgnores)
	}
	// last error 应为空（无错误时该行不出现）
	if le, ok := task["last error"]; ok && le != "" {
		t.Errorf("last error = %q, want empty", le)
	}
}

// realListOutputWithError 含 Last error 行，验证错误解析在嵌套 Configuration 后不被跳过。
const realListOutputWithError = "" +
	"-------------------------------------------------------------------------------\n" +
	"Name: broken-session\n" +
	"Identifier: sync_broken123\n" +
	"Configuration:\n" +
	"\tSynchronization mode: Two Way Resolved\n" +
	"\tSymbolic link mode: Ignore\n" +
	"\tIgnores: None\n" +
	"\tIgnore VCS mode: Ignore\n" +
	"Alpha:\n" +
	"\tURL: C:\\broken\n" +
	"\tConfiguration:\n" +
	"\t\tWatch mode: Default (Portable)\n" +
	"\tConnected: Yes\n" +
	"\tSynchronizable contents:\n" +
	"\t\t1 directory\n" +
	"Beta:\n" +
	"\tURL: test-remote:/broken\n" +
	"\tConfiguration:\n" +
	"\t\tWatch mode: Default (Portable)\n" +
	"\tConnected: No\n" +
	"\tSynchronizable contents:\n" +
	"\t\t1 directory\n" +
	"Status: Disconnected\n" +
	"Last error: unable to connect to beta: dial tcp: connection refused\n" +
	"-------------------------------------------------------------------------------\n"

func TestParseStatusWithError(t *testing.T) {
	e := &Executor{}
	tasks := e.ParseStatus(realListOutputWithError)

	if len(tasks) != 1 {
		t.Fatalf("expected 1 session, got %d: %+v", len(tasks), tasks)
	}

	task := tasks[0]
	if task["status"] != "Disconnected" {
		t.Errorf("status = %q, want %q", task["status"], "Disconnected")
	}
	wantErr := "unable to connect to beta: dial tcp: connection refused"
	if task["last error"] != wantErr {
		t.Errorf("last error = %q, want %q", task["last error"], wantErr)
	}
}

// realListOutputMalformed 模拟极端格式：Status/Last error 行被夹在有缩进的行中间
// （模拟 configSection 状态机混乱时的最坏场景），验证兜底 1 的无条件优先解析。
const realListOutputMalformed = "" +
	"-------------------------------------------------------------------------------\n" +
	"Name: edge-case\n" +
	"Identifier: sync_edge\n" +
	"Configuration:\n" +
	"\tSynchronization mode: Two Way Resolved\n" +
	"\t\tStatus: THIS_SHOULD_BE_IGNORED_inline_status\n" +
	"\tLast error: THIS_SHOULD_BE_IGNORED_inline_error\n" +
	"Alpha:\n" +
	"\tURL: C:\\ftp\n" +
	"Status: Reconnecting (backoff)\n" +
	"Beta:\n" +
	"\tURL: remote:/path\n" +
	"Last error: connection timed out after 30s\n" +
	"-------------------------------------------------------------------------------\n"

func TestParseStatusFallback(t *testing.T) {
	e := &Executor{}
	tasks := e.ParseStatus(realListOutputMalformed)

	if len(tasks) != 1 {
		t.Fatalf("expected 1 session, got %d: %+v", len(tasks), tasks)
	}

	task := tasks[0]
	// 兜底 1 应取到无缩进的顶层 Status/Last error，而不是 Configuration 内缩进的伪造行
	if task["status"] != "Reconnecting (backoff)" {
		t.Errorf("status = %q, want %q (fallback must take top-level)", task["status"], "Reconnecting (backoff)")
	}
	if task["last error"] != "connection timed out after 30s" {
		t.Errorf("last error = %q, want %q", task["last error"], "connection timed out after 30s")
	}
	// 基础字段不丢
	if task["name"] != "edge-case" {
		t.Errorf("name = %q", task["name"])
	}
	if task["alpha_url"] != "C:\\ftp" {
		t.Errorf("alpha_url = %q", task["alpha_url"])
	}
	if task["beta_url"] != "remote:/path" {
		t.Errorf("beta_url = %q", task["beta_url"])
	}
}

func TestNormalizeModes(t *testing.T) {
	// 显式模式归一化为 CLI 标志值
	syncCases := []struct{ in, want string }{
		{"Two Way Resolved", "two-way-resolved"},
		{"Two Way Safe", "two-way-safe"},
		{"One Way Replica", "one-way-replica"},
		{"two-way-resolved", "two-way-resolved"}, // 已是 canonical
		{"", ""},
		// 默认形式 -> 空串（不下发 --mode，使用 mutagen 默认），修复默认模式重建失败
		{"Default (Two Way Safe)", ""},
		{"Default (Two Way Resolved)", ""},
		{"default", ""},
		{"garbage value", ""},
	}
	for _, c := range syncCases {
		if got := normalizeMode(c.in); got != c.want {
			t.Errorf("normalizeMode(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	symlinkCases := []struct{ in, want string }{
		{"Portable", "portable"},
		{"Ignore", "ignore"},
		{"POSIX Raw", "posix-raw"},
		{"portable", "portable"},
		{"", ""},
		{"Default (Portable)", ""},
		{"Default (Ignore)", ""},
		{"bogus", ""},
	}
	for _, c := range symlinkCases {
		if got := normalizeSymlinkMode(c.in); got != c.want {
			t.Errorf("normalizeSymlinkMode(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
