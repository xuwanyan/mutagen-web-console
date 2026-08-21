package local

// [CUSTOM PATCH] Pre-transition backup engine.
//
// This file implements an opt-in backup that runs inside the local endpoint
// immediately before content is overwritten or deleted on disk (i.e. before
// core.Transition applies changes). Because it runs inside the endpoint, it
// backs up the machine whose files are about to change: when the Windows side
// is modified it backs up on Windows, and when the remote side is modified it
// backs up on the remote host. Configuration is read locally on each machine
// (environment variables, then ~/.mutagen/backup.json) because endpoint
// configuration is not transmitted across the network.

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mutagen-io/mutagen/pkg/filesystem"
	"github.com/mutagen-io/mutagen/pkg/logging"
	"github.com/mutagen-io/mutagen/pkg/synchronization/core"
)

// backupConfig holds the effective, per-endpoint backup settings.
type backupConfig struct {
	// enabled indicates whether pre-transition backup is active.
	enabled bool
	// dir is the absolute backup destination directory.
	dir string
	// retentionDays is the number of days of dated backups to retain. Values
	// less than or equal to zero disable retention-based pruning.
	retentionDays int
	// failOpen determines the behavior when a backup fails. When true, the
	// error is logged and the transition proceeds. When false, the affected
	// transition is skipped to protect the existing content.
	failOpen bool
	// sessionIdentifier is recorded in the manifest for auditing. It is not a
	// configuration input and is populated by the endpoint.
	sessionIdentifier string
}

// backupFileSettings is the JSON schema for ~/.mutagen/backup.json. Pointers
// are used so that omitted fields fall back to defaults rather than zero
// values.
type backupFileSettings struct {
	Enabled       *bool  `json:"enabled"`
	Dir           string `json:"dir"`
	RetentionDays *int   `json:"retentionDays"`
	FailOpen      *bool  `json:"failOpen"`
}

const (
	// backupDateLayout is the layout for dated backup subdirectories.
	backupDateLayout = "2006-01-02"
	// backupSettingsFileName is the name of the local backup settings file
	// inside the Mutagen data directory.
	backupSettingsFileName = "backup.json"
	// defaultBackupRetentionDays is the default number of days to retain
	// backups when not otherwise specified.
	defaultBackupRetentionDays = 7
	// backupDirectorySuffix is appended to the synchronization root's base name
	// to compute the default (sibling) backup directory.
	backupDirectorySuffix = ".mutagen-backup"
)

