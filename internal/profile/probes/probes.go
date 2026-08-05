package probes

import (
	"time"

	"github.com/user/agent/internal/profile"
)

func result(ok bool, code, message string, latency time.Duration, details map[string]interface{}) profile.CapabilityResult {
	return profile.CapabilityResult{
		Supported: ok || code != profile.CodeCameraNotFound,
		Available: ok,
		Healthy:   ok,
		Result:    ok,
		LatencyMS: latency.Milliseconds(),
		ErrorCode: code,
		Message:   message,
		Details:   details,
	}
}
