#!/usr/bin/env bash
# Sends SIMULATED device traffic through the public ADMS hostname to prove the
# path works end to end before the real terminal is connected.
# Register a device with this serial on the Devices page first.
#   ./scripts/simulate-device.sh SIMTEST0001
set -euo pipefail
cd "$(dirname "$0")/.."
set -a; . ./.env; set +a
SN="${1:-SIMTEST0001}"
docker run --rm -v "$PWD/../backend:/src" -w /src golang:1.24-bookworm \
  go run ./cmd/simdevice -url "https://$ADMS_DOMAIN" -sn "$SN"