// loadBackupConfig computes the effective backup configuration for a
// synchronization root. Settings are read from ~/.mutagen/backup.json first and
// then overridden by any MUTAGEN_BACKUP_* environment variables. When enabled
// without an explicit directory, the backup directory defaults to a sibling of
// the synchronization root (so that it lies outside the root and is not itself
// synchronized). If the resolved directory lies inside the synchronization
// root, backup is disabled to avoid recursive synchronization of backups.
func loadBackupConfig(root string, logger *logging.Logger) backupConfig {
	logf := func(format string, v ...any) {
		if logger != nil {
			logger.Errorf(format, v...)
		}
	}

	// Start from defaults.
	cfg := backupConfig{
		enabled:       false,
		retentionDays: defaultBackupRetentionDays,
		failOpen:      true,
	}

	// Layer in settings from the local settings file, if present.
	if path, err := filesystem.Mutagen(false, backupSettingsFileName); err == nil {
		if data, rerr := os.ReadFile(path); rerr == nil {
			var settings backupFileSettings
			if jerr := json.Unmarshal(data, &settings); jerr != nil {
				logf("backup: unable to parse %s: %v", path, jerr)
			} else {
				if settings.Enabled != nil {
					cfg.enabled = *settings.Enabled
				}
				if settings.Dir != "" {
					cfg.dir = settings.Dir
				}
				if settings.RetentionDays != nil {
					cfg.retentionDays = *settings.RetentionDays
				}
				if settings.FailOpen != nil {
					cfg.failOpen = *settings.FailOpen
				}
			}
		}
	}

	// Layer in environment variable overrides.
	if v, ok := os.LookupEnv("MUTAGEN_BACKUP_ENABLED"); ok {
		cfg.enabled = parseBoolDefault(v, cfg.enabled)
	}
	if v, ok := os.LookupEnv("MUTAGEN_BACKUP_DIR"); ok && strings.TrimSpace(v) != "" {
		cfg.dir = v
	}
	if v, ok := os.LookupEnv("MUTAGEN_BACKUP_RETENTION_DAYS"); ok {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			cfg.retentionDays = n
		}
	}
	if v, ok := os.LookupEnv("MUTAGEN_BACKUP_FAIL_OPEN"); ok {
		cfg.failOpen = parseBoolDefault(v, cfg.failOpen)
	}

	// If backup is disabled, there's nothing further to compute.
	if !cfg.enabled {
		return cfg
	}

	// Compute the backup directory.
	if strings.TrimSpace(cfg.dir) == "" {
		// No directory specified: default to a sibling of the synchronization
		// root. This already isolates each root, so no further subdivision is
		// needed.
		cleanRoot := filepath.Clean(root)
		cfg.dir = filepath.Join(
			filepath.Dir(cleanRoot),
			filepath.Base(cleanRoot)+backupDirectorySuffix,
		)
	} else {
		// An explicit directory was provided (backup.json or environment). Since
		// this same base directory is shared by every session on the machine,
		// append a per-root subdirectory derived from the synchronization root's
		// path so that multiple tasks do not write colliding relative paths into
		// one another. [CUSTOM PATCH]
		if sub := rootBackupSubdir(root); sub != "" {
			cfg.dir = filepath.Join(cfg.dir, sub)
		}
	}

	// Normalize the backup directory to an absolute path where possible.
	if abs, err := filepath.Abs(cfg.dir); err == nil {
		cfg.dir = abs
	}

	// Refuse to enable backup if the destination lies inside the
	// synchronization root, since that would cause backups to be synchronized.
	if backupDirInsideRoot(root, cfg.dir) {
		logf("backup: backup directory %q is inside synchronization root %q; disabling backup", cfg.dir, root)
		cfg.enabled = false
	}

	return cfg
}

// parseBoolDefault parses a boolean-like string, returning fallback on failure.
func parseBoolDefault(v string, fallback bool) bool {
	b, err := strconv.ParseBool(strings.TrimSpace(v))
	if err != nil {
		return fallback
	}
	return b
}

// backupDirInsideRoot reports whether dir is equal to or nested within root.
func backupDirInsideRoot(root, dir string) bool {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		absRoot = filepath.Clean(root)
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		absDir = filepath.Clean(dir)
	}
	rel, err := filepath.Rel(absRoot, absDir)
	if err != nil {
		// Paths on different volumes (Windows) are not relative and thus not
		// nested.
		return false
	}
	if rel == "." {
		return true
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return false
	}
	return true
}

// rootBackupSubdir derives a filesystem-safe subdirectory that uniquely
// identifies a synchronization root. It is appended to an explicitly configured
// backup base directory so that multiple synchronization tasks sharing one base
// directory are stored separately.
//
// The layout is the flattened path with separators replaced by "_" and the
// volume/drive letter stripped, suffixed with a 4-character FNV-1a hash to
// guarantee uniqueness even when two different paths flatten to the same string
// (e.g. "a_b/c" and "a/b_c"). For example:
//
//	Windows: C:\ImpPath\Nems\InBox → ImpPath_Nems_InBox_a1b2
//	Linux:   /srv/ftp/SW/sw4/Nems/InBox → srv_ftp_SW_sw4_Nems_InBox_c3d4
//
// This produces a single directory level that is self-documenting and avoids
// Windows MAX_PATH issues. [CUSTOM PATCH]
func rootBackupSubdir(root string) string {
	clean := filepath.Clean(root)

	// Strip the volume name (drive letter on Windows, e.g. "C:").
	rest := clean[len(filepath.VolumeName(clean)):]
	rest = strings.TrimLeft(rest, `\/`)

	// Split by OS separator, filter empty, join with "_".
	parts := strings.FieldsFunc(rest, func(r rune) bool {
		return r == '/' || r == '\\'
	})
	if len(parts) == 0 {
		parts = append(parts, "root")
	}
	flat := strings.Join(parts, "_")

	// Append a short hash to guarantee uniqueness across all sync roots.
	hash := fnv1aHex(clean)
	if len(hash) > 4 {
		hash = hash[:4]
	}
	return flat + "_" + hash
}

