package remote

import (
	"encoding/json"
	"log"
	"time"

	"github.com/MINGTIANJIAN886/edge_agent/internal/command"
	"github.com/MINGTIANJIAN886/edge_agent/internal/config"
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

func ExecuteCommand(req CommandRequest, runtimeCfg config.Runtime) CommandResult {
	execResult := command.Execute(req.Command, req.Timeout, runtimeCfg)
	return CommandResult{
		ID:       req.ID,
		Success:  execResult.Success,
		Stdout:   execResult.Stdout,
		Stderr:   execResult.Stderr,
		ExitCode: execResult.ExitCode,
		Duration: execResult.Duration.String(),
	}
}

func SubscribeCommands(client mqtt.Client, deviceID string, topic string, runtimeCfg config.Runtime) {
	token := client.Subscribe(topic, 1, func(_ mqtt.Client, msg mqtt.Message) {
		var req CommandRequest
		if err := json.Unmarshal(msg.Payload(), &req); err != nil {
			log.Printf("failed to parse command request: %v", err)
			return
		}

		log.Printf("executing command: %s (id=%s, timeout=%d)", req.Command, req.ID, req.Timeout)
		result := ExecuteCommand(req, runtimeCfg)
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
