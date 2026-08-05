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
	CapabilityProbe CapabilityProbeCfg `yaml:"capability_probe"`
}

// CapabilityProbeCfg configures the robot profile capability probes and
// the optional real-time web dashboard.
type CapabilityProbeCfg struct {
	Enabled         bool                       `yaml:"enabled"`
	ProbeOnStartup  bool                       `yaml:"probe_on_startup"`
	ProfilePath     string                     `yaml:"profile_path"`
	WebListen       string                     `yaml:"web_listen"` // e.g. ":8080", empty = dashboard disabled
	Intervals       map[string]int             `yaml:"intervals"` // per-capability seconds, 0/absent = no periodic
	Camera          CameraProbeCfg             `yaml:"camera"`
	OTA             OTAProbeCfg                `yaml:"ota"`
	Inference       InferenceProbeCfg          `yaml:"inference"`
}

// CameraProbeCfg configures the camera capability probe. Mode "auto"
// tries V4L2 devices first, then ROS image topics (future).
type CameraProbeCfg struct {
	Mode             string   `yaml:"mode"`
	Devices          []string `yaml:"devices"`
	RosTopics        []string `yaml:"ros_topics"`
	ScriptPath       string   `yaml:"script_path"`
	TimeoutSeconds   int      `yaml:"timeout_seconds"`
	StreamScriptPath string   `yaml:"stream_script_path"` // live MJPEG source script
	StreamFPS        int      `yaml:"stream_fps"`         // live stream frames per second
}

// OTAProbeCfg configures the OTA link probe. The test file is a small
// file in the OTA repo used to verify the download+checksum path
// without side effects. Signature checks are disabled by default until
// a signing mechanism is deployed.
type OTAProbeCfg struct {
	TestFileURL    string `yaml:"test_file_url"`
	CacheDir       string `yaml:"cache_dir"`
	MinFreeDiskMB  int64  `yaml:"min_free_disk_mb"`
	CheckPublicKey bool   `yaml:"check_public_key"`
	PublicKeyPath  string `yaml:"public_key_path"`
}

// InferenceProbeCfg configures the inference service reachability probe.
type InferenceProbeCfg struct {
	TimeoutSeconds int `yaml:"timeout_seconds"`
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
	AutoCheck           bool   `yaml:"auto_check"`            // true=periodic auto update; false=MQTT triggered only
	Task                string `yaml:"task"`                  // default task for this device, empty = not bound
	DeviceCap           DeviceCap `yaml:"device_cap"`         // device capability, auto-detected unless overridden
	Filter              OTAFilter `yaml:"filter"`             // candidate selection rules
}

// DeviceCap describes the deployment capability of this device. Zero
// values in the config mean the value is auto-detected at startup.
type DeviceCap struct {
	CPU    int  `yaml:"cpu_cores"`  // number of CPU cores, 0 = auto-detect
	MemMB  int  `yaml:"memory_mb"`  // total memory in MB, 0 = auto-detect
	HasGPU bool `yaml:"has_gpu"`    // whether a GPU accelerator is present
}

// OTAFilter holds the static selection rules. Zero threshold values mean
// the corresponding dimension is not enforced.
type OTAFilter struct {
	MaxSizeMB      float64 `yaml:"max_size_mb"`      // max model size in MB, 0 = unlimited
	MinAccuracy    float64 `yaml:"min_accuracy"`     // min accuracy threshold, 0 = unlimited
	RequiredFormat string  `yaml:"required_format"`  // e.g. "ncnn", empty = any
	MaxLatencyMS   float64 `yaml:"max_latency_ms"`   // max inference latency ms, 0 = unlimited
	PreferAccuracy bool    `yaml:"prefer_accuracy"`  // true = pick highest accuracy, false = pick latest version
	TaskMatchBonus float64 `yaml:"task_match_bonus"` // fixed bonus for task strong match, 0 = disabled
	TagBonus       float64 `yaml:"tag_bonus"`        // fixed bonus per overlapping tag, 0 = disabled
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
