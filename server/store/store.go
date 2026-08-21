package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"mutagen-web/server/models"
)

// Store 使用 JSON 文件存储数据
type Store struct {
	path     string
	mu       sync.RWMutex
	loaded   bool // 标记数据是否已成功加载（防止 load 失败后 save 覆盖为空数据）
	machines []models.Machine
	tasks    []models.SyncTask
	configs  []models.MachineConfig
	nextID   uint
}

// DataPath 返回数据文件路径
func (s *Store) DataPath() string {
	return s.path
}

// New 创建 Store
func New(dataPath string) (*Store, error) {
	if dataPath == "" {
		// 默认使用可执行文件同目录下的 data/data.json
		// 避免 ~/.mutagen-web/ 在沙箱环境下权限不足
		exePath, err := os.Executable()
		if err != nil {
			return nil, err
		}
		exeDir := filepath.Dir(exePath)
		dataDir := filepath.Join(exeDir, "data")
		dataPath = filepath.Join(dataDir, "data.json")

		// 迁移：如果新路径不存在但旧路径有数据，则复制过来
		if _, err := os.Stat(dataPath); os.IsNotExist(err) {
			home, _ := os.UserHomeDir()
			oldPath := filepath.Join(home, ".mutagen-web", "data.json")
			if oldData, err := os.ReadFile(oldPath); err == nil {
				os.MkdirAll(dataDir, 0755)
				os.WriteFile(dataPath, oldData, 0644)
			}
		}
	}
	if err := os.MkdirAll(filepath.Dir(dataPath), 0755); err != nil {
		return nil, err
	}

	s := &Store{path: dataPath, nextID: 1}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			s.loaded = true
			return nil
		}
		return err
	}

	var snapshot struct {
		Machines []models.Machine       `json:"machines"`
		Tasks    []models.SyncTask      `json:"tasks"`
		Configs  []models.MachineConfig `json:"configs"`
		NextID   uint                   `json:"nextId"`
	}
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return err
	}

	s.machines = snapshot.Machines
	s.tasks = snapshot.Tasks
	s.configs = snapshot.Configs
	s.nextID = snapshot.NextID
	if s.nextID == 0 {
		s.nextID = 1
	}
	s.loaded = true
	return nil
}

func (s *Store) save() error {
	if !s.loaded {
		return nil
	}
	snapshot := struct {
		Machines []models.Machine       `json:"machines"`
		Tasks    []models.SyncTask      `json:"tasks"`
		Configs  []models.MachineConfig `json:"configs"`
		NextID   uint                   `json:"nextId"`
	}{
		Machines: s.machines,
		Tasks:    s.tasks,
		Configs:  s.configs,
		NextID:   s.nextID,
	}
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}
	// 原子写入：先写临时文件再 Rename，保证数据一致性
	tmpPath := s.path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		os.Remove(tmpPath)
		return err
	}
	os.Remove(s.path)
	return os.Rename(tmpPath, s.path)
}

func (s *Store) NextID() uint {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.nextID
	s.nextID++
	if err := s.save(); err != nil {
		// 保存失败日志记录，不回退 ID 避免下次冲突
		// 调用方可以继续使用已分配的 ID
	}
	return id
}

// Machine operations
func (s *Store) ListMachines() []models.Machine {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]models.Machine, len(s.machines))
	copy(result, s.machines)
	return result
}

func (s *Store) GetMachine(id uint) *models.Machine {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for i := range s.machines {
		if s.machines[i].ID == id {
			m := s.machines[i]
			return &m
		}
	}
	return nil
}

func (s *Store) GetMachineByToken(token string) *models.Machine {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for i := range s.machines {
		if s.machines[i].Token == token {
			m := s.machines[i]
			return &m
		}
	}
	return nil
}

func (s *Store) CreateMachine(m *models.Machine) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m.ID = s.nextID
	s.nextID++
	m.CreatedAt = time.Now()
	m.UpdatedAt = time.Now()
	s.machines = append(s.machines, *m)
	return s.save()
}

func (s *Store) SaveMachine(m *models.Machine) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.machines {
		if s.machines[i].ID == m.ID {
			s.machines[i] = *m
			return s.save()
		}
	}
	return nil
}

