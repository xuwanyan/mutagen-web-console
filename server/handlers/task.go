package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"strings"
	"time"

	"mutagen-web/server/db"
	"mutagen-web/server/models"
	"mutagen-web/server/ws"

	"github.com/gin-gonic/gin"
)

type CommandTemplateResponse struct {
	Commands []string `json:"commands"`
	Hosts    []SSHHost `json:"hosts"`
}

type CreateTaskRequest struct {
	Name        string   `json:"name" binding:"required"`
	Alpha       string   `json:"alpha" binding:"required"`
	Beta        string   `json:"beta" binding:"required"`
	Mode        string   `json:"mode"`
	IgnoreVCS   bool     `json:"ignoreVcs"`
	SymlinkMode string   `json:"symlinkMode"`
	IgnorePaths []string `json:"ignorePaths"`
}

type UpdateTaskRequest struct {
	Name        string   `json:"name"`
	Alpha       string   `json:"alpha"`
	Beta        string   `json:"beta"`
	Mode        string   `json:"mode"`
	IgnoreVCS   *bool    `json:"ignoreVcs"`
	SymlinkMode string   `json:"symlinkMode"`
	IgnorePaths []string `json:"ignorePaths"`
}

func RegisterTaskRoutes(r *gin.Engine, hub *ws.Hub) {
	g := r.Group("/api/machines/:id/tasks")
	{
		g.GET("", func(c *gin.Context) { listTasks(c) })
		g.POST("", func(c *gin.Context) { createTask(c, hub) })
		g.POST("/pause-all", func(c *gin.Context) { pauseAllTasks(c, hub) })
		g.POST("/resume-all", func(c *gin.Context) { resumeAllTasks(c, hub) })
		g.POST("/:taskId/pause", func(c *gin.Context) { pauseTask(c, hub) })
		g.POST("/:taskId/resume", func(c *gin.Context) { resumeTask(c, hub) })
		g.DELETE("/:taskId", func(c *gin.Context) { deleteTask(c, hub) })
		g.POST("/:taskId/retry", func(c *gin.Context) { retryTask(c, hub) })
		g.PUT("/:taskId", func(c *gin.Context) { updateTask(c, hub) })
	}
	r.POST("/api/machines/:id/refresh-status", func(c *gin.Context) { refreshStatus(c, hub) })
	r.POST("/api/machines/:id/refresh-remote-agents", func(c *gin.Context) { refreshRemoteAgents(c, hub) })
	r.POST("/api/machines/:id/tasks/terminate-all", func(c *gin.Context) { terminateAllTasks(c, hub) })
	r.GET("/api/backup-data", func(c *gin.Context) { backupData(c) })
	r.GET("/api/machines/:id/command-template", func(c *gin.Context) { getCommandTemplate(c) })
}

func listTasks(c *gin.Context) {
	machineID, err := ws.MachineIDParam(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid machine id"})
		return
	}

	tasks := db.GetStore().ListTasks(machineID)
	c.JSON(http.StatusOK, tasks)
}

func createTask(c *gin.Context, hub *ws.Hub) {
	machineID, err := ws.MachineIDParam(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid machine id"})
		return
	}

	var req CreateTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// 任务名称重复校验
	if existing := db.GetStore().FindTaskByName(machineID, req.Name); existing != nil {
		c.JSON(http.StatusConflict, gin.H{"error": fmt.Sprintf("task name '%s' already exists", req.Name)})
		return
	}

	mode := req.Mode
	if mode == "" {
		mode = "two-way-resolved"
	}
	symlinkMode := req.SymlinkMode
	if symlinkMode == "" {
		symlinkMode = "ignore"
	}

	task := models.SyncTask{
		MachineID:   machineID,
		Name:        req.Name,
		Alpha:       req.Alpha,
		Beta:        req.Beta,
		Mode:        mode,
		IgnoreVCS:   req.IgnoreVCS,
		SymlinkMode: symlinkMode,
		IgnorePaths: req.IgnorePaths,
	}

	if err := db.GetStore().CreateTask(&task); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	cmdID := ws.GenerateToken()
	cmd := &ws.CommandPayload{
		CommandID: cmdID,
		Command:   "create_sync",
		Params: map[string]interface{}{
			"taskId":      task.ID,
			"name":        task.Name,
			"alpha":       task.Alpha,
			"beta":        task.Beta,
			"mode":        task.Mode,
			"ignoreVcs":   task.IgnoreVCS,
			"symlinkMode": task.SymlinkMode,
			"ignorePaths": task.IgnorePaths,
		},
	}

	if err := hub.SendCommand(machineID, cmd); err != nil {
		task.LastError = err.Error()
		db.GetStore().SaveTask(&task)
		c.JSON(http.StatusServiceUnavailable, gin.H{"task": task, "saved": true, "error": err.Error()})
		return
	}

	task.MutagenSessionName = task.Name
	db.GetStore().SaveTask(&task)

	c.JSON(http.StatusOK, task)
}