// fnv1aHex returns the 64-bit FNV-1a hash of s as a lowercase hexadecimal
// string. It is used to derive a short, collision-resistant suffix for the
// backup subdirectory name.
func fnv1aHex(s string) string {
	const (
		offset64 uint64 = 0xcbf29ce484222325
		prime64  uint64 = 0x100000001b3
	)
	h := offset64
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= prime64
	}
	return strconv.FormatUint(h, 16)
}

// backupEventSeq is a process-wide monotonic counter used to guarantee that
// per-event backup subdirectory names are unique even when two transition
// batches occur within the same millisecond.
var backupEventSeq atomic.Uint64

// newBackupEventStamp returns a unique, sortable subdirectory name identifying a
// single backup event (one transition batch), of the form "150405.000-<seq>".
func newBackupEventStamp() string {
	seq := backupEventSeq.Add(1)
	return fmt.Sprintf("%s-%d", time.Now().Format("150405.000"), seq)
}

// backupFailCooldown is the window during which a path that just failed to
// back up is not retried, to avoid burning IO and flooding problems on every
// scan cycle when a path fails permanently (locked, over-long, etc.). The
// transition is still skipped in fail-closed mode within this window, so the
// existing content remains protected; only the copy attempt is suppressed.
const backupFailCooldown = 60 * time.Second

var (
	// backupFailMu guards backupFailRecent.
	backupFailMu sync.Mutex
	// backupFailRecent records, per absolute path, the time of the most recent
	// backup failure. A path is removed as soon as it backs up successfully.
	backupFailRecent = make(map[string]time.Time)
)

// backupCooldownSkip returns a non-empty reason string if absPath failed to
// back up within the cooldown window and should be skipped, otherwise "".
func backupCooldownSkip(absPath string) string {
	backupFailMu.Lock()
	last, ok := backupFailRecent[absPath]
	backupFailMu.Unlock()
	if !ok {
		return ""
	}
	if elapsed := time.Since(last); elapsed < backupFailCooldown {
		return "backup skipped: repeated failure within " + backupFailCooldown.String() +
			" (last " + elapsed.Round(time.Second).String() + " ago)"
	}
	return ""
}

// backupRecordFailure marks absPath as having just failed to back up.
func backupRecordFailure(absPath string) {
	backupFailMu.Lock()
	backupFailRecent[absPath] = time.Now()
	backupFailMu.Unlock()
}

// backupClearFailure removes absPath from the recent-failure map after a
// successful backup so it is eligible for immediate retry on the next cycle.
func backupClearFailure(absPath string) {
	backupFailMu.Lock()
	delete(backupFailRecent, absPath)
	backupFailMu.Unlock()
}

// classifyChange determines the operation kind for a change based on the
// presence of its old and new content.
func classifyChange(change *core.Change) string {
	switch {
	case change.Old == nil && change.New != nil:
		return "create"
	case change.Old != nil && change.New != nil:
		return "modify"
	case change.Old != nil && change.New == nil:
		return "delete"
	default:
		return "unknown"
	}
}

