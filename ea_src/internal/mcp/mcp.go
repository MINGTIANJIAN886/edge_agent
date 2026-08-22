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
	"github.com/user/agent/internal/profile"
	"github.com/user/agent/internal/ros"
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

const AgentVersion = "1.3.5"

const agentVersion = AgentVersion

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

func PublishTools(client mqtt.Client, deviceID, topic string, rosVer ros.Version) {
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
			Name:        "probe_capabilities",
			Description: "Actively probe device capabilities (camera/device/ros/ota/inference) and return structured results",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]SchemaProperty{
					"capabilities":    {Type: "array", Description: "Optional list of capabilities to probe (camera/device/ros/ota/inference)"},
					"force":           {Type: "boolean", Description: "true = re-probe, false = cached result ok"},
					"timeout_seconds": {Type: "integer", Description: "Probe timeout in seconds (default 30)"},
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
				Type: "object",
				Properties: map[string]SchemaProperty{
					"task":              {Type: "string", Description: "Requested task, e.g. vehicle_detect"},
					"tags":              {Type: "array", Description: "Optional scenario tags for matching"},
					"require_accuracy":  {Type: "number", Description: "Minimum accuracy required (0-1)"},
					"max_latency":       {Type: "number", Description: "Maximum inference latency in ms"},
				},
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
		{
			Name:        "switch_model",
			Description: "Switch to a specific model from the V1 manifest (by model_id, or best match by task)",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]SchemaProperty{
					"model_id":         {Type: "string", Description: "Exact model_id from the V1 manifest, e.g. yolo11n"},
					"task":             {Type: "string", Description: "Task to match, e.g. object_detection"},
					"require_accuracy": {Type: "number", Description: "Minimum accuracy required (0-1)"},
					"max_latency":      {Type: "number", Description: "Maximum inference latency in ms"},
				},
			},
		},
		{
			Name:        "probe_capabilities",
			Description: "Actively probe device capabilities (camera/ota/ros/inference) and return structured results",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]SchemaProperty{
					"capabilities":    {Type: "array", Description: "Capabilities to probe, empty = all"},
					"force":           {Type: "boolean", Description: "true = re-probe, false = cached result ok"},
					"timeout_seconds": {Type: "number", Description: "Overall probe timeout in seconds"},
				},
			},
		},
	}

	if rosVer == ros.ROS1 || rosVer == ros.ROS2 {
		rosTools := []ToolDefinition{
			{
				Name: "ros_version", Description: "Detect ROS version installed on device",
				InputSchema: InputSchema{Type: "object", Properties: map[string]SchemaProperty{}},
			},
			{
				Name: "ros_node_list", Description: "List all ROS nodes",
				InputSchema: InputSchema{Type: "object", Properties: map[string]SchemaProperty{}},
			},
			{
				Name: "ros_topic_list", Description: "List all ROS topics with types",
				InputSchema: InputSchema{Type: "object", Properties: map[string]SchemaProperty{}},
			},
			{
				Name: "ros_service_list", Description: "List all ROS services with types",
				InputSchema: InputSchema{Type: "object", Properties: map[string]SchemaProperty{}},
			},
			{
				Name: "ros_topic_echo", Description: "Echo latest message from a ROS topic",
				InputSchema: InputSchema{
					Type: "object",
					Properties: map[string]SchemaProperty{
						"topic": {Type: "string", Description: "Topic name to echo"},
					},
					Required: []string{"topic"},
				},
			},
			{
				Name: "ros_service_call", Description: "Call a ROS service",
				InputSchema: InputSchema{
					Type: "object",
					Properties: map[string]SchemaProperty{
						"service":  {Type: "string", Description: "Service name"},
						"msg_type": {Type: "string", Description: "Service type (e.g. std_srvs/srv/Empty)"},
						"args":     {Type: "string", Description: "Arguments in YAML/JSON format"},
					},
					Required: []string{"service"},
				},
			},
			{
				Name: "ros_param_get", Description: "Get a ROS parameter value",
				InputSchema: InputSchema{
					Type: "object",
					Properties: map[string]SchemaProperty{
						"name": {Type: "string", Description: "Parameter name"},
					},
					Required: []string{"name"},
				},
			},
			{
				Name: "ros_param_set", Description: "Set a ROS parameter value",
				InputSchema: InputSchema{
					Type: "object",
					Properties: map[string]SchemaProperty{
						"name":  {Type: "string", Description: "Parameter name"},
						"value": {Type: "string", Description: "Parameter value"},
					},
					Required: []string{"name", "value"},
				},
			},
			{
				Name: "bridge_start", Description: "Start the ROS-MQTT bridge node",
				InputSchema: InputSchema{Type: "object", Properties: map[string]SchemaProperty{}},
			},
			{
				Name: "bridge_stop", Description: "Stop the ROS-MQTT bridge node",
				InputSchema: InputSchema{Type: "object", Properties: map[string]SchemaProperty{}},
			},
			{
				Name: "bridge_status", Description: "Check if the ROS-MQTT bridge is running",
				InputSchema: InputSchema{Type: "object", Properties: map[string]SchemaProperty{}},
			},
			{
				Name: "car_cmd_vel", Description: "Send velocity command to robot car",
				InputSchema: InputSchema{
					Type: "object",
					Properties: map[string]SchemaProperty{
						"linear_x":  {Type: "number", Description: "Linear velocity in X (m/s)"},
						"angular_z": {Type: "number", Description: "Angular velocity around Z (rad/s)"},
						"duration":  {Type: "number", Description: "Duration in seconds (0=one-shot)"},
					},
					Required: []string{"linear_x", "angular_z"},
				},
			},
			{
				Name: "car_emergency_stop", Description: "Emergency stop - publish zero velocity",
				InputSchema: InputSchema{Type: "object", Properties: map[string]SchemaProperty{}},
			},
		}
		tools = append(tools, rosTools...)
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

func SubscribeCalls(client mqtt.Client, deviceID, callTopic, inferenceURL string, cfg *config.Config, mgr *profile.ProbeManager, task *profile.TaskTracker) {
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
			if task != nil {
				task.SetTask("object_detection")
			}
			resp = handleDetect(inferenceURL, req)
		case "run_ocr":
			if task != nil {
				task.SetTask("ocr")
			}
			resp = handleRunOCR(cfg, req)
		case "check_update":
			resp = handleCheckUpdate(cfg, client, deviceID, req)
		case "rollback_model":
			resp = handleRollback(cfg, client, deviceID, req)
		case "switch_model":
			resp = handleSwitchModel(cfg, client, deviceID, req)
		case "probe_capabilities":
			resp = handleProbeCapabilities(deviceID, mgr, req)
		case "ros_version":
			resp = handleROSVersion()
		case "ros_node_list":
			resp = handleROSList("node")
		case "ros_topic_list":
			resp = handleROSList("topic")
		case "ros_service_list":
			resp = handleROSList("service")
		case "ros_topic_echo":
			resp = handleROSEcho(req)
		case "ros_param_get":
			resp = handleROSParam(req, "get")
		case "ros_param_set":
			resp = handleROSParam(req, "set")
		case "ros_service_call":
			resp = handleROSServiceCall(req)
		case "car_cmd_vel":
			resp = handleCarCmdVel(req)
		case "car_emergency_stop":
			resp = handleCarEmergencyStop()
		default:
			resp = MCPCallResponse{
				ID:      req.ID,
				Success: false,
				Error:   fmt.Sprintf("unknown method: %s", req.Method),
			}
		}

		resp.ID = req.ID // 回填请求 ID（旧版缺失导致云端无法关联响应）
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
	opts := ota.CheckOptions{}
	if task, ok := req.Params["task"].(string); ok {
		opts.Task = task
	}
	if tags, ok := req.Params["tags"].([]interface{}); ok {
		for _, t := range tags {
			if s, ok := t.(string); ok {
				opts.TaskTags = append(opts.TaskTags, s)
			}
		}
	}
	if v, ok := req.Params["require_accuracy"].(float64); ok {
		opts.RequireAccuracy = v
	}
	if v, ok := req.Params["max_latency"].(float64); ok {
		opts.MaxLatencyMS = v
	}
	msg, err := ota.CheckNow(cfg.OTA, client, deviceID, cfg.MQTT.Topic.Result, opts)
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

// handleSwitchModel 实现 switch_model：从 Manifest V1 按 model_id 或 task
// 选择模型 → 下载（缺失时）→ 换链 → 热重载激活（失败自动回滚）。
func handleSwitchModel(cfg *config.Config, client mqtt.Client, deviceID string, req MCPCallRequest) MCPCallResponse {
	modelID, _ := req.Params["model_id"].(string)
	task, _ := req.Params["task"].(string)
	requireAccuracy, _ := req.Params["require_accuracy"].(float64)
	maxLatency, _ := req.Params["max_latency"].(float64)
	msg, err := ota.SwitchModelV1(cfg.OTA, client, deviceID, cfg.MQTT.Topic.Result,
		modelID, task, requireAccuracy, maxLatency)
	if err != nil {
		return MCPCallResponse{ID: req.ID, Success: false, Error: err.Error()}
	}
	return MCPCallResponse{ID: req.ID, Success: true, Result: msg}
}

// handleProbeCapabilities actively probes device capabilities and
// returns a summary plus detailed structured results.
func handleProbeCapabilities(deviceID string, mgr *profile.ProbeManager, req MCPCallRequest) MCPCallResponse {
	if mgr == nil {
		return MCPCallResponse{ID: req.ID, Success: false, Error: "capability probing disabled"}
	}

	var names []string
	if raw, ok := req.Params["capabilities"].([]interface{}); ok {
		for _, n := range raw {
			if s, ok := n.(string); ok {
				names = append(names, s)
			}
		}
	}
	force := false
	if v, ok := req.Params["force"].(bool); ok {
		force = v
	}
	timeout := 30 * time.Second
	if v, ok := req.Params["timeout_seconds"].(float64); ok && v > 0 {
		timeout = time.Duration(v) * time.Second
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	results := mgr.ProbeAll(ctx, names, force)

	summary := map[string]bool{}
	for name, res := range results {
		summary[name] = res.Result
	}

	payload := map[string]interface{}{
		"device_id": deviceID,
		"summary":   summary,
		"results":   results,
	}
	return MCPCallResponse{ID: req.ID, Success: true, Result: payload}
}

// ---------- ROS 工具 handlers（通过 ros2 CLI 实现） ----------

// ros2Bash 构造带 ROS 环境的 bash 命令（自动探测 distro，修复 HOME）
func ros2Bash(args ...string) string {
	// 用设备真实用户 HOME（复用其 ~/.ros daemon 与发现缓存，避免 /root 冷启动发现不全）
	cmd := "export HOME=$(ls -d /home/* 2>/dev/null | head -1); [ -n \"$HOME\" ] || export HOME=/root; " +
		"VER=$(ls /opt/ros 2>/dev/null | head -1); " +
		"source /opt/ros/$VER/setup.bash >/dev/null 2>&1; ros2"
	for _, a := range args {
		cmd += " " + a
	}
	return cmd
}

func runROS(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "bash", "-c", ros2Bash(args...)).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func handleROSVersion() MCPCallResponse {
	out, err := runROS("--version")
	if err != nil && out == "" {
		return MCPCallResponse{ID: "", Success: false, Error: "ros2 CLI 不可用: " + err.Error()}
	}
	distro := os.Getenv("ROS_DISTRO")
	if distro == "" {
		if v := strings.TrimSpace(strings.ReplaceAll(out, "ros2", "")); v != "" {
			distro = v
		}
	}
	return MCPCallResponse{Success: true, Result: map[string]string{
		"version": "ros2", "distro": distro, "ros2_version": out,
	}}
}

func handleROSList(kind string) MCPCallResponse {
	// ROS2 daemon 冷启动时发现窗口未满，重试几次等收敛
	var out string
	var err error
	for i := 0; i < 4; i++ {
		out, err = runROS(kind, "list")
		if err == nil && strings.Count(out, "/") >= 3 {
			break
		}
		time.Sleep(4 * time.Second)
	}
	if err != nil {
		return MCPCallResponse{Success: false, Error: fmt.Sprintf("ros2 %s list 失败: %v", kind, err)}
	}
	var items []string
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "WARNING") {
			items = append(items, l)
		}
	}
	return MCPCallResponse{Success: true, Result: items}
}

