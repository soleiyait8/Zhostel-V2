#!/usr/bin/env bash
# Creates the first administrator. The password is read without echo and
# passed through the environment, never the command line.
#   ./scripts/create-admin.sh admin@example.com "Full Name"
set -euo pipefail
cd "$(dirname "$0")/.."
EMAIL="${1:?usage: $0 <email> [name]}"
NAME="${2:-Administrator}"
read -r -s -p "Password for $EMAIL (12+ characters): " PW; echo
read -r -s -p "Repeat password: " PW2; echo
[ "$PW" = "$PW2" ] || { echo "Passwords do not match."; exit 1; }
ADMIN_PASSWORD="$PW" docker compose run --rm -e ADMIN_PASSWORD migrate \
  /usr/local/bin/migrate -create-admin "$EMAIL" -admin-name "$NAME"
