package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"mutagen-web/server/db"
	"mutagen-web/server/models"
	"mutagen-web/server/ws"

	"github.com/gin-gonic/gin"
)

type ConfigContentRequest struct {
	Content string `json:"content" binding:"required"`
}

func RegisterConfigRoutes(r *gin.Engine, hub *ws.Hub) {
	g := r.Group("/api/machines/:id/config")
	{
		g.GET("/global", getGlobalConfig)
		g.PUT("/global", func(c *gin.Context) { updateGlobalConfig(c, hub) })
		g.GET("/ssh", getSSHConfig)
		g.PUT("/ssh", func(c *gin.Context) { updateSSHConfig(c, hub) })
		g.GET("/ssh-hosts", getSSHHosts)
		g.PUT("/ssh-hosts", func(c *gin.Context) { updateSSHHosts(c, hub) })
		g.POST("/ssh-hosts/import", func(c *gin.Context) { importSSHHosts(c, hub) })
		g.GET("/backup", getBackupConfig)
		g.PUT("/backup", func(c *gin.Context) { updateBackupConfig(c, hub) })
		g.POST("/backup/verify", func(c *gin.Context) { verifyBackupConfig(c, hub) })
		g.GET("/backup-linux", getBackupLinuxConfig)
		g.PUT("/backup-linux", func(c *gin.Context) { updateBackupLinuxConfig(c, hub) })
		g.POST("/backup-linux/verify", func(c *gin.Context) { verifyBackupLinuxConfig(c, hub) })
	}
}

func getOrCreateConfig(machineID uint, configType string) *models.MachineConfig {
	cfg := db.GetStore().GetConfig(machineID, configType)
	if cfg == nil {
		cfg = &models.MachineConfig{
			MachineID: machineID,
			Type:      configType,
			Content:   "",
		}
		db.GetStore().SaveConfig(cfg)
	}
	return cfg
}

func getGlobalConfig(c *gin.Context) {
	machineID, err := ws.MachineIDParam(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid machine id"})
		return
	}

	cfg := getOrCreateConfig(machineID, "global")
	c.JSON(http.StatusOK, gin.H{"content": cfg.Content})
}

func updateGlobalConfig(c *gin.Context, hub *ws.Hub) {
	machineID, err := ws.MachineIDParam(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid machine id"})
		return
	}

	var req ConfigContentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	cfg := getOrCreateConfig(machineID, "global")
	cfg.Content = req.Content
	cfg.UpdatedAt = time.Now()
	if err := db.GetStore().SaveConfig(cfg); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	cmdID := ws.GenerateToken()
	cmd := &ws.CommandPayload{
		CommandID: cmdID,
		Command:   "update_global_config",
		Params: map[string]interface{}{
			"content": req.Content,
		},
	}

	if err := hub.SendCommand(machineID, cmd); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": err.Error(), "saved": true})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "global config updated", "commandId": cmdID})
}

func getSSHConfig(c *gin.Context) {
	machineID, err := ws.MachineIDParam(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid machine id"})
		return
	}

	cfg := getOrCreateConfig(machineID, "ssh")
	c.JSON(http.StatusOK, gin.H{"content": cfg.Content})
}

func updateSSHConfig(c *gin.Context, hub *ws.Hub) {
	machineID, err := ws.MachineIDParam(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid machine id"})
		return
	}

	var req ConfigContentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	cfg := getOrCreateConfig(machineID, "ssh")
	cfg.Content = req.Content
	cfg.UpdatedAt = time.Now()
	if err := db.GetStore().SaveConfig(cfg); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	cmdID := ws.GenerateToken()
	cmd := &ws.CommandPayload{
		CommandID: cmdID,
		Command:   "update_ssh_config",
		Params: map[string]interface{}{
			"content": req.Content,
		},
	}

	if err := hub.SendCommand(machineID, cmd); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": err.Error(), "saved": true})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "ssh config updated", "commandId": cmdID})
}

