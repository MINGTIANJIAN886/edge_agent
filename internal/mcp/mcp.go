package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

type RegisterRequest struct {
	DeviceID     string   `json:"device_id"`
	Hostname     string   `json:"hostname"`
	Version      string   `json:"version"`
	Capabilities []string `json:"capabilities"`
	Status       string   `json:"status"`
}

type RegisterResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Token   string `json:"token,omitempty"`
}

type ToolDefinition struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema InputSchema `json:"input_schema"`
}

type InputSchema struct {
	Type       string                    `json:"type"`
	Properties map[string]SchemaProperty `json:"properties"`
	Required   []string                  `json:"required,omitempty"`
}

type SchemaProperty struct {
	Type        string   `json:"type"`
	Description string   `json:"description,omitempty"`
	Enum        []string `json:"enum,omitempty"`
}

type MCPRegisterMsg struct {
	DeviceID string          `json:"device_id"`
	Tools    []ToolDefinition `json:"tools"`
}

const agentVersion = "1.0.0"

func Register(apiURL, deviceID, hostname string) error {
	req := RegisterRequest{
		DeviceID: deviceID,
		Hostname: hostname,
		Version:  agentVersion,
		Status:   "online",
		Capabilities: []string{
			"remote_command",
			"heartbeat",
			"file_download",
			"mcp_register",
		},
	}
	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Post(apiURL+"/api/agent/register", "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	var result RegisterResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return fmt.Errorf("parse: %s", string(respBody))
	}
	if !result.Success {
		return fmt.Errorf("failed: %s", result.Message)
	}
	log.Printf("MCP registration: %s", result.Message)
	return nil
}

func PublishTools(client mqtt.Client, deviceID, topic string) {
	tools := []ToolDefinition{
		{
			Name:        "device_info",
			Description: "Get device system information (CPU, memory, disk, uptime)",
			InputSchema: InputSchema{
				Type:       "object",
				Properties: map[string]SchemaProperty{},
			},
		},
		{
			Name:        "execute_command",
			Description: "Execute a shell command on the device and return output",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]SchemaProperty{
					"command": {Type: "string", Description: "Shell command to execute"},
					"timeout": {Type: "integer", Description: "Command timeout in seconds"},
				},
				Required: []string{"command"},
			},
		},
		{
			Name:        "download_file",
			Description: "Download a file from URL to the device",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]SchemaProperty{
					"url":       {Type: "string", Description: "Download URL"},
					"dest_path": {Type: "string", Description: "Destination file path"},
				},
				Required: []string{"url"},
			},
		},
		{
			Name:        "restart_service",
			Description: "Restart a systemd service on the device",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]SchemaProperty{
					"service_name": {Type: "string", Description: "Name of the systemd service"},
				},
				Required: []string{"service_name"},
			},
		},
		{
			Name:        "get_logs",
			Description: "Retrieve recent journald logs from the device",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]SchemaProperty{
					"unit":  {Type: "string", Description: "Journald unit filter (optional)"},
					"lines": {Type: "integer", Description: "Number of log lines to return"},
				},
			},
		},
	}

	msg := MCPRegisterMsg{
		DeviceID: deviceID,
		Tools:    tools,
	}
	payload, err := json.Marshal(msg)
	if err != nil {
		log.Printf("mcp tools marshal error: %v", err)
		return
	}

	token := client.Publish(topic, 1, false, payload)
	token.WaitTimeout(5 * time.Second)
	if token.Error() != nil {
		log.Printf("mcp tools publish error: %v", token.Error())
	} else {
		log.Printf("MCP tools published to %s (%d tools)", topic, len(tools))
	}
}
