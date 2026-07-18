package config

import (
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	DeviceID    string      `yaml:"device_id"`
	MQTT        MQTT        `yaml:"mqtt"`
	CertAPI     string      `yaml:"cert_api"`
	Cert        Cert        `yaml:"cert"`
	Auth        Auth        `yaml:"auth"`
	DownloadDir string      `yaml:"download_dir"`
	Heartbeat   int         `yaml:"heartbeat_interval"`
	LogDir      string      `yaml:"log_dir"`
	OTA         OTA         `yaml:"ota"`
	Inference   Inference   `yaml:"inference"`
	OCR         OCR         `yaml:"ocr"`
	ROS         ROSConfig   `yaml:"ros"`
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
	Method          string `yaml:"method"`
	Token           string `yaml:"token"`
	TokenExchange   bool   `yaml:"token_exchange"`
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
	ModelDir            string `yaml:"model_dir"`             // base dir for versioned models
	CurrentSymlink      string `yaml:"current_symlink"`       // symlink path for "current" model
	BackupCount         int    `yaml:"backup_count"`          // number of old versions to keep
}

type OCR struct {
	Enabled       bool    `yaml:"enabled"`
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
	Enabled          bool    `yaml:"enabled"`
	BridgeScript1    string  `yaml:"bridge_script_ros1"`
	BridgeScript2    string  `yaml:"bridge_script_ros2"`
	PythonBin        string  `yaml:"bridge_python"`
	MaxLinearSpeed   float64 `yaml:"car_max_linear_speed"`
	MaxAngularSpeed  float64 `yaml:"car_max_angular_speed"`
	SafetyWatchdog   int     `yaml:"safety_watchdog_timeout"`
	CmdVelTopic      string  `yaml:"cmd_vel_topic"`
	BridgeResultTopic string `yaml:"bridge_result_topic"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) Save(path string) error {
	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
