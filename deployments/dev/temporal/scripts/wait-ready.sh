#!/usr/bin/env bash
set -euo pipefail

container_state() {
  docker inspect --format '{{.State.Status}}' "$1" 2>/dev/null || true
}

container_health() {
  docker inspect --format '{{.State.Health.Status}}' "$1" 2>/dev/null || true
}

container_exit_code() {
  docker inspect --format '{{.State.ExitCode}}' "$1" 2>/dev/null || true
}

for ((attempt = 0; attempt < 90; attempt++)); do
  if [[ $(container_state factory-temporal-admin-tools) == exited ]] &&
     [[ $(container_exit_code factory-temporal-admin-tools) != 0 ]]; then
    echo 'Temporal schema setup failed' >&2
    exit 1
  fi
  if [[ $(container_state factory-temporal-create-namespace) == exited ]] &&
     [[ $(container_exit_code factory-temporal-create-namespace) != 0 ]]; then
    echo 'Temporal namespace setup failed' >&2
    exit 1
  fi
  if [[ $(container_health factory-temporal-postgresql) == healthy ]] &&
     [[ $(container_health factory-temporal) == healthy ]] &&
     [[ $(container_state factory-temporal-ui) == running ]] &&
     [[ $(container_state factory-temporal-admin-tools) == exited ]] &&
     [[ $(container_exit_code factory-temporal-admin-tools) == 0 ]] &&
     [[ $(container_state factory-temporal-create-namespace) == exited ]] &&
     [[ $(container_exit_code factory-temporal-create-namespace) == 0 ]]; then
    echo 'Temporal development stack is ready'
    exit 0
  fi
  sleep 2
done

echo 'Timed out waiting for the Temporal development stack' >&2
exit 1
