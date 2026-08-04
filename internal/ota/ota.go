package ota

import (
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

func probeGPU() bool {
	if _, err := os.Stat("/dev/nvidia0"); err == nil {
		return true
	}
	if _, err := os.Stat("/dev/dri/renderD128"); err == nil {
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

	if cfg.InferenceRestartCmd != "" {
		cmd := exec.Command("sh", "-c", cfg.InferenceRestartCmd)
		if output, err := cmd.CombinedOutput(); err != nil {
			log.Printf("Restart after rollback failed: %v, output: %s", err, string(output))
		}
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
		currentTarget, _ := GetSymlinkTarget(symlinkPath)
		state.rollbackTo = filepath.Base(currentTarget)
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

	if cfg.InferenceRestartCmd != "" {
		log.Printf("Restarting inference: %s", cfg.InferenceRestartCmd)
		cmd := exec.Command("sh", "-c", cfg.InferenceRestartCmd)
		if output, err := cmd.CombinedOutput(); err != nil {
			return "", fmt.Errorf("restart failed: %s, output: %s", err, string(output))
		}
	}

	msg := fmt.Sprintf("updated to %s", manifest.Version)
	publishResult(client, deviceID, resultTopic, true, msg)
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

func publishResult(client mqtt.Client, deviceID, topic string, success bool, message string) {
	if topic == "" || client == nil {
		return
	}
	payload := fmt.Sprintf(`{"device_id":"%s","success":%v,"message":"%s"}`,
		deviceID, success, message)
	client.Publish(topic, 1, false, []byte(payload))
}
