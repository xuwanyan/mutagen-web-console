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

// buildSSHConfigText 由主机列表生成标准 ssh config 文本
func buildSSHConfigText(hosts []SSHHost) string {
	var b strings.Builder
	for _, h := range hosts {
		if h.Alias == "" {
			continue
		}
		crlf := "\r\n"
		fmt.Fprintf(&b, "Host %s"+crlf, h.Alias)
		if h.HostName != "" {
			fmt.Fprintf(&b, "    HostName %s"+crlf, h.HostName)
		}
		if h.User != "" {
			fmt.Fprintf(&b, "    User %s"+crlf, h.User)
		}
		if h.IdentityFile != "" {
			fmt.Fprintf(&b, "    IdentityFile %s"+crlf, h.IdentityFile)
		}
		if h.Port != "" {
			fmt.Fprintf(&b, "    Port %s"+crlf, h.Port)
		}
		fmt.Fprintf(&b, "    StrictHostKeyChecking accept-new"+crlf)
		fmt.Fprintf(&b, "    IdentitiesOnly yes"+crlf)
		fmt.Fprint(&b, crlf)
	}
	return b.String()
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
	db.GetStore().SaveConfig(hostsCfg)

	sshText := buildSSHConfigText(req.Hosts)
	sshCfg := getOrCreateConfig(machineID, "ssh")
	sshCfg.Content = sshText
	sshCfg.UpdatedAt = time.Now()
	db.GetStore().SaveConfig(sshCfg)

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
