package download

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

type DownloadRequest struct {
	ID       string `json:"id"`
	URL      string `json:"url"`
	DestDir  string `json:"dest_dir"`
	DestName string `json:"dest_name"`
}

type DownloadResult struct {
	ID       string `json:"id"`
	DeviceID string `json:"device_id"`
	Success  bool   `json:"success"`
	Path     string `json:"path"`
	Size     int64  `json:"size"`
	Error    string `json:"error,omitempty"`
}

func DownloadFile(sourceURL, dest string) (int64, error) {
	parsedURL, err := url.Parse(sourceURL)
	if err != nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
		return 0, fmt.Errorf("only http and https URLs are supported")
	}

	client := &http.Client{Timeout: 300 * time.Second}
	resp, err := client.Get(sourceURL)
	if err != nil {
		return 0, fmt.Errorf("http get error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("http status: %s", resp.Status)
	}

	destDir := filepath.Dir(dest)
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return 0, fmt.Errorf("mkdir error: %w", err)
	}

	tmpFile, err := os.CreateTemp(destDir, ".edge-download-*")
	if err != nil {
		return 0, fmt.Errorf("create temporary file error: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	written, err := io.Copy(tmpFile, resp.Body)
	if err != nil {
		tmpFile.Close()
		return 0, fmt.Errorf("write file error: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return 0, fmt.Errorf("close file error: %w", err)
	}
	if err := os.Chmod(tmpPath, 0644); err != nil {
		return 0, fmt.Errorf("chmod error: %w", err)
	}
	if err := os.Rename(tmpPath, dest); err != nil {
		return 0, fmt.Errorf("move file error: %w", err)
	}

	return written, nil
}

func ResolveDestination(baseDir, requestedDir, requestedName, sourceURL string) (string, error) {
	baseAbs, err := filepath.Abs(baseDir)
	if err != nil {
		return "", fmt.Errorf("resolve download_dir: %w", err)
	}

	destDir := requestedDir
	if destDir == "" {
		destDir = baseAbs
	} else if !filepath.IsAbs(destDir) {
		destDir = filepath.Join(baseAbs, destDir)
	}

	destName := requestedName
	if destName == "" {
		parsedURL, err := url.Parse(sourceURL)
		if err != nil {
			return "", fmt.Errorf("parse URL: %w", err)
		}
		destName = filepath.Base(parsedURL.Path)
	}
	if destName == "." || destName == "/" || destName == "" {
		return "", fmt.Errorf("download destination name is empty")
	}

	destPath := filepath.Clean(filepath.Join(destDir, destName))
	rel, err := filepath.Rel(baseAbs, destPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("destination must stay inside download_dir %s", baseAbs)
	}
	return destPath, nil
}

func SubscribeDownloads(client mqtt.Client, deviceID, topic, defaultDir string) {
	token := client.Subscribe(topic, 1, func(_ mqtt.Client, msg mqtt.Message) {
		var req DownloadRequest
		if err := json.Unmarshal(msg.Payload(), &req); err != nil {
			log.Printf("failed to parse download request: %v", err)
			return
		}

		destPath, err := ResolveDestination(defaultDir, req.DestDir, req.DestName, req.URL)
		if err != nil {
			publishResult(client, msg.Topic()+"/result", DownloadResult{
				ID:       req.ID,
				DeviceID: deviceID,
				Success:  false,
				Error:    err.Error(),
			})
			return
		}

		log.Printf("downloading %s -> %s", req.URL, destPath)
		size, err := DownloadFile(req.URL, destPath)
		result := DownloadResult{
			ID:       req.ID,
			DeviceID: deviceID,
			Path:     destPath,
			Success:  err == nil,
			Size:     size,
		}
		if err != nil {
			result.Error = err.Error()
			log.Printf("download failed: %v", err)
		} else {
			log.Printf("download complete: %s (%d bytes)", destPath, size)
		}
		publishResult(client, msg.Topic()+"/result", result)
	})
	token.WaitTimeout(10 * time.Second)
	if token.Error() != nil {
		log.Printf("failed to subscribe to download topic: %v", token.Error())
	}
}

func publishResult(client mqtt.Client, topic string, result DownloadResult) {
	data, _ := json.Marshal(result)
	token := client.Publish(topic, 1, false, data)
	token.WaitTimeout(5 * time.Second)
}
