package handlers

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"mutagen-web/server/db"
	"mutagen-web/server/models"
	"mutagen-web/server/ws"

	"github.com/gin-gonic/gin"
)

type CreateMachineRequest struct {
	Name string `json:"name" binding:"required"`
}

type MachineResponse struct {
	ID           uint       `json:"id"`
	Name         string     `json:"name"`
	Token        string     `json:"token"`
	Online       bool       `json:"online"`
	LastSeenAt   *time.Time `json:"lastSeenAt"`
	AgentVersion string     `json:"agentVersion"`
	OS           string     `json:"os"`
	CreatedAt    time.Time  `json:"createdAt"`
}

func NewMachineResponse(m *models.Machine, hub *ws.Hub) MachineResponse {
	return MachineResponse{
		ID:           m.ID,
		Name:         m.Name,
		Token:        m.Token,
		Online:       hub.IsOnline(m.ID),
		LastSeenAt:   m.LastSeenAt,
		AgentVersion: m.AgentVersion,
		OS:           m.OS,
		CreatedAt:    m.CreatedAt,
	}
}

var agentBinPath string

func RegisterMachineRoutes(r *gin.Engine, hub *ws.Hub, agentBin string) {
	agentBinPath = agentBin
	g := r.Group("/api/machines")
	{
		g.GET("", func(c *gin.Context) { listMachines(c, hub) })
		g.POST("", func(c *gin.Context) { createMachine(c, hub) })
		g.GET("/:id", func(c *gin.Context) { getMachine(c, hub) })
		g.DELETE("/:id", func(c *gin.Context) { deleteMachine(c, hub) })
		g.POST("/:id/regenerate-token", func(c *gin.Context) { regenerateToken(c, hub) })
		g.POST("/:id/test-connection", func(c *gin.Context) { testConnection(c, hub) })
		g.GET("/:id/agent-pack", func(c *gin.Context) { downloadAgentPack(c, hub) })
	}
}

func listMachines(c *gin.Context, hub *ws.Hub) {
	machines := db.GetStore().ListMachines()
	resp := make([]MachineResponse, 0, len(machines))
	for i := range machines {
		resp = append(resp, NewMachineResponse(&machines[i], hub))
	}
	c.JSON(http.StatusOK, resp)
}

func createMachine(c *gin.Context, hub *ws.Hub) {
	var req CreateMachineRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	for _, m := range db.GetStore().ListMachines() {
		if m.Name == req.Name {
			c.JSON(http.StatusConflict, gin.H{"error": "machine name already exists"})
			return
		}
	}
	machine := models.Machine{
		Name:  req.Name,
		Token: ws.GenerateToken(),
	}
	if err := db.GetStore().CreateMachine(&machine); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, NewMachineResponse(&machine, hub))
}

func getMachine(c *gin.Context, hub *ws.Hub) {
	id, err := ws.MachineIDParam(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	machine := db.GetStore().GetMachine(id)
	if machine == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "machine not found"})
		return
	}
	c.JSON(http.StatusOK, NewMachineResponse(machine, hub))
}

func deleteMachine(c *gin.Context, hub *ws.Hub) {
	id, err := ws.MachineIDParam(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	if hub.IsOnline(id) {
		for _, t := range db.GetStore().ListTasks(id) {
			cmd := &ws.CommandPayload{
				CommandID: ws.GenerateToken(),
				Command:   "terminate_sync",
				Params:    map[string]interface{}{"name": t.Name},
			}
			hub.SendCommand(id, cmd)
		}
		stopCmd := &ws.CommandPayload{
			CommandID: ws.GenerateToken(),
			Command:   "stop_agent",
			Params:    map[string]interface{}{},
		}
		hub.SendCommand(id, stopCmd)
	}
	db.GetStore().DeleteTasksByMachine(id)
	db.GetStore().DeleteConfigsByMachine(id)
	if err := db.GetStore().DeleteMachine(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	time.Sleep(500 * time.Millisecond)
	hub.Disconnect(id)
	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}

func regenerateToken(c *gin.Context, hub *ws.Hub) {
	id, err := ws.MachineIDParam(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	machine := db.GetStore().GetMachine(id)
	if machine == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "machine not found"})
		return
	}
	machine.Token = ws.GenerateToken()
	if err := db.GetStore().SaveMachine(machine); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, NewMachineResponse(machine, hub))
}

func testConnection(c *gin.Context, hub *ws.Hub) {
	id, err := ws.MachineIDParam(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	if !hub.IsOnline(id) {
		c.JSON(http.StatusOK, gin.H{"online": false, "message": "machine offline"})
		return
	}
	cmdID := ws.GenerateToken()
	cmd := &ws.CommandPayload{
		CommandID: cmdID,
		Command:   "ping",
		Params:    map[string]interface{}{},
	}
	if err := hub.SendCommand(id, cmd); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"online": true, "commandId": cmdID})
}

