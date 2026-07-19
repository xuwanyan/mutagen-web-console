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
	machines []models.Machine
	tasks    []models.SyncTask
	configs  []models.MachineConfig
	nextID   uint
}

// New 创建 Store
func New(dataPath string) (*Store, error) {
	if dataPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		dataPath = filepath.Join(home, ".mutagen-web", "data.json")
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
	return nil
}

func (s *Store) save() error {
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
	tmpPath := s.path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmpPath, s.path)
}

func (s *Store) NextID() uint {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.nextID
	s.nextID++
	_ = s.save()
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
			return &s.machines[i]
		}
	}
	return nil
}

func (s *Store) GetMachineByToken(token string) *models.Machine {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for i := range s.machines {
		if s.machines[i].Token == token {
			return &s.machines[i]
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
			return &s.tasks[i]
		}
	}
	return nil
}

func (s *Store) FindTaskByName(machineID uint, name string) *models.SyncTask {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for i := range s.tasks {
		if s.tasks[i].MachineID == machineID && s.tasks[i].Name == name {
			return &s.tasks[i]
		}
	}
	return nil
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

// Config operations
func (s *Store) GetConfig(machineID uint, configType string) *models.MachineConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for i := range s.configs {
		if s.configs[i].MachineID == machineID && s.configs[i].Type == configType {
			return &s.configs[i]
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