func (s *Store) DeleteMachine(id uint) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.machines {
		if s.machines[i].ID == id {
			s.machines = append(s.machines[:i], s.machines[i+1:]...)
			return s.save()
		}
	}
	return nil
}

// DeleteTasksByMachine 删除指定机器的所有同步任务记录
func (s *Store) DeleteTasksByMachine(machineID uint) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	filtered := s.tasks[:0]
	for _, t := range s.tasks {
		if t.MachineID != machineID {
			filtered = append(filtered, t)
		}
	}
	s.tasks = filtered
	return s.save()
}

// DeleteConfigsByMachine 删除指定机器的所有配置记录
func (s *Store) DeleteConfigsByMachine(machineID uint) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	filtered := s.configs[:0]
	for _, cfg := range s.configs {
		if cfg.MachineID != machineID {
			filtered = append(filtered, cfg)
		}
	}
	s.configs = filtered
	return s.save()
}

// Task operations
func (s *Store) ListTasks(machineID uint) []models.SyncTask {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]models.SyncTask, 0)
	for _, t := range s.tasks {
		if t.MachineID == machineID {
			result = append(result, t)
		}
	}
	return result
}

func (s *Store) GetTask(machineID uint, id uint) *models.SyncTask {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for i := range s.tasks {
		if s.tasks[i].MachineID == machineID && s.tasks[i].ID == id {
			t := s.tasks[i]
			return &t
		}
	}
	return nil
}

// FindTaskByName 返回任务的拷贝（非切片元素指针），调用方修改后需自行 SaveTask。
func (s *Store) FindTaskByName(machineID uint, name string) *models.SyncTask {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for i := range s.tasks {
		if s.tasks[i].MachineID == machineID && s.tasks[i].Name == name {
			t := s.tasks[i]
			return &t
		}
	}
	return nil
}

