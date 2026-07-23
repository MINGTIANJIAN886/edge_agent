package ros

import (
	"log"
	"os"
	"os/exec"
)

func Detect() Version {
	distro := os.Getenv("ROS_DISTRO")
	switch distro {
	case "noetic", "melodic", "kinetic", "indigo":
		log.Printf("ROS detection: ROS1 (distro=%s)", distro)
		return ROS1
	case "humble", "jazzy", "foxy", "galactic", "rolling":
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

	if entries, err := os.ReadDir("/opt/ros"); err == nil && len(entries) > 0 {
		log.Printf("ROS detection: found /opt/ros (%d distro(s)), assuming ROS2", len(entries))
		return ROS2
	}

	log.Printf("ROS detection: none")
	return None
}
