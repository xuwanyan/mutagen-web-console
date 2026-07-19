package client

import (
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"sync"
	"time"

	"mutagen-web/agent/mutagen"

	"github.com/gorilla/websocket"
)

const (
	AgentVersion = "0.1.0"
	writeWait    = 10 * time.Second
)

// ConfigSaver 配置保存回调
type ConfigSaver func(token, machineID string) error

// failedTask 记录失败的任务参数，用于自动重试
type failedTask struct {
	Params      map[string]interface{}
	Retries     int
	LastAttempt time.Time
}

// Agent 表示一个 Agent 客户端
type Agent struct {
	ServerURL     string
	Token         string
	MachineID     string
	Name          string
	ConfigPath    string
	SaveConfig    ConfigSaver
	Executor      *mutagen.Executor
	conn          *websocket.Conn
	done          chan struct{}
	interrupt     chan os.Signal
	Send          chan []byte
	failedTasks   map[string]*failedTask
	ftMu          sync.Mutex
	reconnecting  bool
	configFileMod time.Time // 记录配置文件修改时间
}

// NewAgent 创建 Agent
func NewAgent(serverURL, token, machineID, name, configPath string, saver ConfigSaver) (*Agent, error) {
	exec, err := mutagen.NewExecutor()
	if err != nil {
		return nil, err
	}
	return &Agent{
		ServerURL:   serverURL,
		Token:       token,
		MachineID:   machineID,
		Name:        name,
		ConfigPath:  configPath,
		SaveConfig:  saver,
		Executor:    exec,
		done:        make(chan struct{}),
		interrupt:   make(chan os.Signal, 1),
		Send:        make(chan []byte, 256),
		failedTasks: make(map[string]*failedTask),
	}, nil
}

// Run 启动 Agent（含自动重连）
func (a *Agent) Run() error {
	signal.Notify(a.interrupt, os.Interrupt)

	// 启动 mutagen daemon
	if _, err := a.Executor.DaemonStart(); err != nil {
		log.Printf("daemon start output: %v", err)
	}

	for {
		a.done = make(chan struct{})
		a.Send = make(chan []byte, 256)
		a.reconnecting = false

		if err := a.connect(); err != nil {
			log.Printf("connect failed: %v, retrying in 5s", err)
			time.Sleep(5 * time.Second)
			continue
		}

		go a.readLoop()
		go a.writePump()
		go a.heartbeatLoop()
		go a.statusReportLoop()
		go a.configWatchLoop()

		// 等待连接断开或中断信号
		select {
		case <-a.done:
			log.Println("connection lost, reconnecting...")
			// 继续循环，自动重连
		case <-a.interrupt:
			log.Println("interrupt received, shutting down")
			a.cleanup()
			return nil
		}
	}
}

// cleanup 清理连接资源
func (a *Agent) cleanup() {
	if a.conn != nil {
		a.conn.Close()
		a.conn = nil
	}
}

// signalDisconnect 通知主循环重连（由 readLoop 在连接断开时调用）
func (a *Agent) signalDisconnect() {
	if !a.reconnecting {
		a.reconnecting = true
		a.cleanup()
		// 重新创建 done 以触发主循环重新连接
		close(a.done)
	}
}

// Close 关闭连接
func (a *Agent) Close() {
	a.cleanup()
	close(a.done)
}

func (a *Agent) connect() error {
	// 尝试用 token 连接
	u, err := url.Parse(a.ServerURL)
	if err != nil {
		return err
	}
	q := u.Query()
	if a.Token != "" {
		q.Set("token", a.Token)
	}
	u.RawQuery = q.Encode()

	log.Printf("connecting to %s", u.String())
	conn, resp, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		// 只有服务器明确返回 401（token 无效）时才自动注册，否则重试
		isAuthFailure := resp != nil && resp.StatusCode == http.StatusUnauthorized
		if isAuthFailure && a.Token != "" && a.Name != "" {
			log.Printf("token rejected (401), trying auto-register...")
			a.Token = ""
			a.MachineID = ""
			u2, _ := url.Parse(a.ServerURL)
			u2.RawQuery = ""
			conn2, _, err2 := websocket.DefaultDialer.Dial(u2.String(), nil)
			if err2 != nil {
				return err2
			}
			conn = conn2
			a.conn = conn
			if err := a.autoRegister(); err != nil {
				return err
			}
			return a.sendRegister()
		}
		return err
	}
	a.conn = conn

	// 如果没有 token，先自动注册
	if a.Token == "" || a.MachineID == "" {
		if a.Name == "" {
			return ErrNameRequired
		}
		if err := a.autoRegister(); err != nil {
			return err
		}
	}

	// 发送注册消息
	return a.sendRegister()
}

