package client

import (
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
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
	ServerURL      string
	Token          string
	MachineID      string
	Name           string
	RegisterKey    string
	ConfigPath     string
	SaveConfig     ConfigSaver
	Executor       *mutagen.Executor
	conn           *websocket.Conn
	done           chan struct{}
	interrupt      chan os.Signal
	Send           chan []byte
	failedTasks    map[string]*failedTask
	ftMu           sync.Mutex
	cmdMu          sync.Mutex    // 串行化 handleCommand 执行，防止并发调用 mutagen CLI
	cfgMu          sync.RWMutex  // 保护 ServerURL/Token/MachineID 的跨 goroutine 读写
	disconnectOnce sync.Once     // 保证每个连接周期内 signalDisconnect 只执行一次，避免 data race 和重复 close panic
	configFileMod  time.Time     // 记录配置文件修改时间
	stopCh         chan struct{} // closed = 永久停止，不再重连
}

// NewAgent 创建 Agent
func NewAgent(serverURL, token, machineID, name, registerKey, configPath string, saver ConfigSaver) (*Agent, error) {
	exec, err := mutagen.NewExecutor()
	if err != nil {
		return nil, err
	}
	return &Agent{
		ServerURL:   serverURL,
		Token:       token,
		MachineID:   machineID,
		Name:        name,
		RegisterKey: registerKey,
		ConfigPath:  configPath,
		SaveConfig:  saver,
		Executor:    exec,
		done:        make(chan struct{}),
		interrupt:   make(chan os.Signal, 1),
		Send:        make(chan []byte, 256),
		failedTasks: make(map[string]*failedTask),
		stopCh:      make(chan struct{}),
	}, nil
}

// --- 受 cfgMu 保护的字段访问器，避免跨 goroutine data race ---
func (a *Agent) getServerURL() string     { a.cfgMu.RLock(); v := a.ServerURL; a.cfgMu.RUnlock(); return v }
func (a *Agent) setServerURL(v string)    { a.cfgMu.Lock(); a.ServerURL = v; a.cfgMu.Unlock() }
func (a *Agent) getToken() string         { a.cfgMu.RLock(); v := a.Token; a.cfgMu.RUnlock(); return v }
func (a *Agent) setToken(v string)        { a.cfgMu.Lock(); a.Token = v; a.cfgMu.Unlock() }
func (a *Agent) getMachineID() string     { a.cfgMu.RLock(); v := a.MachineID; a.cfgMu.RUnlock(); return v }
func (a *Agent) setMachineID(v string)    { a.cfgMu.Lock(); a.MachineID = v; a.cfgMu.Unlock() }

// stopped 判断是否收到永久停止信号
func (a *Agent) stopped() bool { select { case <-a.stopCh: return true; default: return false } }

