package ota

import (
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/user/agent/internal/config"
)

type ManifestFile struct {
	Name     string   `json:"name"`
	URL      string   `json:"url"`
	SHA256   string   `json:"sha256"`
	Version  string   `json:"version"`
	Format   string   `json:"format"`
	SizeMB   float64  `json:"size_mb"`
	SizeBytes int64   `json:"size_bytes"`
	Accuracy float64  `json:"accuracy"`
	LatencyMS float64 `json:"latency_ms"`
	Task     string   `json:"task"`          // task this model is trained for, e.g. vehicle_detect
	Tags     []string `json:"tags"`          // scenario tags for soft matching
	MinCPU   int      `json:"min_cpu_cores"` // minimum CPU cores required, 0 = no constraint
	MinMemMB int      `json:"min_memory_mb"` // minimum memory MB required, 0 = no constraint
	RequiresGPU bool  `json:"requires_gpu"`  // whether a GPU accelerator is mandatory
}

type Manifest struct {
	Version string         `json:"version"`
	Files   []ManifestFile `json:"files"`
}

// CandidateManifest is the candidates.json format produced by the
// publish script: a flat list of candidate model files with metadata.
type CandidateManifest struct {
	Models []ManifestFile `json:"models"`
}

type VersionInfo struct {
	Version  string `json:"version"`
	ModelURL string `json:"model_url"`
	SHA256   string `json:"sha256"`
}

type OTAState struct {
	mu          sync.Mutex
	lastVersion string
	rollbackTo  string
}

var state = &OTAState{}

