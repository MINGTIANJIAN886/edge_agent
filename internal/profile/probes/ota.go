package probes

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/user/agent/internal/config"
	"github.com/user/agent/internal/ota"
	"github.com/user/agent/internal/profile"
)

// OTAProbe verifies the whole OTA link without applying an update:
// manifest reachability, parseability, cache writability, free disk
// space and (optionally) a small test-file download with checksum.
type OTAProbe struct {
	Cfg  config.OTA
	ProbeCfg config.OTAProbeCfg
}

func NewOTAProbe(cfg config.OTA, pcfg config.OTAProbeCfg) *OTAProbe {
	return &OTAProbe{Cfg: cfg, ProbeCfg: pcfg}
}

func (p *OTAProbe) Name() string { return "ota" }

func (p *OTAProbe) Probe(ctx context.Context, req profile.ProbeRequest) profile.CapabilityResult {
	start := time.Now()
	timeout := req.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	if p.Cfg.ServerURL == "" {
		return result(false, profile.CodeOTANotConfigured, "OTA server_url not configured", time.Since(start), nil)
	}

	client := &http.Client{Timeout: timeout}
	manifestURL := strings.TrimRight(p.Cfg.ServerURL, "/") + "/" + p.Cfg.VersionPath

	resp, err := client.Get(manifestURL)
	if err != nil {
		return result(false, profile.CodeOTAManifestUnreachable,
			fmt.Sprintf("cannot reach %s: %v", manifestURL, err), time.Since(start),
			map[string]interface{}{"url": manifestURL})
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		return result(false, profile.CodeOTAManifestUnreachable,
			fmt.Sprintf("manifest HTTP %d from %s", resp.StatusCode, manifestURL), time.Since(start),
			map[string]interface{}{"url": manifestURL, "status": resp.StatusCode})
	}

	var parsed struct {
		Version string            `json:"version"`
		Models  []json.RawMessage `json:"models"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return result(false, profile.CodeOTAManifestInvalid,
			fmt.Sprintf("manifest JSON parse failed: %v", err), time.Since(start),
			map[string]interface{}{"url": manifestURL})
	}

	cacheDir := p.ProbeCfg.CacheDir
	if cacheDir == "" {
		cacheDir = "/var/cache/edge-agent"
	}
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return result(false, profile.CodeOTACacheNotWritable,
			fmt.Sprintf("cache dir %s not writable: %v", cacheDir, err), time.Since(start),
			map[string]interface{}{"cache_dir": cacheDir})
	}
	probeFile := filepath.Join(cacheDir, ".ota_probe_write_test")
	if err := os.WriteFile(probeFile, []byte("probe"), 0644); err != nil {
		return result(false, profile.CodeOTACacheNotWritable,
			fmt.Sprintf("cache dir %s not writable: %v", cacheDir, err), time.Since(start),
			map[string]interface{}{"cache_dir": cacheDir})
	}
	os.Remove(probeFile)

	var st syscall.Statfs_t
	if err := syscall.Statfs(cacheDir, &st); err == nil {
		freeMB := int64(st.Bavail) * int64(st.Bsize) / 1024 / 1024
		minFree := p.ProbeCfg.MinFreeDiskMB
		if minFree <= 0 {
			minFree = 100
		}
		if freeMB < minFree {
			return result(false, profile.CodeOTADiskInsufficient,
				fmt.Sprintf("free disk %dMB < %dMB threshold", freeMB, minFree), time.Since(start),
				map[string]interface{}{"free_mb": freeMB, "min_free_mb": minFree})
		}
	}

	details := map[string]interface{}{
		"manifest_url":     manifestURL,
		"remote_version":   parsed.Version,
		"candidate_count":  len(parsed.Models),
		"update_available": false,
		"cache_dir":        cacheDir,
	}

	if p.ProbeCfg.TestFileURL != "" {
		ok, msg, d := p.testDownload(client, cacheDir, timeout)
		if !ok {
			return result(false, msg, d["message"].(string), time.Since(start), d)
		}
		for k, v := range d {
			details[k] = v
		}
	}

	msg := "OTA link OK: manifest reachable, cache writable"
	if p.ProbeCfg.TestFileURL != "" {
		msg += ", test file download+checksum verified"
	}
	return result(true, profile.CodeOK, msg, time.Since(start), details)
}

// testDownload fetches the ota-probe.json descriptor, then downloads
// the referenced small file and verifies its SHA256, cleaning up after.
func (p *OTAProbe) testDownload(client *http.Client, cacheDir string, timeout time.Duration) (bool, string, map[string]interface{}) {
	d := map[string]interface{}{}
	resp, err := client.Get(p.ProbeCfg.TestFileURL)
	if err != nil {
		d["message"] = fmt.Sprintf("test descriptor unreachable: %v", err)
		return false, profile.CodeOTAManifestUnreachable, d
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	var desc struct {
		URL       string `json:"url"`
		SizeBytes int64  `json:"size_bytes"`
		SHA256    string `json:"sha256"`
	}
	if err := json.Unmarshal(body, &desc); err != nil {
		d["message"] = fmt.Sprintf("test descriptor parse failed: %v", err)
		return false, profile.CodeOTAManifestInvalid, d
	}
	if desc.URL == "" || desc.SHA256 == "" {
		d["message"] = "test descriptor missing url/sha256"
		return false, profile.CodeOTAManifestInvalid, d
	}

	dest := filepath.Join(cacheDir, "ota-probe.bin")
	if err := ota.DownloadFile(desc.URL, desc.SHA256, dest); err != nil {
		os.Remove(dest)
		msg := err.Error()
		code := profile.CodeOTAChecksumFailed
		if strings.Contains(msg, "timed out") || strings.Contains(msg, "timeout") {
			code = profile.CodeOTADownloadTimeout
		}
		d["message"] = "test file download failed: " + msg
		d["test_url"] = desc.URL
		return false, code, d
	}
	os.Remove(dest)
	d["test_url"] = desc.URL
	d["test_size_bytes"] = desc.SizeBytes
	d["message"] = ""
	return true, "", d
}