func handleROSEcho(req MCPCallRequest) MCPCallResponse {
	topic, _ := req.Params["topic"].(string)
	if topic == "" {
		return MCPCallResponse{Success: false, Error: "topic 参数必填"}
	}
	out, err := runROS("topic", "echo", topic, "--once")
	if err != nil {
		return MCPCallResponse{Success: false, Error: fmt.Sprintf("echo %s 失败: %v", topic, err)}
	}
	return MCPCallResponse{Success: true, Result: out}
}

func handleROSParam(req MCPCallRequest, op string) MCPCallResponse {
	node, _ := req.Params["node"].(string)
	name, _ := req.Params["name"].(string)
	if node == "" || name == "" {
		return MCPCallResponse{Success: false, Error: "node 与 name 参数必填"}
	}
	var out string
	var err error
	if op == "get" {
		out, err = runROS("param", "get", node, name)
	} else {
		value, _ := req.Params["value"].(string)
		out, err = runROS("param", "set", node, name, value)
	}
	if err != nil {
		return MCPCallResponse{Success: false, Error: fmt.Sprintf("param %s 失败: %v", op, err)}
	}
	return MCPCallResponse{Success: true, Result: out}
}

func handleROSServiceCall(req MCPCallRequest) MCPCallResponse {
	service, _ := req.Params["service"].(string)
	msgType, _ := req.Params["msg_type"].(string)
	args, _ := req.Params["args"].(string)
	if service == "" || msgType == "" {
		return MCPCallResponse{Success: false, Error: "service 与 msg_type 参数必填"}
	}
	call := fmt.Sprintf("ros2 service call %s %s '%s'", service, msgType, args)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "bash", "-c", ros2Bash("service", "call", service, msgType, "'"+args+"'")).CombinedOutput()
	_ = call
	if err != nil {
		return MCPCallResponse{Success: false, Error: fmt.Sprintf("service call 失败: %v", err)}
	}
	return MCPCallResponse{Success: true, Result: strings.TrimSpace(string(out))}
}

