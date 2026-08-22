package probes

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/user/agent/internal/config"
	"github.com/user/agent/internal/profile"
)

// CameraProbe actively captures one frame per configured device using
// an external cv2 script, distinguishing failure causes via error codes.
type CameraProbe struct {
	Cfg config.CameraProbeCfg
}

func NewCameraProbe(cfg config.CameraProbeCfg) *CameraProbe {
	return &CameraProbe{Cfg: cfg}
}

func (p *CameraProbe) Name() string { return "camera" }

func (p *CameraProbe) Probe(ctx context.Context, req profile.ProbeRequest) profile.CapabilityResult {
	start := time.Now()
	timeout := req.Timeout
	if timeout == 0 {
		timeout = time.Duration(p.Cfg.TimeoutSeconds) * time.Second
		if timeout == 0 {
			timeout = 5 * time.Second
		}
	}

	devices := p.Cfg.Devices
	if len(devices) == 0 {
		devices = []string{"/dev/video0"}
	}

	var lastErr profile.CapabilityResult
	foundAny := false
	for _, dev := range devices {
		res := p.probeDevice(ctx, dev, timeout)
		res.Name = "camera"
		res.LatencyMS = time.Since(start).Milliseconds()
		if res.Result {
			res.Method = "capture_frame"
			res.Message = fmt.Sprintf("成功获取摄像头图像 (%s)", dev)
			res.TestedAt = time.Now()
			return res
		}
		// Any error other than a clean "device node missing" means the
		// device exists and the capability is supported but unusable.
		if res.ErrorCode != profile.CodeCameraNotFound {
			foundAny = true
		}
		lastErr = res
		if res.ErrorCode != profile.CodeCameraNotFound {
			log.Printf("camera: device %s -> code=%s msg=%s", dev, res.ErrorCode, res.Message)
		}
	}

	if !foundAny {
		return profile.CapabilityResult{
			Name:      "camera",
			Supported: false,
			Available: false,
			Healthy:   false,
			Result:    false,
			Method:    "capture_frame",
			LatencyMS: time.Since(start).Milliseconds(),
			TestedAt:  time.Now(),
			ErrorCode: profile.CodeCameraNotFound,
			Message:   "no usable camera device found",
			Details:   map[string]interface{}{"devices": devices, "last_error": lastErr.ErrorCode, "last_message": lastErr.Message},
		}
	}
	lastErr.Name = "camera"
	lastErr.LatencyMS = time.Since(start).Milliseconds()
	lastErr.TestedAt = time.Now()
	lastErr.Supported = true
	return lastErr
}

func (p *CameraProbe) probeDevice(ctx context.Context, device string, timeout time.Duration) profile.CapabilityResult {
	script := p.Cfg.ScriptPath
	if script == "" {
		script = "/opt/edge-agent/probes/camera_probe.py"
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(cctx, "python3", script,
		"--device", device,
		"--timeout", fmt.Sprintf("%.0f", timeout.Seconds()),
	)
	output, err := cmd.Output()
	if err != nil {
		if cctx.Err() == context.DeadlineExceeded {
			return profile.CapabilityResult{
				ErrorCode: profile.CodeCameraFrameTimeout,
				Message:   fmt.Sprintf("camera probe timed out after %v (%s)", timeout, device),
				Details:   map[string]interface{}{"device": device},
			}
		}
		return profile.CapabilityResult{
			ErrorCode: profile.CodeCameraProbeCrashed,
			Message:   fmt.Sprintf("camera probe script failed: %v (is python3+cv2 installed?)", err),
			Details:   map[string]interface{}{"device": device, "stderr": strings.TrimSpace(err.Error())},
		}
	}

	var raw struct {
		Result     bool                   `json:"result"`
		Supported  *bool                  `json:"supported"`
		Available  *bool                  `json:"available"`
		Healthy    *bool                  `json:"healthy"`
		ErrorCode  string                 `json:"error_code"`
		Message    string                 `json:"message"`
		LatencyMS  int64                  `json:"latency_ms"`
		Details    map[string]interface{} `json:"details"`
	}
	if err := json.Unmarshal(output, &raw); err != nil {
		return profile.CapabilityResult{
			ErrorCode: profile.CodeCameraProbeCrashed,
			Message:   fmt.Sprintf("camera probe output parse failed: %v", err),
			Details:   map[string]interface{}{"device": device, "raw": string(output)},
		}
	}

	res := profile.CapabilityResult{
		Supported: raw.Result,
		Available: raw.Result,
		Healthy:   raw.Result,
		Result:    raw.Result,
		Method:    "capture_frame",
		LatencyMS: raw.LatencyMS,
		ErrorCode: raw.ErrorCode,
		Message:   raw.Message,
		Details:   raw.Details,
	}
	if raw.Supported != nil {
		res.Supported = *raw.Supported
	}
	if raw.Available != nil {
		res.Available = *raw.Available
	}
	if raw.Healthy != nil {
		res.Healthy = *raw.Healthy
	}
	// 打开失败时检查是否被其他进程占用（如正在运行的 ROS 相机节点）：
	// 被占用 = 摄像头工作正常，只是独占抓帧与运行节点冲突，不算故障。
	if res.ErrorCode == profile.CodeCameraOpenFailed || res.ErrorCode == profile.CodeCameraBusy {
		if users := fuserUsers(device); len(users) > 0 {
			res.ErrorCode = profile.CodeCameraInUse
			res.Message = fmt.Sprintf("摄像头被进程占用（正常使用中）: %s", strings.Join(users, ", "))
			res.Available = true
			res.Healthy = true
			res.Details = map[string]interface{}{"device": device, "in_use_by": users}
		}
	}
	return res
}

// fuserUsers returns "pid(comm)" entries of processes currently holding
// the given device node open, or nil when no one is using it.
func fuserUsers(device string) []string {
	out, err := exec.Command("sh", "-c", "fuser "+device+" 2>&1").Output()
	if err != nil && len(out) == 0 {
		return nil
	}
	var names []string
	for _, f := range strings.Fields(string(out)) {
		if _, e := strconv.Atoi(f); e == nil {
			if b, e2 := os.ReadFile("/proc/" + f + "/comm"); e2 == nil {
				names = append(names, f+"("+strings.TrimSpace(string(b))+")")
			} else {
				names = append(names, f)
			}
		}
	}
	return names
}
