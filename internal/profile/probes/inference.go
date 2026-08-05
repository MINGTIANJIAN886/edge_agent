package probes

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/user/agent/internal/config"
	"github.com/user/agent/internal/profile"
)

// InferenceProbe checks whether the local inference service is
// reachable. V1 only verifies HTTP reachability (no model load, no
// side effects); a real test inference may be added later behind a
// separate flag.
type InferenceProbe struct {
	ServiceURL string
	Timeout    int
}

func NewInferenceProbe(cfg config.Inference, pcfg config.InferenceProbeCfg) *InferenceProbe {
	return &InferenceProbe{ServiceURL: cfg.ServiceURL, Timeout: pcfg.TimeoutSeconds}
}

func (p *InferenceProbe) Name() string { return "inference" }

func (p *InferenceProbe) Probe(ctx context.Context, req profile.ProbeRequest) profile.CapabilityResult {
	start := time.Now()
	if p.ServiceURL == "" {
		return result(false, profile.CodeInferenceNotConfigured, "inference service_url not configured", time.Since(start), nil)
	}

	timeout := req.Timeout
	if timeout == 0 {
		timeout = 10 * time.Second
		if p.Timeout > 0 {
			timeout = time.Duration(p.Timeout) * time.Second
		}
	}
	client := &http.Client{Timeout: timeout}
	url := strings.TrimRight(p.ServiceURL, "/") + "/health"
	resp, err := client.Get(url)
	if err != nil {
		return result(false, profile.CodeInferenceUnreachable,
			fmt.Sprintf("inference service unreachable: %v", err), time.Since(start),
			map[string]interface{}{"url": url})
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 {
		return result(false, profile.CodeInferenceUnreachable,
			fmt.Sprintf("inference service returned HTTP %d", resp.StatusCode), time.Since(start),
			map[string]interface{}{"url": url, "status": resp.StatusCode})
	}
	return result(true, profile.CodeOK, "inference service reachable", time.Since(start),
		map[string]interface{}{"url": url, "status": resp.StatusCode})
}