func handleCarCmdVel(req MCPCallRequest) MCPCallResponse {
	lx, _ := req.Params["linear_x"].(float64)
	az, _ := req.Params["angular_z"].(float64)
	// 限制速度，防止异常参数
	if lx > 2.0 || lx < -2.0 || az > 3.14 || az < -3.14 {
		return MCPCallResponse{Success: false, Error: "速度超出安全范围 (|linear_x|<=2.0, |angular_z|<=3.14)"}
	}
	twist := fmt.Sprintf("{linear: {x: %f}, angular: {z: %f}}", lx, az)
	out, err := runROS("topic", "pub", "--once", "/cmd_vel",
		"geometry_msgs/msg/Twist", twist)
	if err != nil {
		return MCPCallResponse{Success: false, Error: fmt.Sprintf("cmd_vel 发布失败: %v", err)}
	}
	return MCPCallResponse{Success: true, Result: out}
}

func handleCarEmergencyStop() MCPCallResponse {
	out, err := runROS("topic", "pub", "--once", "/cmd_vel",
		"geometry_msgs/msg/Twist", "{linear: {x: 0.0}, angular: {z: 0.0}}")
	if err != nil {
		return MCPCallResponse{Success: false, Error: fmt.Sprintf("急停失败: %v", err)}
	}
	return MCPCallResponse{Success: true, Result: out}
}
