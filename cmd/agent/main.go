package main

import (
	"crypto/tls"
	"crypto/x509"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/user/agent/internal/config"
	"github.com/user/agent/internal/bridge"
	"github.com/user/agent/internal/download"
	"github.com/user/agent/internal/enroll"
	"github.com/user/agent/internal/heartbeat"
	"github.com/user/agent/internal/mcp"
	"github.com/user/agent/internal/ota"
	"github.com/user/agent/internal/remote"
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
	opts.SetOnConnectHandler(func(c mqtt.Client) {
		log.Println("MQTT connected")
		mcp.PublishTools(c, cfg.DeviceID, cfg.MQTT.Topic.MCPRegister)
		remote.SubscribeCommands(c, cfg.DeviceID, cfg.MQTT.Topic.Command)
		download.SubscribeDownloads(c, cfg.DeviceID, cfg.MQTT.Topic.Download, cfg.DownloadDir)
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

	bm := bridge.NewManager("/home/liyankun/Desktop/car_bridge.py")
	if err := bm.Start(); err != nil {
		log.Printf("car bridge: %v (skip)", err)
	} else {
		defer bm.Stop()
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigCh
	log.Printf("received signal %v, shutting down", sig)
	client.Disconnect(1000)
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
