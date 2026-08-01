package config

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func TestLoadAppliesSafeDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	data := []byte("device_id: jetson-01\nmqtt:\n  broker: mqtt.example.com\n")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MQTT.ClientID != "agent-jetson-01" {
		t.Fatalf("unexpected client ID: %q", cfg.MQTT.ClientID)
	}
	if cfg.MQTT.Topic.Command != "edge/jetson-01/command" {
		t.Fatalf("unexpected command topic: %q", cfg.MQTT.Topic.Command)
	}
	if cfg.ROS.CmdVelTopic != "/cmd_vel" {
		t.Fatalf("unexpected ROS cmd_vel topic: %q", cfg.ROS.CmdVelTopic)
	}
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	data := []byte("device_id: jetson-01\nmqtt:\n  broker: mqtt.example.com\nunknown_option: true\n")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}

	if _, err := Load(path); err == nil {
		t.Fatal("expected unknown field to be rejected")
	}
}

func TestLoadAppliesOrderedDeviceOverlays(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(`
schema_version: 1
device_id: shared-device
device_profile: generic-linux
mqtt:
  broker: mqtt.example.com
  username: shared-user
ros:
  cmd_vel_topic: /cmd_vel
`), 0600); err != nil {
		t.Fatal(err)
	}

	overlayDir := filepath.Join(dir, "config.d")
	if err := os.MkdirAll(overlayDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(overlayDir, "10-platform.yaml"), []byte(`
device_profile: raspberry-pi
runtime:
  workspace_setup: /home/pi/robot_ws/install/setup.bash
`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(overlayDir, "20-device.yaml"), []byte(`
device_id: pi-02
mqtt:
  username: pi-user
ros:
  cmd_vel_topic: /robot/cmd_vel
`), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DeviceID != "pi-02" || cfg.DeviceProfile != ProfileRaspberryPi {
		t.Fatalf("unexpected device identity: %#v", cfg)
	}
	if cfg.MQTT.Broker != "mqtt.example.com" || cfg.MQTT.Username != "pi-user" {
		t.Fatalf("overlay did not preserve shared MQTT settings: %#v", cfg.MQTT)
	}
	if cfg.MQTT.ClientID != "agent-pi-02" {
		t.Fatalf("client ID = %q", cfg.MQTT.ClientID)
	}
	if cfg.ROS.CmdVelTopic != "/robot/cmd_vel" {
		t.Fatalf("cmd_vel topic = %q", cfg.ROS.CmdVelTopic)
	}
	if cfg.Runtime.WorkspaceSetup != "/home/pi/robot_ws/install/setup.bash" {
		t.Fatalf("workspace setup = %q", cfg.Runtime.WorkspaceSetup)
	}
}

func TestLoadRejectsUnknownOverlayFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("device_id: jetson-01\nmqtt:\n  broker: mqtt.example.com\n"), 0600); err != nil {
		t.Fatal(err)
	}
	overlayDir := filepath.Join(dir, "config.d")
	if err := os.MkdirAll(overlayDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(overlayDir, "10-invalid.yaml"), []byte("unknown_option: true\n"), 0600); err != nil {
		t.Fatal(err)
	}

	if _, err := Load(path); err == nil {
		t.Fatal("expected unknown overlay field to be rejected")
	}
}

func TestDetectDeviceProfile(t *testing.T) {
	tests := []struct {
		name    string
		release string
		model   string
		want    string
	}{
		{name: "jetson r32", release: "# R32 (release), REVISION: 6.1", want: ProfileJetsonR32},
		{name: "jetson r36", release: "# R36 (release), REVISION: 4.3", want: ProfileJetsonR36},
		{name: "raspberry pi", model: "Raspberry Pi 5 Model B Rev 1.0\x00", want: ProfileRaspberryPi},
		{name: "generic", model: "Generic ARM board", want: ProfileGenericLinux},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := detectDeviceProfile(tt.release, tt.model); got != tt.want {
				t.Fatalf("profile = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRepositoryConfigExamplesDecode(t *testing.T) {
	basePath := filepath.Join("..", "..", "configs", "config.example.yaml")
	cfg := &Config{}
	if err := decodeFile(basePath, cfg); err != nil {
		t.Fatalf("base example: %v", err)
	}

	overlays, err := filepath.Glob(filepath.Join("..", "..", "configs", "overlays", "*.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(overlays)
	if len(overlays) == 0 {
		t.Fatal("no overlay examples found")
	}
	for _, overlay := range overlays {
		copyCfg := *cfg
		if err := decodeFile(overlay, &copyCfg); err != nil {
			t.Fatalf("%s: %v", overlay, err)
		}
	}
}

func TestSaveUsesPrivatePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent", "config.yaml")
	cfg := &Config{
		DeviceID: "jetson-01",
		MQTT: MQTT{
			Broker: "mqtt.example.com",
		},
	}
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0600 {
		t.Fatalf("config mode = %o, want 600", got)
	}
}
