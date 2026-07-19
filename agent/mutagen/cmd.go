package mutagen

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

type Executor struct {
	mutagenPath string
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
	return e.exec("sync", "list")
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

// ParseStatus 解析 mutagen sync list 输出，提取状态和路径
func (e *Executor) ParseStatus(output string) []map[string]string {
	var tasks []map[string]string
	var current map[string]string
	var section string

	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			if current != nil {
				tasks = append(tasks, current)
				current = nil
				section = ""
			}
			continue
		}
		if current == nil {
			current = make(map[string]string)
			section = ""
		}
		lower := strings.ToLower(trimmed)
		if lower == "alpha:" || lower == "beta:" {
			section = lower[:len(lower)-1]
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
	if current != nil {
		tasks = append(tasks, current)
	}
	return tasks
}

func OS() string {
	return runtime.GOOS
}