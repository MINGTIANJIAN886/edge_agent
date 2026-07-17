package heartbeat

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"runtime"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

type Status struct {
	DeviceID  string `json:"device_id"`
	Timestamp int64  `json:"timestamp"`
	Uptime    string `json:"uptime"`
	GoVersion string `json:"go_version"`
	CPU       int    `json:"cpu"`
	Hostname  string `json:"hostname"`
	OS        string `json:"os"`
	Arch      string `json:"arch"`
}

var startTime time.Time

func init() {
	startTime = time.Now()
}

func Start(client mqtt.Client, deviceID string, interval int, topic string) {
	if interval <= 0 {
		interval = 30
	}
	ticker := time.NewTicker(time.Duration(interval) * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		hostname, _ := os.Hostname()
		status := Status{
			DeviceID:  deviceID,
			Timestamp: time.Now().Unix(),
			Uptime:    fmt.Sprintf("%v", time.Since(startTime).Round(time.Second)),
			GoVersion: runtime.Version(),
			CPU:       runtime.NumCPU(),
			Hostname:  hostname,
			OS:        runtime.GOOS,
			Arch:      runtime.GOARCH,
		}
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
