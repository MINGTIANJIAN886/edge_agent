package profile

import (
	"log"
	"sync"
	"time"

	"github.com/user/agent/internal/ota"
)

// TaskModelInfo is the device's live answer to "what am I doing and
// with which model": the current task plus the model deployed by the
// last OTA auto-download.
type TaskModelInfo struct {
	Task         string    `json:"task"`           // current device task, e.g. object_detection
	RequestedTask string   `json:"requested_task"` // task the last OTA targeted
	Model        string    `json:"model"`          // model file name in use
	Version      string    `json:"version"`        // deployed model version
	ModelTask    string    `json:"model_task"`     // task the model was trained for
	Accuracy     float64   `json:"accuracy"`
	Format       string    `json:"format"`
	LatencyMS    float64   `json:"latency_ms"`
	SizeMB       float64   `json:"size_mb"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// TaskTracker keeps the current task (fed by MCP task calls) and the
// model deployed by OTA. Both are persisted into the robot profile so
// the dashboard and /profile always show what the device is doing and
// with which model. Changes are broadcast so SSE clients update live.
type TaskTracker struct {
	mu      sync.RWMutex
	task    string
	model   TaskModelInfo
	store   *Store
	subs    map[chan struct{}]struct{}
}

func NewTaskTracker(store *Store, defaultTask string) *TaskTracker {
	t := &TaskTracker{
		task:  defaultTask,
		store: store,
		subs:  make(map[chan struct{}]struct{}),
	}
	t.loadFromStore()
	return t
}

func (t *TaskTracker) loadFromStore() {
	if t.store == nil {
		return
	}
	if p, err := t.store.Load(); err == nil {
		t.task = p.Task
		if p.Model != nil {
			t.model = *p.Model
		}
	}
}

// SetTask records the task the device is currently executing (called by
// the MCP task handlers) and persists it.
func (t *TaskTracker) SetTask(name string) {
	t.mu.Lock()
	if t.task == name {
		t.mu.Unlock()
		return
	}
	t.task = name
	t.persistLocked()
	t.mu.Unlock()
	t.notify()
	log.Printf("task tracker: current task = %s", name)
}

// RecordModel records a completed OTA auto-download, tagging it with the
// device's current task, and persists the pair.
func (t *TaskTracker) RecordModel(info ota.UpdateInfo) {
	t.mu.Lock()
	t.model = TaskModelInfo{
		Task:          t.task,
		RequestedTask: info.RequestedTask,
		Model:         info.Model,
		Version:       info.Version,
		ModelTask:     info.ModelTask,
		Accuracy:      info.Accuracy,
		Format:        info.Format,
		LatencyMS:     info.LatencyMS,
		SizeMB:        info.SizeMB,
		UpdatedAt:     info.UpdatedAt,
	}
	t.persistLocked()
	t.mu.Unlock()
	t.notify()
	log.Printf("task tracker: model updated task=%s model=%s version=%s", t.model.Task, t.model.Model, t.model.Version)
}

// SeedVersion records the currently deployed model version (from the
// OTA symlink) so the profile has real data before the next download.
func (t *TaskTracker) SeedVersion(version string) {
	t.mu.Lock()
	if t.model.Version != "" && t.model.Version != version {
		t.mu.Unlock()
		return
	}
	t.model = TaskModelInfo{
		Task:      t.task,
		Version:   version,
		UpdatedAt: time.Now(),
	}
	t.persistLocked()
	t.mu.Unlock()
	t.notify()
	log.Printf("task tracker: seeded current model version %s", version)
}

func (t *TaskTracker) Current() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.task
}

func (t *TaskTracker) Model() TaskModelInfo {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.model
}

func (t *TaskTracker) persistLocked() {
	if t.store == nil {
		return
	}
	if p, err := t.store.Load(); err == nil {
		p.Task = t.task
		model := t.model
		p.Model = &model
		t.store.Save(p)
	}
}

func (t *TaskTracker) Subscribe(ch chan struct{}) {
	t.mu.Lock()
	t.subs[ch] = struct{}{}
	t.mu.Unlock()
}

func (t *TaskTracker) Unsubscribe(ch chan struct{}) {
	t.mu.Lock()
	delete(t.subs, ch)
	t.mu.Unlock()
}

func (t *TaskTracker) notify() {
	t.mu.RLock()
	defer t.mu.RUnlock()
	for ch := range t.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}
