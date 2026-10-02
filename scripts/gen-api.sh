#!/usr/bin/env bash
# Regenerates the API contract between the Go server and the web UI:
#   openapi.json    OpenAPI document of the admin API (internal/admin)
#   ui/src/client/  TypeScript types and SDK generated from it (hey-api)
# Commit the results; the Docker UI build uses them without Go.
set -euo pipefail

cd "$(dirname "$0")/.."

tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT
go run ./cmd/openapi >"$tmp"
mv "$tmp" openapi.json

npx openapi-ts --silent --no-log-file