// SSHHost 单个远程主机结构化配置
type SSHHost struct {
	Alias        string `json:"alias"`
	HostName     string `json:"hostName"`
	User         string `json:"user"`
	IdentityFile string `json:"identityFile"`
	Port         string `json:"port"`
}

type SSHHostsRequest struct {
	Hosts []SSHHost `json:"hosts"`
}

func getSSHHosts(c *gin.Context) {
	machineID, err := ws.MachineIDParam(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid machine id"})
		return
	}
	cfg := getOrCreateConfig(machineID, "ssh_hosts")
	var hosts []SSHHost
	if cfg.Content != "" {
		_ = json.Unmarshal([]byte(cfg.Content), &hosts)
	}
	c.JSON(http.StatusOK, gin.H{"hosts": hosts})
}

// sanitizeSSHField 移除 SSH 配置字段中的换行符，防止注入
func sanitizeSSHField(s string) string {
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return s
}

// buildSSHConfigText 由主机列表生成标准 ssh config 文本
func buildSSHConfigText(hosts []SSHHost) string {
	var b strings.Builder
	for _, h := range hosts {
		if h.Alias == "" {
			continue
		}
		crlf := "\r\n"
		fmt.Fprintf(&b, "Host %s"+crlf, sanitizeSSHField(h.Alias))
		if h.HostName != "" {
			fmt.Fprintf(&b, "    HostName %s"+crlf, sanitizeSSHField(h.HostName))
		}
		if h.User != "" {
			fmt.Fprintf(&b, "    User %s"+crlf, sanitizeSSHField(h.User))
		}
		if h.IdentityFile != "" {
			fmt.Fprintf(&b, "    IdentityFile %s"+crlf, sanitizeSSHField(h.IdentityFile))
		}
		if h.Port != "" {
			fmt.Fprintf(&b, "    Port %s"+crlf, sanitizeSSHField(h.Port))
		}
		fmt.Fprint(&b, "    StrictHostKeyChecking accept-new"+crlf)
		fmt.Fprint(&b, "    IdentitiesOnly yes"+crlf)
		fmt.Fprint(&b, crlf)
	}
	return b.String()
}

// parseSSHConfigText 解析 ssh config 明文为结构化 SSHHost 列表。
// 仅识别 Host/HostName/User/IdentityFile/Port 关键字段，其他字段（Match、ProxyCommand 等）一律跳过。
// Host 行支持多别名，会展开为多个 SSHHost（共享 HostName/User 等属性）。
func parseSSHConfigText(text string) []SSHHost {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(text, "\n")

	var hosts []SSHHost
	var current *SSHHost
	// Host 行多别名展开时，后续字段要同时写入展开出来的多个 host 指针
	var pending []*SSHHost

	commitPending := func() {
		for _, p := range pending {
			if p.Alias != "" {
				hosts = append(hosts, *p)
			}
		}
		pending = nil
	}

	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		// 空行 / 注释
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// 按第一组空白切 key / value，支持 ssh config 的 Key Value 和 Key=Value 两种写法
		var key, value string
		if eq := strings.IndexAny(line, " \t="); eq >= 0 {
			key = strings.ToLower(strings.TrimSpace(line[:eq]))
			// 跳过 = 和空白
			value = strings.TrimSpace(line[eq+1:])
			if strings.HasPrefix(value, "=") {
				value = strings.TrimSpace(strings.TrimPrefix(value, "="))
			}
		} else {
			key = strings.ToLower(line)
		}
		if key == "" {
			continue
		}

		if key == "host" {
			// 先把之前展开的 pending 提交
			commitPending()
			current = nil
			aliases := strings.Fields(value)
			for i, alias := range aliases {
				// 跳过含通配符的别名（*、?）——通配符一般是全局默认段，不适合作为"单主机"录入
				if strings.ContainsAny(alias, "*?") {
					continue
				}
				h := &SSHHost{Alias: alias}
				pending = append(pending, h)
				if i == 0 {
					current = h // 主引用，兼容旧逻辑（虽然实际上都走 pending 了）
				}
			}
			_ = current
			continue
		}

		// 非 Host 字段，写入所有 pending 别名（多别名共享后续属性）
		if len(pending) == 0 {
			continue
		}
		switch key {
		case "hostname":
			for _, p := range pending {
				p.HostName = sanitizeSSHField(value)
			}
		case "user":
			for _, p := range pending {
				p.User = sanitizeSSHField(value)
			}
		case "identityfile":
			for _, p := range pending {
				p.IdentityFile = sanitizeSSHField(value)
			}
		case "port":
			for _, p := range pending {
				p.Port = sanitizeSSHField(value)
			}
		}
	}
	commitPending()
	return hosts
}

