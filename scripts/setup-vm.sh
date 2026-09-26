#!/usr/bin/env bash

set -euo pipefail

if [[ $(id -u) -ne 0 ]]; then
  echo "run this script as root" >&2
  exit 1
fi

esf_version=${ESF_VERSION:-${MACHINIST_VERSION:-}}
if [[ ! $esf_version =~ ^v[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$ ]]; then
  echo "set ESF_VERSION to the release being installed, such as v0.5.0" >&2
  exit 2
fi
script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
repo_dir=$(cd "$script_dir/.." && pwd)
if [[ ! -f $repo_dir/install.sh ]]; then
  echo "run setup-vm.sh from a verified ESF source checkout" >&2
  exit 2
fi

legacy_root_install=false
if [[ -d /root/.machinist ]]; then
  legacy_root_install=true
fi
for legacy_unit in machinist-control-plane.service machinist-worker.service; do
  if [[ -f /etc/systemd/system/$legacy_unit ]] && grep -q '^User=root$' "/etc/systemd/system/$legacy_unit"; then
    legacy_root_install=true
  fi
done
if [[ $legacy_root_install == true ]]; then
  echo "legacy root-based Machinist installation detected" >&2
  echo "follow the v0.1.x migration steps in docs/vm-deployment.md before running this bootstrap" >&2
  exit 3
fi

if [[ ! -r /etc/os-release ]]; then
  echo "unsupported Linux distribution: /etc/os-release is missing" >&2
  exit 1
fi
# shellcheck disable=SC1091 # This system file is checked for readability above.
. /etc/os-release
if [[ ${ID:-} != ubuntu && ${ID:-} != debian ]]; then
  echo "unsupported Linux distribution: ${ID:-unknown}" >&2
  exit 1
fi

export DEBIAN_FRONTEND=noninteractive
apt-get update
apt-get install -y ca-certificates curl git gh jq openssh-client tar

runtime_user=machinist
if ! id "$runtime_user" >/dev/null 2>&1; then
  useradd --create-home --shell /bin/bash "$runtime_user"
fi
runtime_home=$(getent passwd "$runtime_user" | cut -d: -f6)
if [[ -z $runtime_home || ! -d $runtime_home ]]; then
  echo "could not determine home directory for $runtime_user" >&2
  exit 1
fi

ESF_COMPONENT=machinist ESF_VERSION="$esf_version" sh "$repo_dir/install.sh"

runuser -u "$runtime_user" -- env HOME="$runtime_home" machinist init

worker_was_enabled=false
worker_was_active=false
if systemctl is-enabled --quiet machinist-worker.service 2>/dev/null; then
  worker_was_enabled=true
fi
if systemctl is-active --quiet machinist-worker.service 2>/dev/null; then
  worker_was_active=true
fi

install -m 0644 "$repo_dir/deploy/systemd/machinist-control-plane.service" \
  /etc/systemd/system/machinist-control-plane.service
install -m 0644 "$repo_dir/deploy/systemd/machinist-worker.service" \
  /etc/systemd/system/machinist-worker.service
systemctl daemon-reload
systemctl enable machinist-control-plane.service
systemctl restart machinist-control-plane.service
if runuser -u "$runtime_user" -- env HOME="$runtime_home" machinist worker validate --help >/dev/null 2>&1; then
  if runuser -u "$runtime_user" -- env HOME="$runtime_home" machinist worker validate >/dev/null 2>&1; then
    systemctl enable machinist-worker.service
    systemctl restart machinist-worker.service
  else
    systemctl disable --now machinist-worker.service
  fi
else
  if [[ $worker_was_active == true ]]; then
    if [[ $worker_was_enabled == true ]]; then
      systemctl enable machinist-worker.service
    else
      systemctl disable machinist-worker.service
    fi
    systemctl restart machinist-worker.service
    echo "installed Machinist release does not support worker validation; restored the previously active worker" >&2
  else
    if [[ $worker_was_enabled == true ]]; then
      systemctl enable machinist-worker.service
      systemctl stop machinist-worker.service
      echo "installed Machinist release does not support worker validation; preserved the enabled but inactive worker" >&2
    else
      systemctl disable --now machinist-worker.service
    fi
  fi
fi

cat <<'EOF'

VM bootstrap complete.

Next steps:
  1. Run `su - machinist`, then complete the remaining login and repository steps as that user.
  2. Run `gh auth login` if GitHub integration is enabled.
  3. Install the separately qualified coding agent binaries and authenticate them as that user.
  4. Clone each repository agents may use and register its absolute path in
     ~/.machinist/worker.toml.
  5. Exit back to root and run `systemctl enable --now machinist-worker` after registering a repository.
  6. Check `systemctl status machinist-control-plane machinist-worker`.

Keep the control plane on 127.0.0.1. Reach it from your computer with:
  ssh -N -L 7331:127.0.0.1:7331 machinist
EOF
