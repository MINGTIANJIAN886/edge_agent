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
	Name   string `json:"name"`
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
}

type Manifest struct {
	Version string         `json:"version"`
	Files   []ManifestFile `json:"files"`
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
		return nil, fmt.Errorf("manifest missing version field")
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

func CheckNow(cfg config.OTA, client mqtt.Client, deviceID, resultTopic string) (string, error) {
	if cfg.ServerURL == "" {
		return "", fmt.Errorf("OTA server_url not configured")
	}

	manifest, err := FetchManifest(cfg.ServerURL, cfg.VersionPath)
	if err != nil {
		return "", fmt.Errorf("version check failed: %w", err)
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
		msg, err := CheckNow(cfg, client, deviceID, resultTopic)
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
