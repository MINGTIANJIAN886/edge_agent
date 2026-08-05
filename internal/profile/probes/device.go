package probes

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/user/agent/internal/profile"
	"github.com/user/agent/internal/ros"
)

// DeviceProbe gathers hardware and software parameters of the device
// without side effects: board model, CPU/memory/disk, GPU, OS, kernel,
// ROS, CUDA, TensorRT and agent version.
type DeviceProbe struct {
	AgentVersion string
	DetectROS    func() ros.Version
}

func NewDeviceProbe(agentVersion string) *DeviceProbe {
	return &DeviceProbe{AgentVersion: agentVersion, DetectROS: ros.Detect}
}

func (p *DeviceProbe) Name() string { return "device" }

func sh(ctx context.Context, cmd string, timeout time.Duration) string {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	out, err := exec.CommandContext(cctx, "sh", "-c", cmd).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func readFileFirstLine(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimRight(strings.TrimSpace(strings.Split(string(data), "\n")[0]), "\x00")
}

func (p *DeviceProbe) Probe(ctx context.Context, req profile.ProbeRequest) profile.CapabilityResult {
	start := time.Now()

	model := readFileFirstLine("/proc/device-tree/model")
	if model == "" {
		model = "unknown"
	}

	hostname, _ := os.Hostname()
	cores := sh(ctx, "nproc || echo 1", 3*time.Second)
	memMB := sh(ctx, "awk '/MemTotal/{print int($2/1024)}' /proc/meminfo", 3*time.Second)
	disk := sh(ctx, "df -h / | awk 'NR==2{print \"total=\"$2\" used=\"$3\" avail=\"$4\" usage=\"$5}'", 3*time.Second)
	load := sh(ctx, "cat /proc/loadavg | awk '{print \"1m=\"$1\" 5m=\"$2\" 15m=\"$3}'", 3*time.Second)
	uptime := sh(ctx, "uptime -p", 3*time.Second)

	gpu := ""
	if out := sh(ctx, "nvidia-smi --query-gpu=name --format=csv,noheader 2>/dev/null | head -1", 5*time.Second); out != "" {
		gpu = out
	} else if tegra := readFileFirstLine("/proc/device-tree/compatible"); tegra != "" {
		gpu = "tegra:" + strings.Split(tegra, " ")[0]
	} else {
		gpu = "none"
	}

	osName := sh(ctx, "grep PRETTY_NAME /etc/os-release 2>/dev/null | cut -d'=' -f2 | tr -d '\"'", 3*time.Second)
	kernel := sh(ctx, "uname -r", 3*time.Second)
	pythonVer := sh(ctx, "python3 --version 2>&1", 3*time.Second)

	rosVer := p.DetectROS()
	distro := os.Getenv("ROS_DISTRO")

	cuda := ""
	if out := sh(ctx, "nvidia-smi 2>/dev/null | grep -o 'CUDA Version: [0-9.]*' | awk '{print $NF}'", 5*time.Second); out != "" {
		cuda = "nvidia-smi " + out
	} else if v := readFileFirstLine("/usr/local/cuda/version.txt"); v != "" {
		cuda = v
	} else if v := readFileFirstLine("/usr/local/cuda/version.json"); v != "" {
		cuda = v
	} else if out := sh(ctx, "nvcc --version 2>/dev/null | grep release | awk '{print $NF}'", 5*time.Second); out != "" {
		cuda = out
	} else if v := readFileFirstLine("/etc/nv_tegra_release"); v != "" {
		cuda = "l4t " + v
	}

	tensorrt := ""
	if out := sh(ctx, "dpkg-query -W -f='${Version}' libnvinfer-dev 2>/dev/null", 3*time.Second); out != "" {
		tensorrt = out
	} else if out := sh(ctx, "ls -d /usr/lib/aarch64-linux-gnu/libnvinfer.so.* /usr/lib/x86_64-linux-gnu/libnvinfer.so.* 2>/dev/null | head -1", 3*time.Second); out != "" {
		tensorrt = strings.TrimPrefix(strings.TrimPrefix(out, "/usr/lib/aarch64-linux-gnu/libnvinfer.so."), "/usr/lib/x86_64-linux-gnu/libnvinfer.so.")
		tensorrt = strings.Split(tensorrt, ".")[0]
	} else if out := sh(ctx, "python3 -c 'import tensorrt as t; print(t.__version__)' 2>/dev/null", 5*time.Second); out != "" {
		tensorrt = out
	}

	details := map[string]interface{}{
		"hardware": map[string]interface{}{
			"device_type": model,
			"architecture": runtime.GOARCH,
			"hostname":     hostname,
			"cpu_cores":    cores,
			"memory_mb":    memMB,
			"gpu":          gpu,
			"disk":         disk,
		},
		"software": map[string]interface{}{
			"os":            osName,
			"kernel":        kernel,
			"ros":           rosVer.String(),
			"ros_distro":    distro,
			"cuda":          cuda,
			"tensorrt":      tensorrt,
			"python":        pythonVer,
			"agent_version": p.AgentVersion,
		},
		"runtime": map[string]interface{}{
			"uptime": uptime,
			"load":   load,
		},
	}

	return profile.CapabilityResult{
		Supported: true,
		Available: true,
		Healthy:   true,
		Result:    true,
		Method:    "system_inspect",
		LatencyMS: time.Since(start).Milliseconds(),
		TestedAt:  time.Now(),
		Message:   "device hardware/software parameters collected",
		Details:   details,
	}
}
