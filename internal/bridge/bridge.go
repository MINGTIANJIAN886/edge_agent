package bridge

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"syscall"

	"github.com/user/agent/internal/ros"
)

const cmdPath = "/tmp/edge_bridge_cmd"

type Manager struct {
	mu sync.Mutex
}

func New() *Manager {
	return &Manager{}
}

func (m *Manager) Send(input ros.BridgeInput) error {
	data, err := json.Marshal(input)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	data = append(data, '\n')

	m.mu.Lock()
	defer m.mu.Unlock()

	f, err := os.OpenFile(cmdPath, syscall.O_WRONLY|syscall.O_CREAT|syscall.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("open cmd file: %w", err)
	}
	defer f.Close()

	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("write cmd file: %w", err)
	}
	return nil
}
