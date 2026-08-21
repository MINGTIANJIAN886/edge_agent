// Command switch 从 Manifest V1 选择并激活模型（switch_model 的本地 CLI 形态）。
//
// 用法:
//   edge-agent-switch -config /etc/agent/config.yaml -model yolo11n
//   edge-agent-switch -config /etc/agent/config.yaml -task object_detection -require_accuracy 0.3
package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/user/agent/internal/config"
	"github.com/user/agent/internal/ota"
)

func main() {
	cfgPath := flag.String("config", "/etc/agent/config.yaml", "path to config")
	modelID := flag.String("model", "", "exact model_id from the V1 manifest")
	task := flag.String("task", "", "task to match (e.g. object_detection)")
	acc := flag.Float64("require_accuracy", 0, "minimum accuracy (0-1)")
	lat := flag.Float64("max_latency", 0, "maximum latency ms")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	msg, err := ota.SwitchModelV1(cfg.OTA, nil, cfg.DeviceID, "",
		*modelID, *task, *acc, *lat)
	if err != nil {
		fmt.Fprintln(os.Stderr, "switch failed:", err)
		os.Exit(1)
	}
	fmt.Println(msg)
}
