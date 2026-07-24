package bridge

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"sync"
	"syscall"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/user/agent/internal/ros"
)

type Config struct {
	Enabled         bool    `yaml:"enabled"`
	ScriptROS1      string  `yaml:"bridge_script_ros1"`
	ScriptROS2      string  `yaml:"bridge_script_ros2"`
	PythonBin       string  `yaml:"bridge_python"`
	MaxLinearSpeed  float64 `yaml:"car_max_linear_speed"`
	MaxAngularSpeed float64 `yaml:"car_max_angular_speed"`
	WatchdogTimeout int     `yaml:"safety_watchdog_timeout"`
	CmdVelTopic     string  `yaml:"cmd_vel_topic"`
	ResultTopic     string  `yaml:"result_topic"`
}

type Manager struct {
	mu       sync.RWMutex
	cfg      Config
	rosVer   ros.Version
	cmd      *exec.Cmd
	cancel   context.CancelFunc
	stdin    *bufio.Writer
	running  bool
	stopped  bool
	deviceID string
	mqtt     mqtt.Client
}

func New(rosVer ros.Version, cfg Config, deviceID string, mqttClient mqtt.Client) *Manager {
	return &Manager{
		rosVer:   rosVer,
		cfg:      cfg,
		deviceID: deviceID,
		mqtt:     mqttClient,
	}
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
		return "", fmt.Errorf("no setup.bash found in /opt/ros")
	}
	sort.Sort(sort.Reverse(sort.StringSlice(versions)))
	return versions[0], nil
}

func (m *Manager) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.running {
		return fmt.Errorf("bridge already running")
	}

	script := m.cfg.ScriptROS2
	setupSource := ""
	if m.rosVer == ros.ROS1 {
		script = m.cfg.ScriptROS1
	} else if m.rosVer == ros.ROS2 {
		if ver, err := DetectROS2Version(); err == nil {
			setupSource = fmt.Sprintf("source /opt/ros/%s/setup.bash && ", ver)
		}
	}

	if script == "" {
		return fmt.Errorf("bridge script not configured for %s", m.rosVer)
	}

	pythonBin := m.cfg.PythonBin
	if pythonBin == "" {
		pythonBin = "python3"
	}

	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel

	cmdStr := fmt.Sprintf("%sexec %s -u %s", setupSource, pythonBin, script)
	cmd := exec.CommandContext(ctx, "bash", "-c", cmdStr)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return fmt.Errorf("stderr pipe: %w", err)
	}

	m.cmd = cmd
	m.stdin = bufio.NewWriter(stdin)
	m.stopped = false

	if err := cmd.Start(); err != nil {
		cancel()
		return fmt.Errorf("start bridge: %w", err)
	}
	m.running = true

	go m.readStdout(stdout)
	go m.readStderr(stderr)
	go m.waitExit()

	log.Printf("bridge started (pid=%d) for %s", cmd.Process.Pid, m.rosVer)
	return nil
}

func (m *Manager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.running || m.cmd == nil {
		return nil
	}

	m.stopped = true

	if m.cancel != nil {
		m.cancel()
	}

	if err := m.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		m.cmd.Process.Kill()
	}

	done := make(chan struct{})
	go func() {
		m.cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		m.cmd.Process.Kill()
	}

	m.running = false
	log.Println("bridge stopped")
	return nil
}

func (m *Manager) IsRunning() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.running
}

func (m *Manager) Send(input ros.BridgeInput) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if !m.running {
		return fmt.Errorf("bridge not running")
	}

	data, err := json.Marshal(input)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	if _, err := m.stdin.Write(data); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	if err := m.stdin.WriteByte('\n'); err != nil {
		return fmt.Errorf("write newline: %w", err)
	}
	return m.stdin.Flush()
}

func (m *Manager) readStdout(r io.Reader) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 65536), 65536)
	for scanner.Scan() {
		line := scanner.Bytes()
		var output ros.BridgeOutput
		if err := json.Unmarshal(line, &output); err != nil {
			log.Printf("bridge stdout: %s", string(line))
			continue
		}
		m.publishBridgeOutput(output)
	}
}

func (m *Manager) readStderr(r io.Reader) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		log.Printf("bridge stderr: %s", scanner.Text())
	}
}

func (m *Manager) waitExit() {
	err := m.cmd.Wait()
	m.mu.Lock()
	m.running = false
	wasStopped := m.stopped
	m.mu.Unlock()

	if err != nil {
		log.Printf("bridge exited: %v", err)
	} else {
		log.Println("bridge exited cleanly")
	}

	m.publishBridgeOutput(ros.BridgeOutput{
		Type:    "bridge_exit",
		Success: err == nil,
		Error:   func() string { if err != nil { return err.Error() }; return "" }(),
	})

	if !wasStopped {
		log.Println("bridge exited unexpectedly, restarting in 3s...")
		time.Sleep(3 * time.Second)
		if startErr := m.Start(); startErr != nil {
			log.Printf("bridge auto-restart failed: %v", startErr)
		}
	}
}

func (m *Manager) publishBridgeOutput(output ros.BridgeOutput) {
	data, err := json.Marshal(output)
	if err != nil {
		return
	}
	topic := fmt.Sprintf("edge/%s/bridge/result", m.deviceID)
	m.mqtt.Publish(topic, 1, false, data)
}
