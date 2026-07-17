package config

import (
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	DeviceID    string   `yaml:"device_id"`
	MQTT        MQTT     `yaml:"mqtt"`
	CertAPI     string   `yaml:"cert_api"`
	Cert        Cert     `yaml:"cert"`
	Auth        Auth     `yaml:"auth"`
	DownloadDir string   `yaml:"download_dir"`
	Heartbeat   int      `yaml:"heartbeat_interval"`
	LogDir      string   `yaml:"log_dir"`
	OCR         OCR      `yaml:"ocr"`
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

type OCR struct {
	Enabled       bool    `yaml:"enabled"`
	ScriptPath    string  `yaml:"script_path"`
	Interval      int     `yaml:"interval"`
	ConfThreshold float64 `yaml:"conf_threshold"`
	CommandTopic  string  `yaml:"command_topic"`
	ResultTopic   string  `yaml:"result_topic"`
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