// Run 启动 Agent（含自动重连）
func (a *Agent) Run() error {
	signal.Notify(a.interrupt, os.Interrupt)

	// 启动 mutagen daemon
	if _, err := a.Executor.DaemonStart(); err != nil {
		log.Printf("daemon start output: %v", err)
	}

	// 启动时检查一次远端 agent 是否需要更新：
	// 对比本地 mutagen.exe + mutagen-agents.tar.gz 的双指纹，
	// 变化则 SSH 删所有远端主机的 ~/.mutagen/agents/ 目录，
	// 下次 mutagen 连接时自动重装新版本。
	// 仅在启动时执行一次，避免每次 create/resume 都触发，
	// 防止 create_sync 因远端 agent 缺失导致超时失败。
	go func() {
		if err := a.Executor.EnsureRemoteAgentsFresh(false); err != nil {
			log.Printf("startup ensure remote agents fresh failed: %v", err)
		} else {
			log.Printf("startup ensure remote agents fresh completed")
		}
	}()

	for {
		// 收到永久停止信号：不再重连，直接退出
		if a.stopped() {
			log.Println("stop requested, exiting without reconnecting")
			return nil
		}

		a.done = make(chan struct{})
		a.Send = make(chan []byte, 256)
		a.disconnectOnce = sync.Once{} // 重置 once，供本周期使用

		if err := a.connect(); err != nil {
			log.Printf("connect failed: %v, retrying in 5s", err)
			select {
			case <-time.After(5 * time.Second):
			case <-a.stopCh:
				return nil
			}
			continue
		}

		// 用 WaitGroup 跟踪本轮 goroutine，确保重连/退出前它们全部退出，
		// 避免 a.done 重置后旧 goroutine 引用新 channel 导致泄漏。
		var wg sync.WaitGroup
		wg.Add(5)
		go func() { defer wg.Done(); a.readLoop() }()
		go func() { defer wg.Done(); a.writePump() }()
		go func() { defer wg.Done(); a.heartbeatLoop() }()
		go func() { defer wg.Done(); a.statusReportLoop() }()
		go func() { defer wg.Done(); a.configWatchLoop() }()

		// 连接成功后自动上报本地 ssh config，免手动导入
		go a.reportSSHConfig()

		// 等待连接断开、中断信号或永久停止
		select {
		case <-a.done:
			if a.stopped() {
				wg.Wait()
				return nil
			}
			log.Println("connection lost, reconnecting...")
			wg.Wait() // 等待所有 goroutine 退出后再重置 a.done/disconnectOnce
		case <-a.interrupt:
			log.Println("interrupt received, shutting down")
			a.signalDisconnect() // 关闭 a.done 以解除所有 goroutine 阻塞
			wg.Wait()
			return nil
		case <-a.stopCh:
			log.Println("stop requested, shutting down")
			a.signalDisconnect()
			wg.Wait()
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

// signalDisconnect 通知主循环重连（由 readLoop/writePump/configWatchLoop 在连接断开或配置变更时调用）。
// 使用 sync.Once 保证每个连接周期内只执行一次 cleanup + close(done)，
// 避免并发 goroutine 重复 close 导致 panic，同时消除裸 bool 的 data race。
func (a *Agent) signalDisconnect() {
	a.disconnectOnce.Do(func() {
		a.cleanup()
		close(a.done)
	})
}

// Close 关闭连接（用于 stop_agent 等主动退出场景），同样走 sync.Once 避免重复 close。
func (a *Agent) Close() {
	a.signalDisconnect()
}

// Stop 永久停止 agent：关闭 stopCh 使 Run() 退出重连循环并 return nil。
// 进程结束场景（Windows 服务停止、Ctrl+C）使用 Stop，而非 Close——
// Close 只关当前连接，Run() 会继续尝试重连；Stop 让 Run() 干净退出。
func (a *Agent) Stop() {
	close(a.stopCh)
}

func (a *Agent) connect() error {
	// 尝试用 token 连接
	u, err := url.Parse(a.getServerURL())
	if err != nil {
		return err
	}
	q := u.Query()
	if a.getToken() != "" {
		q.Set("token", a.getToken())
	}
	u.RawQuery = q.Encode()

	log.Printf("connecting to %s", u.String())
	conn, resp, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		// 只有服务器明确返回 401（token 无效）时才自动注册，否则重试
		isAuthFailure := resp != nil && resp.StatusCode == http.StatusUnauthorized
		if isAuthFailure && a.getToken() != "" && a.Name != "" {
			log.Printf("token rejected (401), trying auto-register...")
			origToken := a.getToken()
			origMachineID := a.getMachineID()
			a.setToken("")
			a.setMachineID("")
			u2, _ := url.Parse(a.getServerURL())
			u2.RawQuery = ""
			conn2, _, err2 := websocket.DefaultDialer.Dial(u2.String(), nil)
			if err2 != nil {
				a.setToken(origToken)
				a.setMachineID(origMachineID)
				return err2
			}
			if err := a.autoRegisterWithConn(conn2); err != nil {
				conn2.Close() // 失败路径 close，避免泄漏
				a.setToken(origToken)
				a.setMachineID(origMachineID)
				return err
			}
			a.conn = conn2 // 成功：conn2 作为 a.conn 继续使用
			return a.sendRegister(a.getMachineID())
		}
		return err
	}
	a.conn = conn

	// 如果没有 token，先自动注册
	if a.getToken() == "" || a.getMachineID() == "" {
		if a.Name == "" {
			conn.Close()
			return ErrNameRequired
		}
		if err := a.autoRegister(); err != nil {
			conn.Close()
			return err
		}
	}

	return a.sendRegister(a.getMachineID())
}

// reportSSHConfig 连接成功后自动读取本地 ~/.ssh/config 并上报给 server，
// 这样换新机器时不需要手动点"从本机导入"。
func (a *Agent) reportSSHConfig() {
	content, err := a.Executor.ReadSSHConfig()
	if err != nil {
		log.Printf("auto report ssh config failed: %v", err)
		return
	}
	if strings.TrimSpace(content) == "" {
		return
	}
	payload := SSHConfigReportPayload{
		MachineID: a.getMachineID(),
		Content:   content,
	}
	msg, err := NewMessage(MsgTypeSSHConfigReport, payload)
	if err != nil {
		log.Printf("ssh config report message error: %v", err)
		return
	}
	a.enqueue(msg)
	log.Printf("auto reported ssh config (%d bytes)", len(content))
}

func (a *Agent) autoRegisterWithConn(conn *websocket.Conn) error {
	// 只负责通过 conn 完成自动注册握手，不持有 conn、不 close conn。
	// conn 的所有权归调用方（connect()）：成功则作为 a.conn 继续使用，
	// 失败由调用方 close。
	payload := AutoRegisterPayload{
		Name:         a.Name,
		AgentVersion: AgentVersion,
		OS:           mutagen.OS(),
		RegisterKey:  a.RegisterKey,
	}
	msg, err := NewMessage(MsgTypeAutoRegister, payload)
	if err != nil {
		return err
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	conn.SetWriteDeadline(time.Now().Add(writeWait))
	if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
		return err
	}

	log.Println("waiting for auto register result...")
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	defer conn.SetReadDeadline(time.Time{})
	for {
		_, data, err := conn.ReadMessage()
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
		a.setToken(result.Token)
		a.setMachineID(result.MachineID)
		log.Printf("auto registered: machineId=%s", a.getMachineID())

		if a.SaveConfig != nil {
			if err := a.SaveConfig(a.getToken(), a.getMachineID()); err != nil {
				log.Printf("save config failed: %v", err)
			}
		}
		return nil
	}
}

func (a *Agent) autoRegister() error {
	payload := AutoRegisterPayload{
		Name:         a.Name,
		AgentVersion: AgentVersion,
		OS:           mutagen.OS(),
		RegisterKey:  a.RegisterKey,
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
		a.setToken(result.Token)
		a.setMachineID(result.MachineID)
		log.Printf("auto registered: machineId=%s", a.getMachineID())

		if a.SaveConfig != nil {
			if err := a.SaveConfig(a.getToken(), a.getMachineID()); err != nil {
				log.Printf("save config failed: %v", err)
			}
		}
		return nil
	}
}

func (a *Agent) sendRegister(curMachineID string) error {
	regPayload := RegisterPayload{
		MachineID:    curMachineID,
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
			payload := HeartbeatPayload{MachineID: a.getMachineID()}
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
					ServerURL string          `json:"server"`
					Token     string          `json:"token"`
					MachineID string          `json:"machineId"`
					Backup    json.RawMessage `json:"backup"`
				}
				if json.Unmarshal(data, &cfg) != nil {
					continue
				}

				// 检查关键字段是否变化
				changed := false
				if cfg.ServerURL != "" && cfg.ServerURL != a.getServerURL() {
					log.Printf("server URL changed: %s -> %s", a.getServerURL(), cfg.ServerURL)
					a.setServerURL(cfg.ServerURL)
					changed = true
				}
				if cfg.Token != "" && cfg.Token != a.getToken() {
					log.Printf("token changed, reconnecting")
					a.setToken(cfg.Token)
					a.setMachineID(cfg.MachineID)
					changed = true
				}

				// 检查 backup 段是否有变化，如有则写入 ~/.mutagen/backup.json
				if cfg.Backup != nil && len(cfg.Backup) > 0 {
					if err := a.Executor.UpdateBackupConfig(string(cfg.Backup)); err != nil {
						log.Printf("failed to reload backup config: %v", err)
					} else {
						log.Printf("backup config reloaded from agent-config.json")
					}
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
		identifier := t["identifier"]
		name := t["name"]
		// 以 identifier（mutagen 保证唯一、非空）为唯一键上报。
		// mutagen 允许会话无名或重名，因此不能再按 name 过滤：
		// name 为空时用 identifier 兜底作为显示名，保证每个会话都能被接管。
		if identifier == "" && name == "" {
			continue
		}
		displayName := name
		if displayName == "" {
			displayName = identifier
		}
		// 从 Configuration 章节提取配置参数。
		// mutagen 输出展示名（"Two Way Resolved"）或默认形式（"Default (Two Way Safe)"）。
		// 归一化为 CLI 标志值；默认形式归一化为空，重建时不下发 --mode，使用 mutagen 默认。
		mode := canonicalSyncMode(t["config_synchronization_mode"])
		symlinkMode := canonicalSymlinkMode(t["config_symbolic_link_mode"])
		// ignoreVcs 大小写不敏感匹配 "ignore"：真实输出首字母大写 "Ignore"，
		// 默认形式 "Default (Ignore)" 含 "ignore" 也视为启用（与 mutagen 默认一致）。
		ignoreVcs := strings.Contains(strings.ToLower(t["config_ignore_vcs_mode"]), "ignore")
		var ignorePaths []string
		if ig := t["config_ignores"]; ig != "" {
			ignorePaths = strings.Split(ig, ";")
		}

		// 解析 transition problems（path|error;path|error 格式）
		var transitionProblems []TransitionProblem
		if tp := t["transition_problems"]; tp != "" {
			for _, item := range strings.Split(tp, ";") {
				if idx := strings.Index(item, "|"); idx > 0 {
					transitionProblems = append(transitionProblems, TransitionProblem{
						Path:  item[:idx],
						Error: item[idx+1:],
					})
				}
			}
		}

		statusList = append(statusList, TaskStatus{
			Identifier:         identifier,
			Name:               displayName,
			Status:             t["status"],
			Error:              t["last error"],
			Alpha:              t["alpha_url"],
			Beta:               t["beta_url"],
			Mode:               mode,
			SymlinkMode:        symlinkMode,
			IgnoreVcs:          ignoreVcs,
			IgnorePaths:        ignorePaths,
			TransitionProblems: transitionProblems,
		})
	}

	payload := SyncStatusPayload{
		MachineID: a.getMachineID(),
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
	a.cmdMu.Lock()
	defer a.cmdMu.Unlock()

	result := &CommandResultPayload{
		CommandID: cmd.CommandID,
		Success:   true,
	}

	switch cmd.Command {
	case "ping":
		result.Data = "pong"

	case "create_sync":
		// EnsureRemoteAgentsFresh 已移到 Agent.Run() 启动时执行，不在这里触发。
		// 原因：在 create_sync 前删远端 agents 会导致 create 时远端 agent 缺失，
		// 触发自动安装，消耗 SSH 连接时间，容易让 create 超时失败。
		name := getString(cmd.Params, "name")
		alpha := getString(cmd.Params, "alpha")
		beta := getString(cmd.Params, "beta")
		mode := getString(cmd.Params, "mode")
		ignoreVcs := getBool(cmd.Params, "ignoreVcs")
		symlinkMode := getString(cmd.Params, "symlinkMode")
		ignorePaths := getStringSlice(cmd.Params, "ignorePaths")
		// 幂等创建：先终止同名会话，避免重建/重试时产生重名会话（不存在时忽略错误）
		if out, terr := a.Executor.TerminateSync(name); terr != nil {
			log.Printf("pre-create terminate %s: %s | %v", name, out, terr)
		}
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
		// EnsureRemoteAgentsFresh 已移到 Agent.Run() 启动时执行。
		name := getString(cmd.Params, "name")
		output, err := a.Executor.ResumeSync(name)
		setResult(result, output, err)

	case "terminate_sync":
		name := getString(cmd.Params, "name")
		output, err := a.Executor.TerminateSync(name)
		setResult(result, output, err)
		// 同步清理失败重试队列，避免自动重试把已删除的任务重新建回来
		a.ftMu.Lock()
		delete(a.failedTasks, name)
		a.ftMu.Unlock()

	case "refresh_remote_agents":
		// 用户主动触发：强制检查 mutagen.exe + mutagen-agents.tar.gz 指纹，
		// 即使本地指纹没变化，也会去每台远端读取 marker + 对比，确保 marker 未被篡改。
		// 用于 mutagen 升级后用户想立即推送新 agent 给远端的场景。
		if err := a.Executor.EnsureRemoteAgentsFresh(true); err != nil {
			result.Success = false
			result.Error = err.Error()
		} else {
			result.Data = "remote agents refresh check completed (force mode)"
		}

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

	case "read_ssh_config":
		content, err := a.Executor.ReadSSHConfig()
		if err != nil {
			result.Success = false
			result.Error = err.Error()
		} else {
			result.Data = content
		}

	case "update_backup_config":
		content := getString(cmd.Params, "content")
		if err := a.Executor.UpdateBackupConfig(content); err != nil {
			result.Success = false
			result.Error = "local backup.json: " + err.Error()
			break
		}
		// 同步写回 agent-config.json 的 backup 段，避免 agent 重启时用旧配置覆盖 backup.json
		if err := a.persistBackupToAgentConfig(content); err != nil {
			log.Printf("warning: persist backup to agent-config.json failed: %v", err)
		}
		result.Data = "local backup.json updated"

	case "verify_backup_config":
		home, herr := os.UserHomeDir()
		var sb strings.Builder
		if herr != nil {
			result.Success = false
			sb.WriteString("local backup.json: cannot resolve home: " + herr.Error())
		} else {
			actualPath := filepath.Join(home, ".mutagen", "backup.json")
			localBytes, lerr := os.ReadFile(actualPath)
			if lerr != nil {
				result.Success = false
				sb.WriteString("local backup.json: MISSING (" + lerr.Error() + ")")
			} else {
				expect := getString(cmd.Params, "content")
				actualNorm := normalizeJSON(localBytes)
				if expect == "" {
					sb.WriteString("local backup.json: OK (read-only: " + strings.TrimSpace(string(localBytes)) + ")")
				} else if normalizeJSON([]byte(expect)) == actualNorm {
					sb.WriteString("local backup.json: MATCH")
				} else {
					result.Success = false
					sb.WriteString("local backup.json: DIFF (actual=" + strings.TrimSpace(string(localBytes)) + ")")
				}
			}
		}
		result.Data = sb.String()

	case "update_remote_backup_config":
		content := getString(cmd.Params, "content")
		var sb2 strings.Builder
		var existingDir string
		var existingRaw string
		for _, host := range getStringSlice(cmd.Params, "remoteHosts") {
			if host == "" {
				continue
			}
			out, err := a.Executor.PushRemoteBackupConfig(host, content)
			if err != nil {
				result.Success = false
				sb2.WriteString("; remote " + host + " FAILED: " + strings.TrimSpace(out+" "+err.Error()))
			} else if strings.HasPrefix(out, "BACKUP_EXISTS:") {
				// 远端已存在 backup.json，不覆盖。回读远端内容，提取 dir 给前端回填+锁定。
				raw := strings.TrimPrefix(out, "BACKUP_EXISTS:")
				raw = strings.TrimSpace(raw)
				sb2.WriteString("; remote " + host + ": EXISTS (using existing backup config)")
				if existingRaw == "" {
					existingRaw = raw
					var cfg struct {
						Dir string `json:"dir"`
					}
					if json.Unmarshal([]byte(raw), &cfg) == nil {
						existingDir = cfg.Dir
					}
				}
			} else {
				sb2.WriteString("; remote " + host + " ok")
			}
		}
		// 用 JSON 包一层，把 existingDir 带回去给 server
		if existingDir != "" {
			wrapped, _ := json.Marshal(map[string]string{
				"summary":     "remote backup config pushed" + sb2.String(),
				"existingDir": existingDir,
				"existingRaw": existingRaw,
			})
			result.Data = string(wrapped)
		} else {
			result.Data = "remote backup config pushed" + sb2.String()
		}

	case "verify_remote_backup_config":
		var sb3 strings.Builder
		for _, host := range getStringSlice(cmd.Params, "remoteHosts") {
			if host == "" {
				continue
			}
			out, err := a.Executor.ReadRemoteBackupConfig(host)
			if err != nil {
				result.Success = false
				sb3.WriteString("\n" + host + ": FAILED (" + strings.TrimSpace(out) + ")")
				continue
			}
			sb3.WriteString("\n" + host + ": OK (" + strings.TrimSpace(out) + ")")
		}
		result.Data = "remote backup config verified" + sb3.String()

	case "list_syncs":
		output, err := a.Executor.ListSyncs()
		setResult(result, output, err)

	case "report_status":
		a.reportStatus()
		result.Data = "status reported"

	case "stop_agent":
		result.Data = "agent stopping"
		msg, _ := NewMessage(MsgTypeCommandResult, result)
		a.enqueue(msg)
		// 给 writePump 短暂窗口发送结果，再发永久停止信号。
		// 不再用 os.Exit(0)（会跳过所有 defer、丢失结果消息）。
		// 改用 close(stopCh)：Run() 主循环收到后关闭 a.done、
		// wg.Wait() 等所有 goroutine 退出后 return nil，服务由 SCM/进程正常结束。
		time.Sleep(200 * time.Millisecond)
		log.Println("received stop_agent command, requesting graceful shutdown")
		close(a.stopCh)

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

// retryFailedTasks 自动重试失败的 create_sync 任务。
// 在 statusReportLoop 的 goroutine 中调用，需持 cmdMu 避免与服务端命令
// 并发执行 mutagen CLI（加锁顺序：先 cmdMu 再 ftMu，与 handleCommand 一致）。
func (a *Agent) retryFailedTasks() {
	a.cmdMu.Lock()
	defer a.cmdMu.Unlock()
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

		// 幂等创建：先终止同名会话，防止上次半成功造成重名（不存在时忽略错误）
		a.Executor.TerminateSync(name)
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

// canonicalSyncMode 将 mutagen sync list -l 输出的同步模式展示名归一化为 CLI 标志值。
// 显式模式 "Two Way Resolved" -> "two-way-resolved"；默认形式 "Default (Two Way Safe)"
// -> ""（重建时 CreateSync 不下发 --mode，使用 mutagen 默认，避免非法标志）。
// 与 cmd.go 的 normalizeMode 逻辑一致，但此处作用于 agent 上报前。
func canonicalSyncMode(display string) string {
	lower := strings.ToLower(display)
	if lower == "" || strings.HasPrefix(lower, "default") {
		return ""
	}
	canon := strings.ReplaceAll(lower, " ", "-")
	switch canon {
	case "two-way-safe", "two-way-resolved", "one-way-safe", "one-way-replica":
		return canon
	}
	return ""
}

// canonicalSymlinkMode 同上，针对符号链接模式：portable | ignore | posix-raw。
// "POSIX Raw" -> "posix-raw"；"Default (Portable)" -> ""。
func canonicalSymlinkMode(display string) string {
	lower := strings.ToLower(display)
	if lower == "" || strings.HasPrefix(lower, "default") {
		return ""
	}
	canon := strings.ReplaceAll(lower, " ", "-")
	switch canon {
	case "portable", "ignore", "posix-raw":
		return canon
	}
	return ""
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

// persistBackupToAgentConfig 将备份配置写回 agent-config.json 的 backup 段，
// 使之与 ~/.mutagen/backup.json 保持一致，避免 agent 重启时用旧配置覆盖。
// 采用 map 合并以保留 agent-config.json 中其他字段（server/token/machineId/name）。
func (a *Agent) persistBackupToAgentConfig(content string) error {
	if a.ConfigPath == "" {
		return nil
	}
	var backupObj map[string]interface{}
	if err := json.Unmarshal([]byte(content), &backupObj); err != nil {
		return err
	}
	cfg := map[string]interface{}{}
	if data, err := os.ReadFile(a.ConfigPath); err == nil {
		_ = json.Unmarshal(data, &cfg)
	}
	cfg["backup"] = backupObj
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	// 原子写入：先写临时文件再 Rename，避免 configWatchLoop 读到半写文件
	tmpPath := a.ConfigPath + ".tmp"
	if err := os.WriteFile(tmpPath, out, 0644); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return os.Rename(tmpPath, a.ConfigPath)
}

// normalizeJSON 将 JSON 字节规范化（重新编码，键按字典序、去除空白差异），
// 用于对比两份 backup.json 内容是否等价；解析失败时回退为去空白的原文。
func normalizeJSON(data []byte) string {
	var v interface{}
	if err := json.Unmarshal(data, &v); err != nil {
		return strings.TrimSpace(string(data))
	}
	out, err := json.Marshal(v)
	if err != nil {
		return strings.TrimSpace(string(data))
	}
	return string(out)
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
