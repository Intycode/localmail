#!/usr/bin/env bash
# check-all — run every quality gate in order.
#   frontend: strict TS + astro check, unit tests, prettier
#   backend:  go vet, go test, go build
set -euo pipefail
cd "$(dirname "$0")/.."

echo "── frontend ────────────────────────────────"
(cd apps/client && bun run check)
(cd apps/client && bun run test)
(cd apps/client && bun run lint)

echo "── backend ─────────────────────────────────"
(cd backend && go vet ./...)
(cd backend && go test ./...)
(cd backend && go build ./...)

echo "── ok ──────────────────────────────────────"
