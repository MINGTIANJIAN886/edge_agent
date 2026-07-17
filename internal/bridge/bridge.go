package bridge

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"sync"
)

type Manager struct {
	scriptPath string
	cmd        *exec.Cmd
	mu         sync.Mutex
}

func NewManager(scriptPath string) *Manager {
	return &Manager{scriptPath: scriptPath}
}

func DetectROS2Version() (string, error) {
	entries, err := os.ReadDir("/opt/ros")
	if err != nil {
		return "", fmt.Errorf("cannot read /opt/ros: %w", err)
	}
	var versions []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join("/opt/ros", e.Name(), "setup.bash")); err == nil {
			versions = append(versions, e.Name())
		}
	}
	if len(versions) == 0 {
		return "", fmt.Errorf("no ROS2 setup.bash found in /opt/ros")
	}
	sort.Sort(sort.Reverse(sort.StringSlice(versions)))
	return versions[0], nil
}

func (m *Manager) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cmd != nil && m.cmd.Process != nil && m.cmd.ProcessState == nil {
		return fmt.Errorf("bridge already running (pid %d)", m.cmd.Process.Pid)
	}

	rosVer, err := DetectROS2Version()
	if err != nil {
		return fmt.Errorf("detect ros2: %w", err)
	}

	log.Printf("bridge: detected ROS2 version %s, launching %s", rosVer, m.scriptPath)

	cmd := exec.Command("bash", "-c",
		fmt.Sprintf("source /opt/ros/%s/setup.bash && exec python3 -u %s", rosVer, m.scriptPath))
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start bridge: %w", err)
	}

	m.cmd = cmd
	log.Printf("bridge: started (pid %d) with ROS2 %s", cmd.Process.Pid, rosVer)
	return nil
}

func (m *Manager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cmd == nil || m.cmd.Process == nil {
		return nil
	}
	if m.cmd.ProcessState != nil {
		m.cmd = nil
		return nil
	}
	log.Printf("bridge: stopping (pid %d)", m.cmd.Process.Pid)
	if err := m.cmd.Process.Signal(os.Interrupt); err != nil {
		m.cmd.Process.Kill()
	}
	m.cmd.Wait()
	m.cmd = nil
	return nil
}

func (m *Manager) Running() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cmd != nil && m.cmd.Process != nil && m.cmd.ProcessState == nil
}
