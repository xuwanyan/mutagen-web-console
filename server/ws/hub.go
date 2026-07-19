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

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
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

// Hub 管理所有 Agent 连接
type Hub struct {
	clients    map[uint]*Client
	register   chan *Client
	unregister chan *Client
	broadcast  chan []byte
	mu         sync.RWMutex
}

func NewHub() *Hub {
	return &Hub{
		clients:    make(map[uint]*Client),
		register:   make(chan *Client),
		unregister: make(chan *Client),
		broadcast:  make(chan []byte),
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
			if _, ok := h.clients[client.MachineID]; ok {
				delete(h.clients, client.MachineID)
				close(client.Send)
			}
			h.mu.Unlock()
			log.Printf("Machine %d disconnected", client.MachineID)

		case message := <-h.broadcast:
			h.mu.RLock()
			for _, client := range h.clients {
				select {
				case client.Send <- message:
				default:
					close(client.Send)
				}
			}
			h.mu.RUnlock()
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
	defer h.mu.Unlock()
	if client, ok := h.clients[machineID]; ok {
		client.Conn.Close()
		delete(h.clients, machineID)
		close(client.Send)
		log.Printf("Machine %d disconnected forcefully", machineID)
	}
}

func (h *Hub) SendCommand(machineID uint, cmd *CommandPayload) error {
	client := h.GetClient(machineID)
	if client == nil {
		return ErrMachineOffline
	}

	msg, err := NewMessage(MsgTypeCommand, cmd)
	if err != nil {
		return err
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	select {
	case client.Send <- data:
		return nil
	default:
		return ErrSendBufferFull
	}
}

var (
	ErrMachineOffline   = &HubError{Message: "machine offline"}
	ErrSendBufferFull   = &HubError{Message: "send buffer full"}
	ErrInvalidToken     = &HubError{Message: "invalid token"}
	ErrMissingMachineID = &HubError{Message: "missing machine id"}
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
			c.Conn.WriteMessage(websocket.TextMessage, message)

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
		for _, task := range payload.Tasks {
			t := db.GetStore().FindTaskByName(c.MachineID, task.Name)
			if t != nil {
				t.Status = task.Status
				t.LastError = task.Error
				if task.Alpha != "" {
					t.Alpha = task.Alpha
				}
				if task.Beta != "" {
					t.Beta = task.Beta
				}
				t.UpdatedAt = time.Now()
				db.GetStore().SaveTask(t)
			} else {
				// 自动发现/接管：Agent 上报了 Server 无记录的会话，补建任务记录
				nt := models.SyncTask{
					MachineID: c.MachineID,
					Name:      task.Name,
					Alpha:     task.Alpha,
					Beta:      task.Beta,
					Status:    task.Status,
					LastError: task.Error,
					UpdatedAt: time.Now(),
				}
				db.GetStore().CreateTask(&nt)
			}
		}

	case MsgTypeCommandResult:
		var payload CommandResultPayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil {
			log.Printf("command result payload error: %v", err)
			return
		}
		log.Printf("command %s result: success=%v, error=%s", payload.CommandID, payload.Success, payload.Error)
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
	rand.Read(b)
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
