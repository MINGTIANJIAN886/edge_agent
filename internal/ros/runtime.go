package ros

import (
	"sync"

	"github.com/MINGTIANJIAN886/edge_agent/internal/config"
)

var (
	runtimeMu      sync.RWMutex
	commandRuntime config.Runtime
)

func ConfigureRuntime(runtimeCfg config.Runtime) {
	runtimeMu.Lock()
	commandRuntime = runtimeCfg
	runtimeMu.Unlock()
}

func configuredRuntime() config.Runtime {
	runtimeMu.RLock()
	defer runtimeMu.RUnlock()
	return commandRuntime
}
