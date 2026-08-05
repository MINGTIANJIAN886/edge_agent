package profile

import (
	"context"
	"time"
)

// CapabilityResult is the structured outcome of one capability probe.
// The three core fields have distinct meanings:
//
//	supported - hardware/software exists at all (e.g. camera chip present)
//	available - currently usable (e.g. not locked by another process)
//	healthy   - the most recent active test passed
type CapabilityResult struct {
	Name      string                 `json:"name"`
	Supported bool                   `json:"supported"`
	Available bool                   `json:"available"`
	Healthy   bool                   `json:"healthy"`
	Result    bool                   `json:"result"`
	Method    string                 `json:"method"`
	LatencyMS int64                  `json:"latency_ms"`
	TestedAt  time.Time              `json:"tested_at"`
	ErrorCode string                 `json:"error_code,omitempty"`
	Message   string                 `json:"message"`
	Details   map[string]interface{} `json:"details,omitempty"`
}

// ProbeRequest carries per-call options. Zero Timeout lets the probe use
// its own configured default.
type ProbeRequest struct {
	Force   bool
	Timeout time.Duration
}

// CapabilityProbe is the unified interface every probe implements.
type CapabilityProbe interface {
	Name() string
	Probe(ctx context.Context, req ProbeRequest) CapabilityResult
}

// Shared error codes. Probes may define additional codes in this block.
const (
	CodeOK            = ""
	CodeProbeUnknown  = "PROBE_UNKNOWN"
	CodeProbeBusy     = "PROBE_BUSY"
	CodeProbeTimedOut = "PROBE_TIMEOUT"
	CodeNotConfigured = "NOT_CONFIGURED"

	// camera
	CodeCameraNotFound      = "CAMERA_NOT_FOUND"
	CodeCameraPermission    = "CAMERA_PERMISSION_DENIED"
	CodeCameraOpenFailed    = "CAMERA_OPEN_FAILED"
	CodeCameraBusy          = "CAMERA_BUSY"
	CodeCameraFrameTimeout  = "CAMERA_FRAME_TIMEOUT"
	CodeCameraEmptyFrame    = "CAMERA_EMPTY_FRAME"
	CodeCameraProbeCrashed  = "CAMERA_PROBE_CRASHED"

	// ota
	CodeOTANotConfigured    = "OTA_NOT_CONFIGURED"
	CodeOTAManifestUnreachable = "OTA_MANIFEST_UNREACHABLE"
	CodeOTAManifestInvalid   = "OTA_MANIFEST_INVALID"
	CodeOTACacheNotWritable  = "OTA_CACHE_NOT_WRITABLE"
	CodeOTADiskInsufficient  = "OTA_DISK_INSUFFICIENT"
	CodeOTADownloadTimeout   = "OTA_DOWNLOAD_TIMEOUT"
	CodeOTAChecksumFailed    = "OTA_CHECKSUM_FAILED"
	CodeOTAPublicKeyMissing  = "OTA_PUBLIC_KEY_MISSING"
	CodeOTASignatureInvalid  = "OTA_SIGNATURE_INVALID"

	// inference
	CodeInferenceNotConfigured = "INFERENCE_NOT_CONFIGURED"
	CodeInferenceUnreachable   = "INFERENCE_UNREACHABLE"
)

// Profile is the persisted robot profile stored on device.
type Profile struct {
	DeviceID       string                      `json:"device_id"`
	ProfileVersion int                         `json:"profile_version"`
	UpdatedAt      time.Time                   `json:"updated_at"`
	Capabilities   map[string]CapabilityResult `json:"capabilities"`
	Task           string                      `json:"task,omitempty"`
	Model          *TaskModelInfo              `json:"model,omitempty"`
}
