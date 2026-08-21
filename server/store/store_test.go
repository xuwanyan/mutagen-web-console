package store

import (
	"path/filepath"
	"testing"

	"mutagen-web/server/models"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := New(filepath.Join(t.TempDir(), "data.json"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

// TestBulkUpsertTakeoverOnCreate: UI 创建任务（空 identifier）后 agent 上报真实会话，
// 应接管同名的空记录而非新建，结果只有 1 条记录，且 ID 保持不变。
func TestBulkUpsertTakeoverOnCreate(t *testing.T) {
	s := newTestStore(t)
	const mid uint = 1

	task := &models.SyncTask{MachineID: mid, Name: "test-sync", Alpha: `C:\sync`, Beta: "host:/p"}
	if err := s.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	before := s.ListTasks(mid)
	if len(before) != 1 {
		t.Fatalf("expected 1 after create, got %d", len(before))
	}

	reported := []models.SyncTask{{
		MachineID: mid, Name: "test-sync", Identifier: "sync_YYYY",
		Alpha: `C:\sync`, Beta: "host:/p", Status: "Watching for changes",
	}}
	if err := s.BulkUpsertSyncTaskStatus(mid, reported); err != nil {
		t.Fatalf("BulkUpsert: %v", err)
	}

	after := s.ListTasks(mid)
	if len(after) != 1 {
		t.Fatalf("expected 1 record after upsert (takeover), got %d: %+v", len(after), after)
	}
	if after[0].Identifier != "sync_YYYY" {
		t.Errorf("identifier = %q, want sync_YYYY", after[0].Identifier)
	}
	if after[0].Status != "Watching for changes" {
		t.Errorf("status = %q", after[0].Status)
	}
	if after[0].ID != before[0].ID {
		t.Errorf("ID changed %d -> %d (takeover should preserve ID)", before[0].ID, after[0].ID)
	}
}

// TestBulkUpsertCleansExistingDup: 历史遗留的重复（空记录 + 同名有 identifier 记录），
// agent 上报后应清理空记录，只剩 1 条。
func TestBulkUpsertCleansExistingDup(t *testing.T) {
	s := newTestStore(t)
	const mid uint = 1

	s.CreateTask(&models.SyncTask{MachineID: mid, Name: "dup", Identifier: "", Alpha: "a", Beta: "b"})
	s.CreateTask(&models.SyncTask{MachineID: mid, Name: "dup", Identifier: "sync_OLD", Alpha: "a", Beta: "b"})
	if len(s.ListTasks(mid)) != 2 {
		t.Fatalf("expected 2 (pre-fix dup), got %d", len(s.ListTasks(mid)))
	}

	reported := []models.SyncTask{{
		MachineID: mid, Name: "dup", Identifier: "sync_OLD", Status: "Watching for changes",
	}}
	if err := s.BulkUpsertSyncTaskStatus(mid, reported); err != nil {
		t.Fatalf("BulkUpsert: %v", err)
	}

	after := s.ListTasks(mid)
	if len(after) != 1 {
		t.Fatalf("expected 1 after cleanup, got %d: %+v", len(after), after)
	}
	if after[0].Identifier != "sync_OLD" {
		t.Errorf("kept wrong record: identifier=%q", after[0].Identifier)
	}
}

// TestBulkUpsertRecreateTakeover: 编辑重建（旧会话 X 被新会话 Y 同名取代），
// 应接管旧记录而非软标记残留，结果 1 条且非离线。
func TestBulkUpsertRecreateTakeover(t *testing.T) {
	s := newTestStore(t)
	const mid uint = 1

	s.CreateTask(&models.SyncTask{MachineID: mid, Name: "edittask", Identifier: "sync_XXX", Alpha: "a1", Beta: "b1"})

	reported := []models.SyncTask{{
		MachineID: mid, Name: "edittask", Identifier: "sync_YYY", Alpha: "a2", Beta: "b2", Status: "Watching for changes",
	}}
	if err := s.BulkUpsertSyncTaskStatus(mid, reported); err != nil {
		t.Fatalf("BulkUpsert: %v", err)
	}

	after := s.ListTasks(mid)
	if len(after) != 1 {
		t.Fatalf("expected 1 after recreate takeover, got %d: %+v", len(after), after)
	}
	if after[0].Identifier != "sync_YYY" {
		t.Errorf("identifier = %q, want sync_YYY", after[0].Identifier)
	}
	if after[0].Status == "离线" {
		t.Errorf("old record was soft-marked offline instead of taken over")
	}
}

// TestBulkUpsertSoftMarkUnreported: 有 identifier 的会话未被上报（解析遗漏等），
// 应软标记离线保留记录，不删除。
func TestBulkUpsertSoftMarkUnreported(t *testing.T) {
	s := newTestStore(t)
	const mid uint = 1

	s.CreateTask(&models.SyncTask{MachineID: mid, Name: "ghost", Identifier: "sync_G", Alpha: "a", Beta: "b"})

	if err := s.BulkUpsertSyncTaskStatus(mid, nil); err != nil {
		t.Fatalf("BulkUpsert: %v", err)
	}

	after := s.ListTasks(mid)
	if len(after) != 1 {
		t.Fatalf("expected 1 (soft-marked, not deleted), got %d", len(after))
	}
	if after[0].Status != "离线" {
		t.Errorf("status = %q, want 离线", after[0].Status)
	}
	if after[0].Identifier != "sync_G" {
		t.Errorf("identifier = %q, want sync_G (should not be cleared)", after[0].Identifier)
	}
}
