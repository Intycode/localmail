#!/usr/bin/env bash
# setup — bootstrap the repo: install JS deps and Go modules.
set -euo pipefail
cd "$(dirname "$0")/.."

bun install
(cd backend && go mod tidy)
echo "setup ok — run `bun run check-all` next"
