#!/usr/bin/env bash
set -eo pipefail

BRIDGE_ROS1="${BRIDGE_ROS1:-/opt/agent/bridge_ros1.py}"
BRIDGE_ROS2="${BRIDGE_ROS2:-/opt/agent/bridge_ros2.py}"

if [ -n "${ROS_DISTRO:-}" ] && [ -f "/opt/ros/${ROS_DISTRO}/setup.bash" ]; then
  ROS_SETUP="/opt/ros/${ROS_DISTRO}/setup.bash"
else
  ROS_SETUP=""
  for candidate in /opt/ros/*/setup.bash; do
    if [ -f "${candidate}" ]; then
      ROS_SETUP="${candidate}"
    fi
  done
fi

if [ -z "${ROS_SETUP}" ]; then
  echo "No ROS installation found under /opt/ros" >&2
  exit 1
fi

# ROS setup scripts are not guaranteed to support nounset.
# shellcheck disable=SC1090
source "${ROS_SETUP}"

case "${ROS_DISTRO:-}" in
  kinetic|melodic|noetic)
    exec python3 "${BRIDGE_ROS1}"
    ;;
  foxy|galactic|humble|iron|jazzy|rolling)
    exec python3 "${BRIDGE_ROS2}"
    ;;
esac

if command -v ros2 >/dev/null 2>&1; then
  exec python3 "${BRIDGE_ROS2}"
fi
if command -v rostopic >/dev/null 2>&1; then
  exec python3 "${BRIDGE_ROS1}"
fi

echo "Unable to determine whether this is ROS1 or ROS2" >&2
exit 1
