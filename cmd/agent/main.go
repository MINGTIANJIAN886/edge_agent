package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/user/agent/internal/bridge"
	"github.com/user/agent/internal/config"
	"github.com/user/agent/internal/download"
	"github.com/user/agent/internal/enroll"
	"github.com/user/agent/internal/heartbeat"
	"github.com/user/agent/internal/mcp"
	"github.com/user/agent/internal/ocr"
	"github.com/user/agent/internal/ota"
	"github.com/user/agent/internal/remote"
	"github.com/user/agent/internal/ros"
)

func main() {
	cfgPath := flag.String("config", "/etc/edge-agent/config.yaml", "path to config file")
	enrollFlag := flag.Bool("enroll", false, "run certificate enrollment and exit")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)
	log.Printf("agent starting, device_id=%s", cfg.DeviceID)

	certDir := filepath.Dir(cfg.Cert.CertFile)
	if certDir == "." {
		certDir = "/etc/edge-agent/certs"
	}

	// Certificate auto-enrollment
	if cfg.Cert.AutoEnroll || *enrollFlag {
		token := cfg.Cert.Token
		if token == "" {
			token = cfg.Auth.Token
		}
		if err := enroll.AutoEnroll(cfg.CertAPI, cfg.DeviceID, token, certDir, ""); err != nil {
			log.Printf("certificate enrollment failed: %v", err)
			if *enrollFlag {
				os.Exit(1)
			}
		}
		if *enrollFlag {
			log.Println("enrollment complete")
			os.Exit(0)
		}
	}

	// Build TLS config
	tlsConfig, err := buildTLSConfig(cfg)
	if err != nil {
		log.Fatalf("TLS config error: %v", err)
	}

	// Determine auth method
	username := cfg.MQTT.Username
	password := cfg.MQTT.Password

	switch cfg.Auth.Method {
	case "token":
		username = "token-" + cfg.Auth.Token
		password = ""
	case "cert":
		if username == "" {
			username = cfg.DeviceID
		}
	case "password":
		// use configured username/password
	case "admin":
		username = "admin"
		password = cfg.MQTT.Password
	}

	opts := mqtt.NewClientOptions()
	scheme := "tcp"
	if tlsConfig != nil {
		scheme = "ssl"
	}
	opts.AddBroker(fmt.Sprintf("%s://%s:%d", scheme, cfg.MQTT.Broker, cfg.MQTT.Port))
	opts.SetClientID(cfg.MQTT.ClientID)
	opts.SetUsername(username)
	opts.SetPassword(password)
	opts.SetKeepAlive(20 * time.Second)
	opts.SetPingTimeout(5 * time.Second)
	opts.SetAutoReconnect(true)
	opts.SetMaxReconnectInterval(5 * time.Second)
	opts.SetConnectionLostHandler(func(c mqtt.Client, err error) {
		log.Printf("MQTT connection lost: %v", err)
	})
	opts.SetReconnectingHandler(func(_ mqtt.Client, _ *mqtt.ClientOptions) {
		log.Println("MQTT reconnecting...")
	})

	rosVer := resolveROSVersion(cfg)
	log.Printf("ROS version: %s (config=%d, distro=%s)", rosVer, cfg.ROS.Version, cfg.ROS.Distro)

	// Declare long-lived state before setting OnConnectHandler
	// (closures capture by reference, initialized before Connect)
	var (
		bridgeMgr        *bridge.Manager
		ocrCtrl          *ocr.Controller
		carWatchdogCancel context.CancelFunc
	)

	opts.SetOnConnectHandler(func(c mqtt.Client) {
		log.Println("MQTT connected")
		mcp.PublishTools(c, cfg.DeviceID, cfg.MQTT.Topic.MCPRegister, rosVer)
		remote.SubscribeCommands(c, cfg.DeviceID, cfg.MQTT.Topic.Command)
		download.SubscribeDownloads(c, cfg.DeviceID, cfg.MQTT.Topic.Download, cfg.DownloadDir)

		// OCR: only re-subscribe, timer goroutine was started once
		if ocrCtrl != nil {
			ocrCtrl.SubscribeCommands()
		}

		mcp.SubscribeCalls(c, cfg.DeviceID, cfg.MQTT.Topic.MCPCall, cfg.Inference.ServiceURL, cfg)

		// Car bridge: cancel old watchdog, subscribe with new context
		if bridgeMgr != nil {
			if carWatchdogCancel != nil {
				carWatchdogCancel()
			}
			var ctx context.Context
			ctx, carWatchdogCancel = context.WithCancel(context.Background())
			subscribeBridgeCommands(ctx, c, cfg.DeviceID, cfg.MQTT.Topic, bridgeMgr, rosVer, cfg.ROS)
		}
	})

	if tlsConfig != nil {
		opts.SetTLSConfig(tlsConfig)
	}

	client := mqtt.NewClient(opts)

	// Initialize once — runs before first OnConnectHandler fires
	if cfg.ROS.Enabled && rosVer != ros.None {
		bridgeMgr = bridge.New()
		log.Println("ROS bridge commands active (car_bridge.service must be running)")
	}

	if cfg.OCR.Enabled {
		ocrCtrl = ocr.NewController(client, cfg.OCR, cfg.DeviceID)
		if cfg.OCR.Interval > 0 {
			ocrCtrl.Start(0)
		}
	}

	token := client.Connect()
	token.WaitTimeout(15 * time.Second)
	if token.Error() != nil {
		log.Printf("MQTT connection failed: %v", token.Error())
	} else {
		log.Println("MQTT connected successfully")
	}

	hostname, _ := os.Hostname()

	if err := mcp.Register(cfg.CertAPI, cfg.DeviceID, hostname); err != nil {
		log.Printf("MCP registration warning: %v", err)
	}

	ota.InitRollbackState(cfg.OTA)
	go heartbeat.Start(client, cfg.DeviceID, cfg.Heartbeat, cfg.MQTT.Topic.Heartbeat)
	go ota.StartPeriodicCheck(cfg.OTA, client, cfg.DeviceID, cfg.MQTT.Topic.Result)
	go mqttWatchdog(client)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigCh
	log.Printf("received signal %v, shutting down", sig)

	client.Disconnect(1000)
}

