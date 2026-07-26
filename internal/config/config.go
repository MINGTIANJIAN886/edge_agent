package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	DeviceID    string    `yaml:"device_id"`
	MQTT        MQTT      `yaml:"mqtt"`
	CertAPI     string    `yaml:"cert_api"`
	Cert        Cert      `yaml:"cert"`
	Auth        Auth      `yaml:"auth"`
	DownloadDir string    `yaml:"download_dir"`
	Heartbeat   int       `yaml:"heartbeat_interval"`
	LogDir      string    `yaml:"log_dir"`
	OTA         OTA       `yaml:"ota"`
	Inference   Inference `yaml:"inference"`
	OCR         OCR       `yaml:"ocr"`
	ROS         ROSConfig `yaml:"ros"`
}

type MQTT struct {
	Broker   string `yaml:"broker"`
	Port     int    `yaml:"port"`
	ClientID string `yaml:"client_id"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
	Topic    Topic  `yaml:"topic"`
}

type Cert struct {
	CertFile   string `yaml:"cert_file"`
	KeyFile    string `yaml:"key_file"`
	CAFile     string `yaml:"ca_file"`
	AutoEnroll bool   `yaml:"auto_enroll"`
	Token      string `yaml:"token"`
}

type Auth struct {
	Method        string `yaml:"method"`
	Token         string `yaml:"token"`
	TokenExchange bool   `yaml:"token_exchange"`
}

type Topic struct {
	Command     string `yaml:"command"`
	Download    string `yaml:"download"`
	Heartbeat   string `yaml:"heartbeat"`
	Result      string `yaml:"result"`
	Register    string `yaml:"register"`
	MCPRegister string `yaml:"mcp_register"`
	MCPCall     string `yaml:"mcp_call"`
}

type OTA struct {
	ServerURL           string `yaml:"server_url"`
	ModelPath           string `yaml:"model_path"`
	VersionPath         string `yaml:"version_path"`
	CheckInterval       int    `yaml:"check_interval"`
	CurrentVersion      string `yaml:"current_version"`
	ModelFile           string `yaml:"model_file"`
	InferenceRestartCmd string `yaml:"inference_restart_cmd"`
	ModelDir            string `yaml:"model_dir"`       // base dir for versioned models
	CurrentSymlink      string `yaml:"current_symlink"` // symlink path for "current" model
	BackupCount         int    `yaml:"backup_count"`    // number of old versions to keep
}

type OCR struct {
	Enabled       bool    `yaml:"enabled"`
	PythonBin     string  `yaml:"python_bin"`
	ScriptPath    string  `yaml:"script_path"`
	Interval      int     `yaml:"interval"`
	ConfThreshold float64 `yaml:"conf_threshold"`
	CommandTopic  string  `yaml:"command_topic"`
	ResultTopic   string  `yaml:"result_topic"`
}

type Inference struct {
	ServiceURL string `yaml:"service_url"`
	Timeout    int    `yaml:"timeout"`
}

type ROSConfig struct {
	Enabled           bool    `yaml:"enabled"`
	BridgeScript1     string  `yaml:"bridge_script_ros1"`
	BridgeScript2     string  `yaml:"bridge_script_ros2"`
	PythonBin         string  `yaml:"bridge_python"`
	MaxLinearSpeed    float64 `yaml:"car_max_linear_speed"`
	MaxAngularSpeed   float64 `yaml:"car_max_angular_speed"`
	SafetyWatchdog    int     `yaml:"safety_watchdog_timeout"`
	CmdVelTopic       string  `yaml:"cmd_vel_topic"`
	BridgeResultTopic string  `yaml:"bridge_result_topic"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg := &Config{}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(cfg); err != nil {
		return nil, err
	}
	cfg.setDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) Save(path string) error {
	c.setDefaults()
	if err := c.Validate(); err != nil {
		return err
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return os.Chmod(path, 0600)
}

func (c *Config) setDefaults() {
	if c.DownloadDir == "" {
		c.DownloadDir = "/tmp/agent/downloads"
	}
	if c.Heartbeat <= 0 {
		c.Heartbeat = 30
	}
	if c.MQTT.Port == 0 {
		c.MQTT.Port = 8883
	}
	if c.MQTT.ClientID == "" && c.DeviceID != "" {
		c.MQTT.ClientID = "agent-" + c.DeviceID
	}
	if c.DeviceID != "" {
		prefix := "edge/" + c.DeviceID
		if c.MQTT.Topic.Command == "" {
			c.MQTT.Topic.Command = prefix + "/command"
		}
		if c.MQTT.Topic.Download == "" {
			c.MQTT.Topic.Download = prefix + "/download"
		}
		if c.MQTT.Topic.Heartbeat == "" {
			c.MQTT.Topic.Heartbeat = prefix + "/heartbeat"
		}
		if c.MQTT.Topic.Result == "" {
			c.MQTT.Topic.Result = prefix + "/result"
		}
		if c.MQTT.Topic.Register == "" {
			c.MQTT.Topic.Register = prefix + "/register"
		}
		if c.MQTT.Topic.MCPRegister == "" {
			c.MQTT.Topic.MCPRegister = prefix + "/mcp/register"
		}
		if c.MQTT.Topic.MCPCall == "" {
			c.MQTT.Topic.MCPCall = prefix + "/mcp/call"
		}
	}
	if c.Auth.Method == "" {
		c.Auth.Method = "password"
	}
	if c.ROS.CmdVelTopic == "" {
		c.ROS.CmdVelTopic = "/cmd_vel"
	}
	if c.ROS.MaxLinearSpeed <= 0 {
		c.ROS.MaxLinearSpeed = 2.0
	}
	if c.ROS.MaxAngularSpeed <= 0 {
		c.ROS.MaxAngularSpeed = 3.14
	}
	if c.ROS.SafetyWatchdog <= 0 {
		c.ROS.SafetyWatchdog = 5
	}
	if c.OCR.PythonBin == "" {
		c.OCR.PythonBin = "python3"
	}
}

func (c *Config) Validate() error {
	if strings.TrimSpace(c.DeviceID) == "" {
		return fmt.Errorf("device_id is required")
	}
	if strings.TrimSpace(c.MQTT.Broker) == "" {
		return fmt.Errorf("mqtt.broker is required")
	}
	if c.MQTT.Port < 1 || c.MQTT.Port > 65535 {
		return fmt.Errorf("mqtt.port must be between 1 and 65535")
	}
	switch c.Auth.Method {
	case "password", "token", "cert", "admin":
	default:
		return fmt.Errorf("auth.method must be password, token, cert, or admin")
	}
	if (c.Cert.CertFile == "") != (c.Cert.KeyFile == "") {
		return fmt.Errorf("cert.cert_file and cert.key_file must be configured together")
	}
	if c.ROS.Enabled && !strings.HasPrefix(c.ROS.CmdVelTopic, "/") {
		return fmt.Errorf("ros.cmd_vel_topic must be a ROS topic beginning with /")
	}
	return nil
}