func (a *Agent) autoRegister() error {
	payload := AutoRegisterPayload{
		Name:         a.Name,
		AgentVersion: AgentVersion,
		OS:           mutagen.OS(),
	}
	msg, err := NewMessage(MsgTypeAutoRegister, payload)
	if err != nil {
		return err
	}
	if err := a.send(msg); err != nil {
		return err
	}

	log.Println("waiting for auto register result...")
	// 此时 readLoop 尚未启动，同步读取自动注册结果
	a.conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	defer a.conn.SetReadDeadline(time.Time{})
	for {
		_, data, err := a.conn.ReadMessage()
		if err != nil {
			return ErrAutoRegisterTimeout
		}
		var resp Message
		if err := json.Unmarshal(data, &resp); err != nil {
			continue
		}
		if resp.Type != MsgTypeAutoRegisterResult {
			continue
		}
		var result AutoRegisterResultPayload
		if err := json.Unmarshal(resp.Payload, &result); err != nil {
			return err
		}
		if !result.Success {
			return NewAutoRegisterError(result.Error)
		}
		a.Token = result.Token
		a.MachineID = result.MachineID
		log.Printf("auto registered: machineId=%s", a.MachineID)

		// 保存配置到本地
		if a.SaveConfig != nil {
			if err := a.SaveConfig(a.Token, a.MachineID); err != nil {
				log.Printf("save config failed: %v", err)
			}
		}
		return nil
	}
}

func (a *Agent) sendRegister() error {
	regPayload := RegisterPayload{
		MachineID:    a.MachineID,
		AgentVersion: AgentVersion,
		OS:           mutagen.OS(),
	}
	msg, err := NewMessage(MsgTypeRegister, regPayload)
	if err != nil {
		return err
	}
	return a.send(msg)
}

func (a *Agent) send(msg *Message) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	a.conn.SetWriteDeadline(time.Now().Add(writeWait))
	return a.conn.WriteMessage(websocket.TextMessage, data)
}

// enqueue 将消息投递到发送队列，由 writePump 统一串行写入，
// 避免多个 goroutine 并发写同一个 websocket 连接（gorilla/websocket 不支持并发写）。
func (a *Agent) enqueue(msg *Message) {
	data, err := json.Marshal(msg)
	if err != nil {
		log.Printf("marshal message error: %v", err)
		return
	}
	select {
	case a.Send <- data:
	case <-a.done:
	}
}

func (a *Agent) readLoop() {
	defer func() {
		log.Println("read loop ended, signaling disconnect")
		a.signalDisconnect()
	}()

	a.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	a.conn.SetPongHandler(func(string) error {
		a.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	for {
		_, data, err := a.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("read error: %v", err)
			}
			return
		}

		var msg Message
		if err := json.Unmarshal(data, &msg); err != nil {
			log.Printf("unmarshal error: %v", err)
			continue
		}

		a.handleMessage(&msg)
	}
}

func (a *Agent) writePump() {
	ticker := time.NewTicker(30 * time.Second)
	defer func() {
		ticker.Stop()
		log.Println("write pump ended, signaling disconnect")
		a.signalDisconnect()
	}()

	for {
		select {
		case <-a.done:
			return
		case message, ok := <-a.Send:
			if !ok {
				return
			}
			a.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := a.conn.WriteMessage(websocket.TextMessage, message); err != nil {
				return
			}

		case <-ticker.C:
			a.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := a.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func (a *Agent) handleMessage(msg *Message) {
	switch msg.Type {
	case MsgTypeCommand:
		var cmd CommandPayload
		if err := json.Unmarshal(msg.Payload, &cmd); err != nil {
			log.Printf("command payload error: %v", err)
			return
		}
		go a.handleCommand(&cmd)
	}
}

func (a *Agent) heartbeatLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-a.done:
			return
		case <-ticker.C:
			payload := HeartbeatPayload{MachineID: a.MachineID}
			msg, err := NewMessage(MsgTypeHeartbeat, payload)
			if err != nil {
				continue
			}
			a.enqueue(msg)
		}
	}
}

