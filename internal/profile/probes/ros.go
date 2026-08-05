package probes

import (
	"context"
	"os"
	"time"

	"github.com/user/agent/internal/profile"
	"github.com/user/agent/internal/ros"
)

// ROSProbe checks whether a ROS distribution is present and (v1) whether
// the bridge process is alive. No side effects: it never starts or
// stops ROS processes.
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
		return result(true, profile.CodeOK, "ROS environment detected", time.Since(start), details)
	}
	return result(false, "ROS_NOT_FOUND", "no ROS1/ROS2 installation detected", time.Since(start), details)
}