// changeKind reports whether the entry involved in a change is a directory or a
// file. It describes the backed-up content, so it prefers the pre-change entry
// (Old) for modify/delete and falls back to the new entry for create.
// Symbolic links (and anything that is not a directory) are reported as "file"
// to keep the manifest "type" column to two values. [CUSTOM PATCH]
func changeKind(change *core.Change) string {
	entry := change.Old
	if entry == nil {
		entry = change.New
	}
	if entry != nil && entry.Kind == core.EntryKind_Directory {
		return "dir"
	}
	return "file"
}

// backupBeforeTransition backs up the on-disk content that is about to be
// modified or deleted by the provided transitions. Created content has no
// existing on-disk data and is recorded in the manifest as a marker only. The
// returned map contains, for each transition whose backup failed, the relative
// path mapped to the failure message; callers that operate in fail-closed mode
// use it to skip the corresponding transitions.
func backupBeforeTransition(root string, transitions []*core.Change, cfg backupConfig, logger *logging.Logger) map[string]string {
	logf := func(format string, v ...any) {
		if logger != nil {
			logger.Errorf(format, v...)
		}
	}

	if !cfg.enabled || strings.TrimSpace(cfg.dir) == "" {
		return nil
	}

	day := time.Now().Format(backupDateLayout)
	// Each call corresponds to a single transition batch (one sync event). All
	// files changed in this event are stored under a unique per-event
	// subdirectory (below the operation directory) so that repeated changes to
	// the same path are retained side-by-side rather than overwriting one
	// another. [CUSTOM PATCH]
	eventStamp := newBackupEventStamp()
	failures := make(map[string]string)
	manifestLines := make([]string, 0, len(transitions))

	for _, change := range transitions {
		if change == nil {
			continue
		}
		op := classifyChange(change)
		relPath := change.Path
		kind := changeKind(change)
		// Absolute on-disk path of the synced entry, using the endpoint's native
		// separators so the manifest is directly usable for recovery on this
		// machine and stays unambiguous even if logs are merged. [CUSTOM PATCH]
		absPath := filepath.Join(root, filepath.FromSlash(relPath))

		var bytesCopied int64
		if op == "modify" || op == "delete" {
			// Layout: <day>/<op>/<event>/<relative path>. Grouping by operation
			// first places every version of a file under a single op directory
			// (e.g. all "modify" backups together), with time-sortable event
			// subdirectories, so the earliest "modify" event holds the original
			// pre-change content. [CUSTOM PATCH]
			dest := filepath.Join(cfg.dir, day, op, eventStamp, filepath.FromSlash(relPath))
			// Short-circuit paths that failed very recently: a permanently
			// failing path (locked / over-long on Windows) would otherwise be
			// retried on every scan cycle, burning IO and flooding problems.
			// The transition is still skipped in fail-closed mode (data stays
			// protected); we only avoid re-attempting the copy within the
			// cooldown window. [CUSTOM PATCH]
			if msg := backupCooldownSkip(absPath); msg != "" {
				failures[relPath] = msg
				continue
			}
			n, err := backupCopyPath(absPath, dest)
			if err != nil {
				backupRecordFailure(absPath)
				logf("backup: failed to back up %q (%s): %v", absPath, op, err)
				failures[relPath] = err.Error()
				continue
			}
			backupClearFailure(absPath)
			bytesCopied = n
		}

		// Manifest columns: time, event, op, type(dir|file), absolute path,
		// bytes, session. [CUSTOM PATCH]
		manifestLines = append(manifestLines, fmt.Sprintf("%s\t%s\t%s\t%s\t%s\t%d\t%s",
			time.Now().Format(time.RFC3339), eventStamp, op, kind, absPath, bytesCopied, cfg.sessionIdentifier))
	}

	appendManifest(cfg.dir, day, manifestLines, logger)
	pruneOldBackups(cfg.dir, cfg.retentionDays, logger)

	return failures
}