// saveSSHParsedConfig 解析 ssh config 明文，保存结构化+明文两份配置，
// 并将标准化明文推回 Agent。供手动导入和自动上报共用。
func SaveSSHParsedConfig(machineID uint, rawText string, hub *ws.Hub) (int, string, error) {
	// 解析成结构化 SSHHost
	parsed := parseSSHConfigText(rawText)

	// 去重：按 Alias 合并
	seen := make(map[string]*SSHHost, len(parsed))
	var order []string
	for i := range parsed {
		h := parsed[i]
		if _, ok := seen[h.Alias]; !ok {
			order = append(order, h.Alias)
		}
		seen[h.Alias] = &h
	}
	merged := make([]SSHHost, 0, len(order))
	for _, alias := range order {
		merged = append(merged, *seen[alias])
	}

	// 结构化数据存 ssh_hosts
	hostsData, _ := json.Marshal(merged)
	hostsCfg := getOrCreateConfig(machineID, "ssh_hosts")
	hostsCfg.Content = string(hostsData)
	hostsCfg.UpdatedAt = time.Now()
	if err := db.GetStore().SaveConfig(hostsCfg); err != nil {
		return 0, "", err
	}

	// 重新生成标准明文存 ssh
	sshText := buildSSHConfigText(merged)
	sshCfg := getOrCreateConfig(machineID, "ssh")
	sshCfg.Content = sshText
	sshCfg.UpdatedAt = time.Now()
	if err := db.GetStore().SaveConfig(sshCfg); err != nil {
		return 0, "", err
	}

	// 推回 Agent
	pushCmd := &ws.CommandPayload{
		CommandID: ws.GenerateToken(),
		Command:   "update_ssh_config",
		Params: map[string]interface{}{
			"content": sshText,
		},
	}
	var pushErr string
	if perr := hub.SendCommand(machineID, pushCmd); perr != nil {
		pushErr = perr.Error()
	}

	return len(merged), pushErr, nil
}

// importSSHHosts 从 Agent 端拉取本地 ~/.ssh/config 明文，解析为结构化 SSHHost，
// 保存到 ssh_hosts（结构化）+ ssh（明文，回写 Agent）两份配置，
// 实现"换新机器时自动导入老配置"。
func importSSHHosts(c *gin.Context, hub *ws.Hub) {
	machineID, err := ws.MachineIDParam(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid machine id"})
		return
	}

	// 1. 发命令给 Agent 拉远端 ssh config 原文
	cmd := &ws.CommandPayload{
		CommandID: ws.GenerateToken(),
		Command:   "read_ssh_config",
		Params:    map[string]interface{}{},
	}
	res, err := hub.SendCommandAndWait(machineID, cmd, 30*time.Second)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "read ssh config from agent failed: " + err.Error()})
		return
	}
	if !res.Success {
		c.JSON(http.StatusBadGateway, gin.H{"error": "agent read ssh config failed: " + res.Error})
		return
	}
	rawText := res.Data

	count, pushErr, err := SaveSSHParsedConfig(machineID, rawText, hub)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":   "ssh hosts imported",
		"count":     count,
		"pushError": pushErr,
	})
}