func retryTask(c *gin.Context, hub *ws.Hub) {
	machineID, err := ws.MachineIDParam(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid machine id"})
		return
	}

	taskIDStr := c.Param("taskId")
	if taskIDStr == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing task id"})
		return
	}

	var taskID uint
	if _, err := fmt.Sscanf(taskIDStr, "%d", &taskID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid task id"})
		return
	}

	task := db.GetStore().GetTask(machineID, taskID)
	if task == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "task not found"})
		return
	}

	cmdID := ws.GenerateToken()
	cmd := &ws.CommandPayload{
		CommandID: cmdID,
		Command:   "create_sync",
		Params: map[string]interface{}{
			"taskId":      task.ID,
			"name":        task.Name,
			"alpha":       task.Alpha,
			"beta":        task.Beta,
			"mode":        task.Mode,
			"ignoreVcs":   task.IgnoreVCS,
			"symlinkMode": task.SymlinkMode,
			"ignorePaths": task.IgnorePaths,
		},
	}

	if err := hub.SendCommand(machineID, cmd); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": err.Error(), "task": task})
		return
	}

	task.LastError = ""
	task.MutagenSessionName = task.Name
	db.GetStore().SaveTask(task)

	c.JSON(http.StatusOK, gin.H{"message": "retry command sent", "task": task})
}

func updateTask(c *gin.Context, hub *ws.Hub) {
	machineID, err := ws.MachineIDParam(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid machine id"})
		return
	}

	taskIDStr := c.Param("taskId")
	if taskIDStr == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing task id"})
		return
	}

	var taskID uint
	if _, err := fmt.Sscanf(taskIDStr, "%d", &taskID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid task id"})
		return
	}

	task := db.GetStore().GetTask(machineID, taskID)
	if task == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "task not found"})
		return
	}

	var req UpdateTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// 保存旧值快照，用于判断是否有实质变化
	oldName := task.Name
	oldAlpha := task.Alpha
	oldBeta := task.Beta
	oldMode := task.Mode
	oldIgnoreVCS := task.IgnoreVCS
	oldSymlinkMode := task.SymlinkMode
	oldIgnorePaths := make([]string, len(task.IgnorePaths))
	copy(oldIgnorePaths, task.IgnorePaths)

	// 重名校验（排除自己）
	if req.Name != "" && req.Name != task.Name {
		if existing := db.GetStore().FindTaskByName(machineID, req.Name); existing != nil {
			c.JSON(http.StatusConflict, gin.H{"error": fmt.Sprintf("task name '%s' already exists", req.Name)})
			return
		}
		task.Name = req.Name
	}
	if req.Alpha != "" {
		task.Alpha = req.Alpha
	}
	if req.Beta != "" {
		task.Beta = req.Beta
	}
	if req.Mode != "" {
		task.Mode = req.Mode
	}
	if req.IgnoreVCS != nil {
		task.IgnoreVCS = *req.IgnoreVCS
	}
	if req.SymlinkMode != "" {
		task.SymlinkMode = req.SymlinkMode
	}
	if req.IgnorePaths != nil {
		task.IgnorePaths = req.IgnorePaths
	}

	// 判断是否有实质变化
	nameChanged := oldName != task.Name
	contentChanged := oldAlpha != task.Alpha ||
		oldBeta != task.Beta ||
		oldMode != task.Mode ||
		oldIgnoreVCS != task.IgnoreVCS ||
		oldSymlinkMode != task.SymlinkMode ||
		!reflect.DeepEqual(oldIgnorePaths, task.IgnorePaths)

	if err := db.GetStore().SaveTask(task); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 没有变化：只更新 DB，不重建会话
	if !nameChanged && !contentChanged {
		c.JSON(http.StatusOK, gin.H{"message": "task updated (no changes, skipping rebuild)", "task": task})
		return
	}

	// 有变化且 agent 在线时才重建会话（异步执行，前端立即返回）
	// 改名场景：先 terminate 旧 name，再 create 新 name
	// 改内容场景（name 没变）：create_sync，agent 内部幂等逻辑先 terminate 再 create
	if hub.IsOnline(machineID) {
		// 异步执行重建，不阻塞 HTTP 响应。
		// goroutine 读取独立副本 taskSnap 的配置字段（Name/Alpha/...）下发重建命令，
		// 与下方 c.JSON(task) 序列化分离，避免共享同一指针；重建结果只写 LastError
		// （走 UpdateTaskError，不整体覆盖最新记录的其他字段）。
		taskSnap := *task
		go func(mID uint, t *models.SyncTask, oldN string) {
			// 改名场景：先 terminate 旧 name
			if oldN != "" && oldN != t.Name {
				termCmd := &ws.CommandPayload{
					CommandID: ws.GenerateToken(),
					Command:   "terminate_sync",
					Params:    map[string]interface{}{"name": oldN},
				}
				termResult, termErr := hub.SendCommandAndWait(mID, termCmd, 30*time.Second)
				if termErr != nil {
					errMsg := fmt.Sprintf("terminate old '%s' failed: %v", oldN, termErr)
					if termResult != nil && termResult.Error != "" {
						errMsg = fmt.Sprintf("terminate old '%s' failed: %s (agent output: %s)", oldN, termErr, termResult.Error)
					}
					db.GetStore().UpdateTaskError(mID, t.ID, errMsg)
					return
				}
			}

			// 发 create_sync（用新 name），agent 内部会先 TerminateSync(新name) 再 CreateSync(新name)
			createCmd := &ws.CommandPayload{
				CommandID: ws.GenerateToken(),
				Command:   "create_sync",
				Params: map[string]interface{}{
					"taskId":      t.ID,
					"name":        t.Name,
					"alpha":       t.Alpha,
					"beta":        t.Beta,
					"mode":        t.Mode,
					"ignoreVcs":   t.IgnoreVCS,
					"symlinkMode": t.SymlinkMode,
					"ignorePaths": t.IgnorePaths,
				},
			}
			result, createErr := hub.SendCommandAndWait(mID, createCmd, 60*time.Second)
			if createErr != nil {
				errMsg := fmt.Sprintf("recreate sync failed: %v", createErr)
				if result != nil && result.Error != "" {
					errMsg = fmt.Sprintf("recreate sync failed: %s (agent output: %s)", createErr, result.Error)
				}
				db.GetStore().UpdateTaskError(mID, t.ID, errMsg)
				return
			}
			// create 成功：清理错误状态（只清 LastError，不覆盖期间被 agent
			// 上报更新的 Status/Identifier 等字段）
			db.GetStore().UpdateTaskError(mID, t.ID, "")
			// 触发 agent 上报：新 identifier 入库 + BulkUpsertSyncTaskStatus 自动清理旧记录
			reportCmd := &ws.CommandPayload{
				CommandID: ws.GenerateToken(),
				Command:   "report_status",
			}
			_, _ = hub.SendCommandAndWait(mID, reportCmd, 15*time.Second)
		}(machineID, &taskSnap, oldName)
	}

	c.JSON(http.StatusOK, gin.H{"message": "task updated", "task": task})
}

