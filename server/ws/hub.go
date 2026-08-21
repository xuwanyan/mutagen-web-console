package ws

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"mutagen-web/server/db"
	"mutagen-web/server/models"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// upgrader 不设置 CheckOrigin，使用 gorilla/websocket 默认的同源检查：
// - 无 Origin 头（非浏览器客户端，如 Go agent）→ 允许
// - Origin 与 Host 同源（浏览器同域请求）→ 允许
// - 跨域浏览器请求 → 拒绝，防止 CSRF 式 WebSocket 攻击
var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

// Client 表示一个 Agent 连接
type Client struct {
	Hub       *Hub
	Conn      *websocket.Conn
	MachineID uint
	Token     string
	Send      chan []byte
}

// pendingEntry 记录一个等待 agent 回执的请求。machineID 用于在 agent 断连时
// 定位并通知所有等待该机器的请求立即返回，避免它们阻塞到完整超时。
type pendingEntry struct {
	ch        chan *CommandResultPayload
	machineID uint
}

// Hub 管理所有 Agent 连接
type Hub struct {
	clients     map[uint]*Client
	register    chan *Client
	unregister  chan *Client
	mu          sync.RWMutex
	pending     map[string]*pendingEntry // 按 CommandID 等待 agent 回执
	pendMu      sync.Mutex
	registerKey string // 可选注册密钥：非空时 auto_register 必须携带此密钥

	// SSHConfigHandler 由 handlers 包注入，处理 agent 自动上报的 ssh config。
	// 避免循环依赖：ws 包不直接 import handlers。
	SSHConfigHandler func(machineID uint, content string, hub *Hub)
}

func NewHub(registerKey string) *Hub {
	return &Hub{
		clients:     make(map[uint]*Client),
		register:    make(chan *Client),
		unregister:  make(chan *Client),
		pending:     make(map[string]*pendingEntry),
		registerKey: registerKey,
	}
}

func (h *Hub) Run() {
	for {
		select {
		case client := <-h.register:
			h.mu.Lock()
			if old, ok := h.clients[client.MachineID]; ok {
				old.Conn.Close()
			}
			h.clients[client.MachineID] = client
			h.mu.Unlock()
			log.Printf("Machine %d connected", client.MachineID)

		case client := <-h.unregister:
			h.mu.Lock()
			// 只在 map 里仍是同一个 client 时才删除，避免重连后旧连接的
			// unregister 把新连接踢掉（重连场景：register 已用新连接覆盖 map）。
			if current, ok := h.clients[client.MachineID]; ok && current == client {
				delete(h.clients, client.MachineID)
				close(client.Send)
			}
			h.mu.Unlock()
			// 通知所有等待该机器回执的请求立即返回离线错误，
			// 不必阻塞到完整超时。
			h.failPending(client.MachineID)
			log.Printf("Machine %d disconnected", client.MachineID)

		}
	}
}

func (h *Hub) GetClient(machineID uint) *Client {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.clients[machineID]
}

func (h *Hub) IsOnline(machineID uint) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	_, ok := h.clients[machineID]
	return ok
}

// Disconnect 断开指定机器的 agent 连接
func (h *Hub) Disconnect(machineID uint) {
	h.mu.Lock()
	if client, ok := h.clients[machineID]; ok {
		client.Conn.Close()
		delete(h.clients, machineID)
		close(client.Send)
		log.Printf("Machine %d disconnected forcefully", machineID)
	}
	h.mu.Unlock()
	// 主动踢线同样要通知等待回执的请求
	h.failPending(machineID)
}

// failPending 在 agent 断连时通知所有等待该机器回执的请求返回离线错误。
// 用 nil 信号（而非 close(ch)）投递，避免与 command_result 的非阻塞投递
// 竞争导致 send-on-closed-channel panic；channel 已有结果时保留原结果。
func (h *Hub) failPending(machineID uint) {
	h.pendMu.Lock()
	defer h.pendMu.Unlock()
	for id, entry := range h.pending {
		if entry.machineID != machineID {
			continue
		}
		select {
		case entry.ch <- nil:
		default: // channel 已有结果，保留原结果
		}
		delete(h.pending, id)
	}
}

