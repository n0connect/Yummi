#!/usr/bin/env bash
set -euo pipefail
IFS=$'\n\t'

./scripts/install.sh

docker compose up -d n0

echo "n0 is running in development mode"
