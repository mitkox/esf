#!/usr/bin/env bash
# Install offline artifacts into a template staging filesystem, never a run.
set -euo pipefail
[[ $# = 2 ]] || { echo 'usage: install-pi-agent.sh DIGEST_PINNED_NODE_BINARY DESTINATION' >&2; exit 2; }
[[ $(uname -s)/$(uname -m) = Linux/x86_64 ]] || { echo 'Pi v1 qualifies Linux/amd64 only' >&2; exit 2; }
python3 scripts/verify-agent-file.py pi agents/pi/dist/pi-runner.mjs
python3 scripts/verify-agent-file.py pi-node "$1"
python3 scripts/verify-pi-inventory.py
mkdir -p "$2"
install -m 0755 "$1" "$2/.node.new"
install -m 0644 agents/pi/dist/pi-runner.mjs "$2/.pi-runner.mjs.new"
install -m 0644 agents/pi/dist/THIRD_PARTY_NOTICES.txt "$2/.THIRD_PARTY_NOTICES.txt.new"
mv -f "$2/.node.new" "$2/node"
mv -f "$2/.pi-runner.mjs.new" "$2/pi-runner.mjs"
mv -f "$2/.THIRD_PARTY_NOTICES.txt.new" "$2/THIRD_PARTY_NOTICES.txt"
printf 'installed verified Pi 1.0.4 execution chain in %s\n' "$2"
