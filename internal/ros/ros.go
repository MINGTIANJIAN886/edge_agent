package ros

import "encoding/json"

type Version int

const (
	None Version = iota
	ROS1
	ROS2
)

func (v Version) String() string {
	switch v {
	case ROS1:
		return "ros1"
	case ROS2:
		return "ros2"
	default:
		return "none"
	}
}

type NodeInfo struct {
	Name string `json:"name"`
}

type TopicInfo struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type ServiceInfo struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type ParamItem struct {
	Name  string          `json:"name"`
	Value json.RawMessage `json:"value,omitempty"`
}

type TwistCmd struct {
	LinearX  float64 `json:"linear_x"`
	LinearY  float64 `json:"linear_y"`
	LinearZ  float64 `json:"linear_z"`
	AngularX float64 `json:"angular_x"`
	AngularY float64 `json:"angular_y"`
	AngularZ float64 `json:"angular_z"`
}

type BridgeOutput struct {
	Type    string          `json:"type"`
	Topic   string          `json:"topic,omitempty"`
	Success bool            `json:"success,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
	Error   string          `json:"error,omitempty"`
}

type BridgeInput struct {
	Cmd     string          `json:"cmd"`
	Topic   string          `json:"topic,omitempty"`
	MsgType string          `json:"msg_type,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
	Service string          `json:"service,omitempty"`
	Name    string          `json:"name,omitempty"`
	Value   json.RawMessage `json:"value,omitempty"`
}
