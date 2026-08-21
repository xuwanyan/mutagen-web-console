package main

import (
	"encoding/json"
	"flag"
	"log"
	"os"
	"path/filepath"

	"mutagen-web/agent/client"
)

type AgentConfig struct {
	ServerURL   string `json:"server"`
	Token       string `json:"token"`
	MachineID   string `json:"machineId"`
	Name        string `json:"name"`
	RegisterKey string `json:"registerKey,omitempty"` // 服务端配置 -register-key 时需携带
	// Backup, when present, is written to ~/.mutagen/backup.json on startup so
	// that the custom mutagen build performs pre-transition backups on this
	// machine. See mutagen pkg/synchronization/endpoint/local/backup.go.
	Backup *BackupConfig `json:"backup,omitempty"`
}

// BackupConfig mirrors the schema of ~/.mutagen/backup.json read by the custom
// mutagen build. Pointers are used so that omitted fields are not written and
// thus fall back to mutagen's defaults.
type BackupConfig struct {
	Enabled       *bool  `json:"enabled,omitempty"`
	Dir           string `json:"dir,omitempty"`
	RetentionDays *int   `json:"retentionDays,omitempty"`
	FailOpen      *bool  `json:"failOpen,omitempty"`
}

func loadConfig(path string) (*AgentConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg AgentConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func saveConfig(path string, cfg *AgentConfig) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// writeBackupSettings materializes the agent's backup configuration into
// ~/.mutagen/backup.json so that the custom mutagen daemon on this machine can
// read it locally (endpoint configuration is not transmitted across the
// network). It is a no-op when no backup section is configured.
func writeBackupSettings(cfg *AgentConfig) error {
	if cfg.Backup == nil {
		return nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	dir := filepath.Join(home, ".mutagen")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg.Backup, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "backup.json"), data, 0644)
}

func main() {
	var (
		configPath  = flag.String("config", "", "config file path")
		serverURL   = flag.String("server", "ws://localhost:8080/ws/agent", "server websocket url")
		token       = flag.String("token", "", "machine token")
		machineID   = flag.String("machine-id", "", "machine id")
		name        = flag.String("name", "", "machine name (for auto register)")
		registerKey = flag.String("register-key", "", "register key for auto-registration (required when server has -register-key set)")
		logFile     = flag.String("log", "", "log file path (default: C:\\mutagen\\agent.log)")
	)
	flag.Parse()

	// 日志重定向到文件
	logPath := *logFile
	if logPath == "" {
		logPath = "C:\\mutagen\\agent.log"
	}
	if f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); err == nil {
		log.SetOutput(f)
		log.SetFlags(log.Ltime | log.Lshortfile)
	}

	cfg := &AgentConfig{
		ServerURL:   *serverURL,
		Token:       *token,
		MachineID:   *machineID,
		Name:        *name,
		RegisterKey: *registerKey,
	}

	// 确定配置文件路径：显式指定的优先，否则用 exe 同目录的 agent-config.json
	resolvedConfigPath := *configPath
	if resolvedConfigPath == "" {
		if exe, err := os.Executable(); err == nil {
			resolvedConfigPath = filepath.Join(filepath.Dir(exe), "agent-config.json")
		}
	}

	if loaded, err := loadConfig(resolvedConfigPath); err == nil {
		if loaded.ServerURL != "" {
			cfg.ServerURL = loaded.ServerURL
		}
		if loaded.Token != "" {
			cfg.Token = loaded.Token
		}
		if loaded.MachineID != "" {
			cfg.MachineID = loaded.MachineID
		}
		if loaded.Name != "" {
			cfg.Name = loaded.Name
		}
		if loaded.RegisterKey != "" {
			cfg.RegisterKey = loaded.RegisterKey
		}
		if loaded.Backup != nil {
			cfg.Backup = loaded.Backup
		}
	} else if *configPath != "" {
		log.Fatalf("load config failed: %v", err)
	}

	// Materialize backup settings for the local mutagen daemon (if configured).
	if err := writeBackupSettings(cfg); err != nil {
		log.Printf("warning: unable to write backup settings: %v", err)
	}

	// 校验：要么有 token+machineId（已注册），要么有 name（可自动注册）
	if (cfg.Token == "" || cfg.MachineID == "") && cfg.Name == "" {
		log.Fatal("either token+machineId or name is required (set 'name' in config for auto register)")
	}

	// 自动注册成功后的配置保存回调
	saver := func(newToken, newMachineID string) error {
		cfg.Token = newToken
		cfg.MachineID = newMachineID
		if err := saveConfig(resolvedConfigPath, cfg); err != nil {
			return err
		}
		log.Printf("config saved to %s", resolvedConfigPath)
		return nil
	}

	agent, err := client.NewAgent(cfg.ServerURL, cfg.Token, cfg.MachineID, cfg.Name, cfg.RegisterKey, resolvedConfigPath, saver)
	if err != nil {
		log.Fatalf("create agent failed: %v", err)
	}

	// 检查是否以 Windows 服务运行
	if runAsService(cfg, resolvedConfigPath, saver) {
		return
	}

	if cfg.Token == "" {
		log.Printf("agent starting in auto-register mode, server=%s, name=%s", cfg.ServerURL, cfg.Name)
	} else {
		log.Printf("agent starting, server=%s, machine-id=%s", cfg.ServerURL, cfg.MachineID)
	}
	if err := agent.Run(); err != nil {
		log.Fatalf("agent error: %v", err)
	}
}