func (a *Agent) statusReportLoop() {
	ticker := time.NewTicker(10 * time.Second)
	retryTicker := time.NewTicker(30 * time.Second)
	defer func() {
		ticker.Stop()
		retryTicker.Stop()
	}()

	for {
		select {
		case <-a.done:
			return
		case <-ticker.C:
			a.reportStatus()
		case <-retryTicker.C:
			a.retryFailedTasks()
		}
	}
}

// configWatchLoop 定期检查 agent-config.json 是否有变化
func (a *Agent) configWatchLoop() {
	if a.ConfigPath == "" {
		return
	}

	// 初始化文件修改时间
	if fi, err := os.Stat(a.ConfigPath); err == nil {
		a.configFileMod = fi.ModTime()
	}

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-a.done:
			return
		case <-ticker.C:
			fi, err := os.Stat(a.ConfigPath)
			if err != nil {
				continue
			}
			if fi.ModTime().After(a.configFileMod) {
				a.configFileMod = fi.ModTime()
				log.Printf("config file changed, reloading: %s", a.ConfigPath)

				// 重新读取配置文件
				data, err := os.ReadFile(a.ConfigPath)
				if err != nil {
					continue
				}
				var cfg struct {
					ServerURL string `json:"server"`
					Token     string `json:"token"`
					MachineID string `json:"machineId"`
				}
				if json.Unmarshal(data, &cfg) != nil {
					continue
				}

				// 检查关键字段是否变化
				changed := false
				if cfg.ServerURL != "" && cfg.ServerURL != a.ServerURL {
					log.Printf("server URL changed: %s -> %s", a.ServerURL, cfg.ServerURL)
					a.ServerURL = cfg.ServerURL
					changed = true
				}
				if cfg.Token != "" && cfg.Token != a.Token {
					log.Printf("token changed, reconnecting")
					a.Token = cfg.Token
					a.MachineID = cfg.MachineID
					changed = true
				}

				if changed {
					a.signalDisconnect()
				}
			}
		}
	}
}

func (a *Agent) reportStatus() {
	output, err := a.Executor.SyncStatus()
	if err != nil {
		log.Printf("sync status error: %v, output: %s", err, output)
	}

	tasks := a.Executor.ParseStatus(output)
	var statusList []TaskStatus
	for _, t := range tasks {
		name := t["name"]
		if name == "" {
			continue
		}
		statusList = append(statusList, TaskStatus{
			Name:   name,
			Status: t["status"],
			Error:  t["last error"],
			Alpha:  t["alpha_url"],
			Beta:   t["beta_url"],
		})
	}

	payload := SyncStatusPayload{
		MachineID: a.MachineID,
		Tasks:     statusList,
	}
	msg, err := NewMessage(MsgTypeSyncStatus, payload)
	if err != nil {
		log.Printf("status message error: %v", err)
		return
	}
	a.enqueue(msg)
}

