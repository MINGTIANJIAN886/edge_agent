package config

import (
	"os"
	"path/filepath"
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