func pauseTask(c *gin.Context, hub *ws.Hub) {
	doTaskCommand(c, hub, "pause_sync")
}

// pauseAllTasks 暂停指定机器的全部任务
func pauseAllTasks(c *gin.Context, hub *ws.Hub) {
	machineID, err := ws.MachineIDParam(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid machine id"})
		return
	}

	tasks := db.GetStore().ListTasks(machineID)
	if len(tasks) == 0 {
		c.JSON(http.StatusOK, gin.H{"message": "no tasks to pause", "count": 0})
		return
	}

	sent := 0
	failed := 0
	for _, t := range tasks {
		cmd := &ws.CommandPayload{
			CommandID: ws.GenerateToken(),
			Command:   "pause_sync",
			Params: map[string]interface{}{
				"taskId": t.ID,
				"name":   t.Name,
			},
		}
		if err := hub.SendCommand(machineID, cmd); err != nil {
			failed++
		} else {
			sent++
		}
	}

	c.JSON(http.StatusOK, gin.H{"message": "pause all sent", "sent": sent, "failed": failed})
}

func resumeTask(c *gin.Context, hub *ws.Hub) {
	doTaskCommand(c, hub, "resume_sync")
}

func deleteTask(c *gin.Context, hub *ws.Hub) {
	machineID, err := ws.MachineIDParam(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid machine id"})
		return
	}

	taskIDStr := c.Param("taskId")
	if taskIDStr == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing task id"})
		return
	}

	var taskID uint
	if _, err := fmt.Sscanf(taskIDStr, "%d", &taskID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid task id"})
		return
	}

	task := db.GetStore().GetTask(machineID, taskID)
	if task == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "task not found"})
		return
	}

	cmdID := ws.GenerateToken()
	cmd := &ws.CommandPayload{
		CommandID: cmdID,
		Command:   "terminate_sync",
		Params: map[string]interface{}{
			"taskId": task.ID,
			"name":   task.Name,
		},
	}

	// 先等待 terminate 执行完毕再删记录，防止幽灵任务
	if hub.IsOnline(machineID) {
		result, waitErr := hub.SendCommandAndWait(machineID, cmd, 30*time.Second)
		if waitErr != nil {
			// 超时或离线：记录到 LastError 但仍删除，避免用户卡死
			task.LastError = fmt.Sprintf("terminate may have failed: %v", waitErr)
			db.GetStore().SaveTask(task)
		} else if !result.Success {
			task.LastError = fmt.Sprintf("terminate failed: %s", result.Error)
			db.GetStore().SaveTask(task)
		}
	}

	db.GetStore().DeleteTask(machineID, taskID)
	c.JSON(http.StatusOK, gin.H{"commandId": cmdID, "message": "task deleted"})
}