func findAgentBin() string {
	if agentBinPath != "" {
		if _, err := os.Stat(agentBinPath); err == nil {
			return agentBinPath
		}
	}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		candidates := []string{
			filepath.Join(dir, "mutagen-web-agent.exe"),
			filepath.Join(dir, "agent.exe"),
			filepath.Join(dir, "..", "build", "mutagen-web-agent.exe"),
			filepath.Join(dir, "..", "build", "agent.exe"),
		}
		for _, p := range candidates {
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	return ""
}

func findMutagenBin() string {
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		candidates := []string{
			filepath.Join(dir, "mutagen.exe"),
			filepath.Join(dir, "..", "tools", "mutagen.exe"),
			filepath.Join(dir, "..", "build", "mutagen.exe"),
			"C:\\mutagen\\mutagen.exe",
		}
		for _, p := range candidates {
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	return ""
}

func downloadAgentPack(c *gin.Context, hub *ws.Hub) {
	id, err := ws.MachineIDParam(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	machine := db.GetStore().GetMachine(id)
	if machine == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "machine not found"})
		return
	}

	scheme := "ws"
	if c.Request.TLS != nil {
		scheme = "wss"
	}
	serverURL := fmt.Sprintf("%s://%s/ws/agent", scheme, c.Request.Host)

	cfg := map[string]string{
		"server":    serverURL,
		"token":     machine.Token,
		"machineId": fmt.Sprintf("%d", machine.ID),
		"name":      machine.Name,
	}
	cfgData, _ := json.MarshalIndent(cfg, "", "  ")

	agentPath := findAgentBin()
	if agentPath == "" {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "agent.exe not found"})
		return
	}
	agentData, err := os.ReadFile(agentPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "read agent.exe failed: " + err.Error()})
		return
	}

	mutagenPath := findMutagenBin()
	var mutagenData, mutagenAgentsData []byte
	if mutagenPath != "" {
		mutagenData, _ = os.ReadFile(mutagenPath)
		agentsPath := filepath.Join(filepath.Dir(mutagenPath), "mutagen-agents.tar.gz")
		mutagenAgentsData, _ = os.ReadFile(agentsPath)
	}

	installBat := "@echo off\ntitle Mutagen Web Agent - Install\n\nset MUTAGEN_DIR=C:\\mutagen\nset SCRIPT_DIR=%%~dp0\n\necho ========================================\necho Mutagen Web Agent - Installing\necho ========================================\necho.\n\nnet session >nul 2>&1\nif %%ERRORLEVEL%% neq 0 (\n    echo [ERROR] Please run as Administrator!\n    pause\n    exit /b 1\n)\n\necho [1/3] Copying files to %%MUTAGEN_DIR%%...\nif not exist \"%%MUTAGEN_DIR%%\" mkdir \"%%MUTAGEN_DIR%%\"\ncopy /Y \"%%SCRIPT_DIR%%mutagen.exe\" \"%%MUTAGEN_DIR%%\\\"\ncopy /Y \"%%SCRIPT_DIR%%mutagen-agents.tar.gz\" \"%%MUTAGEN_DIR%%\\\"\ncopy /Y \"%%SCRIPT_DIR%%mutagen-web-agent.exe\" \"%%MUTAGEN_DIR%%\\\"\ncopy /Y \"%%SCRIPT_DIR%%agent-config.json\" \"%%MUTAGEN_DIR%%\\\"\necho OK\n\necho [2/3] Registering auto-start task...\nschtasks /create /tn \"MutagenWebAgent\" /tr \"%%MUTAGEN_DIR%%\\mutagen-web-agent.exe --config %%MUTAGEN_DIR%%\\agent-config.json -log %%MUTAGEN_DIR%%\\agent.log\" /sc onlogon /ru %%USERNAME%% /rl highest /f\necho OK\n\necho [3/3] Windows Service\necho.\necho To register service manually, run:\necho.\necho   sc create MutagenWebAgent binPath= \"%%MUTAGEN_DIR%%\\mutagen-web-agent.exe --config %%MUTAGEN_DIR%%\\agent-config.json -log %%MUTAGEN_DIR%%\\agent.log\" start= auto obj= \".\\%%USERNAME%%\" password= \"YOUR_PASSWORD\"\necho.\necho Then start:\necho   sc start MutagenWebAgent\necho.\necho ========================================\necho  Install completed!\necho ========================================\npause\n"

	readmeText := "Mutagen Web Agent - Installation Guide\n\n1. Extract all files to a folder on the target machine\n\n2. Right-click install.bat > Run as Administrator\n   This will copy files to C:/mutagen/ and register auto-start.\n\n3. To register as Windows service, run as Administrator:\n   sc create MutagenWebAgent binPath= \"C:/mutagen/mutagen-web-agent.exe --config C:/mutagen/agent-config.json -log C:/mutagen/agent.log\" start= auto obj= \"./rpa\" password= \"YOUR_PASSWORD\"\n\n4. Start: sc start MutagenWebAgent\n\nManagement:\n  Start:   sc start MutagenWebAgent\n  Stop:    sc stop MutagenWebAgent\n  Status:  sc query MutagenWebAgent\n  Logs:    C:/mutagen/agent.log\n"

	buf := new(bytes.Buffer)
	w := zip.NewWriter(buf)

	if len(mutagenData) > 0 {
		f, _ := w.Create("mutagen.exe")
		f.Write(mutagenData)
	}
	if len(mutagenAgentsData) > 0 {
		f, _ := w.Create("mutagen-agents.tar.gz")
		f.Write(mutagenAgentsData)
	}
	f1, _ := w.Create("mutagen-web-agent.exe")
	f1.Write(agentData)
	f2, _ := w.Create("agent-config.json")
	f2.Write(cfgData)
	f4, _ := w.Create("install.bat")
	f4.Write([]byte(installBat))
	f5, _ := w.Create("README.txt")
	f5.Write([]byte(readmeText))

	w.Close()

	safeName := fmt.Sprintf("agent-pack-%s", machine.Name)
	c.Header("Content-Type", "application/zip")
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%s.zip", safeName))
	c.Data(http.StatusOK, "application/zip", buf.Bytes())
}