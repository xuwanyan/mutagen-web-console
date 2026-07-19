package handlers

import (
	"fmt"
	"net/http"

	"mutagen-web/server/db"
	"mutagen-web/server/models"
	"mutagen-web/server/ws"

	"github.com/gin-gonic/gin"
)

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
		g.POST("/:taskId/pause", func(c *gin.Context) { pauseTask(c, hub) })
		g.POST("/:taskId/resume", func(c *gin.Context) { resumeTask(c, hub) })
		g.DELETE("/:taskId", func(c *gin.Context) { deleteTask(c, hub) })
		g.POST("/:taskId/retry", func(c *gin.Context) { retryTask(c, hub) })
		g.PUT("/:taskId", func(c *gin.Context) { updateTask(c, hub) })
	}
	r.POST("/api/machines/:id/refresh-status", func(c *gin.Context) { refreshStatus(c, hub) })
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
		c.JSON(http.StatusOK, gin.H{"task": task, "saved": true, "error": err.Error()})
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

	if err := db.GetStore().SaveTask(task); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 如果 agent 在线，terminate 旧同步 + create 新同步
	if hub.IsOnline(machineID) {
		// 先 terminate 旧的
		termCmd := &ws.CommandPayload{
			CommandID: ws.GenerateToken(),
			Command:   "terminate_sync",
			Params:    map[string]interface{}{"name": task.Name},
		}
		hub.SendCommand(machineID, termCmd)

		// 再 create 新的
		createCmd := &ws.CommandPayload{
			CommandID: ws.GenerateToken(),
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
		if err := hub.SendCommand(machineID, createCmd); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": err.Error(), "saved": true, "task": task})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{"message": "task updated", "task": task})
}

func pauseTask(c *gin.Context, hub *ws.Hub) {
	doTaskCommand(c, hub, "pause_sync")
}

func resumeTask(c *gin.Context, hub *ws.Hub) {
	doTaskCommand(c, hub, "resume_sync")
}

func deleteTask(c *gin.Context, hub *ws.Hub) {
	doTaskCommand(c, hub, "terminate_sync")
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

	if command == "terminate_sync" {
		db.GetStore().DeleteTask(machineID, taskID)
	}

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
