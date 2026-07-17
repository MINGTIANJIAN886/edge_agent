package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/user/agent/internal/config"
	"github.com/user/agent/internal/ocr"
	"github.com/user/agent/internal/ota"
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

type MCPCallRequest struct {
	ID      string                 `json:"id"`
	Method  string                 `json:"method"`
	Params  map[string]interface{} `json:"params"`
}

type MCPCallResponse struct {
	ID      string      `json:"id"`
	Success bool        `json:"success"`
	Result  interface{} `json:"result,omitempty"`
	Error   string      `json:"error,omitempty"`
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
			"ota_update",
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
		{
			Name:        "detect_objects",
			Description: "Run object detection on an image and return results",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]SchemaProperty{
					"image_url": {Type: "string", Description: "URL of the image to analyze"},
					"threshold": {Type: "number", Description: "Detection confidence threshold (0-1)"},
				},
				Required: []string{"image_url"},
			},
		},
		{
			Name:        "run_ocr",
			Description: "Trigger OCR text recognition on the device camera and return results",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]SchemaProperty{
					"conf_threshold": {Type: "number", Description: "Confidence threshold (0-1)"},
				},
			},
		},
		{
			Name:        "check_update",
			Description: "Check for model updates from the OTA server and apply if available",
			InputSchema: InputSchema{
				Type:       "object",
				Properties: map[string]SchemaProperty{},
			},
		},
		{
			Name:        "rollback_model",
			Description: "Rollback the model to the previous version",
			InputSchema: InputSchema{
				Type:       "object",
				Properties: map[string]SchemaProperty{},
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

func SubscribeCalls(client mqtt.Client, deviceID, callTopic, inferenceURL string, cfg *config.Config) {
	token := client.Subscribe(callTopic, 1, func(_ mqtt.Client, msg mqtt.Message) {
		var req MCPCallRequest
		if err := json.Unmarshal(msg.Payload(), &req); err != nil {
			log.Printf("MCP call parse error: %v", err)
			return
		}
		log.Printf("MCP call: method=%s, id=%s", req.Method, req.ID)

		var resp MCPCallResponse
		resp.ID = req.ID

		switch req.Method {
		case "device_info":
			resp = handleDeviceInfo()
		case "execute_command":
			resp = handleExecuteCommand(req)
		case "download_file":
			resp = handleDownloadFile(cfg, client, deviceID, req)
		case "restart_service":
			resp = handleRestartService(req)
		case "get_logs":
			resp = handleGetLogs(req)
		case "detect_objects":
			resp = handleDetect(inferenceURL, req)
		case "run_ocr":
			resp = handleRunOCR(cfg, req)
		case "check_update":
			resp = handleCheckUpdate(cfg, client, deviceID, req)
		case "rollback_model":
			resp = handleRollback(cfg, client, deviceID, req)
		default:
			resp = MCPCallResponse{
				ID:      req.ID,
				Success: false,
				Error:   fmt.Sprintf("unknown method: %s", req.Method),
			}
		}

		payload, _ := json.Marshal(resp)
		respTopic := strings.Replace(callTopic, "/call", "/call/resp", 1)
		client.Publish(respTopic, 1, false, payload)
		log.Printf("MCP call response published to %s", respTopic)
	})
	token.WaitTimeout(10 * time.Second)
	if token.Error() != nil {
		log.Printf("MCP call subscribe error: %v", token.Error())
	} else {
		log.Printf("MCP call subscribed: %s", callTopic)
	}
}

func shell(cmd string) string {
	out, err := exec.Command("sh", "-c", cmd).Output()
	if err != nil {
		return fmt.Sprintf("error: %v", err)
	}
	return strings.TrimSpace(string(out))
}

func handleDeviceInfo() MCPCallResponse {
	hostname, _ := os.Hostname()
	info := map[string]interface{}{
		"hostname":  hostname,
		"platform":  runtime.GOOS + "/" + runtime.GOARCH,
		"go_version": runtime.Version(),
		"cpu":       shell("nproc || echo 1"),
		"memory":    shell("free -h | awk 'NR==2{print \"total=\"$2\" used=\"$3\" free=\"$4}'"),
		"disk":      shell("df -h / | awk 'NR==2{print \"total=\"$2\" used=\"$3\" avail=\"$4\" usage=\"$5}'"),
		"uptime":    shell("uptime -p"),
		"load":      shell("cat /proc/loadavg | awk '{print \"1m=\"$1\" 5m=\"$2\" 15m=\"$3}'"),
		"kernel":    shell("uname -r"),
		"timestamp": time.Now().Unix(),
	}
	return MCPCallResponse{Success: true, Result: info}
}

func handleExecuteCommand(req MCPCallRequest) MCPCallResponse {
	cmd, _ := req.Params["command"].(string)
	if cmd == "" {
		return MCPCallResponse{Success: false, Error: "missing command"}
	}
	timeout := 30
	if t, ok := req.Params["timeout"].(float64); ok {
		timeout = int(t)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "sh", "-c", cmd).Output()
	if err != nil {
		return MCPCallResponse{Success: false, Error: err.Error(), Result: string(out)}
	}
	return MCPCallResponse{Success: true, Result: string(out)}
}

func handleDownloadFile(cfg *config.Config, client mqtt.Client, deviceID string, req MCPCallRequest) MCPCallResponse {
	url, _ := req.Params["url"].(string)
	destPath, _ := req.Params["dest_path"].(string)
	if url == "" {
		return MCPCallResponse{Success: false, Error: "missing url"}
	}
	if destPath == "" {
		destPath = cfg.DownloadDir + "/" + url[strings.LastIndex(url, "/")+1:]
	}
	// use the download package's internal downloadFile
	// we replicate the logic here since it's unexported
	log.Printf("mcp download: %s -> %s", url, destPath)
	// trigger via the download subscription topic
	// for now, execute as a shell curl
	out, err := exec.Command("sh", "-c", fmt.Sprintf("mkdir -p $(dirname '%s') && curl -fsSL -o '%s' '%s' && ls -lh '%s'", destPath, destPath, url, destPath)).CombinedOutput()
	if err != nil {
		return MCPCallResponse{Success: false, Error: string(out)}
	}
	return MCPCallResponse{Success: true, Result: strings.TrimSpace(string(out))}
}

func handleRestartService(req MCPCallRequest) MCPCallResponse {
	name, _ := req.Params["service_name"].(string)
	if name == "" {
		return MCPCallResponse{Success: false, Error: "missing service_name"}
	}
	out, err := exec.Command("systemctl", "restart", name).CombinedOutput()
	if err != nil {
		return MCPCallResponse{Success: false, Error: string(out)}
	}
	return MCPCallResponse{Success: true, Result: fmt.Sprintf("service %s restarted", name)}
}

func handleGetLogs(req MCPCallRequest) MCPCallResponse {
	unit, _ := req.Params["unit"].(string)
	lines := 50
	if l, ok := req.Params["lines"].(float64); ok {
		lines = int(l)
	}
	args := []string{"--no-pager", "-n", fmt.Sprintf("%d", lines)}
	if unit != "" {
		args = append(args, "-u", unit)
	}
	out, err := exec.Command("journalctl", args...).Output()
	if err != nil {
		return MCPCallResponse{Success: false, Error: err.Error()}
	}
	return MCPCallResponse{Success: true, Result: string(out)}
}

func handleDetect(inferenceURL string, req MCPCallRequest) MCPCallResponse {
	url := inferenceURL + "/detect"
	body, _ := json.Marshal(req.Params)

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return MCPCallResponse{ID: req.ID, Success: false, Error: err.Error()}
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	var result interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return MCPCallResponse{ID: req.ID, Success: false, Error: string(respBody)}
	}

	return MCPCallResponse{ID: req.ID, Success: true, Result: result}
}

func handleRunOCR(cfg *config.Config, req MCPCallRequest) MCPCallResponse {
	scriptPath := cfg.OCR.ScriptPath
	if scriptPath == "" {
		scriptPath = "/opt/agent/edge_ocr.py"
	}
	confThreshold := cfg.OCR.ConfThreshold
	if t, ok := req.Params["conf_threshold"].(float64); ok {
		confThreshold = t
	}
	result, err := ocr.RunOCRFromMCP(scriptPath, confThreshold)
	if err != nil {
		return MCPCallResponse{ID: req.ID, Success: false, Error: err.Error()}
	}
	return MCPCallResponse{ID: req.ID, Success: true, Result: result}
}

func handleCheckUpdate(cfg *config.Config, client mqtt.Client, deviceID string, req MCPCallRequest) MCPCallResponse {
	msg, err := ota.CheckNow(cfg.OTA, client, deviceID, cfg.MQTT.Topic.Result)
	if err != nil {
		return MCPCallResponse{ID: req.ID, Success: false, Error: err.Error()}
	}
	return MCPCallResponse{ID: req.ID, Success: true, Result: msg}
}

func handleRollback(cfg *config.Config, client mqtt.Client, deviceID string, req MCPCallRequest) MCPCallResponse {
	msg, err := ota.Rollback(cfg.OTA, client, deviceID, cfg.MQTT.Topic.Result)
	if err != nil {
		return MCPCallResponse{ID: req.ID, Success: false, Error: err.Error()}
	}
	return MCPCallResponse{ID: req.ID, Success: true, Result: msg}
}
