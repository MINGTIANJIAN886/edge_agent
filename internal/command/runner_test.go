package command

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MINGTIANJIAN886/edge_agent/internal/config"
)

func TestExecuteLoadsROSAndWorkspaceEnvironment(t *testing.T) {
	dir := t.TempDir()
	rosSetup := filepath.Join(dir, "ros.bash")
	workspaceSetup := filepath.Join(dir, "workspace.bash")
	if err := os.WriteFile(rosSetup, []byte("export EDGE_TEST_ROS=ros\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(workspaceSetup, []byte("export EDGE_TEST_WS=workspace\n"), 0600); err != nil {
		t.Fatal(err)
	}

	result := Execute(
		`printf '%s/%s' "$EDGE_TEST_ROS" "$EDGE_TEST_WS"`,
		5,
		config.Runtime{
			CommandShell:   "/bin/bash",
			ROSSetup:       rosSetup,
			WorkspaceSetup: workspaceSetup,
		},
	)
	if !result.Success {
		t.Fatalf("command failed: %s", result.Stderr)
	}
	if result.Stdout != "ros/workspace" {
		t.Fatalf("stdout = %q", result.Stdout)
	}
}

func TestExecutePreservesArbitraryShellCommands(t *testing.T) {
	result := Execute(`value=4; printf '%d' "$((value * 2))"`, 5, config.Runtime{CommandShell: "/bin/bash"})
	if !result.Success {
		t.Fatalf("command failed: %s", result.Stderr)
	}
	if result.Stdout != "8" {
		t.Fatalf("stdout = %q", result.Stdout)
	}
}

func TestExecuteArgsPreservesArguments(t *testing.T) {
	result := ExecuteArgs(
		"printf",
		[]string{"%s", "topic name; not shell code"},
		5,
		config.Runtime{CommandShell: "/bin/bash"},
	)
	if !result.Success {
		t.Fatalf("command failed: %s", result.Stderr)
	}
	if result.Stdout != "topic name; not shell code" {
		t.Fatalf("stdout = %q", result.Stdout)
	}
}
