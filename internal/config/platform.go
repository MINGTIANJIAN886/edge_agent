package config

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	ProfileAuto         = "auto"
	ProfileGenericLinux = "generic-linux"
	ProfileRaspberryPi  = "raspberry-pi"
	ProfileJetson       = "jetson"
	ProfileJetsonR32    = "jetson-r32"
	ProfileJetsonR35    = "jetson-r35"
	ProfileJetsonR36    = "jetson-r36"
)

var l4tReleasePattern = regexp.MustCompile(`(?m)^# R([0-9]+)`)

func (c *Config) resolveRuntime() {
	if c.DeviceProfile == "" || c.DeviceProfile == ProfileAuto {
		c.DeviceProfile = DetectDeviceProfile()
	}

	switch strings.ToLower(strings.TrimSpace(c.Runtime.ROSSetup)) {
	case "", ProfileAuto:
		c.Runtime.ROSSetup = DetectROSSetup(c.DeviceProfile)
	case "none", "disabled":
		c.Runtime.ROSSetup = ""
	}
}

func DetectDeviceProfile() string {
	tegraRelease, _ := os.ReadFile("/etc/nv_tegra_release")
	model, _ := os.ReadFile("/proc/device-tree/model")
	return detectDeviceProfile(string(tegraRelease), string(model))
}

func detectDeviceProfile(tegraRelease, model string) string {
	if tegraRelease != "" {
		match := l4tReleasePattern.FindStringSubmatch(tegraRelease)
		if len(match) == 2 {
			switch match[1] {
			case "32":
				return ProfileJetsonR32
			case "35":
				return ProfileJetsonR35
			case "36":
				return ProfileJetsonR36
			}
		}
		return ProfileJetson
	}

	model = strings.ToLower(strings.TrimSpace(strings.TrimRight(model, "\x00")))
	if strings.Contains(model, "raspberry pi") {
		return ProfileRaspberryPi
	}
	if strings.Contains(model, "jetson") || strings.Contains(model, "nvidia") {
		return ProfileJetson
	}
	return ProfileGenericLinux
}

func DetectROSSetup(profile string) string {
	if distro := strings.TrimSpace(os.Getenv("ROS_DISTRO")); distro != "" {
		path := filepath.Join("/opt/ros", distro, "setup.bash")
		if fileExists(path) {
			return path
		}
	}

	preferred := []string{"humble", "jazzy", "iron", "foxy", "noetic", "melodic", "kinetic", "rolling"}
	if profile == ProfileJetsonR32 {
		preferred = []string{"noetic", "melodic", "kinetic", "humble", "foxy"}
	}
	for _, distro := range preferred {
		path := filepath.Join("/opt/ros", distro, "setup.bash")
		if fileExists(path) {
			return path
		}
	}

	matches, _ := filepath.Glob("/opt/ros/*/setup.bash")
	sort.Strings(matches)
	if len(matches) > 0 {
		return matches[len(matches)-1]
	}
	return ""
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
