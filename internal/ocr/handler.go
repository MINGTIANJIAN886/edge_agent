package ocr

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/user/agent/internal/config"
)

type CommandPayload struct {
	Action   string `json:"action"`             // "start" | "stop" | "snapshot"
	Interval int    `json:"interval,omitempty"` // override interval on start
}

type publishPayload struct {
	DeviceID  string      `json:"device_id"`
	Success   bool        `json:"success"`
	Trigger   string      `json:"trigger"`
	Texts     []TextEntry `json:"texts,omitempty"`
	Error     string      `json:"error,omitempty"`
	Timestamp float64     `json:"timestamp"`
}

type Controller struct {
	client   mqtt.Client
	cfg      config.OCR
	deviceID string

	mu      sync.Mutex
	running bool
	cancel  context.CancelFunc
}

func NewController(client mqtt.Client, cfg config.OCR, deviceID string) *Controller {
	return &Controller{
		client:   client,
		cfg:      cfg,
		deviceID: deviceID,
	}
}

func (c *Controller) Start(interval int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.running {
		log.Println("OCR scheduler already running")
		return
	}

	if interval <= 0 {
		interval = c.cfg.Interval
	}
	if interval <= 0 {
		interval = 30
	}

	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	c.running = true

	go func() {
		ticker := time.NewTicker(time.Duration(interval) * time.Second)
		defer ticker.Stop()
		defer func() {
			c.mu.Lock()
			c.running = false
			c.mu.Unlock()
		}()

		log.Printf("OCR scheduler started: interval=%ds", interval)
		for {
			select {
			case <-ticker.C:
				result, err := RunOCR(c.cfg.ScriptPath, c.cfg.ConfThreshold)
				publishResult(c.client, c.cfg.ResultTopic, c.deviceID, "timer", result, err)
			case <-ctx.Done():
				log.Println("OCR scheduler stopped")
				return
			}
		}
	}()
}

func (c *Controller) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.running {
		log.Println("OCR scheduler not running")
		return
	}

	if c.cancel != nil {
		c.cancel()
		c.cancel = nil
	}
	c.running = false
	log.Println("OCR scheduler stopped")
}

func (c *Controller) Snapshot() {
	result, err := RunOCR(c.cfg.ScriptPath, c.cfg.ConfThreshold)
	publishResult(c.client, c.cfg.ResultTopic, c.deviceID, "command", result, err)
}

func (c *Controller) SubscribeCommands() {
	token := c.client.Subscribe(c.cfg.CommandTopic, 1, func(_ mqtt.Client, msg mqtt.Message) {
		var cmd CommandPayload
		if err := json.Unmarshal(msg.Payload(), &cmd); err != nil {
			log.Printf("OCR command (default snapshot): %s", msg.Payload())
			c.Snapshot()
			return
		}

		switch cmd.Action {
		case "start":
			log.Printf("OCR command: start (interval=%d)", cmd.Interval)
			c.Start(cmd.Interval)
		case "stop":
			log.Println("OCR command: stop")
			c.Stop()
		case "snapshot":
			log.Println("OCR command: snapshot")
			c.Snapshot()
		default:
			log.Printf("OCR unknown action %q, default to snapshot", cmd.Action)
			c.Snapshot()
		}
	})
	token.WaitTimeout(10 * time.Second)
	if token.Error() != nil {
		log.Printf("OCR subscribe error: %v", token.Error())
	} else {
		log.Printf("OCR subscribed to command topic: %s", c.cfg.CommandTopic)
	}
}

func RunOCRFromMCP(scriptPath string, confThreshold float64) (interface{}, error) {
	result, err := RunOCR(scriptPath, confThreshold)
	if err != nil {
		return nil, err
	}
	return result, nil
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
