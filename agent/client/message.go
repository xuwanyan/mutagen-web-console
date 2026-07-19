package client

import (
	"encoding/json"
	"time"
)

// MessageType 定义 WebSocket 消息类型
const (
	// Agent -> Server
	MsgTypeRegister      = "register"
	MsgTypeAutoRegister  = "auto_register"
	MsgTypeHeartbeat     = "heartbeat"
	MsgTypeSyncStatus    = "sync_status"
	MsgTypeCommandResult = "command_result"

	// Server -> Agent
	MsgTypeCommand            = "command"
	MsgTypeAutoRegisterResult = "auto_register_result"
)

// Message 是 WebSocket 通信的通用消息结构
type Message struct {
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload,omitempty"`
	CommandID string          `json:"commandId,omitempty"`
	Error     string          `json:"error,omitempty"`
	Timestamp time.Time       `json:"timestamp"`
}

// RegisterPayload Agent 注册信息
type RegisterPayload struct {
	MachineID    string `json:"machineId"`
	AgentVersion string `json:"agentVersion"`
	OS           string `json:"os"`
}

// AutoRegisterPayload Agent 自动注册请求
type AutoRegisterPayload struct {
	Name         string `json:"name"`
	AgentVersion string `json:"agentVersion"`
	OS           string `json:"os"`
}

// AutoRegisterResultPayload 自动注册结果
type AutoRegisterResultPayload struct {
	Success   bool   `json:"success"`
	MachineID string `json:"machineId"`
	Token     string `json:"token"`
	Error     string `json:"error,omitempty"`
}

// HeartbeatPayload 心跳信息
type HeartbeatPayload struct {
	MachineID string `json:"machineId"`
}

// SyncStatusPayload 同步状态上报
type SyncStatusPayload struct {
	MachineID string       `json:"machineId"`
	Tasks     []TaskStatus `json:"tasks"`
}

// TaskStatus 单个任务状态
type TaskStatus struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
	Alpha  string `json:"alpha,omitempty"`
	Beta   string `json:"beta,omitempty"`
}

// CommandPayload 服务端下发的命令
type CommandPayload struct {
	Command   string                 `json:"command"`
	CommandID string                 `json:"commandId"`
	Params    map[string]interface{} `json:"params"`
}

// CommandResultPayload Agent 执行结果
type CommandResultPayload struct {
	CommandID string `json:"commandId"`
	Success   bool   `json:"success"`
	Data      string `json:"data,omitempty"`
	Error     string `json:"error,omitempty"`
}

// NewMessage 创建消息
func NewMessage(msgType string, payload interface{}) (*Message, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return &Message{
		Type:      msgType,
		Payload:   data,
		Timestamp: time.Now(),
	}, nil
}