func doTaskCommand(c *gin.Context, hub *ws.Hub, command string) {
	machineID, err := ws.MachineIDParam(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid machine id"})
		return
	}

	taskIDStr := c.Param("taskId")
	if taskIDStr == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing task id"})
		return
	}

	var taskID uint
	if _, err := fmt.Sscanf(taskIDStr, "%d", &taskID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid task id"})
		return
	}

	task := db.GetStore().GetTask(machineID, taskID)
	if task == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "task not found"})
		return
	}

	cmdID := ws.GenerateToken()
	cmd := &ws.CommandPayload{
		CommandID: cmdID,
		Command:   command,
		Params: map[string]interface{}{
			"taskId": task.ID,
			"name":   task.Name,
		},
	}

	if err := hub.SendCommand(machineID, cmd); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": err.Error()})
		return
	}

	// 命令下发后立即触发 agent 上报状态，让前端尽快看到最新状态
	// agent 命令是串行处理的，report_status 会在前一个命令执行完后才执行
	reportCmd := &ws.CommandPayload{
		CommandID: ws.GenerateToken(),
		Command:   "report_status",
	}
	_ = hub.SendCommand(machineID, reportCmd)

	c.JSON(http.StatusOK, gin.H{"commandId": cmdID, "message": fmt.Sprintf("%s sent", command)})
}

// refreshStatus 下发 report_status，令指定机器立即上报所有会话状态
func refreshStatus(c *gin.Context, hub *ws.Hub) {
	machineID, err := ws.MachineIDParam(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid machine id"})
		return
	}
	cmdID := ws.GenerateToken()
	cmd := &ws.CommandPayload{
		CommandID: cmdID,
		Command:   "report_status",
		Params:    map[string]interface{}{},
	}
	if err := hub.SendCommand(machineID, cmd); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"commandId": cmdID})
}

// refreshRemoteAgents 让指定机器的 agent 立即检查本地 mutagen.exe +
// mutagen-agents.tar.gz 指纹，若变化则 SSH 删远端所有主机的 ~/.mutagen/agents/
// 目录，触发下次连接时自动重装新版本。
// 用于用户手动触发"强制推送新 agent 给远端"，避免每次 create/resume 都检查。
func refreshRemoteAgents(c *gin.Context, hub *ws.Hub) {
	machineID, err := ws.MachineIDParam(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid machine id"})
		return
	}
	if !hub.IsOnline(machineID) {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "machine offline"})
		return
	}
	cmdID := ws.GenerateToken()
	cmd := &ws.CommandPayload{
		CommandID: cmdID,
		Command:   "refresh_remote_agents",
		Params:    map[string]interface{}{},
	}
	// 等待 agent 返回结果（最多 60s，SSH 多主机删除可能耗时）
	result, err := hub.SendCommandAndWait(machineID, cmd, 60*time.Second)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": err.Error(), "commandId": cmdID})
		return
	}
	if !result.Success {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":     result.Error,
			"commandId": cmdID,
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"commandId": cmdID,
		"message":   result.Data,
	})
}

// resumeAllTasks 恢复指定机器的全部任务
func resumeAllTasks(c *gin.Context, hub *ws.Hub) {
	machineID, err := ws.MachineIDParam(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid machine id"})
		return
	}

	tasks := db.GetStore().ListTasks(machineID)
	if len(tasks) == 0 {
		c.JSON(http.StatusOK, gin.H{"message": "no tasks to resume", "count": 0})
		return
	}

	sent := 0
	failed := 0
	for _, t := range tasks {
		cmd := &ws.CommandPayload{
			CommandID: ws.GenerateToken(),
			Command:   "resume_sync",
			Params: map[string]interface{}{
				"taskId": t.ID,
				"name":   t.Name,
			},
		}
		if err := hub.SendCommand(machineID, cmd); err != nil {
			failed++
		} else {
			sent++
		}
	}

	c.JSON(http.StatusOK, gin.H{"message": "resume all sent", "sent": sent, "failed": failed})
}