func updateSSHHosts(c *gin.Context, hub *ws.Hub) {
	machineID, err := ws.MachineIDParam(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid machine id"})
		return
	}
	var req SSHHostsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	data, _ := json.Marshal(req.Hosts)
	hostsCfg := getOrCreateConfig(machineID, "ssh_hosts")
	hostsCfg.Content = string(data)
	hostsCfg.UpdatedAt = time.Now()
	if err := db.GetStore().SaveConfig(hostsCfg); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "save ssh hosts config failed: " + err.Error()})
		return
	}

	sshText := buildSSHConfigText(req.Hosts)
	sshCfg := getOrCreateConfig(machineID, "ssh")
	sshCfg.Content = sshText
	sshCfg.UpdatedAt = time.Now()
	if err := db.GetStore().SaveConfig(sshCfg); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "save ssh config failed: " + err.Error()})
		return
	}

	cmdID := ws.GenerateToken()
	cmd := &ws.CommandPayload{
		CommandID: cmdID,
		Command:   "update_ssh_config",
		Params: map[string]interface{}{
			"content": sshText,
		},
	}
	if err := hub.SendCommand(machineID, cmd); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": err.Error(), "saved": true})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "ssh hosts updated", "commandId": cmdID})
}

// BackupConfigRequest 备份配置表单，对应 mutagen ~/.mutagen/backup.json
type BackupConfigRequest struct {
	Enabled       bool   `json:"enabled"`
	Dir           string `json:"dir"`
	RetentionDays int    `json:"retentionDays"`
	FailOpen      bool   `json:"failOpen"`
}

func getBackupConfig(c *gin.Context) {
	machineID, err := ws.MachineIDParam(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid machine id"})
		return
	}
	cfg := getOrCreateConfig(machineID, "backup")
	// 默认值（与 mutagen loadBackupConfig 的回退默认对齐）
	resp := BackupConfigRequest{Enabled: false, Dir: "", RetentionDays: 7, FailOpen: true}
	if cfg.Content != "" {
		_ = json.Unmarshal([]byte(cfg.Content), &resp)
	}
	c.JSON(http.StatusOK, resp)
}

// collectRemoteHosts 从已保存的 SSH 主机列表提取别名，作为需要同步/校验备份配置的远端目标
func collectRemoteHosts(machineID uint) []string {
	var remoteHosts []string
	hostsCfg := db.GetStore().GetConfig(machineID, "ssh_hosts")
	if hostsCfg != nil && hostsCfg.Content != "" {
		var hosts []SSHHost
		if json.Unmarshal([]byte(hostsCfg.Content), &hosts) == nil {
			for _, h := range hosts {
				if h.Alias != "" {
					remoteHosts = append(remoteHosts, h.Alias)
				}
			}
		}
	}
	return remoteHosts
}

func updateBackupConfig(c *gin.Context, hub *ws.Hub) {
	machineID, err := ws.MachineIDParam(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid machine id"})
		return
	}
	var req BackupConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.RetentionDays < 0 {
		req.RetentionDays = 0
	}

	// 统一用 / 分隔符，避免 Windows 反斜杠写入 JSON 后转义问题（dir 留空时两端通用）
	req.Dir = strings.ReplaceAll(req.Dir, "\\", "/")
	content, _ := json.MarshalIndent(req, "", "  ")

	cfg := getOrCreateConfig(machineID, "backup")
	cfg.Content = string(content)
	cfg.UpdatedAt = time.Now()
	if err := db.GetStore().SaveConfig(cfg); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	cmdID := ws.GenerateToken()
	cmd := &ws.CommandPayload{
		CommandID: cmdID,
		Command:   "update_backup_config",
		Params: map[string]interface{}{
			"content":     string(content),
			"remoteHosts": []string{},
		},
	}
	if err := hub.SendCommand(machineID, cmd); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": err.Error(), "saved": true})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "backup config updated (local only)", "commandId": cmdID})
}

