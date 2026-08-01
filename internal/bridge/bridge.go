package bridge

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/MINGTIANJIAN886/edge_agent/internal/ros"
)

const (
	cmdPath        = "/tmp/edge_bridge_cmd"
	maxCommandSize = 1024 * 1024
)

type Manager struct {
	mu            sync.Mutex
	stateMu       sync.Mutex
	ver           ros.Version
	topic         string
	maxLinear     float64
	maxAngular    float64
	safetyTimeout time.Duration
	lastCommand   time.Time
	watchdogArmed bool
}

func New(ver ros.Version, topic string, maxLinear, maxAngular float64, safetyWatchdog int) *Manager {
	manager := &Manager{
		ver:           ver,
		topic:         topic,
		maxLinear:     maxLinear,
		maxAngular:    maxAngular,
		safetyTimeout: time.Duration(safetyWatchdog) * time.Second,
	}
	if manager.safetyTimeout > 0 {
		go manager.watchdog()
	}
	return manager
}

func (m *Manager) Send(input ros.BridgeInput) error {
	data, err := json.Marshal(input)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	data = append(data, '\n')

	m.mu.Lock()
	defer m.mu.Unlock()

	if info, err := os.Stat(cmdPath); err == nil && info.Size() >= maxCommandSize {
		if err := os.Truncate(cmdPath, 0); err != nil {
			return fmt.Errorf("truncate command file: %w", err)
		}
	}

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

func (m *Manager) SendTwist(ver ros.Version, topic string, linearX, angularZ, maxLinear, maxAngular float64) error {
	input, err := BuildTwistInput(ver, topic, linearX, angularZ, maxLinear, maxAngular)
	if err != nil {
		return err
	}
	return m.Send(input)
}

func (m *Manager) EmergencyStop(ver ros.Version, topic string) error {
	return m.SendTwist(ver, topic, 0, 0, 0, 0)
}

func (m *Manager) SendVelocity(linearX, angularZ float64) error {
	if err := m.SendTwist(m.ver, m.topic, linearX, angularZ, m.maxLinear, m.maxAngular); err != nil {
		return err
	}
	m.stateMu.Lock()
	m.lastCommand = time.Now()
	m.watchdogArmed = linearX != 0 || angularZ != 0
	m.stateMu.Unlock()
	return nil
}

func (m *Manager) StopVehicle() error {
	if err := m.EmergencyStop(m.ver, m.topic); err != nil {
		return err
	}
	m.stateMu.Lock()
	m.watchdogArmed = false
	m.stateMu.Unlock()
	return nil
}

func BuildTwistInput(ver ros.Version, topic string, linearX, angularZ, maxLinear, maxAngular float64) (ros.BridgeInput, error) {
	msgType := map[ros.Version]string{
		ros.ROS1: "geometry_msgs/Twist",
		ros.ROS2: "geometry_msgs/msg/Twist",
	}[ver]
	if msgType == "" {
		return ros.BridgeInput{}, fmt.Errorf("unsupported ROS version: %s", ver)
	}
	if topic == "" {
		topic = "/cmd_vel"
	}

	data, err := json.Marshal(map[string]interface{}{
		"linear": map[string]float64{
			"x": clamp(linearX, maxLinear),
			"y": 0,
			"z": 0,
		},
		"angular": map[string]float64{
			"x": 0,
			"y": 0,
			"z": clamp(angularZ, maxAngular),
		},
	})
	if err != nil {
		return ros.BridgeInput{}, fmt.Errorf("marshal twist: %w", err)
	}

	return ros.BridgeInput{
		Cmd:     "publish",
		Topic:   topic,
		MsgType: msgType,
		Data:    data,
	}, nil
}

func clamp(value, limit float64) float64 {
	if limit <= 0 {
		return value
	}
	return math.Max(-limit, math.Min(limit, value))
}

func (m *Manager) watchdog() {
	ticker := time.NewTicker(minDuration(m.safetyTimeout/2, time.Second))
	defer ticker.Stop()

	for range ticker.C {
		m.stateMu.Lock()
		expired := m.watchdogArmed && time.Since(m.lastCommand) >= m.safetyTimeout
		if expired {
			m.watchdogArmed = false
		}
		m.stateMu.Unlock()

		if expired {
			if err := m.EmergencyStop(m.ver, m.topic); err != nil {
				log.Printf("ROS safety watchdog stop failed: %v", err)
			} else {
				log.Printf("ROS safety watchdog stopped the robot after %s without a command", m.safetyTimeout)
			}
		}
	}
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
