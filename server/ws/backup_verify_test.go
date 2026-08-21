package ws

import (
	"encoding/json"
	"testing"
	"time"
)

// TestSendCommandAndWait 验证下发命令 + 同步等待 agent 回执的关联机制：
// SendCommandAndWait 应把命令投递到该机器的发送队列，并在收到匹配 CommandID
// 的 command_result 后返回对应结果。
func TestSendCommandAndWait(t *testing.T) {
	h := NewHub("")
	client := &Client{Hub: h, MachineID: 1, Send: make(chan []byte, 4)}
	h.mu.Lock()
	h.clients[1] = client
	h.mu.Unlock()

	type outcome struct {
		res *CommandResultPayload
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := h.SendCommandAndWait(1, &CommandPayload{Command: "verify_backup_config"}, 2*time.Second)
		done <- outcome{res, err}
	}()

	// 读取被下发的命令，取出自动生成的 CommandID
	var dispatched []byte
	select {
	case dispatched = <-client.Send:
	case <-time.After(time.Second):
		t.Fatal("command was not dispatched to client send queue")
	}
	var msg Message
	if err := json.Unmarshal(dispatched, &msg); err != nil {
		t.Fatalf("unmarshal dispatched message: %v", err)
	}
	if msg.Type != MsgTypeCommand {
		t.Fatalf("expected command message, got %q", msg.Type)
	}
	var cmd CommandPayload
	if err := json.Unmarshal(msg.Payload, &cmd); err != nil {
		t.Fatalf("unmarshal command payload: %v", err)
	}
	if cmd.CommandID == "" {
		t.Fatal("expected auto-generated CommandID")
	}

	// 模拟 agent 回执
	resultMsg, _ := NewMessage(MsgTypeCommandResult, CommandResultPayload{
		CommandID: cmd.CommandID,
		Success:   true,
		Data:      "local backup.json: OK\nlinux-1: MATCH",
	})
	data, _ := json.Marshal(resultMsg)
	var incoming Message
	_ = json.Unmarshal(data, &incoming)
	client.handleMessage(&incoming)

	select {
	case o := <-done:
		if o.err != nil {
			t.Fatalf("SendCommandAndWait returned error: %v", o.err)
		}
		if o.res == nil || !o.res.Success {
			t.Fatalf("expected successful result, got %+v", o.res)
		}
		if o.res.Data == "" {
			t.Fatal("expected non-empty report data")
		}
	case <-time.After(time.Second):
		t.Fatal("SendCommandAndWait did not return after result delivery")
	}
}

// TestSendCommandAndWaitTimeout 验证 agent 不回执时超时返回错误。
func TestSendCommandAndWaitTimeout(t *testing.T) {
	h := NewHub("")
	client := &Client{Hub: h, MachineID: 1, Send: make(chan []byte, 4)}
	h.mu.Lock()
	h.clients[1] = client
	h.mu.Unlock()

	start := time.Now()
	_, err := h.SendCommandAndWait(1, &CommandPayload{Command: "verify_backup_config"}, 200*time.Millisecond)
	if err != ErrCommandTimeout {
		t.Fatalf("expected ErrCommandTimeout, got %v", err)
	}
	if time.Since(start) < 150*time.Millisecond {
		t.Fatal("returned before timeout elapsed")
	}
}

// TestSendCommandAndWaitOffline 验证机器离线时立即返回离线错误。
func TestSendCommandAndWaitOffline(t *testing.T) {
	h := NewHub("")
	_, err := h.SendCommandAndWait(99, &CommandPayload{Command: "verify_backup_config"}, time.Second)
	if err != ErrMachineOffline {
		t.Fatalf("expected ErrMachineOffline, got %v", err)
	}
}

// TestSendCommandAndWaitDisconnect 验证命令已下发、等待回执期间 agent 断连时，
// failPending 会让等待方立即返回 ErrMachineOffline，而非阻塞到完整超时。
func TestSendCommandAndWaitDisconnect(t *testing.T) {
	h := NewHub("")
	client := &Client{Hub: h, MachineID: 1, Send: make(chan []byte, 4)}
	h.mu.Lock()
	h.clients[1] = client
	h.mu.Unlock()

	done := make(chan error, 1)
	go func() {
		_, err := h.SendCommandAndWait(1, &CommandPayload{Command: "verify_backup_config"}, 2*time.Second)
		done <- err
	}()

	// 等命令下发，确保等待方已注册到 pending map。
	select {
	case <-client.Send:
	case <-time.After(time.Second):
		t.Fatal("command was not dispatched to client send queue")
	}

	// 模拟 agent 断连：unregister 分支会调用 failPending，这里直接调用以
	// 避免在单测中启动 Hub.Run 与 unregister 通道的完整时序。
	h.failPending(1)

	select {
	case err := <-done:
		if err != ErrMachineOffline {
			t.Fatalf("expected ErrMachineOffline, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("SendCommandAndWait did not return promptly after disconnect")
	}
}
