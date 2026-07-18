package ros

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

func NodeList(ver Version) ([]NodeInfo, error) {
	switch ver {
	case ROS1:
		out, err := runCLI("rosnode", "list")
		if err != nil {
			return nil, err
		}
		return toNodes(out), nil
	case ROS2:
		out, err := runCLI("ros2", "node", "list")
		if err != nil {
			return nil, err
		}
		return toNodes(out), nil
	}
	return nil, fmt.Errorf("no ROS detected")
}

func TopicList(ver Version) ([]TopicInfo, error) {
	switch ver {
	case ROS1:
		out, err := runCLI("rostopic", "list", "-v")
		if err != nil {
			return nil, err
		}
		return parseTopicsROS1(out), nil
	case ROS2:
		out, err := runCLI("ros2", "topic", "list", "-t")
		if err != nil {
			return nil, err
		}
		return parseTopicsROS2(out), nil
	}
	return nil, fmt.Errorf("no ROS detected")
}

func ServiceList(ver Version) ([]ServiceInfo, error) {
	switch ver {
	case ROS1:
		out, err := runCLI("rosservice", "list")
		if err != nil {
			return nil, err
		}
		return toServices(out), nil
	case ROS2:
		out, err := runCLI("ros2", "service", "list", "-t")
		if err != nil {
			return nil, err
		}
		return parseServicesROS2(out), nil
	}
	return nil, fmt.Errorf("no ROS detected")
}

func TopicEchoOnce(ver Version, topic string) (string, error) {
	switch ver {
	case ROS1:
		return runCLI("rostopic", "echo", topic, "-n", "1")
	case ROS2:
		return runCLI("ros2", "topic", "echo", topic, "--once")
	}
	return "", fmt.Errorf("no ROS detected")
}

func ServiceCall(ver Version, service, srvType, args string) (string, error) {
	switch ver {
	case ROS1:
		cliArgs := []string{"call", service}
		if args != "" {
			cliArgs = append(cliArgs, args)
		}
		return runCLI("rosservice", cliArgs...)
	case ROS2:
		cliArgs := []string{"service", "call", service, srvType}
		if args != "" {
			cliArgs = append(cliArgs, args)
		}
		return runCLI("ros2", cliArgs...)
	}
	return "", fmt.Errorf("no ROS detected")
}

func ParamGet(ver Version, name string) (string, error) {
	switch ver {
	case ROS1:
		return runCLI("rosparam", "get", name)
	case ROS2:
		out, err := runCLI("ros2", "param", "list")
		if err != nil {
			return "", err
		}
		for _, node := range strings.Fields(out) {
			if node == "" {
				continue
			}
			result, err := runCLI("ros2", "param", "get", node, name)
			if err == nil {
				return result, nil
			}
		}
		return "", fmt.Errorf("param %s not found on any node", name)
	}
	return "", fmt.Errorf("no ROS detected")
}

func ParamSet(ver Version, name, value string) error {
	switch ver {
	case ROS1:
		_, err := runCLI("rosparam", "set", name, value)
		return err
	case ROS2:
		out, err := runCLI("ros2", "param", "list")
		if err != nil {
			return err
		}
		for _, node := range strings.Fields(out) {
			if node == "" {
				continue
			}
			_, err := runCLI("ros2", "param", "set", node, name, value)
			if err == nil {
				return nil
			}
		}
		return fmt.Errorf("param %s not found on any node", name)
	}
	return fmt.Errorf("no ROS detected")
}

func runCLI(name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("%s timed out", name)
		}
		return "", fmt.Errorf("%s: %w\n%s", name, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

func toNodes(out string) []NodeInfo {
	var nodes []NodeInfo
	for _, l := range strings.Fields(out) {
		if l != "" {
			nodes = append(nodes, NodeInfo{Name: l})
		}
	}
	return nodes
}

func toServices(out string) []ServiceInfo {
	var svcs []ServiceInfo
	for _, l := range strings.Fields(out) {
		if l != "" {
			svcs = append(svcs, ServiceInfo{Name: l})
		}
	}
	return svcs
}

func parseTopicsROS1(out string) []TopicInfo {
	var topics []TopicInfo
	published := false
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		if strings.Contains(l, "Published topics:") {
			published = true
			continue
		}
		if strings.Contains(l, "Subscribed topics:") {
			break
		}
		if !published || l == "" {
			continue
		}
		parts := strings.Fields(l)
		if len(parts) >= 2 {
			topics = append(topics, TopicInfo{
				Type: parts[len(parts)-2],
				Name: parts[len(parts)-1],
			})
		}
	}
	return topics
}

func parseTopicsROS2(out string) []TopicInfo {
	var topics []TopicInfo
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || !strings.Contains(l, "[") {
			continue
		}
		parts := strings.Fields(l)
		if len(parts) >= 2 {
			topics = append(topics, TopicInfo{
				Name: parts[0],
				Type: strings.Trim(parts[1], "[]"),
			})
		}
	}
	return topics
}

func parseServicesROS2(out string) []ServiceInfo {
	var svcs []ServiceInfo
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || !strings.Contains(l, "[") {
			continue
		}
		parts := strings.Fields(l)
		if len(parts) >= 2 {
			svcs = append(svcs, ServiceInfo{
				Name: parts[0],
				Type: strings.Trim(parts[1], "[]"),
			})
		}
	}
	return svcs
}