func resolveROSVersion(cfg *config.Config) ros.Version {
	if cfg.ROS.Version == 1 {
		return ros.ROS1
	}
	if cfg.ROS.Version == 2 {
		return ros.ROS2
	}
	return ros.Detect()
}

func subscribeBridgeCommands(ctx context.Context, client mqtt.Client, deviceID string, topics config.Topic, mgr *bridge.Manager, ver ros.Version, rosCfg config.ROSConfig) {
	mqttCmdVelTopic := rosCfg.CmdVelTopic
	if mqttCmdVelTopic == "" {
		mqttCmdVelTopic = strings.Replace(topics.Command, "/command", "/car/cmd_vel", 1)
	}
	rosCmdVelTopic := rosCfg.RosCmdVelTopic
	if rosCmdVelTopic == "" {
		rosCmdVelTopic = "/cmd_vel"
	}
	emergencyTopic := strings.Replace(topics.Command, "/command", "/car/emergency_stop", 1)
	resultTopic := rosCfg.BridgeResultTopic
	if resultTopic == "" {
		resultTopic = strings.Replace(topics.Command, "/command", "/bridge/result", 1)
	}
	maxLinear := rosCfg.MaxLinearSpeed
	maxAngular := rosCfg.MaxAngularSpeed
	watchdogTimeout := rosCfg.SafetyWatchdog

	msgType := rosCfg.CmdVelMessageType
	if msgType == "" {
		msgType = map[ros.Version]string{
			ros.ROS1: "geometry_msgs/Twist",
			ros.ROS2: "geometry_msgs/msg/Twist",
		}[ver]
	}

	var lastCmdMu sync.Mutex
	lastCmd := time.Now()

	if token := client.Subscribe(mqttCmdVelTopic, 1, func(_ mqtt.Client, msg mqtt.Message) {
		var req struct {
			LinearX  float64 `json:"linear_x"`
			AngularZ float64 `json:"angular_z"`
		}
		if err := json.Unmarshal(msg.Payload(), &req); err != nil {
			log.Printf("cmd_vel parse error: %v", err)
			return
		}
		if maxLinear > 0 {
			if req.LinearX > maxLinear {
				req.LinearX = maxLinear
			}
			if req.LinearX < -maxLinear {
				req.LinearX = -maxLinear
			}
		}
		if maxAngular > 0 {
			if req.AngularZ > maxAngular {
				req.AngularZ = maxAngular
			}
			if req.AngularZ < -maxAngular {
				req.AngularZ = -maxAngular
			}
		}
		data, _ := json.Marshal(map[string]interface{}{
			"linear":  map[string]float64{"x": req.LinearX, "y": 0, "z": 0},
			"angular": map[string]float64{"x": 0, "y": 0, "z": req.AngularZ},
		})
		if err := mgr.Send(ros.BridgeInput{
			Cmd:     "publish",
			Topic:   rosCmdVelTopic,
			MsgType: msgType,
			Data:    data,
		}); err != nil {
			log.Printf("send cmd_vel failed: %v", err)
			return
		}
		lastCmdMu.Lock()
		lastCmd = time.Now()
		lastCmdMu.Unlock()

		if resultTopic != "" {
			resultData, _ := json.Marshal(map[string]interface{}{
				"linear_x":  req.LinearX,
				"angular_z": req.AngularZ,
				"status":    "ok",
			})
			client.Publish(resultTopic, 1, false, resultData)
		}
	}); token.WaitTimeout(5*time.Second) && token.Error() != nil {
		log.Printf("subscribe cmd_vel error: %v", token.Error())
	}

	if token := client.Subscribe(emergencyTopic, 1, func(_ mqtt.Client, msg mqtt.Message) {
		data, _ := json.Marshal(map[string]interface{}{
			"linear":  map[string]float64{"x": 0, "y": 0, "z": 0},
			"angular": map[string]float64{"x": 0, "y": 0, "z": 0},
		})
		mgr.Send(ros.BridgeInput{Cmd: "publish", Topic: rosCmdVelTopic, MsgType: msgType, Data: data})
		lastCmdMu.Lock()
		lastCmd = time.Now()
		lastCmdMu.Unlock()
		if resultTopic != "" {
			resultData, _ := json.Marshal(map[string]interface{}{
				"status": "emergency_stop",
			})
			client.Publish(resultTopic, 1, false, resultData)
		}
	}); token.WaitTimeout(5*time.Second) && token.Error() != nil {
		log.Printf("subscribe emergency_stop error: %v", token.Error())
	}

	// Safety watchdog: publish zero velocity if no cmd_vel received within timeout
	if watchdogTimeout > 0 {
		go func() {
			ticker := time.NewTicker(time.Duration(watchdogTimeout) * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					log.Println("car watchdog cancelled (reconnect)")
					return
				case <-ticker.C:
					lastCmdMu.Lock()
					elapsed := time.Since(lastCmd)
					lastCmdMu.Unlock()
					if elapsed >= time.Duration(watchdogTimeout)*time.Second {
						log.Printf("safety watchdog: no cmd_vel for %ds, stopping", watchdogTimeout)
						data, _ := json.Marshal(map[string]interface{}{
							"linear":  map[string]float64{"x": 0, "y": 0, "z": 0},
							"angular": map[string]float64{"x": 0, "y": 0, "z": 0},
						})
						mgr.Send(ros.BridgeInput{
							Cmd:     "publish",
							Topic:   rosCmdVelTopic,
							MsgType: msgType,
							Data:    data,
						})
					}
				}
			}
		}()
	}
}

