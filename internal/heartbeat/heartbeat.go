package heartbeat

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

type Status struct {
	DeviceID  string `json:"device_id"`
	Timestamp int64  `json:"timestamp"`
	Hostname  string `json:"hostname"`
	Uptime    string `json:"uptime"`
	OS        string `json:"os"`
	Arch      string `json:"arch"`
	GoVersion string `json:"go_version"`
	Kernel    string `json:"kernel"`
	CPU       string `json:"cpu"`
	Load      string `json:"load"`
	Memory    string `json:"memory"`
	Disk      string `json:"disk"`
}

var startTime = time.Now()

func shell(cmd string) string {
	out, err := exec.Command("sh", "-c", cmd).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func collectStatus(deviceID string) Status {
	hostname, _ := os.Hostname()
	return Status{
		DeviceID:  deviceID,
		Timestamp: time.Now().Unix(),
		Hostname:  hostname,
		Uptime:    fmt.Sprintf("%v", time.Since(startTime).Round(time.Second)),
		OS:        runtime.GOOS,
		Arch:      runtime.GOARCH,
		GoVersion: runtime.Version(),
		Kernel:    shell("uname -r"),
		CPU:       shell("nproc 2>/dev/null || echo 1"),
		Load:      shell("cat /proc/loadavg | awk '{print \"1m=\"$1\" 5m=\"$2\" 15m=\"$3}'"),
		Memory:    shell("free -h | awk 'NR==2{print \"total=\"$2\" used=\"$3\" free=\"$4}'"),
		Disk:      shell("df -h / | awk 'NR==2{print \"total=\"$2\" used=\"$3\" avail=\"$4\" usage=\"$5}'"),
	}
}

func Start(client mqtt.Client, deviceID string, interval int, topic string) {
	if interval <= 0 {
		interval = 30
	}
	ticker := time.NewTicker(time.Duration(interval) * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		status := collectStatus(deviceID)
		data, err := json.Marshal(status)
		if err != nil {
			log.Printf("heartbeat marshal error: %v", err)
			continue
		}
		token := client.Publish(topic, 1, false, data)
		token.WaitTimeout(5 * time.Second)
		if token.Error() != nil {
			log.Printf("heartbeat publish error: %v", token.Error())
		} else {
			log.Printf("heartbeat sent: %s", string(data))
		}
	}
}
