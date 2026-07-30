package ros

import (
	"log"
	"os"
	"os/exec"
	"strings"
)

func Detect() Version {
	distro := os.Getenv("ROS_DISTRO")
	switch distro {
	case "noetic", "melodic", "kinetic", "indigo", "lunar":
		log.Printf("ROS detection: ROS1 (distro=%s)", distro)
		return ROS1
	case "humble", "jazzy", "foxy", "galactic", "rolling", "iron", "eloquent", "dashing":
		log.Printf("ROS detection: ROS2 (distro=%s)", distro)
		return ROS2
	}

	if _, err := exec.LookPath("ros2"); err == nil {
		log.Printf("ROS detection: ROS2 (found ros2 CLI)")
		return ROS2
	}
	if _, err := exec.LookPath("rostopic"); err == nil {
		log.Printf("ROS detection: ROS1 (found rostopic CLI)")
		return ROS1
	}

	if entries, err := os.ReadDir("/opt/ros"); err == nil {
		switch len(entries) {
		case 0:
			// nothing found
		case 1:
			name := entries[0].Name()
			switch name {
			case "noetic", "melodic", "kinetic", "indigo", "lunar":
				log.Printf("ROS detection: ROS1 (distro=%s)", name)
				return ROS1
			case "humble", "jazzy", "foxy", "galactic", "rolling", "iron", "eloquent", "dashing":
				log.Printf("ROS detection: ROS2 (distro=%s)", name)
				return ROS2
			default:
				// Unknown distro: check setup.bash for hint
				setupPath := "/opt/ros/" + name + "/setup.bash"
				if data, err := os.ReadFile(setupPath); err == nil && strings.Contains(string(data), "ROS_VERSION=2") {
					log.Printf("ROS detection: ROS2 (distro=%s, via setup.bash)", name)
					return ROS2
				}
				log.Printf("ROS detection: ROS1 (distro=%s, default fallback)", name)
				return ROS1
			}
		default:
			names := make([]string, 0, len(entries))
			for _, e := range entries {
				names = append(names, e.Name())
			}
			log.Printf("ROS detection: multiple distros %v, set ROS_VERSION in config", names)
			return None
		}
	}

	log.Printf("ROS detection: none")
	return None
}
