#!/usr/bin/env bash
set -euo pipefail

REPO="${EDGE_AGENT_REPO:-MINGTIANJIAN886/edge_agent}"
RUN_TMP_DIR="$(mktemp -d)"
trap 'rm -rf "${RUN_TMP_DIR}"' EXIT

ARCH="$(uname -m)"
case "${ARCH}" in
  x86_64|amd64)  BINARY="agent-amd64" ;;
  aarch64|arm64) BINARY="agent-aarch64" ;;
  armv7l|armhf)  BINARY="agent-armv7l" ;;
  *) echo "Unsupported architecture: ${ARCH}" >&2; exit 1 ;;
esac

BINARY_URL="https://github.com/${REPO}/releases/latest/download/${BINARY}"
CHECKSUM_URL="https://github.com/${REPO}/releases/latest/download/SHA256SUMS"

curl -fsSL -o "${RUN_TMP_DIR}/${BINARY}" "${BINARY_URL}"
curl -fsSL -o "${RUN_TMP_DIR}/SHA256SUMS" "${CHECKSUM_URL}"

EXPECTED_SHA="$(awk -v name="${BINARY}" '$2 ~ ("/" name "$") || $2 == name {print $1; exit}' "${RUN_TMP_DIR}/SHA256SUMS")"
if [ -z "${EXPECTED_SHA}" ]; then
  echo "No checksum found for ${BINARY}" >&2
  exit 1
fi

ACTUAL_SHA="$(sha256sum "${RUN_TMP_DIR}/${BINARY}" | awk '{print $1}')"
if [ "${EXPECTED_SHA}" != "${ACTUAL_SHA}" ]; then
  echo "SHA256 mismatch for ${BINARY}" >&2
  exit 1
fi

chmod +x "${RUN_TMP_DIR}/${BINARY}"
exec "${RUN_TMP_DIR}/${BINARY}" "$@"