func (h *Hub) SendCommand(machineID uint, cmd *CommandPayload) error {
	msg, err := NewMessage(MsgTypeCommand, cmd)
	if err != nil {
		return err
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	// 在 RLock 内完成"查 client + 投递"，与 unregister 的 close(client.Send)
	// （持写锁）互斥，保证不会向已关闭 channel 发送导致 panic。
	h.mu.RLock()
	defer h.mu.RUnlock()
	client, ok := h.clients[machineID]
	if !ok {
		return ErrMachineOffline
	}
	select {
	case client.Send <- data:
		return nil
	default:
		return ErrSendBufferFull
	}
}

// SendCommandAndWait 下发命令并同步等待 agent 回执（用于需要返回结果的命令，如校验）。
// agent 在等待期间断连时，failPending 会向通道投递 nil，本方法立即返回
// ErrMachineOffline，无需阻塞到完整超时。
func (h *Hub) SendCommandAndWait(machineID uint, cmd *CommandPayload, timeout time.Duration) (*CommandResultPayload, error) {
	if cmd.CommandID == "" {
		cmd.CommandID = GenerateToken()
	}
	entry := &pendingEntry{
		ch:        make(chan *CommandResultPayload, 1),
		machineID: machineID,
	}
	h.pendMu.Lock()
	h.pending[cmd.CommandID] = entry
	h.pendMu.Unlock()
	defer func() {
		h.pendMu.Lock()
		delete(h.pending, cmd.CommandID)
		h.pendMu.Unlock()
	}()

	if err := h.SendCommand(machineID, cmd); err != nil {
		return nil, err
	}
	select {
	case res := <-entry.ch:
		if res == nil {
			// agent 断连，failPending 投递的 nil 信号
			return nil, ErrMachineOffline
		}
		return res, nil
	case <-time.After(timeout):
		return nil, ErrCommandTimeout
	}
}

var (
	ErrMachineOffline   = &HubError{Message: "machine offline"}
	ErrSendBufferFull   = &HubError{Message: "send buffer full"}
	ErrInvalidToken     = &HubError{Message: "invalid token"}
	ErrMissingMachineID = &HubError{Message: "missing machine id"}
	ErrCommandTimeout   = &HubError{Message: "command timeout: agent did not respond"}
)

type HubError struct {
	Message string
}

func (e *HubError) Error() string {
	return e.Message
}

// HandleAgentWebSocket 处理 Agent WebSocket 连接
func (h *Hub) HandleAgentWebSocket(c *gin.Context) {
	token := c.Query("token")

	var machineID uint
	if token != "" {
		machine := db.GetStore().GetMachineByToken(token)
		if machine == nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
			return
		}
		machineID = machine.ID
	}

	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Printf("upgrade error: %v", err)
		return
	}

	client := &Client{
		Hub:       h,
		Conn:      conn,
		MachineID: machineID,
		Token:     token,
		Send:      make(chan []byte, 256),
	}

	h.register <- client

	go client.writePump()
	go client.readPump()
}

func (c *Client) readPump() {
	defer func() {
		c.Hub.unregister <- c
		c.Conn.Close()
	}()

	c.Conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	c.Conn.SetPongHandler(func(string) error {
		c.Conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	for {
		_, data, err := c.Conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("websocket error: %v", err)
			}
			break
		}

		var msg Message
		if err := json.Unmarshal(data, &msg); err != nil {
			log.Printf("unmarshal error: %v", err)
			continue
		}

		c.handleMessage(&msg)
	}
}

