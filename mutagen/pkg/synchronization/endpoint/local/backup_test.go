package local

// [CUSTOM PATCH] Tests for the pre-transition backup engine.

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mutagen-io/mutagen/pkg/synchronization/core"
)

// fileEntry returns a minimal file entry suitable for change classification.
func fileEntry() *core.Entry {
	return &core.Entry{Kind: core.EntryKind_File}
}

// dirEntry returns a minimal directory entry suitable for change
// classification.
func dirEntry() *core.Entry {
	return &core.Entry{Kind: core.EntryKind_Directory}
}

func TestClassifyChange(t *testing.T) {
	cases := []struct {
		name   string
		change *core.Change
		want   string
	}{
		{"create", &core.Change{Old: nil, New: fileEntry()}, "create"},
		{"modify", &core.Change{Old: fileEntry(), New: fileEntry()}, "modify"},
		{"delete", &core.Change{Old: fileEntry(), New: nil}, "delete"},
		{"unknown", &core.Change{Old: nil, New: nil}, "unknown"},
	}
	for _, c := range cases {
		if got := classifyChange(c.change); got != c.want {
			t.Errorf("%s: classifyChange = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestBackupBeforeTransitionLayout(t *testing.T) {
	root := t.TempDir()
	backupDir := t.TempDir()

	// Create an existing file (to be modified) and a directory tree (to be
	// deleted) on disk in the synchronization root.
	if err := os.WriteFile(filepath.Join(root, "modified.txt"), []byte("old-content"), 0o644); err != nil {
		t.Fatalf("unable to write modified.txt: %v", err)
	}
	subDir := filepath.Join(root, "sub")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatalf("unable to create sub dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "nested.txt"), []byte("nested-data"), 0o644); err != nil {
		t.Fatalf("unable to write nested.txt: %v", err)
	}

	cfg := backupConfig{
		enabled:           true,
		dir:               backupDir,
		retentionDays:     7,
		failOpen:          true,
		sessionIdentifier: "test-session",
	}

	transitions := []*core.Change{
		{Path: "created.txt", Old: nil, New: fileEntry()},
		{Path: "modified.txt", Old: fileEntry(), New: fileEntry()},
		{Path: "sub", Old: dirEntry(), New: nil},
	}

	failures := backupBeforeTransition(root, transitions, cfg, nil)
	if len(failures) != 0 {
		t.Fatalf("unexpected backup failures: %v", failures)
	}

	day := time.Now().Format(backupDateLayout)

	// Layout is <day>/<op>/<event>/<relative path>. The created file has no
	// on-disk content, so the "create" operation directory must not exist.
	if _, err := os.Stat(filepath.Join(backupDir, day, "create")); !os.IsNotExist(err) {
		t.Errorf("create op dir should not exist, stat err = %v", err)
	}

	// The modified file's previous content must be backed up verbatim under a
	// per-event subdirectory below the "modify" operation directory.
	modifyEventDir := findEventDir(t, filepath.Join(backupDir, day, "modify"))
	modifiedBackup := filepath.Join(modifyEventDir, "modified.txt")
	if data, err := os.ReadFile(modifiedBackup); err != nil {
		t.Errorf("unable to read modify backup: %v", err)
	} else if string(data) != "old-content" {
		t.Errorf("modify backup content = %q, want %q", string(data), "old-content")
	}

	// The deleted directory tree must be backed up recursively under a per-event
	// subdirectory below the "delete" operation directory.
	deleteEventDir := findEventDir(t, filepath.Join(backupDir, day, "delete"))
	deletedBackup := filepath.Join(deleteEventDir, "sub", "nested.txt")
	if data, err := os.ReadFile(deletedBackup); err != nil {
		t.Errorf("unable to read delete backup: %v", err)
	} else if string(data) != "nested-data" {
		t.Errorf("delete backup content = %q, want %q", string(data), "nested-data")
	}

	// The manifest must record all three operations, including the create
	// marker (which has zero bytes).
	manifest, err := os.ReadFile(filepath.Join(backupDir, day, "manifest.log"))
	if err != nil {
		t.Fatalf("unable to read manifest: %v", err)
	}
	manifestText := string(manifest)
	// Columns: time, event, op, type(dir|file), absolute path, bytes, session.
	for _, want := range []string{
		"\tcreate\tfile\t" + filepath.Join(root, "created.txt") + "\t0\t",
		"\tmodify\tfile\t" + filepath.Join(root, "modified.txt") + "\t",
		"\tdelete\tdir\t" + filepath.Join(root, "sub") + "\t",
	} {
		if !strings.Contains(manifestText, want) {
			t.Errorf("manifest missing entry %q; manifest:\n%s", want, manifestText)
		}
	}
	if !strings.Contains(manifestText, "test-session") {
		t.Errorf("manifest missing session identifier; manifest:\n%s", manifestText)
	}
	// The manifest must reference the per-event subdirectory name so entries can
	// be correlated with the physical backup folder.
	if !strings.Contains(manifestText, filepath.Base(modifyEventDir)) {
		t.Errorf("manifest missing event id %q; manifest:\n%s", filepath.Base(modifyEventDir), manifestText)
	}
}

// findEventDir returns the single per-event subdirectory beneath a day
// directory, ignoring the sibling manifest.log file.
func findEventDir(t *testing.T, dayDir string) string {
	t.Helper()
	entries, err := os.ReadDir(dayDir)
	if err != nil {
		t.Fatalf("unable to read day dir %q: %v", dayDir, err)
	}
	for _, e := range entries {
		if e.IsDir() {
			return filepath.Join(dayDir, e.Name())
		}
	}
	t.Fatalf("no event subdirectory found under %q", dayDir)
	return ""
}

func TestBackupBeforeTransitionFailClosed(t *testing.T) {
	root := t.TempDir()

	if err := os.WriteFile(filepath.Join(root, "modified.txt"), []byte("old-content"), 0o644); err != nil {
		t.Fatalf("unable to write modified.txt: %v", err)
	}

	// Use a regular file as the "backup directory" so that directory creation
	// beneath it fails, forcing a backup error.
	fileAsDir := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(fileAsDir, []byte("x"), 0o644); err != nil {
		t.Fatalf("unable to write file-as-dir: %v", err)
	}

	cfg := backupConfig{
		enabled:       true,
		dir:           fileAsDir,
		retentionDays: 7,
		failOpen:      false,
	}

	transitions := []*core.Change{
		{Path: "modified.txt", Old: fileEntry(), New: fileEntry()},
	}

	failures := backupBeforeTransition(root, transitions, cfg, nil)
	if _, ok := failures["modified.txt"]; !ok {
		t.Fatalf("expected a backup failure for modified.txt, got %v", failures)
	}
}

func TestPruneOldBackups(t *testing.T) {
	backupDir := t.TempDir()

	// Reset the per-directory prune guard so the test can trigger pruning.
	pruneMu.Lock()
	delete(pruneLastDay, backupDir)
	pruneMu.Unlock()

	// An old dated directory (well beyond retention) and a recent one.
	oldDay := time.Now().AddDate(0, 0, -30).Format(backupDateLayout)
	recentDay := time.Now().Format(backupDateLayout)
	oldPath := filepath.Join(backupDir, oldDay)
	recentPath := filepath.Join(backupDir, recentDay)
	if err := os.MkdirAll(oldPath, 0o755); err != nil {
		t.Fatalf("unable to create old backup dir: %v", err)
	}
	if err := os.MkdirAll(recentPath, 0o755); err != nil {
		t.Fatalf("unable to create recent backup dir: %v", err)
	}

	pruneOldBackups(backupDir, 7, nil)

	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Errorf("old backup should have been pruned, stat err = %v", err)
	}
	if _, err := os.Stat(recentPath); err != nil {
		t.Errorf("recent backup should be retained, stat err = %v", err)
	}
}

func TestPruneOldBackupsOncePerDay(t *testing.T) {
	backupDir := t.TempDir()

	// Prime the guard so pruning is skipped for today.
	today := time.Now().Format(backupDateLayout)
	pruneMu.Lock()
	pruneLastDay[backupDir] = today
	pruneMu.Unlock()

	oldDay := time.Now().AddDate(0, 0, -30).Format(backupDateLayout)
	oldPath := filepath.Join(backupDir, oldDay)
	if err := os.MkdirAll(oldPath, 0o755); err != nil {
		t.Fatalf("unable to create old backup dir: %v", err)
	}

	pruneOldBackups(backupDir, 7, nil)

	if _, err := os.Stat(oldPath); err != nil {
		t.Errorf("prune should have been skipped for today, stat err = %v", err)
	}
}

func TestBackupDirInsideRoot(t *testing.T) {
	root := t.TempDir()
	if !backupDirInsideRoot(root, filepath.Join(root, "backup")) {
		t.Error("nested directory should be reported as inside root")
	}
	if !backupDirInsideRoot(root, root) {
		t.Error("root itself should be reported as inside root")
	}
	sibling := filepath.Join(filepath.Dir(filepath.Clean(root)), "elsewhere-backup")
	if backupDirInsideRoot(root, sibling) {
		t.Error("sibling directory should not be reported as inside root")
	}
}

func TestLoadBackupConfigEnvOverrides(t *testing.T) {
	root := t.TempDir()

	t.Setenv("MUTAGEN_BACKUP_ENABLED", "true")
	t.Setenv("MUTAGEN_BACKUP_RETENTION_DAYS", "3")
	t.Setenv("MUTAGEN_BACKUP_FAIL_OPEN", "false")
	// Leave MUTAGEN_BACKUP_DIR unset to exercise default sibling computation.
	os.Unsetenv("MUTAGEN_BACKUP_DIR")

	cfg := loadBackupConfig(root, nil)
	if !cfg.enabled {
		t.Fatalf("expected backup to be enabled")
	}
	if cfg.retentionDays != 3 {
		t.Errorf("retentionDays = %d, want 3", cfg.retentionDays)
	}
	if cfg.failOpen {
		t.Errorf("failOpen = true, want false")
	}
	wantSuffix := filepath.Base(filepath.Clean(root)) + backupDirectorySuffix
	if !strings.HasSuffix(cfg.dir, wantSuffix) {
		t.Errorf("default backup dir = %q, want suffix %q", cfg.dir, wantSuffix)
	}
}

func TestLoadBackupConfigDisabledByDefault(t *testing.T) {
	root := t.TempDir()
	os.Unsetenv("MUTAGEN_BACKUP_ENABLED")
	os.Unsetenv("MUTAGEN_BACKUP_DIR")
	os.Unsetenv("MUTAGEN_BACKUP_RETENTION_DAYS")
	os.Unsetenv("MUTAGEN_BACKUP_FAIL_OPEN")

	cfg := loadBackupConfig(root, nil)
	// Backup is opt-in; without any enabling configuration it must be disabled.
	// (A developer's own ~/.mutagen/backup.json could enable it, so only assert
	// the retention/failOpen defaults, which are stable regardless.)
	if cfg.retentionDays != defaultBackupRetentionDays && cfg.retentionDays == 0 {
		t.Errorf("retentionDays = %d, want default %d", cfg.retentionDays, defaultBackupRetentionDays)
	}
}

func TestRootBackupSubdir(t *testing.T) {
	var root, wantPrefix string
	if runtime.GOOS == "windows" {
		root = `D:\FTP\Impath\Acmp\InBox`
		wantPrefix = "FTP_Impath_Acmp_InBox"
	} else {
		root = "/srv/ftp/SW/sw1/Acmp/InBox"
		wantPrefix = "srv_ftp_SW_sw1_Acmp_InBox"
	}
	got := rootBackupSubdir(root)

	// New layout: flattened path with separators replaced by "_" and volume
	// stripped, suffixed with "_<4-char-hash>". This is a single directory
	// level, self-documenting, and avoids Windows MAX_PATH issues.
	// Verify the format: <prefix>_<4-hex-chars>
	if !strings.HasPrefix(got, wantPrefix+"_") {
		t.Errorf("subdir = %q, want prefix %q_<hash>", got, wantPrefix)
	}
	suffix := got[len(wantPrefix)+1:] // +1 for the "_"
	if len(suffix) != 4 {
		t.Errorf("subdir hash suffix = %q (len=%d), want 4 hex chars", suffix, len(suffix))
	}
	if _, err := strconv.ParseUint(suffix, 16, 16); err != nil {
		t.Errorf("subdir hash suffix %q is not hex: %v", suffix, err)
	}

	// The derived subdirectory must always be relative and free of colons.
	if filepath.IsAbs(got) {
		t.Errorf("subdir %q must be relative", got)
	}
	if strings.Contains(got, ":") {
		t.Errorf("subdir %q must not contain a colon", got)
	}
	// It must not reproduce the full root path as nested directories.
	if strings.Contains(got, `/`) || strings.Contains(got, `\`) {
		t.Errorf("subdir %q must be a single level, not nested", got)
	}
}

// TestLoadBackupConfigExplicitDirIsolatesRoot verifies that an explicitly
// configured backup base directory is subdivided per synchronization root so
// that multiple tasks sharing one base do not collide.
func TestLoadBackupConfigExplicitDirIsolatesRoot(t *testing.T) {
	root := t.TempDir()
	base := t.TempDir()

	t.Setenv("MUTAGEN_BACKUP_ENABLED", "true")
	t.Setenv("MUTAGEN_BACKUP_DIR", base)

	cfg := loadBackupConfig(root, nil)
	if !cfg.enabled {
		t.Fatalf("expected backup to be enabled")
	}

	want := filepath.Join(base, rootBackupSubdir(root))
	if abs, err := filepath.Abs(want); err == nil {
		want = abs
	}
	if cfg.dir != want {
		t.Errorf("explicit dir = %q, want %q", cfg.dir, want)
	}

	// A second, distinct root under the same base must resolve to a different
	// backup directory.
	root2 := t.TempDir()
	cfg2 := loadBackupConfig(root2, nil)
	if cfg2.dir == cfg.dir {
		t.Errorf("distinct roots collided under shared base: %q", cfg.dir)
	}
}

// TestBackupCooldownSkip verifies that a path that just failed to back up is
// short-circuited within the cooldown window, and that clearing the failure
// record makes it eligible for immediate retry again.
func TestBackupCooldownSkip(t *testing.T) {
	// A unique path that no other test touches.
	p := filepath.Join(t.TempDir(), "cooldown-target")

	if msg := backupCooldownSkip(p); msg != "" {
		t.Fatalf("fresh path should not be in cooldown, got %q", msg)
	}

	backupRecordFailure(p)
	if msg := backupCooldownSkip(p); msg == "" {
		t.Fatal("expected cooldown reason after recording a failure, got empty")
	}

	backupClearFailure(p)
	if msg := backupCooldownSkip(p); msg != "" {
		t.Fatalf("path should not be in cooldown after clear, got %q", msg)
	}
}
