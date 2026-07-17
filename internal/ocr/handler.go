package ocr

import (
	"encoding/json"
	"fmt"
	"log"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/user/agent/internal/config"
)

type publishPayload struct {
	DeviceID  string     `json:"device_id"`
	Success   bool       `json:"success"`
	Trigger   string     `json:"trigger"`
	Texts     []TextEntry `json:"texts,omitempty"`
	Error     string     `json:"error,omitempty"`
	Timestamp float64    `json:"timestamp"`
}

func StartScheduler(client mqtt.Client, cfg config.OCR, deviceID string) {
	if cfg.Interval <= 0 {
		return
	}
	ticker := time.NewTicker(time.Duration(cfg.Interval) * time.Second)
	go func() {
		for range ticker.C {
			result, err := RunOCR(cfg.ScriptPath, cfg.ConfThreshold)
			publishResult(client, cfg.ResultTopic, deviceID, "timer", result, err)
		}
	}()
	log.Printf("OCR scheduler started: interval=%ds, result_topic=%s", cfg.Interval, cfg.ResultTopic)
}

func SubscribeCommands(client mqtt.Client, cfg config.OCR, deviceID string) {
	token := client.Subscribe(cfg.CommandTopic, 1, func(_ mqtt.Client, msg mqtt.Message) {
		log.Printf("OCR command received on %s", msg.Topic())
		result, err := RunOCR(cfg.ScriptPath, cfg.ConfThreshold)
		publishResult(client, cfg.ResultTopic, deviceID, "command", result, err)
	})
	token.WaitTimeout(10 * time.Second)
	if token.Error() != nil {
		log.Printf("OCR subscribe error: %v", token.Error())
	} else {
		log.Printf("OCR subscribed to command topic: %s", cfg.CommandTopic)
	}
}

func publishResult(client mqtt.Client, topic, deviceID, trigger string, result *OCRResult, err error) {
	payload := publishPayload{
		DeviceID: deviceID,
		Trigger:  trigger,
	}

	if err != nil {
		payload.Success = false
		payload.Error = fmt.Sprintf("ocr failed: %v", err)
	} else {
		payload.Success = result.Success
		payload.Texts = result.Texts
		payload.Error = result.Error
		payload.Timestamp = result.Timestamp
	}

	data, marshalErr := json.Marshal(payload)
	if marshalErr != nil {
		log.Printf("OCR marshal error: %v", marshalErr)
		return
	}

	token := client.Publish(topic, 1, false, data)
	token.WaitTimeout(5 * time.Second)
	if token.Error() != nil {
		log.Printf("OCR publish error: %v", token.Error())
	} else {
		log.Printf("OCR result published to %s (trigger=%s, success=%v)", topic, trigger, payload.Success)
	}
}