func mqttWatchdog(client mqtt.Client) {
	const checkInterval = 10 * time.Second
	const maxDisconnected = 45 * time.Second

	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()

	disconnectedSince := time.Time{}

	for range ticker.C {
		if client.IsConnected() {
			disconnectedSince = time.Time{}
			continue
		}
		if disconnectedSince.IsZero() {
			disconnectedSince = time.Now()
			log.Println("watchdog: detected MQTT disconnect")
		} else if time.Since(disconnectedSince) > maxDisconnected {
			log.Fatalf("watchdog: MQTT disconnected for %v, restarting", maxDisconnected)
		}
	}
}

func buildTLSConfig(cfg *config.Config) (*tls.Config, error) {
	certFile := cfg.Cert.CertFile
	keyFile := cfg.Cert.KeyFile
	caFile := cfg.Cert.CAFile

	if certFile == "" && keyFile == "" && caFile == "" {
		return nil, nil
	}

	tlsCfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
	}

	// Load client certificate for mutual TLS
	if certFile != "" && keyFile != "" {
		cert, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return nil, fmt.Errorf("failed to load client cert: %w", err)
		}
		tlsCfg.Certificates = []tls.Certificate{cert}
	}

	// Load CA certificate for server verification
	if caFile != "" {
		caData, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("failed to load CA cert: %w", err)
		}
		caPool := x509.NewCertPool()
		if !caPool.AppendCertsFromPEM(caData) {
			return nil, fmt.Errorf("failed to parse CA cert")
		}
		tlsCfg.RootCAs = caPool
	}

	log.Printf("TLS configured: cert=%s, key=%s, ca=%s", certFile, keyFile, caFile)
	return tlsCfg, nil
}
