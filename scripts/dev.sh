#!/usr/bin/env bash
set -euo pipefail
IFS=$'\n\t'

./scripts/install.sh

docker compose up -d sandbox

echo "sandbox is running in development mode"