func (a *Agent) handleCommand(cmd *CommandPayload) {
	result := &CommandResultPayload{
		CommandID: cmd.CommandID,
		Success:   true,
	}

	switch cmd.Command {
	case "ping":
		result.Data = "pong"

	case "create_sync":
		name := getString(cmd.Params, "name")
		alpha := getString(cmd.Params, "alpha")
		beta := getString(cmd.Params, "beta")
		mode := getString(cmd.Params, "mode")
		ignoreVcs := getBool(cmd.Params, "ignoreVcs")
		symlinkMode := getString(cmd.Params, "symlinkMode")
		ignorePaths := getStringSlice(cmd.Params, "ignorePaths")
		output, err := a.Executor.CreateSync(name, alpha, beta, mode, ignoreVcs, symlinkMode, ignorePaths)
		if err != nil {
			result.Success = false
			result.Error = output + "\n" + err.Error()
			// 加入失败队列，自动重试
			a.ftMu.Lock()
			a.failedTasks[name] = &failedTask{
				Params:      cmd.Params,
				Retries:     0,
				LastAttempt: time.Now(),
			}
			a.ftMu.Unlock()
		} else {
			result.Data = output
			// 如果之前有失败记录，清除
			a.ftMu.Lock()
			delete(a.failedTasks, name)
			a.ftMu.Unlock()
		}

	case "pause_sync":
		name := getString(cmd.Params, "name")
		output, err := a.Executor.PauseSync(name)
		setResult(result, output, err)

	case "resume_sync":
		name := getString(cmd.Params, "name")
		output, err := a.Executor.ResumeSync(name)
		setResult(result, output, err)

	case "terminate_sync":
		name := getString(cmd.Params, "name")
		output, err := a.Executor.TerminateSync(name)
		setResult(result, output, err)

	case "update_global_config":
		content := getString(cmd.Params, "content")
		if err := a.Executor.UpdateGlobalConfig(content); err != nil {
			result.Success = false
			result.Error = err.Error()
		} else {
			result.Data = "global config updated"
		}

	case "update_ssh_config":
		content := getString(cmd.Params, "content")
		if err := a.Executor.UpdateSSHConfig(content); err != nil {
			result.Success = false
			result.Error = err.Error()
		} else {
			result.Data = "ssh config updated"
		}

	case "list_syncs":
		output, err := a.Executor.ListSyncs()
		setResult(result, output, err)

	case "report_status":
		a.reportStatus()
		result.Data = "status reported"

	case "stop_agent":
		result.Data = "agent stopping"
		// 先发结果，再退出
		msg, _ := NewMessage(MsgTypeCommandResult, result)
		a.enqueue(msg)
		time.Sleep(500 * time.Millisecond)
		log.Println("received stop_agent command, cleaning up and exiting")
		if a.ConfigPath != "" {
			os.Remove(a.ConfigPath)
			log.Printf("removed config: %s", a.ConfigPath)
		}
		a.Close()
		os.Exit(0)

	default:
		result.Success = false
		result.Error = "unknown command: " + cmd.Command
	}

	msg, err := NewMessage(MsgTypeCommandResult, result)
	if err != nil {
		log.Printf("result message error: %v", err)
		return
	}
	a.enqueue(msg)
}

// retryFailedTasks 自动重试失败的 create_sync 任务
func (a *Agent) retryFailedTasks() {
	a.ftMu.Lock()
	defer a.ftMu.Unlock()

	for name, ft := range a.failedTasks {
		if time.Since(ft.LastAttempt) < 30*time.Second {
			continue
		}
		if ft.Retries >= 10 {
			log.Printf("task %s: max retries reached, removing", name)
			delete(a.failedTasks, name)
			continue
		}

		alpha := getString(ft.Params, "alpha")
		beta := getString(ft.Params, "beta")
		mode := getString(ft.Params, "mode")
		ignoreVcs := getBool(ft.Params, "ignoreVcs")
		symlinkMode := getString(ft.Params, "symlinkMode")
		ignorePaths := getStringSlice(ft.Params, "ignorePaths")

		output, err := a.Executor.CreateSync(name, alpha, beta, mode, ignoreVcs, symlinkMode, ignorePaths)
		ft.Retries++
		ft.LastAttempt = time.Now()

		if err == nil {
			log.Printf("task %s: auto-retry succeeded (attempt %d)", name, ft.Retries)
			delete(a.failedTasks, name)
		} else {
			log.Printf("task %s: auto-retry failed (attempt %d): %s | %v", name, ft.Retries, output, err)
		}
	}
}

type agentError string

func (e agentError) Error() string {
	return string(e)
}

const (
	ErrNameRequired        agentError = "name is required for auto register"
	ErrAutoRegisterTimeout agentError = "auto register timeout"
)

func NewAutoRegisterError(msg string) error {
	return agentError("auto register failed: " + msg)
}

func setResult(result *CommandResultPayload, output string, err error) {
	if err != nil {
		result.Success = false
		result.Error = output + "\n" + err.Error()
	} else {
		result.Data = output
	}
}

func getString(m map[string]interface{}, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func getBool(m map[string]interface{}, key string) bool {
	if v, ok := m[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return false
}

func getStringSlice(m map[string]interface{}, key string) []string {
	var result []string
	if v, ok := m[key]; ok {
		if arr, ok := v.([]interface{}); ok {
			for _, item := range arr {
				if s, ok := item.(string); ok {
					result = append(result, s)
				}
			}
		}
	}
	return result
}
