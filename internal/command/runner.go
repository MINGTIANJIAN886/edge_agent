package command

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"time"

	"github.com/MINGTIANJIAN886/edge_agent/internal/config"
)

type Result struct {
	Success  bool
	Stdout   string
	Stderr   string
	ExitCode int
	Duration time.Duration
}

const environmentPreamble = `
if [ -n "${EDGE_AGENT_ROS_SETUP:-}" ] && [ -f "${EDGE_AGENT_ROS_SETUP}" ]; then
  source "${EDGE_AGENT_ROS_SETUP}"
fi
if [ -n "${EDGE_AGENT_WORKSPACE_SETUP:-}" ] && [ -f "${EDGE_AGENT_WORKSPACE_SETUP}" ]; then
  source "${EDGE_AGENT_WORKSPACE_SETUP}"
fi
`

func Execute(commandText string, timeoutSeconds int, runtimeCfg config.Runtime) Result {
	start := time.Now()
	ctx, cancel, cmd := shellCommand(timeoutSeconds, runtimeCfg, environmentPreamble+"\n"+commandText)
	defer cancel()
	return run(ctx, cmd, start)
}

func ExecuteArgs(name string, args []string, timeoutSeconds int, runtimeCfg config.Runtime) Result {
	start := time.Now()
	script := environmentPreamble + "\nexec \"$@\""
	shellArgs := append([]string{"-lc", script, "edge-agent", name}, args...)
	ctx, cancel, cmd := shellCommand(timeoutSeconds, runtimeCfg, "", shellArgs...)
	defer cancel()
	return run(ctx, cmd, start)
}

func shellCommand(timeoutSeconds int, runtimeCfg config.Runtime, script string, args ...string) (context.Context, context.CancelFunc, *exec.Cmd) {
	if timeoutSeconds <= 0 {
		timeoutSeconds = 30
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSeconds)*time.Second)
	shell := runtimeCfg.CommandShell
	if shell == "" {
		shell = "/bin/bash"
	}
	if len(args) == 0 {
		args = []string{"-lc", script}
	}
	cmd := exec.CommandContext(ctx, shell, args...)
	cmd.Env = append(os.Environ(),
		"EDGE_AGENT_ROS_SETUP="+runtimeCfg.ROSSetup,
		"EDGE_AGENT_WORKSPACE_SETUP="+runtimeCfg.WorkspaceSetup,
	)
	return ctx, cancel, cmd
}

func run(ctx context.Context, cmd *exec.Cmd, start time.Time) Result {
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()

	result := Result{
		Success:  err == nil,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: 0,
		Duration: time.Since(start).Round(time.Millisecond),
	}
	if err == nil {
		return result
	}

	result.ExitCode = -1
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		result.Stderr += "command timed out\n"
	} else if result.Stderr == "" {
		result.Stderr = err.Error()
	}
	return result
}
