#!/usr/bin/env bash
# Starts the Go server and the Vite dev server for local testing.
# Extra arguments are passed to the Go server, e.g.:
#   scripts/run.sh -path ~/Videos -log-level debug
# Stop both with Ctrl-C.
set -euo pipefail

cd "$(dirname "$0")/.."

npm install

bin="${TMPDIR:-/tmp}/just-dlna-dev"
go build -o "$bin" .

pids=()
cleanup() {
	trap - EXIT INT TERM
	for pid in "${pids[@]}"; do
		kill "$pid" 2>/dev/null || true
	done
	wait 2>/dev/null || true
}
trap cleanup EXIT INT TERM

# The Vite dev server proxies /api to the Go server's UI port (1340).
"$bin" "$@" &
pids+=($!)

npm run dev &
pids+=($!)

# Exit as soon as either process stops (macOS bash 3.2 has no `wait -n`).
while :; do
	for pid in "${pids[@]}"; do
		if ! kill -0 "$pid" 2>/dev/null; then
			exit 0
		fi
	done
	sleep 1
done