// verifyBackupConfig 下发校验命令，同步等待 agent 回执：只校验本机 ~/.mutagen/backup.json 是否与 Server DB 中保存的本地备份配置一致。
func verifyBackupConfig(c *gin.Context, hub *ws.Hub) {
	machineID, err := ws.MachineIDParam(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid machine id"})
		return
	}
	cfg := getOrCreateConfig(machineID, "backup")
	content := cfg.Content
	cmd := &ws.CommandPayload{
		CommandID: ws.GenerateToken(),
		Command:   "verify_backup_config",
		Params: map[string]interface{}{
			"content":     content,
			"remoteHosts": []string{},
		},
	}
	res, err := hub.SendCommandAndWait(machineID, cmd, 30*time.Second)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": res.Success, "report": res.Data, "error": res.Error})
}

func getBackupLinuxConfig(c *gin.Context) {
	machineID, err := ws.MachineIDParam(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid machine id"})
		return
	}
	cfg := getOrCreateConfig(machineID, "backup_linux")
	resp := BackupConfigRequest{Enabled: false, Dir: "", RetentionDays: 7, FailOpen: true}
	if cfg.Content != "" {
		_ = json.Unmarshal([]byte(cfg.Content), &resp)
	}
	c.JSON(http.StatusOK, resp)
}

func updateBackupLinuxConfig(c *gin.Context, hub *ws.Hub) {
	machineID, err := ws.MachineIDParam(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid machine id"})
		return
	}
	var req BackupConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.RetentionDays < 0 {
		req.RetentionDays = 0
	}
	req.Dir = strings.ReplaceAll(req.Dir, "\\", "/")
	content, _ := json.MarshalIndent(req, "", "  ")

	cfg := getOrCreateConfig(machineID, "backup_linux")
	cfg.Content = string(content)
	cfg.UpdatedAt = time.Now()
	if err := db.GetStore().SaveConfig(cfg); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	remoteHosts := collectRemoteHosts(machineID)
	cmdID := ws.GenerateToken()
	cmd := &ws.CommandPayload{
		CommandID: cmdID,
		Command:   "update_remote_backup_config",
		Params: map[string]interface{}{
			"content":     string(content),
			"remoteHosts": remoteHosts,
		},
	}
	// 同步等待 agent 回执，以便把"远端已存在"等提示信息返回给前端
	res, err := hub.SendCommandAndWait(machineID, cmd, 30*time.Second)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": err.Error(), "saved": true})
		return
	}

	// 尝试解析 agent 回执里的 JSON（远端已存在时 agent 用 JSON 包了 existingDir 等字段）
	var parsed struct {
		Summary     string `json:"summary"`
		ExistingDir string `json:"existingDir"`
		ExistingRaw string `json:"existingRaw"`
	}
	reportText := res.Data
	existingDir := ""
	if json.Unmarshal([]byte(res.Data), &parsed) == nil && parsed.ExistingDir != "" {
		existingDir = parsed.ExistingDir
		reportText = parsed.Summary
		// 落库：把 backup_linux 的 dir 改成远端现有的，保证 B 下次打开页面看到的是远端的路径
		var remoteReq BackupConfigRequest
		if json.Unmarshal([]byte(parsed.ExistingRaw), &remoteReq) == nil {
			remoteReq.Dir = strings.ReplaceAll(remoteReq.Dir, "\\", "/")
			remoteContent, _ := json.MarshalIndent(remoteReq, "", "  ")
			cfg.Content = string(remoteContent)
			cfg.UpdatedAt = time.Now()
			_ = db.GetStore().SaveConfig(cfg)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"message":     "remote backup config updated",
		"commandId":  cmdID,
		"success":    res.Success,
		"report":     reportText,
		"error":      res.Error,
		"existingDir": existingDir,
	})
}

func verifyBackupLinuxConfig(c *gin.Context, hub *ws.Hub) {
	machineID, err := ws.MachineIDParam(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid machine id"})
		return
	}
	remoteHosts := collectRemoteHosts(machineID)
	cmd := &ws.CommandPayload{
		CommandID: ws.GenerateToken(),
		Command:   "verify_remote_backup_config",
		Params: map[string]interface{}{
			"remoteHosts": remoteHosts,
		},
	}
	res, err := hub.SendCommandAndWait(machineID, cmd, 30*time.Second)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": res.Success, "report": res.Data, "error": res.Error})
}