// backupCopyPath copies the file, directory tree, or symbolic link at src to
// dest, returning the total number of content bytes copied. A missing source
// is treated as an empty backup (no error).
func backupCopyPath(src, dest string) (int64, error) {
	info, err := os.Lstat(src)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}

	if info.Mode()&os.ModeSymlink != 0 {
		return backupSymlink(src, dest)
	}

	if info.IsDir() {
		var total int64
		walkErr := filepath.WalkDir(src, func(p string, d os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			rel, rerr := filepath.Rel(src, p)
			if rerr != nil {
				return rerr
			}
			target := filepath.Join(dest, rel)
			if d.IsDir() {
				return os.MkdirAll(target, 0o755)
			}
			fi, ferr := d.Info()
			if ferr != nil {
				return ferr
			}
			if fi.Mode()&os.ModeSymlink != 0 {
				n, serr := backupSymlink(p, target)
				total += n
				return serr
			}
			if !fi.Mode().IsRegular() {
				// Skip special files (devices, sockets, pipes).
				return nil
			}
			n, cerr := copyFile(p, target, fi.Mode())
			total += n
			return cerr
		})
		return total, walkErr
	}

	if !info.Mode().IsRegular() {
		return 0, nil
	}
	return copyFile(src, dest, info.Mode())
}

// backupSymlink backs up a symbolic link. It attempts to recreate the link at
// dest and, failing that, records the link target in a sidecar file.
func backupSymlink(src, dest string) (int64, error) {
	target, err := os.Readlink(src)
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return 0, err
	}
	if err := os.Symlink(target, dest); err != nil {
		if werr := os.WriteFile(dest+".symlink", []byte(target), 0o644); werr != nil {
			return 0, werr
		}
	}
	return int64(len(target)), nil
}

// copyFile copies a single regular file, creating parent directories as needed.
func copyFile(src, dest string, mode os.FileMode) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return 0, err
	}
	in, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer in.Close()
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode.Perm())
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	return n, err
}

// appendManifest appends the provided manifest lines to the dated manifest log.
func appendManifest(dir, day string, lines []string, logger *logging.Logger) {
	if len(lines) == 0 {
		return
	}
	logf := func(format string, v ...any) {
		if logger != nil {
			logger.Errorf(format, v...)
		}
	}
	manifestPath := filepath.Join(dir, day, "manifest.log")
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0o755); err != nil {
		logf("backup: unable to create backup directory: %v", err)
		return
	}
	f, err := os.OpenFile(manifestPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		logf("backup: unable to open manifest: %v", err)
		return
	}
	defer f.Close()
	for _, line := range lines {
		if _, err := f.WriteString(line + "\n"); err != nil {
			logf("backup: unable to write manifest: %v", err)
			return
		}
	}
}

var (
	// pruneMu guards pruneLastDay.
	pruneMu sync.Mutex
	// pruneLastDay tracks, per backup directory, the last day on which pruning
	// ran, so that pruning happens at most once per day per directory within a
	// process.
	pruneLastDay = make(map[string]string)
)

// pruneOldBackups removes dated backup subdirectories older than retentionDays.
// It runs at most once per day per directory to avoid per-cycle overhead.
func pruneOldBackups(dir string, retentionDays int, logger *logging.Logger) {
	if retentionDays <= 0 {
		return
	}

	today := time.Now().Format(backupDateLayout)
	pruneMu.Lock()
	if pruneLastDay[dir] == today {
		pruneMu.Unlock()
		return
	}
	pruneLastDay[dir] = today
	pruneMu.Unlock()

	logf := func(format string, v ...any) {
		if logger != nil {
			logger.Errorf(format, v...)
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if !os.IsNotExist(err) {
			logf("backup: unable to read backup directory for pruning: %v", err)
		}
		return
	}

	now := time.Now()
	cutoff := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).
		AddDate(0, 0, -retentionDays)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		day, perr := time.Parse(backupDateLayout, entry.Name())
		if perr != nil {
			continue
		}
		if day.Before(cutoff) {
			if err := os.RemoveAll(filepath.Join(dir, entry.Name())); err != nil {
				logf("backup: unable to prune old backup %q: %v", entry.Name(), err)
			}
		}
	}
}
