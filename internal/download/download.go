package download

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

type DownloadRequest struct {
	ID      string `json:"id"`
	URL     string `json:"url"`
	DestDir string `json:"dest_dir"`
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

func downloadFile(url, dest string) (int64, error) {
	client := &http.Client{Timeout: 300 * time.Second}

	resp, err := client.Get(url)
	if err != nil {
		return 0, fmt.Errorf("http get error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("http status: %s", resp.Status)
	}

	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return 0, fmt.Errorf("mkdir error: %w", err)
	}

	out, err := os.Create(dest)
	if err != nil {
		return 0, fmt.Errorf("create file error: %w", err)
	}
	defer out.Close()

	written, err := io.Copy(out, resp.Body)
	if err != nil {
		return 0, fmt.Errorf("write file error: %w", err)
	}

	if err := os.Chmod(dest, 0755); err != nil {
		return 0, fmt.Errorf("chmod error: %w", err)
	}

	return written, nil
}

func SubscribeDownloads(client mqtt.Client, deviceID string, topic string, defaultDir string) {
	token := client.Subscribe(topic, 1, func(_ mqtt.Client, msg mqtt.Message) {
		var req DownloadRequest
		if err := json.Unmarshal(msg.Payload(), &req); err != nil {
			log.Printf("failed to parse download request: %v", err)
			return
		}

		destDir := req.DestDir
		if destDir == "" {
			destDir = defaultDir
		}
		destName := req.DestName
		if destName == "" {
			destName = filepath.Base(req.URL)
		}
		destPath := filepath.Join(destDir, destName)

		log.Printf("downloading %s -> %s", req.URL, destPath)
		size, err := downloadFile(req.URL, destPath)

		result := DownloadResult{
			ID:       req.ID,
			DeviceID: deviceID,
			Path:     destPath,
		}

		if err != nil {
			result.Success = false
			result.Error = err.Error()
			log.Printf("download failed: %v", err)
		} else {
			result.Success = true
			result.Size = size
			log.Printf("download complete: %s (%d bytes)", destPath, size)
		}

		data, _ := json.Marshal(result)
		resultTopic := msg.Topic() + "/result"
		token := client.Publish(resultTopic, 1, false, data)
		token.WaitTimeout(5 * time.Second)
	})
	token.WaitTimeout(10 * time.Second)
	if token.Error() != nil {
		log.Printf("failed to subscribe to download topic: %v", token.Error())
	}
}