// UpdateInfo describes a completed model auto-download: what the device
// was doing and which model was selected and deployed.
type UpdateInfo struct {
	Version      string    `json:"version"`      // deployed version
	Model        string    `json:"model"`        // selected model file name
	RequestedTask string   `json:"requested_task"` // task the OTA targeted
	ModelTask    string    `json:"model_task"`   // task the model was trained for
	Accuracy     float64   `json:"accuracy"`
	Format       string    `json:"format"`
	LatencyMS    float64   `json:"latency_ms"`
	SizeMB       float64   `json:"size_mb"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// updateHook is invoked after a successful auto-download so the rest of
// the agent (profile/task tracker) can record the new model. Set once
// at startup via SetUpdateHook.
var updateHook func(UpdateInfo)

func SetUpdateHook(h func(UpdateInfo)) {
	updateHook = h
}

func notifyUpdate(info UpdateInfo) {
	if updateHook != nil {
		updateHook(info)
	}
}

func FetchManifest(serverURL, versionPath string) (*Manifest, error) {
	url := serverURL + "/" + versionPath
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("fetch manifest failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read manifest failed: %w", err)
	}

	var manifest Manifest
	if err := json.Unmarshal(body, &manifest); err != nil {
		return nil, fmt.Errorf("parse manifest failed: %w", err)
	}
	if manifest.Version == "" {
		var candidates CandidateManifest
		if err := json.Unmarshal(body, &candidates); err != nil {
			return nil, fmt.Errorf("parse manifest failed: %w", err)
		}
		if len(candidates.Models) == 0 {
			return nil, fmt.Errorf("manifest missing version field")
		}
		manifest.Version = candidates.Models[0].Version
		manifest.Files = candidates.Models
	}
	return &manifest, nil
}

func FetchVersion(serverURL, versionPath string) (*VersionInfo, error) {
	url := serverURL + "/" + versionPath
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("fetch version failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response failed: %w", err)
	}

	var info VersionInfo
	if err := json.Unmarshal(body, &info); err != nil {
		return nil, fmt.Errorf("parse version.json failed: %w", err)
	}
	return &info, nil
}

func DownloadFile(url, sha256sum, destPath string) error {
	log.Printf("Downloading file from %s", url)

	dir := filepath.Dir(destPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("mkdir failed: %w", err)
	}

	tmpPath := destPath + ".tmp"
	out, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("create temp file failed: %w", err)
	}

	client := &http.Client{Timeout: 300 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		out.Close()
		return fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()

	written, err := io.Copy(out, resp.Body)
	out.Close()
	if err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("write failed: %w", err)
	}

	log.Printf("Downloaded %d bytes, verifying SHA256", written)

	if sha256sum != "" {
		if err := verifySHA256(tmpPath, sha256sum); err != nil {
			os.Remove(tmpPath)
			return err
		}
	}

	if err := os.Rename(tmpPath, destPath); err != nil {
		return fmt.Errorf("rename failed: %w", err)
	}

	log.Printf("File saved to %s", destPath)
	return nil
}

func verifySHA256(path, expected string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open for verify failed: %w", err)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("sha256 read failed: %w", err)
	}

	actual := fmt.Sprintf("%x", h.Sum(nil))
	if actual != expected {
		return fmt.Errorf("SHA256 mismatch: expected %s, got %s", expected, actual)
	}
	return nil
}

func DownloadManifestFiles(manifest *Manifest, destDir string) error {
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return fmt.Errorf("mkdir %s failed: %w", destDir, err)
	}

	for _, f := range manifest.Files {
		destPath := filepath.Join(destDir, f.Name)
		if err := DownloadFile(f.URL, f.SHA256, destPath); err != nil {
			return fmt.Errorf("download %s failed: %w", f.Name, err)
		}
	}
	return nil
}

// FilterCandidates applies the configured hard rules to the candidate
// manifest, then scores the surviving models to pick the best match for
// the requested task. Zero threshold values mean the dimension is not
// enforced. Hard elimination (format/size/latency/accuracy/device
// capability) happens before scoring; scoring only orders survivors.
func FilterCandidates(manifest *Manifest, filter config.OTAFilter, task string, taskTags []string, cap config.DeviceCap) (*Manifest, error) {
	if len(manifest.Files) == 0 {
		return nil, fmt.Errorf("manifest has no files")
	}

	var passed []ManifestFile
	for _, f := range manifest.Files {
		if filter.MaxSizeMB > 0 && f.SizeBytes > 0 {
			sizeMB := float64(f.SizeBytes) / 1024 / 1024
			if sizeMB > filter.MaxSizeMB {
				log.Printf("filter: skip %s version=%s size=%.1fMB > max=%.1fMB", f.Name, f.Version, sizeMB, filter.MaxSizeMB)
				continue
			}
		}
		if filter.MinAccuracy > 0 && f.Accuracy > 0 && f.Accuracy < filter.MinAccuracy {
			log.Printf("filter: skip %s version=%s accuracy=%.4f < min=%.4f", f.Name, f.Version, f.Accuracy, filter.MinAccuracy)
			continue
		}
		if filter.RequiredFormat != "" && f.Format != "" && f.Format != filter.RequiredFormat {
			log.Printf("filter: skip %s version=%s format=%s != %s", f.Name, f.Version, f.Format, filter.RequiredFormat)
			continue
		}
		if filter.MaxLatencyMS > 0 && f.LatencyMS > 0 && f.LatencyMS > filter.MaxLatencyMS {
			log.Printf("filter: skip %s version=%s latency=%.0fms > max=%.0fms", f.Name, f.Version, f.LatencyMS, filter.MaxLatencyMS)
			continue
		}
		// device capability hard constraints
		if f.MinCPU > 0 && cap.CPU > 0 && f.MinCPU > cap.CPU {
			log.Printf("filter: skip %s version=%s needs %d cores, device has %d", f.Name, f.Version, f.MinCPU, cap.CPU)
			continue
		}
		if f.MinMemMB > 0 && cap.MemMB > 0 && f.MinMemMB > cap.MemMB {
			log.Printf("filter: skip %s version=%s needs %dMB RAM, device has %dMB", f.Name, f.Version, f.MinMemMB, cap.MemMB)
			continue
		}
		if f.RequiresGPU && !cap.HasGPU {
			log.Printf("filter: skip %s version=%s requires GPU, device has none", f.Name, f.Version)
			continue
		}
		passed = append(passed, f)
	}
	if len(passed) == 0 {
		return nil, fmt.Errorf("no model passed the filter rules")
	}

	taskMatch := func(f ManifestFile) bool {
		return task != "" && f.Task == task
	}
	tagOverlap := func(f ManifestFile) int {
		n := 0
		for _, t := range taskTags {
			for _, ft := range f.Tags {
				if t == ft {
					n++
				}
			}
		}
		return n
	}

	best := passed[0]
	bestScore := scoreModel(best, filter, taskMatch(best), tagOverlap(best))
	for _, f := range passed[1:] {
		s := scoreModel(f, filter, taskMatch(f), tagOverlap(f))
		if s > bestScore {
			best = f
			bestScore = s
		}
	}
	log.Printf("filter: selected model %s version=%s accuracy=%.4f score=%.2f task=%s",
		best.Name, best.Version, best.Accuracy, bestScore, best.Task)

	out := &Manifest{Version: best.Version}
	for _, f := range passed {
		if f.Version == best.Version {
			out.Files = append(out.Files, f)
		}
	}
	if len(out.Files) == 0 {
		out.Files = passed
	}
	return out, nil
}

// scoreModel computes the selection score: task strong match and tag
// overlaps add fixed bonuses, accuracy is weighted when prefer_accuracy.
func scoreModel(f ManifestFile, filter config.OTAFilter, taskMatch bool, tagOverlap int) float64 {
	score := 0.0
	if filter.PreferAccuracy && f.Accuracy > 0 {
		score += f.Accuracy
	}
	if taskMatch && filter.TaskMatchBonus > 0 {
		score += filter.TaskMatchBonus
	}
	if filter.TagBonus > 0 {
		score += float64(tagOverlap) * filter.TagBonus
	}
	return score
}

// DetectDeviceCap probes the local device capability. The configured
// values take precedence when non-zero.
func DetectDeviceCap(cfg config.DeviceCap) config.DeviceCap {
	out := cfg
	if out.CPU <= 0 {
		out.CPU = probeInt("nproc")
	}
	if out.MemMB <= 0 {
		out.MemMB = probeMemMB()
	}
	out.HasGPU = cfg.HasGPU || probeGPU()
	return out
}

func probeInt(cmd string) int {
	out, err := exec.Command("sh", "-c", cmd).Output()
	if err != nil {
		return 0
	}
	var v int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(out)), "%d", &v); err != nil {
		return 0
	}
	return v
}

func probeMemMB() int {
	out, err := exec.Command("sh", "-c", "grep MemTotal /proc/meminfo | awk '{print $2}'").Output()
	if err != nil {
		return 0
	}
	var kb int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(out)), "%d", &kb); err != nil {
		return 0
	}
	return kb / 1024
}

// probeGPU reports whether the device has a GPU usable for inference
// (CUDA/ncnn-GPU). Only NVIDIA devices count: /dev/nvidia0 exists on
// Jetson and discrete NVIDIA GPUs. Graphics-only GPUs (e.g. the V3D on
// a Raspberry Pi, exposed via /dev/dri/renderD128) must NOT count,
// otherwise OTA would push GPU-requiring models to devices that cannot
// run them.
func probeGPU() bool {
	if _, err := os.Stat("/dev/nvidia0"); err == nil {
		return true
	}
	return false
}

func SwitchSymlink(targetDir, symlinkPath string) error {
	tmpLink := symlinkPath + ".tmp"
	os.Remove(tmpLink)
	if err := os.Symlink(targetDir, tmpLink); err != nil {
		return fmt.Errorf("create temp symlink failed: %w", err)
	}
	if err := os.Rename(tmpLink, symlinkPath); err != nil {
		os.Remove(tmpLink)
		return fmt.Errorf("rename symlink failed: %w", err)
	}
	log.Printf("Symlink %s -> %s", symlinkPath, targetDir)
	return nil
}

func GetSymlinkTarget(symlinkPath string) (string, error) {
	target, err := os.Readlink(symlinkPath)
	if err != nil {
		return "", err
	}
	return target, nil
}

func CurrentVersionFromSymlink(symlinkPath string) string {
	target, err := GetSymlinkTarget(symlinkPath)
	if err != nil {
		return ""
	}
	return filepath.Base(target)
}

func CleanOldVersions(modelDir string, keep int) error {
	entries, err := os.ReadDir(modelDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var dirs []os.DirEntry
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			dirs = append(dirs, e)
		}
	}

	if len(dirs) <= keep {
		return nil
	}

	sort.Slice(dirs, func(i, j int) bool {
		infoI, _ := dirs[i].Info()
		infoJ, _ := dirs[j].Info()
		if infoI == nil || infoJ == nil {
			return dirs[i].Name() < dirs[j].Name()
		}
		return infoI.ModTime().Before(infoJ.ModTime())
	})

	toRemove := len(dirs) - keep
	for i := 0; i < toRemove; i++ {
		path := filepath.Join(modelDir, dirs[i].Name())
		if err := os.RemoveAll(path); err != nil {
			log.Printf("Cleanup: failed to remove %s: %v", path, err)
		} else {
			log.Printf("Cleanup: removed old version %s", dirs[i].Name())
		}
	}
	return nil
}

// ActivateModel 在换链后调用推理服务热重载协议并确认生效。
//
// mode=http: POST inference_reload.url 触发 /reload，然后（若 confirm）
//   轮询 ready_url 直到 active_version == targetVersion；失败/超时返回错误，
//   由调用方执行自动回滚（旧引擎在推理服务内继续服务，零中断）。
// mode=cmd: 执行 inference_restart_cmd（旧行为，向后兼容）。
// mode=none/空: 仅换链，不执行激活动作。
func ActivateModel(cfg config.OTA, targetVersion string) error {
	r := cfg.InferenceReload
	switch r.Mode {
	case "http":
		if r.URL == "" {
			return fmt.Errorf("inference_reload.url not set")
		}
		timeout := r.TimeoutSeconds
		if timeout <= 0 {
			timeout = 120
		}
		httpc := &http.Client{Timeout: time.Duration(timeout) * time.Second}
		resp, err := httpc.Post(r.URL, "application/json", nil)
		if err != nil {
			return fmt.Errorf("reload request failed: %w", err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode >= 400 {
			return fmt.Errorf("reload returned HTTP %d", resp.StatusCode)
		}
		if r.Confirm {
			if r.ReadyURL == "" {
				return fmt.Errorf("inference_reload.ready_url not set (confirm mode)")
			}
			deadline := time.Now().Add(time.Duration(timeout) * time.Second)
			for time.Now().Before(deadline) {
				h, herr := fetchHealth(httpc, r.ReadyURL)
				if herr == nil && h.Status == "ready" && h.ActiveVersion == targetVersion {
					log.Printf("OTA: inference confirmed version %s", targetVersion)
					return nil
				}
				time.Sleep(1 * time.Second)
			}
			return fmt.Errorf("reload confirm timeout (target %s)", targetVersion)
		}
		return nil
	case "cmd":
		if cfg.InferenceRestartCmd == "" {
			return nil
		}
		log.Printf("Restarting inference: %s", cfg.InferenceRestartCmd)
		cmd := exec.Command("sh", "-c", cfg.InferenceRestartCmd)
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("restart failed: %s, output: %s", err, string(output))
		}
		return nil
	default:
		return nil
	}
}

type healthInfo struct {
	Status        string `json:"status"`
	ActiveVersion string `json:"active_version"`
}

func fetchHealth(client *http.Client, url string) (*healthInfo, error) {
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("health HTTP %d", resp.StatusCode)
	}
	var h healthInfo
	if err := json.NewDecoder(resp.Body).Decode(&h); err != nil {
		return nil, err
	}
	return &h, nil
}

func Rollback(cfg config.OTA, client mqtt.Client, deviceID, resultTopic string) (string, error) {
	state.mu.Lock()
	defer state.mu.Unlock()

	if state.rollbackTo == "" {
		return "", fmt.Errorf("no rollback target available")
	}

	symlinkPath := cfg.CurrentSymlink
	if symlinkPath == "" {
		return "", fmt.Errorf("current_symlink not configured")
	}

	rollbackDir := filepath.Join(cfg.ModelDir, state.rollbackTo)
	if _, err := os.Stat(rollbackDir); os.IsNotExist(err) {
		return "", fmt.Errorf("rollback version %s not found", state.rollbackTo)
	}

	currentTarget, _ := GetSymlinkTarget(symlinkPath)
	currentName := filepath.Base(currentTarget)

	if err := SwitchSymlink(rollbackDir, symlinkPath); err != nil {
		return "", fmt.Errorf("symlink switch failed: %w", err)
	}

	state.rollbackTo = currentName
	msg := fmt.Sprintf("rollback to %s", state.rollbackTo)

	// 激活协议：换回旧链后同样触发 reload 并确认
	if err := ActivateModel(cfg, state.rollbackTo); err != nil {
		log.Printf("Rollback: activate failed after rollback: %v", err)
	}

	publishResult(client, deviceID, resultTopic, true, msg)
	return msg, nil
}

// CheckOptions carries per-call dynamic overrides. Empty/zero values
// fall back to the static config filter.
type CheckOptions struct {
	Task           string   // requested task, overrides cfg.OTA.Task
	TaskTags       []string // requested tags for soft matching
	RequireAccuracy float64 // per-call min accuracy override, 0 = use config
	MaxLatencyMS   float64 // per-call max latency override, 0 = use config
}

func CheckNow(cfg config.OTA, client mqtt.Client, deviceID, resultTopic string, opts CheckOptions) (string, error) {
	if cfg.ServerURL == "" {
		return "", fmt.Errorf("OTA server_url not configured")
	}

	manifest, err := FetchManifest(cfg.ServerURL, cfg.VersionPath)
	if err != nil {
		return "", fmt.Errorf("version check failed: %w", err)
	}

	cap := DetectDeviceCap(cfg.DeviceCap)
	log.Printf("OTA: device cap cpu=%d mem=%dMB gpu=%v", cap.CPU, cap.MemMB, cap.HasGPU)

	task := opts.Task
	if task == "" {
		task = cfg.Task
	}

	filter := cfg.Filter
	if opts.RequireAccuracy > 0 {
		filter.MinAccuracy = opts.RequireAccuracy
	}
	if opts.MaxLatencyMS > 0 {
		filter.MaxLatencyMS = opts.MaxLatencyMS
	}

	if filter.MaxSizeMB > 0 || filter.MinAccuracy > 0 ||
		filter.RequiredFormat != "" || filter.MaxLatencyMS > 0 {
		filtered, err := FilterCandidates(manifest, filter, task, opts.TaskTags, cap)
		if err != nil {
			return "", fmt.Errorf("model filter failed: %w", err)
		}
		manifest = filtered
	}

	// the model that will be deployed (first file of the chosen version)
	var picked ManifestFile
	if len(manifest.Files) > 0 {
		picked = manifest.Files[0]
	}
	log.Printf("OTA: picked model %s version=%s task=%s accuracy=%.4f",
		picked.Name, manifest.Version, picked.Task, picked.Accuracy)

	symlinkPath := cfg.CurrentSymlink
	currentVersion := cfg.CurrentVersion

	if symlinkPath != "" {
		if v := CurrentVersionFromSymlink(symlinkPath); v != "" {
			currentVersion = v
		}
	}

	log.Printf("OTA: remote version=%s, local version=%s", manifest.Version, currentVersion)

	if manifest.Version == currentVersion {
		return "already up-to-date", nil
	}

	newDir := filepath.Join(cfg.ModelDir, manifest.Version)
	if err := DownloadManifestFiles(manifest, newDir); err != nil {
		return "", fmt.Errorf("download failed: %w", err)
	}

	state.mu.Lock()
	if symlinkPath != "" {
		// 注意：GetSymlinkTarget 失败时 currentTarget=""，
		// filepath.Base("") 返回 "." 而非空串，会把回滚目标写成 model_dir
		state.rollbackTo = ""
		if currentTarget, err := GetSymlinkTarget(symlinkPath); err == nil && currentTarget != "" {
			state.rollbackTo = filepath.Base(currentTarget)
		}
	}

	if symlinkPath != "" {
		if err := SwitchSymlink(newDir, symlinkPath); err != nil {
			state.mu.Unlock()
			os.RemoveAll(newDir)
			return "", fmt.Errorf("symlink switch failed: %w", err)
		}
	}

	state.lastVersion = manifest.Version
	state.mu.Unlock()

	if cfg.BackupCount > 0 && cfg.ModelDir != "" {
		CleanOldVersions(cfg.ModelDir, cfg.BackupCount)
	}

	// 激活协议：reload 推理服务并确认生效；失败自动回滚到旧版本
	// （推理服务内旧引擎在加载期间继续服务，切换零中断）
	switchStart := time.Now()
	if err := ActivateModel(cfg, manifest.Version); err != nil {
		// 仅当存在可回滚的旧版本时才回滚（首次部署失败直接报错）
		if symlinkPath != "" && state.rollbackTo != "" {
			rollbackDir := filepath.Join(cfg.ModelDir, state.rollbackTo)
			if _, statErr := os.Stat(rollbackDir); statErr == nil {
				if rerr := SwitchSymlink(rollbackDir, symlinkPath); rerr == nil {
					log.Printf("OTA: auto-rolled back to %s after activate failure", state.rollbackTo)
					// 回滚后让推理服务重新加载旧版本（旧引擎可能仍在服务，尽力同步）
					if aerr := ActivateModel(cfg, state.rollbackTo); aerr != nil {
						log.Printf("OTA: activate old version after rollback failed: %v", aerr)
					}
				} else {
					log.Printf("OTA: auto-rollback symlink switch failed: %v", rerr)
				}
			} else {
				log.Printf("OTA: rollback dir %s missing: %v", rollbackDir, statErr)
			}
		} else {
			log.Printf("OTA: activate failed, no previous version to roll back to: %v", err)
		}
		return "", fmt.Errorf("activate failed (rolled back to %s): %w", state.rollbackTo, err)
	}
	switchMS := time.Since(switchStart).Milliseconds()

	msg := fmt.Sprintf("updated to %s (switch %dms)", manifest.Version, switchMS)
	if resultTopic != "" && client != nil {
		payload := fmt.Sprintf(`{"device_id":%q,"success":true,"message":%q,"task":%q,"model":%q,"version":%q,"model_task":%q,"accuracy":%.4f}`,
			deviceID, msg, task, picked.Name, manifest.Version, picked.Task, picked.Accuracy)
		client.Publish(resultTopic, 1, false, []byte(payload))
	}

	notifyUpdate(UpdateInfo{
		Version:       manifest.Version,
		Model:         picked.Name,
		RequestedTask: task,
		ModelTask:     picked.Task,
		Accuracy:      picked.Accuracy,
		Format:        picked.Format,
		LatencyMS:     picked.LatencyMS,
		SizeMB:        picked.SizeMB,
		UpdatedAt:     time.Now(),
	})
	return msg, nil
}

func StartPeriodicCheck(cfg config.OTA, client mqtt.Client, deviceID, resultTopic string) {
	if cfg.ServerURL == "" {
		log.Println("OTA: server_url not configured, skipping")
		return
	}
	interval := cfg.CheckInterval
	if interval <= 0 {
		interval = 300
	}
	log.Printf("OTA: checking every %ds at %s", interval, cfg.ServerURL)

	ticker := time.NewTicker(time.Duration(interval) * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		msg, err := CheckNow(cfg, client, deviceID, resultTopic, CheckOptions{})
		if err != nil {
			log.Printf("OTA check error: %v", err)
		} else if msg != "already up-to-date" {
			log.Printf("OTA: %s", msg)
		}
	}
}

func InitRollbackState(cfg config.OTA) {
	if cfg.CurrentSymlink == "" {
		return
	}
	target, err := GetSymlinkTarget(cfg.CurrentSymlink)
	if err != nil {
		log.Printf("OTA: no current symlink to initialize rollback from: %v", err)
		return
	}
	state.mu.Lock()
	state.rollbackTo = filepath.Base(target)
	state.mu.Unlock()
	log.Printf("OTA: rollback initialized to %s", state.rollbackTo)
}

// ==================== Manifest V1（channels/beta.json）====================

// ManifestV1Artifact 是 V1 模型包内的单个文件。
type ManifestV1Artifact struct {
	Name        string `json:"name"`
	URL         string `json:"url"`
	SizeBytes   int64  `json:"size_bytes"`
	SHA256      string `json:"sha256"`
	Compression string `json:"compression"` // "" 或 "gzip"
}

// ManifestV1Model 是 V1 清单中的一个模型包（model_id + 多 artifact）。
type ManifestV1Model struct {
	ModelID     string               `json:"model_id"`
	Version     string               `json:"version"`
	Task        string               `json:"task"`
	Tags        []string             `json:"tags"`
	Accuracy    float64              `json:"accuracy"`
	LatencyMS   float64              `json:"latency_ms"`
	MinCPU      int                  `json:"min_cpu_cores"`
	MinMemMB    int                  `json:"min_memory_mb"`
	RequiresGPU bool                 `json:"requires_gpu"`
	Artifacts   []ManifestV1Artifact `json:"artifacts"`
	MetadataURL string               `json:"metadata_url"`
}

// ManifestV1 是 Manifest V1 顶层结构（schema_version=1.0）。
type ManifestV1 struct {
	SchemaVersion string            `json:"schema_version"`
	Channel       string            `json:"channel"`
	ReleaseID     int64             `json:"release_id"`
	Models        []ManifestV1Model `json:"models"`
}

// FetchManifestV1 拉取并解析 Manifest V1（默认 channels/beta.json）。
func FetchManifestV1(serverURL, versionPath string) (*ManifestV1, error) {
	if versionPath == "" {
		versionPath = "channels/beta.json"
	}
	url := strings.TrimRight(serverURL, "/") + "/" + versionPath
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("fetch V1 manifest failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read V1 manifest failed: %w", err)
	}
	var m ManifestV1
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("parse V1 manifest failed: %w", err)
	}
	if m.SchemaVersion != "1.0" {
		return nil, fmt.Errorf("not a V1 manifest (schema_version=%q)", m.SchemaVersion)
	}
	return &m, nil
}

// ModelPackageDir 返回 V1 模型包目录：model_dir/versions/<model_id>/<version>。
// 与旧版 model_dir/<version> 布局共存；消费端一律读 current 软链接。
func ModelPackageDir(modelDir, modelID, version string) string {
	return filepath.Join(modelDir, "versions", modelID, version)
}

// gunzipFile 解压 src 到 dst（Gitee 审核要求类别表/字典 gzip 存储）。
func gunzipFile(src, dst string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("gzip open %s failed: %w", src, err)
	}
	defer gz.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, gz); err != nil {
		return fmt.Errorf("gunzip %s failed: %w", src, err)
	}
	return nil
}

// EnsureModelPackage 确保模型包已下载并校验（缺失或损坏时重新下载）。
// 返回包目录；已存在（model.ncnn.param 在位）时跳过下载。
func EnsureModelPackage(cfg config.OTA, m ManifestV1Model) (string, error) {
	dir := ModelPackageDir(cfg.ModelDir, m.ModelID, m.Version)
	ready := func() bool {
		for _, a := range m.Artifacts {
			if !osFileExists(filepath.Join(dir, a.Name)) {
				return false
			}
		}
		return true
	}
	if ready() {
		return dir, nil
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	for _, a := range m.Artifacts {
		dest := filepath.Join(dir, a.Name)
		if err := DownloadFile(a.URL, a.SHA256, dest); err != nil {
			return "", fmt.Errorf("download %s failed: %w", a.Name, err)
		}
		if a.Compression == "gzip" && strings.HasSuffix(a.Name, ".gz") {
			plain := strings.TrimSuffix(dest, ".gz")
			if err := gunzipFile(dest, plain); err != nil {
				return "", err
			}
		}
	}
	log.Printf("OTA: model package %s@%s ready at %s (%d files)",
		m.ModelID, m.Version, dir, len(m.Artifacts))
	return dir, nil
}

func osFileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// SwitchModelV1 从 V1 manifest 选择并激活模型（switch_model MCP 工具）。
//   - modelID 精确指定（优先）；否则按 task + 阈值筛选，score = accuracy + task 加成
//   - 包缺失时自动下载（含 gzip 解压）
//   - 换链后走 ActivateModel（热重载协议）；激活失败自动回滚
func SwitchModelV1(cfg config.OTA, client mqtt.Client, deviceID, resultTopic string,
	modelID, task string, requireAccuracy, maxLatency float64) (string, error) {

	if cfg.ServerURL == "" {
		return "", fmt.Errorf("OTA server_url not configured")
	}
	if cfg.V1VersionPath == "" {
		return "", fmt.Errorf("v1_version_path not configured (switch_model 需要 Manifest V1)")
	}
	m, err := FetchManifestV1(cfg.ServerURL, cfg.V1VersionPath)
	if err != nil {
		return "", err
	}
	if len(m.Models) == 0 {
		return "", fmt.Errorf("V1 manifest has no models")
	}

	// ---- 选模型 ----
	var pick *ManifestV1Model
	if modelID != "" {
		for i := range m.Models {
			if m.Models[i].ModelID == modelID {
				pick = &m.Models[i]
				break
			}
		}
		if pick == nil {
			return "", fmt.Errorf("model_id %q not found in V1 manifest", modelID)
		}
	} else {
		cap := DetectDeviceCap(cfg.DeviceCap)
		best, bestScore := -1, -1.0
		for i := range m.Models {
			mm := &m.Models[i]
			if task != "" && mm.Task != task {
				continue
			}
			if requireAccuracy > 0 && mm.Accuracy > 0 && mm.Accuracy < requireAccuracy {
				continue
			}
			if maxLatency > 0 && mm.LatencyMS > 0 && mm.LatencyMS > maxLatency {
				continue
			}
			if mm.MinCPU > 0 && cap.CPU > 0 && mm.MinCPU > cap.CPU {
				continue
			}
			if mm.MinMemMB > 0 && cap.MemMB > 0 && mm.MinMemMB > cap.MemMB {
				continue
			}
			if mm.RequiresGPU && !cap.HasGPU {
				continue
			}
			score := mm.Accuracy
			if task != "" && mm.Task == task {
				score += 4.0
			}
			if score > bestScore {
				best, bestScore = i, score
			}
		}
		if best < 0 {
			return "", fmt.Errorf("no model matches task=%q (acc>=%.2f lat<=%.0f)",
				task, requireAccuracy, maxLatency)
		}
		pick = &m.Models[best]
	}
	log.Printf("OTA: switch_model picked %s@%s task=%s accuracy=%.4f",
		pick.ModelID, pick.Version, pick.Task, pick.Accuracy)

	// ---- 下载（缺失时）----
	dir, err := EnsureModelPackage(cfg, *pick)
	if err != nil {
		return "", err
	}

	// ---- 激活（换链 + 热重载 + 失败回滚）----
	symlinkPath := cfg.CurrentSymlink
	if symlinkPath == "" {
		symlinkPath = filepath.Join(cfg.ModelDir, "current")
	}
	state.mu.Lock()
	state.rollbackTo = ""
	if t, err := GetSymlinkTarget(symlinkPath); err == nil && t != "" {
		state.rollbackTo = filepath.Base(t)
	}
	state.mu.Unlock()

	switchStart := time.Now()
	if err := SwitchSymlink(dir, symlinkPath); err != nil {
		return "", fmt.Errorf("symlink switch failed: %w", err)
	}
	if err := ActivateModel(cfg, pick.Version); err != nil {
		if state.rollbackTo != "" {
			rollbackDir := filepath.Join(cfg.ModelDir, state.rollbackTo)
			if _, statErr := os.Stat(rollbackDir); statErr == nil {
				if rerr := SwitchSymlink(rollbackDir, symlinkPath); rerr == nil {
					log.Printf("OTA: switch_model auto-rolled back to %s", state.rollbackTo)
					ActivateModel(cfg, state.rollbackTo)
				}
			}
		}
		return "", fmt.Errorf("activate %s@%s failed (rolled back): %w",
			pick.ModelID, pick.Version, err)
	}
	switchMS := time.Since(switchStart).Milliseconds()

	msg := fmt.Sprintf("switched to %s@%s (switch %dms)", pick.ModelID, pick.Version, switchMS)
	publishResult(client, deviceID, resultTopic, true, msg)
	notifyUpdate(UpdateInfo{
		Version:    pick.Version,
		Model:      pick.ModelID,
		RequestedTask: task,
		ModelTask:  pick.Task,
		Accuracy:   pick.Accuracy,
		LatencyMS:  pick.LatencyMS,
		UpdatedAt:  time.Now(),
	})
	return msg, nil
}

func publishResult(client mqtt.Client, deviceID, topic string, success bool, message string) {
	if topic == "" || client == nil {
		return
	}
	payload := fmt.Sprintf(`{"device_id":"%s","success":%v,"message":"%s"}`,
		deviceID, success, message)
	client.Publish(topic, 1, false, []byte(payload))
}
