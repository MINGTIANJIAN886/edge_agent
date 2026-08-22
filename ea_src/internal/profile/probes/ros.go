package probes

import (
	"context"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/user/agent/internal/profile"
	"github.com/user/agent/internal/ros"
)

// WatchTopics are the ROS topics reported on the dashboard; each is
// present when it currently exists in the running ROS graph.
var WatchTopics = []string{"scan", "PowerVoltage", "tf", "cmd_vel", "tf_static"}

// ROSProbe checks whether a ROS distribution is present, which of the
// watched topics currently exist in the graph, and (v1) whether the
// bridge process is alive. No side effects: it never starts or stops
// ROS processes.
type ROSProbe struct {
	Detect func() ros.Version
}

func NewROSProbe() *ROSProbe {
	return &ROSProbe{Detect: ros.Detect}
}

func (p *ROSProbe) Name() string { return "ros" }

func (p *ROSProbe) Probe(ctx context.Context, req profile.ProbeRequest) profile.CapabilityResult {
	start := time.Now()
	ver := p.Detect()
	details := map[string]interface{}{
		"ros_version": ver.String(),
	}
	if ver != ros.None {
		distro := os.Getenv("ROS_DISTRO")
		if distro != "" {
			details["distro"] = distro
		}
	}
	topics := listTopics(ctx, ver)
	details["topics_all"] = topics
	present := map[string]bool{}
	for _, t := range WatchTopics {
		present[t] = topicPresent(topics, t)
	}
	details["topics"] = present
	// 包→可执行文件映射（帮助云端 LLM 生成 launch 时避免臆造可执行文件）
	if exes := listExecutables(ctx, ver); len(exes) > 0 {
		details["executables"] = exes
	}
	if ver != ros.None {
		return result(true, profile.CodeOK, "ROS environment detected", time.Since(start), details)
	}
	return result(false, "ROS_NOT_FOUND", "no ROS1/ROS2 installation detected", time.Since(start), details)
}

// listExecutables returns a package -> [executables] map for robot-relevant
// ROS2 packages (only those likely needed for launch generation).
func listExecutables(ctx context.Context, ver ros.Version) map[string][]string {
	if ver != ros.ROS2 {
		return nil
	}
	cctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, "bash", "-c",
		`VER=$(ls /opt/ros | head -1); export HOME=${HOME:-/root}; source /opt/ros/$VER/setup.bash 2>/dev/null;
for p in $(ros2 pkg list 2>/dev/null | grep -E 'usb_cam|car_|ld_|d500|robot|imu|serial|motor|chassis|base|driver|slam|nav2_(amcl|bt_navigator|planner|controller|costmap|lifecycle|core|bringup)|ros2_control|controller_manager|joint_state|tf2_ros|lidar|camera|orbbec|depth|teleop|joy|scan' | head -60); do
  ros2 pkg executables $p 2>/dev/null | tail -n +2 | while read -r _ ex; do [ -n "$ex" ] && echo "$p: $ex"; done
done`)
	out, err := cmd.CombinedOutput()
	if err != nil && len(out) == 0 {
		return nil
	}
	m := map[string][]string{}
	for _, line := range strings.Split(string(out), "\n") {
		if i := strings.Index(line, ":"); i > 0 {
			pkg, exe := line[:i], strings.TrimSpace(line[i+1:])
			if exe != "" {
				m[pkg] = append(m[pkg], exe)
			}
		}
	}
	return m
}

// listTopics returns the topics currently present in the ROS graph
// (empty when ROS is missing or the CLI fails). ROS2 needs the distro
// setup sourced before ros2 works; --no-daemon avoids leaving a
// background daemon behind.
func listTopics(ctx context.Context, ver ros.Version) []string {
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var cmd *exec.Cmd
	switch ver {
	case ros.ROS2:
		cmd = exec.CommandContext(cctx, "bash", "-c",
			`VER=$(ls /opt/ros | head -1); export HOME=${HOME:-/root}; source /opt/ros/$VER/setup.bash 2>/dev/null; ros2 topic list --no-daemon`)
	case ros.ROS1:
		cmd = exec.CommandContext(cctx, "bash", "-c", "rostopic list")
	default:
		return nil
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("ros probe: topic list failed (ver=%d): %v: %s", ver, err, strings.TrimSpace(string(out)))
		return nil
	}
	var topics []string
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			topics = append(topics, l)
		}
	}
	return topics
}

// topicPresent matches a watch topic against the graph, ignoring the
// leading "/" ROS uses on absolute topic names.
func topicPresent(topics []string, want string) bool {
	w := strings.TrimPrefix(want, "/")
	for _, t := range topics {
		if strings.TrimPrefix(t, "/") == w {
			return true
		}
	}
	return false
}
