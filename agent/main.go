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
	ServerURL string `json:"server"`
	Token     string `json:"token"`
	MachineID string `json:"machineId"`
	Name      string `json:"name"`
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

func main() {
	var (
		configPath = flag.String("config", "", "config file path")
		serverURL  = flag.String("server", "ws://localhost:8080/ws/agent", "server websocket url")
		token      = flag.String("token", "", "machine token")
		machineID  = flag.String("machine-id", "", "machine id")
		name       = flag.String("name", "", "machine name (for auto register)")
		logFile    = flag.String("log", "", "log file path (default: C:\\mutagen\\agent.log)")
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
		ServerURL: *serverURL,
		Token:     *token,
		MachineID: *machineID,
		Name:      *name,
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
	} else if *configPath != "" {
		log.Fatalf("load config failed: %v", err)
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

	agent, err := client.NewAgent(cfg.ServerURL, cfg.Token, cfg.MachineID, cfg.Name, resolvedConfigPath, saver)
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
