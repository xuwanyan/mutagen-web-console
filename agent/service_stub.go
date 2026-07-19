//go:build !windows

package main

// runAsService 非 Windows 平台不做任何事
func runAsService(cfg *AgentConfig, configPath string, saver func(token, machineID string) error) bool {
	return false
}