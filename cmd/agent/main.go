package main

import (
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
	cfgPath := flag.String("config", "/etc/agent/config.yaml", "path to config file")
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
		certDir = "/etc/agent/certs"
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
		// TLS client cert handles auth; username can be device ID
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
	rosVer := ros.Detect()
	log.Printf("ROS version: %s", rosVer)

	opts.SetOnConnectHandler(func(c mqtt.Client) {
		log.Println("MQTT connected")
		mcp.PublishTools(c, cfg.DeviceID, cfg.MQTT.Topic.MCPRegister, rosVer)
		remote.SubscribeCommands(c, cfg.DeviceID, cfg.MQTT.Topic.Command)
		download.SubscribeDownloads(c, cfg.DeviceID, cfg.MQTT.Topic.Download, cfg.DownloadDir)
		if cfg.OCR.Enabled {
			ctrl := ocr.NewController(c, cfg.OCR, cfg.DeviceID)
			ctrl.SubscribeCommands()
			if cfg.OCR.Interval > 0 {
				ctrl.Start(0)
			}
		}
		mcp.SubscribeCalls(c, cfg.DeviceID, cfg.MQTT.Topic.MCPCall, cfg.Inference.ServiceURL, cfg)
	})
	if tlsConfig != nil {
		opts.SetTLSConfig(tlsConfig)
	}

	client := mqtt.NewClient(opts)
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

	var bridgeMgr *bridge.Manager
	if cfg.ROS.Enabled && rosVer != ros.None {
		bridgeCfg := bridge.Config{
			Enabled:         cfg.ROS.Enabled,
			ScriptROS1:      cfg.ROS.BridgeScript1,
			ScriptROS2:      cfg.ROS.BridgeScript2,
			PythonBin:       cfg.ROS.PythonBin,
			MaxLinearSpeed:  cfg.ROS.MaxLinearSpeed,
			MaxAngularSpeed: cfg.ROS.MaxAngularSpeed,
			WatchdogTimeout: cfg.ROS.SafetyWatchdog,
			CmdVelTopic:     cfg.ROS.CmdVelTopic,
			ResultTopic:     cfg.ROS.BridgeResultTopic,
		}
		bridgeMgr = bridge.New(rosVer, bridgeCfg, cfg.DeviceID, client)
		subscribeBridgeCommands(client, cfg.DeviceID, cfg.MQTT.Topic, bridgeMgr, rosVer)
		log.Println("ROS bridge manager initialized")
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigCh
	log.Printf("received signal %v, shutting down", sig)

	if bridgeMgr != nil {
		bridgeMgr.Stop()
	}
	client.Disconnect(1000)
}

func subscribeBridgeCommands(client mqtt.Client, deviceID string, topics config.Topic, mgr *bridge.Manager, ver ros.Version) {
	cmdVelTopic := strings.Replace(topics.Command, "/command", "/car/cmd_vel", 1)
	svcCallTopic := strings.Replace(topics.Command, "/command", "/car/service_call", 1)
	paramTopic := strings.Replace(topics.Command, "/command", "/car/param", 1)
	emergencyTopic := strings.Replace(topics.Command, "/command", "/car/emergency_stop", 1)
	ctrlTopic := strings.Replace(topics.Command, "/command", "/bridge/control", 1)

	msgType := map[ros.Version]string{
		ros.ROS1: "geometry_msgs/Twist",
		ros.ROS2: "geometry_msgs/msg/Twist",
	}[ver]

	if token := client.Subscribe(cmdVelTopic, 1, func(_ mqtt.Client, msg mqtt.Message) {
		var req struct {
			LinearX  float64 `json:"linear_x"`
			AngularZ float64 `json:"angular_z"`
		}
		if err := json.Unmarshal(msg.Payload(), &req); err != nil {
			log.Printf("cmd_vel parse error: %v", err)
			return
		}
		data, _ := json.Marshal(map[string]interface{}{
			"linear":  map[string]float64{"x": req.LinearX, "y": 0, "z": 0},
			"angular": map[string]float64{"x": 0, "y": 0, "z": req.AngularZ},
		})
		mgr.Send(ros.BridgeInput{
			Cmd:     "publish",
			Topic:   "/cmd_vel",
			MsgType: msgType,
			Data:    data,
		})
	}); token.WaitTimeout(5*time.Second) && token.Error() != nil {
		log.Printf("subscribe cmd_vel error: %v", token.Error())
	}

	if token := client.Subscribe(svcCallTopic, 1, func(_ mqtt.Client, msg mqtt.Message) {
		var req ros.BridgeInput
		if err := json.Unmarshal(msg.Payload(), &req); err != nil {
			log.Printf("service_call parse error: %v", err)
			return
		}
		mgr.Send(req)
	}); token.WaitTimeout(5*time.Second) && token.Error() != nil {
		log.Printf("subscribe service_call error: %v", token.Error())
	}

	if token := client.Subscribe(paramTopic, 1, func(_ mqtt.Client, msg mqtt.Message) {
		var req ros.BridgeInput
		if err := json.Unmarshal(msg.Payload(), &req); err != nil {
			log.Printf("param parse error: %v", err)
			return
		}
		mgr.Send(req)
	}); token.WaitTimeout(5*time.Second) && token.Error() != nil {
		log.Printf("subscribe param error: %v", token.Error())
	}

	if token := client.Subscribe(emergencyTopic, 1, func(_ mqtt.Client, msg mqtt.Message) {
		data, _ := json.Marshal(map[string]interface{}{
			"linear":  map[string]float64{"x": 0, "y": 0, "z": 0},
			"angular": map[string]float64{"x": 0, "y": 0, "z": 0},
		})
		mgr.Send(ros.BridgeInput{Cmd: "publish", Topic: "/cmd_vel", MsgType: msgType, Data: data})
	}); token.WaitTimeout(5*time.Second) && token.Error() != nil {
		log.Printf("subscribe emergency_stop error: %v", token.Error())
	}

	if token := client.Subscribe(ctrlTopic, 1, func(_ mqtt.Client, msg mqtt.Message) {
		var ctrl struct{ Cmd string `json:"cmd"` }
		if err := json.Unmarshal(msg.Payload(), &ctrl); err != nil {
			log.Printf("bridge control parse error: %v", err)
			return
		}
		switch ctrl.Cmd {
		case "start":
			if err := mgr.Start(); err != nil {
				log.Printf("bridge start error: %v", err)
			}
		case "stop":
			mgr.Stop()
		case "restart":
			mgr.Stop()
			time.Sleep(time.Second)
			if err := mgr.Start(); err != nil {
				log.Printf("bridge restart error: %v", err)
			}
		}
	}); token.WaitTimeout(5*time.Second) && token.Error() != nil {
		log.Printf("subscribe bridge control error: %v", token.Error())
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