// terminateAllTasks 终止指定机器的全部任务（先 terminate 再删除记录）
func terminateAllTasks(c *gin.Context, hub *ws.Hub) {
	machineID, err := ws.MachineIDParam(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid machine id"})
		return
	}

	tasks := db.GetStore().ListTasks(machineID)
	if len(tasks) == 0 {
		c.JSON(http.StatusOK, gin.H{"message": "no tasks to terminate", "count": 0})
		return
	}

	terminated := 0
	failed := 0
	for _, t := range tasks {
		cmd := &ws.CommandPayload{
			CommandID: ws.GenerateToken(),
			Command:   "terminate_sync",
			Params: map[string]interface{}{
				"taskId": t.ID,
				"name":   t.Name,
			},
		}
		if hub.IsOnline(machineID) {
			_, waitErr := hub.SendCommandAndWait(machineID, cmd, 15*time.Second)
			if waitErr != nil {
				task := t
				task.LastError = fmt.Sprintf("terminate may have failed: %v", waitErr)
				db.GetStore().SaveTask(&task)
				failed++
				continue
			}
		}
		db.GetStore().DeleteTask(machineID, t.ID)
		terminated++
	}

	c.JSON(http.StatusOK, gin.H{"message": "terminate all done", "terminated": terminated, "failed": failed})
}

// backupData 下载 data.json 备份文件
func backupData(c *gin.Context) {
	dataPath := db.GetStore().DataPath()
	if _, err := os.Stat(dataPath); os.IsNotExist(err) {
		c.JSON(http.StatusNotFound, gin.H{"error": "data file not found"})
		return
	}
	filename := fmt.Sprintf("mutagen-web-data-%s.json", time.Now().Format("20060102_150405"))
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	c.Header("Content-Type", "application/json")
	c.File(dataPath)
}

// getCommandTemplate 返回当前机器所有任务的 mutagen sync create 命令（用于恢复重建）
func getCommandTemplate(c *gin.Context) {
	machineID, err := ws.MachineIDParam(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid machine id"})
		return
	}

	tasks := db.GetStore().ListTasks(machineID)
	var commands []string
	for _, t := range tasks {
		var parts []string
		parts = append(parts, "mutagen", "sync", "create")
		parts = append(parts, "--name="+t.Name)
		if m := normalizeMode(t.Mode); m != "" {
			parts = append(parts, "--mode="+m)
		}
		if sm := normalizeSymlinkMode(t.SymlinkMode); sm != "" {
			parts = append(parts, "--symlink-mode="+sm)
		}
		if t.IgnoreVCS {
			parts = append(parts, "--ignore-vcs")
		}
		for _, ip := range t.IgnorePaths {
			if ip != "" {
				parts = append(parts, "--ignore="+ip)
			}
		}
		parts = append(parts, t.Alpha, t.Beta)
		commands = append(commands, strings.Join(parts, " "))
	}

	hostsCfg := db.GetStore().GetConfig(machineID, "ssh_hosts")
	var hosts []SSHHost
	if hostsCfg != nil && hostsCfg.Content != "" {
		_ = json.Unmarshal([]byte(hostsCfg.Content), &hosts)
	}
	c.JSON(http.StatusOK, CommandTemplateResponse{Commands: commands, Hosts: hosts})
}

// normalizeMode 把可能的人类可读模式名（如 "Two Way Resolved"）转回 mutagen CLI 接受的
// 小写连字符格式（如 "two-way-resolved"）。已规范的值保持不变。
func normalizeMode(m string) string {
	if m == "" {
		return ""
	}
	canon := strings.ToLower(strings.ReplaceAll(m, " ", "-"))
	switch canon {
	case "two-way-safe", "two-way-resolved", "one-way-safe", "one-way-replica":
		return canon
	}
	// 未知值原样返回，不丢用户数据
	return m
}

// normalizeSymlinkMode 同上，针对 symlink-mode 字段。
// mutagen 接受: portable | ignore | posix-raw
func normalizeSymlinkMode(m string) string {
	if m == "" {
		return ""
	}
	canon := strings.ToLower(strings.ReplaceAll(m, " ", "-"))
	switch canon {
	case "portable", "ignore", "posix-raw":
		return canon
	}
	return m
}
