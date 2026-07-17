package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"os/exec"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

type CommandRequest struct {
	ID      string `json:"id"`
	Command string `json:"command"`
	Timeout int    `json:"timeout"`
}

type CommandResult struct {
	ID       string `json:"id"`
	DeviceID string `json:"device_id"`
	Success  bool   `json:"success"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exit_code"`
	Duration string `json:"duration"`
}

func ExecuteCommand(req CommandRequest) CommandResult {
	start := time.Now()

	timeout := req.Timeout
	if timeout <= 0 {
		timeout = 30
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", req.Command)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	result := CommandResult{
		ID:       req.ID,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		Duration: time.Since(start).Round(time.Millisecond).String(),
	}

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
		} else {
			result.ExitCode = -1
			result.Stderr = err.Error()
		}
		result.Success = false
	} else {
		result.Success = true
		result.ExitCode = 0
	}

	return result
}

func SubscribeCommands(client mqtt.Client, deviceID string, topic string) {
	token := client.Subscribe(topic, 1, func(_ mqtt.Client, msg mqtt.Message) {
		var req CommandRequest
		if err := json.Unmarshal(msg.Payload(), &req); err != nil {
			log.Printf("failed to parse command request: %v", err)
			return
		}

		log.Printf("executing command: %s (id=%s, timeout=%d)", req.Command, req.ID, req.Timeout)
		result := ExecuteCommand(req)
		result.DeviceID = deviceID

		data, err := json.Marshal(result)
		if err != nil {
			log.Printf("failed to marshal result: %v", err)
			return
		}

		resultTopic := msg.Topic() + "/result"
		token := client.Publish(resultTopic, 1, false, data)
		token.WaitTimeout(5 * time.Second)
		if token.Error() != nil {
			log.Printf("failed to publish result: %v", token.Error())
		} else {
			log.Printf("command result published to %s", resultTopic)
		}
	})
	token.WaitTimeout(10 * time.Second)
	if token.Error() != nil {
		log.Printf("failed to subscribe to command topic: %v", token.Error())
	} else {
		log.Printf("subscribed to command topic: %s", topic)
	}
}
