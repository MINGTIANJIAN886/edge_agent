#!/bin/bash
set -euo pipefail

TMPDIR=$(mktemp -d)
trap "rm -rf $TMPDIR" EXIT

ARCH=$(uname -m)
case "$ARCH" in
    x86_64|amd64)  BIN="agent-amd64" ;;
    aarch64|arm64) BIN="agent-aarch64" ;;
    armv7l|armhf)  BIN="agent-armv7l" ;;
    *) echo "Unsupported arch: $ARCH"; exit 1 ;;
esac

cd "$TMPDIR"
curl -fsSL -o agent.tar.gz "https://github.com/MINGTIANJIAN886/edge_agent/raw/main/agent.tar.gz"
tar -xzf agent.tar.gz

if [ $# -gt 0 ]; then
    exec "agent/build/$BIN" "$@"
else
    exec "agent/build/$BIN"
fi