// FindTaskByIdentifier 按 mutagen 会话唯一标识符查找任务，返回拷贝（非切片元素指针），
// 调用方修改后需自行 SaveTask。identifier 由 mutagen 保证全局唯一，是去重会话的正确主键。
func (s *Store) FindTaskByIdentifier(machineID uint, identifier string) *models.SyncTask {
	if identifier == "" {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for i := range s.tasks {
		if s.tasks[i].MachineID == machineID && s.tasks[i].Identifier == identifier {
			t := s.tasks[i]
			return &t
		}
	}
	return nil
}

// UpsertSyncTaskStatus 根据 identifier 查找任务并更新状态字段；
// 命中时补写 identifier（若上游非空），未命中则新建。整个"查+改+存"在写锁内
// 原子完成，避免 Find 返回切片元素指针后锁外修改引发的 data race。这是 agent
// 上报状态时的正确入口；HTTP handler 里需要读后改的场景仍可用 Find+SaveTask。
// 只用 identifier 作为去重键，不再按 name 兜底，避免同名会话互相覆盖。
func (s *Store) UpsertSyncTaskStatus(machineID uint, identifier, name, status, errStr, alpha, beta string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var found *models.SyncTask
	// 只按 identifier 查找。identifier 为空时（理论上不会发生，
	// 因为 agent 上报时会带）跳过查找直接新建。
	if identifier != "" {
		for i := range s.tasks {
			if s.tasks[i].MachineID == machineID && s.tasks[i].Identifier == identifier {
				found = &s.tasks[i]
				break
			}
		}
	}

	now := time.Now()
	if found != nil {
		if identifier != "" {
			found.Identifier = identifier
		}
		found.Status = status
		found.LastError = errStr
		if alpha != "" {
			found.Alpha = alpha
		}
		if beta != "" {
			found.Beta = beta
		}
		found.UpdatedAt = now
		return s.save()
	}

	// 自动发现/接管：Agent 上报了 Server 无记录的会话，补建任务记录
	nt := models.SyncTask{
		MachineID:  machineID,
		Identifier: identifier,
		Name:       name,
		Alpha:      alpha,
		Beta:       beta,
		Status:     status,
		LastError:  errStr,
		UpdatedAt:  now,
	}
	nt.ID = s.nextID
	s.nextID++
	nt.CreatedAt = now
	s.tasks = append(s.tasks, nt)
	return s.save()
}

// BulkUpsertSyncTaskStatus 批量 upsert 多个任务状态，只写一次文件。
// 用于 agent 批量上报场景，避免 N 个任务各写一次文件。
// 以 identifier（mutagen 保证全局唯一、非空）作为唯一去重键，
// 不再按 name 兜底，避免同名会话互相覆盖导致只剩 1 条记录。
// 额外职责：清理——该 machineID 下已有 identifier 但不在上报列表中的任务
// 被标记为"已消失"（不删除，保留配置供用户查看/重建）。
// 没有 identifier 的任务（手动创建但尚未创建会话）不会被清理。
func (s *Store) BulkUpsertSyncTaskStatus(machineID uint, tasks []models.SyncTask) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	// 收集本次上报的所有 identifier，用于后续清理
	reportedIDs := make(map[string]bool, len(tasks))
	for _, t := range tasks {
		identifier := t.Identifier
		name := t.Name
		if identifier == "" && name == "" {
			continue
		}

		reportedIDs[identifier] = true

		var found *models.SyncTask
		// 只按 identifier 查找。identifier 为空时（理论上不会发生，
		// 因为 agent 上报时会带）跳过查找直接新建。
		if identifier != "" {
			for i := range s.tasks {
				if s.tasks[i].MachineID == machineID && s.tasks[i].Identifier == identifier {
					found = &s.tasks[i]
					break
				}
			}
		}

		if found != nil {
			if identifier != "" {
				found.Identifier = identifier
			}
			found.Status = t.Status
			found.LastError = t.LastError
			if t.Alpha != "" {
				found.Alpha = t.Alpha
			}
			if t.Beta != "" {
				found.Beta = t.Beta
			}
			if t.Mode != "" {
				found.Mode = t.Mode
			}
			if t.SymlinkMode != "" {
				found.SymlinkMode = t.SymlinkMode
			}
			found.IgnoreVCS = t.IgnoreVCS
			if t.IgnorePaths != nil {
				found.IgnorePaths = t.IgnorePaths
			}
			found.TransitionProblems = t.TransitionProblems
			found.UpdatedAt = now
		} else {
			// 没按 identifier 命中。先尝试接管一条同名且 identifier 已不在本次上报里
			// （空 identifier 的手动创建记录，或旧会话已被重建 terminate 取代）的记录，
			// 复用其 DB ID，避免重建/首次创建后出现同名重复（旧记录残留 + 新记录）。
			// 只接管 identifier 不在上报里的记录，不会抢走本次仍在活跃上报的同名会话。
			var takeover *models.SyncTask
			for i := range s.tasks {
				cand := &s.tasks[i]
				if cand.MachineID != machineID || cand.Name != name {
					continue
				}
				if cand.Identifier == "" || !reportedIDs[cand.Identifier] {
					takeover = cand
					break
				}
			}
			if takeover != nil {
				takeover.Identifier = identifier
				takeover.Status = t.Status
				takeover.LastError = t.LastError
				if t.Alpha != "" {
					takeover.Alpha = t.Alpha
				}
				if t.Beta != "" {
					takeover.Beta = t.Beta
				}
				if t.Mode != "" {
					takeover.Mode = t.Mode
				}
				if t.SymlinkMode != "" {
					takeover.SymlinkMode = t.SymlinkMode
				}
				takeover.IgnoreVCS = t.IgnoreVCS
			if t.IgnorePaths != nil {
				takeover.IgnorePaths = t.IgnorePaths
			}
			takeover.TransitionProblems = t.TransitionProblems
			takeover.UpdatedAt = now
			} else {
				nt := models.SyncTask{
				MachineID:          machineID,
				Identifier:         identifier,
				Name:               name,
				Alpha:              t.Alpha,
				Beta:               t.Beta,
				Status:             t.Status,
				LastError:          t.LastError,
				Mode:               t.Mode,
				SymlinkMode:        t.SymlinkMode,
				IgnoreVCS:          t.IgnoreVCS,
				IgnorePaths:        t.IgnorePaths,
				TransitionProblems: t.TransitionProblems,
				UpdatedAt:          now,
			}
				nt.ID = s.nextID
				s.nextID++
				nt.CreatedAt = now
				s.tasks = append(s.tasks, nt)
			}
		}
	}

	// 不删除未上报的任务，改为标记"离线"。
	// agent 上报可能因解析失败/输出异常/网络问题遗漏某些任务，
	// 直接删除会导致用户看不到这些任务的配置，无法排查或重建。
	// 标记离线后保留记录，用户可手动终止或重建。
	for i := range s.tasks {
		if s.tasks[i].MachineID == machineID && s.tasks[i].Identifier != "" && !reportedIDs[s.tasks[i].Identifier] {
			s.tasks[i].Status = "离线"
			s.tasks[i].LastError = "会话未上报，可能已终止或解析遗漏"
			s.tasks[i].UpdatedAt = now
		}
	}

	// 清理：空 identifier 的手动记录，若同名已有活跃会话（identifier 在上报里），
	// 说明它已被该会话取代（如 UI 创建后 agent 上报接管，或历史重复数据），
	// 删除避免残留同名重复。仅清理空 identifier 的"无会话桩"，不碰有 identifier 的记录。
	reportedNames := make(map[string]bool, len(tasks))
	for _, t := range tasks {
		if t.Identifier != "" && t.Name != "" {
			reportedNames[t.Name] = true
		}
	}
	if len(reportedNames) > 0 {
		kept := s.tasks[:0]
		for _, t := range s.tasks {
			if t.MachineID == machineID && t.Identifier == "" && t.Name != "" && reportedNames[t.Name] {
				continue // 被同名的活跃会话取代，删除
			}
			kept = append(kept, t)
		}
		s.tasks = kept
	}

	return s.save()
}

