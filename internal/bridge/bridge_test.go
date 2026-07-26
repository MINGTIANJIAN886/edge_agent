package bridge

import (
	"encoding/json"
	"testing"

	"github.com/MINGTIANJIAN886/edge_agent/internal/ros"
)

func TestBuildTwistInputClampsSpeed(t *testing.T) {
	input, err := BuildTwistInput(ros.ROS2, "/robot/cmd_vel", 4.5, -5, 2, 3.14)
	if err != nil {
		t.Fatal(err)
	}
	if input.Topic != "/robot/cmd_vel" {
		t.Fatalf("topic = %q", input.Topic)
	}
	if input.MsgType != "geometry_msgs/msg/Twist" {
		t.Fatalf("message type = %q", input.MsgType)
	}

	var payload struct {
		Linear  map[string]float64 `json:"linear"`
		Angular map[string]float64 `json:"angular"`
	}
	if err := json.Unmarshal(input.Data, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Linear["x"] != 2 {
		t.Fatalf("linear.x = %v, want 2", payload.Linear["x"])
	}
	if payload.Angular["z"] != -3.14 {
		t.Fatalf("angular.z = %v, want -3.14", payload.Angular["z"])
	}
}

func TestBuildTwistInputRejectsMissingROS(t *testing.T) {
	if _, err := BuildTwistInput(ros.None, "/cmd_vel", 0, 0, 1, 1); err == nil {
		t.Fatal("expected unsupported ROS version error")
	}
}