func (c *Client) writePump() {
	ticker := time.NewTicker(30 * time.Second)
	defer func() {
		ticker.Stop()
		c.Conn.Close()
	}()

	for {
		select {
		case message, ok := <-c.Send:
			c.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok {
				c.Conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.Conn.WriteMessage(websocket.TextMessage, message); err != nil {
				// 写失败（conn 已坏）：退出 writePump，defer 关闭 conn，
				// 进而触发 readPump 失败 → unregister，避免命令静默丢失。
				return
			}

		case <-ticker.C:
			c.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func (c *Client) handleMessage(msg *Message) {
	switch msg.Type {
	case MsgTypeRegister:
		var payload RegisterPayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil {
			log.Printf("register payload error: %v", err)
			return
		}
		m := db.GetStore().GetMachine(c.MachineID)
		if m != nil {
			now := time.Now()
			m.LastSeenAt = &now
			m.AgentVersion = payload.AgentVersion
			m.OS = payload.OS
			db.GetStore().SaveMachine(m)
		}

	case MsgTypeAutoRegister:
		var payload AutoRegisterPayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil {
			log.Printf("auto register payload error: %v", err)
			c.sendAutoRegisterResult(false, "", "", "invalid payload")
			return
		}
		if payload.Name == "" {
			c.sendAutoRegisterResult(false, "", "", "name is required")
			return
		}
		if c.MachineID != 0 {
			c.sendAutoRegisterResult(false, "", "", "already registered")
			return
		}
		// 自动注册必须携带服务端配置的注册密钥。/ws/agent 对无 token 连接开放，
		// 若不强制 registerKey，任何能连到该端口的未授权方都能注册新机器并拿到
		// 合法 token，以受信 agent 身份接入。未设 -register-key 时禁用自动注册，
		// 请在 web UI 预创建机器（生成 token 写入安装包）。
		if c.Hub.registerKey == "" {
			c.sendAutoRegisterResult(false, "", "", "auto-registration disabled: set -register-key on server, or pre-register machine in web UI")
			return
		}
		if payload.RegisterKey != c.Hub.registerKey {
			c.sendAutoRegisterResult(false, "", "", "invalid register key")
			return
		}

		machine := models.Machine{
			Name:  payload.Name,
			Token: GenerateToken(),
		}
		if err := db.GetStore().CreateMachine(&machine); err != nil {
			c.sendAutoRegisterResult(false, "", "", err.Error())
			return
		}

		c.MachineID = machine.ID
		c.Token = machine.Token

		now := time.Now()
		machine.LastSeenAt = &now
		machine.AgentVersion = payload.AgentVersion
		machine.OS = payload.OS
		db.GetStore().SaveMachine(&machine)

		c.sendAutoRegisterResult(true, formatUint(machine.ID), machine.Token, "")
		log.Printf("auto registered machine: %d %s", machine.ID, machine.Name)

	case MsgTypeHeartbeat:
		m := db.GetStore().GetMachine(c.MachineID)
		if m != nil {
			now := time.Now()
			m.LastSeenAt = &now
			db.GetStore().SaveMachine(m)
		}

	case MsgTypeSyncStatus:
		var payload SyncStatusPayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil {
			log.Printf("sync status payload error: %v", err)
			return
		}
		// 批量 upsert：所有任务在单次写锁内完成，只写一次文件
		bulk := make([]models.SyncTask, 0, len(payload.Tasks))
		for _, task := range payload.Tasks {
			if task.Identifier == "" && task.Name == "" {
				continue
			}
			bulk = append(bulk, models.SyncTask{
			Identifier:         task.Identifier,
			Name:               task.Name,
			Status:             task.Status,
			LastError:          task.Error,
			Alpha:              task.Alpha,
			Beta:               task.Beta,
			Mode:               task.Mode,
			SymlinkMode:        task.SymlinkMode,
			IgnoreVCS:          task.IgnoreVcs,
			IgnorePaths:        task.IgnorePaths,
			TransitionProblems: task.TransitionProblems,
		})
		}
		// 即使 bulk 为空也要调用，让 store 有机会清理已不存在的会话
		if err := db.GetStore().BulkUpsertSyncTaskStatus(c.MachineID, bulk); err != nil {
			log.Printf("bulk upsert sync task status failed (machine %d): %v", c.MachineID, err)
		}

	case MsgTypeCommandResult:
		var payload CommandResultPayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil {
			log.Printf("command result payload error: %v", err)
			return
		}
		log.Printf("command %s result: success=%v, error=%s", payload.CommandID, payload.Success, payload.Error)
		// 若有 HTTP 请求在等待该命令回执，投递结果
		c.Hub.pendMu.Lock()
		entry, ok := c.Hub.pending[payload.CommandID]
		c.Hub.pendMu.Unlock()
		if ok {
			p := payload
			select {
			case entry.ch <- &p:
			default:
			}
		}

	case MsgTypeSSHConfigReport:
		var payload SSHConfigReportPayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil {
			log.Printf("ssh config report payload error: %v", err)
			return
		}
		if c.Hub.SSHConfigHandler != nil {
			log.Printf("auto ssh config report from machine %d (%d bytes)", c.MachineID, len(payload.Content))
			c.Hub.SSHConfigHandler(c.MachineID, payload.Content, c.Hub)
		}
	}
}

func (c *Client) sendAutoRegisterResult(success bool, machineID, token, errMsg string) {
	result := AutoRegisterResultPayload{
		Success:   success,
		MachineID: machineID,
		Token:     token,
		Error:     errMsg,
	}
	msg, err := NewMessage(MsgTypeAutoRegisterResult, result)
	if err != nil {
		log.Printf("new auto register result error: %v", err)
		return
	}
	data, err := json.Marshal(msg)
	if err != nil {
		log.Printf("marshal auto register result error: %v", err)
		return
	}
	c.Send <- data
}

func formatUint(v uint) string {
	return strconv.FormatUint(uint64(v), 10)
}

// GenerateToken 生成随机 token
func GenerateToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand 失败意味着系统 CSPRNG 不可用，无法继续安全运行。
		log.Fatalf("crypto/rand.Read failed: %v", err)
	}
	return hex.EncodeToString(b)
}

// MachineIDParam 从 URL 解析 machine id
func MachineIDParam(c *gin.Context) (uint, error) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		return 0, err
	}
	return uint(id), nil
}