func (s *Store) CreateTask(t *models.SyncTask) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t.ID = s.nextID
	s.nextID++
	t.CreatedAt = time.Now()
	t.UpdatedAt = time.Now()
	s.tasks = append(s.tasks, *t)
	return s.save()
}

func (s *Store) SaveTask(t *models.SyncTask) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.tasks {
		if s.tasks[i].ID == t.ID {
			s.tasks[i] = *t
			return s.save()
		}
	}
	return nil
}

// UpdateTaskError 原子更新单个任务的 LastError 字段（写锁内查改存），
// 只改 LastError + UpdatedAt，不覆盖期间被 BulkUpsertSyncTaskStatus 等更新的
// 其他字段（Status/Identifier/Alpha/Beta/...）。用于 updateTask 异步重建
// goroutine 写回重建结果，避免用重建前的旧快照整体覆盖最新记录。
func (s *Store) UpdateTaskError(machineID, id uint, lastError string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.tasks {
		if s.tasks[i].MachineID == machineID && s.tasks[i].ID == id {
			s.tasks[i].LastError = lastError
			s.tasks[i].UpdatedAt = time.Now()
			return s.save()
		}
	}
	return nil
}

func (s *Store) DeleteTask(machineID uint, id uint) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.tasks {
		if s.tasks[i].MachineID == machineID && s.tasks[i].ID == id {
			s.tasks = append(s.tasks[:i], s.tasks[i+1:]...)
			return s.save()
		}
	}
	return nil
}

// DeleteTaskByIdentifier 按 identifier 删除任务记录。
// 用于重建会话后立即清理旧 identifier 的"幽灵"记录，避免前端出现重名。
func (s *Store) DeleteTaskByIdentifier(machineID uint, identifier string) {
	if identifier == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.tasks[:0]
	for _, t := range s.tasks {
		if t.MachineID == machineID && t.Identifier == identifier {
			continue
		}
		kept = append(kept, t)
	}
	s.tasks = kept
	s.save()
}

// Config operations
func (s *Store) GetConfig(machineID uint, configType string) *models.MachineConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for i := range s.configs {
		if s.configs[i].MachineID == machineID && s.configs[i].Type == configType {
			c := s.configs[i]
			return &c
		}
	}
	return nil
}

func (s *Store) SaveConfig(cfg *models.MachineConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.configs {
		if s.configs[i].MachineID == cfg.MachineID && s.configs[i].Type == cfg.Type {
			s.configs[i] = *cfg
			return s.save()
		}
	}
	cfg.ID = s.nextID
	s.nextID++
	s.configs = append(s.configs, *cfg)
	return s.save()
}
