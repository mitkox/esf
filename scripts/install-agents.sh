#!/usr/bin/env bash
set -euo pipefail

if [[ $(uname -s)/$(uname -m) != Linux/x86_64 ]]; then
  echo 'the current agent inventory only qualifies Linux/amd64' >&2
  exit 2
fi
command -v npm >/dev/null || { echo 'npm is required' >&2; exit 2; }
command -v gh >/dev/null || { echo 'GitHub CLI is required' >&2; exit 2; }
python3 scripts/verify-release-inventory.py
mapfile -t agent_versions < <(python3 -c 'import json; x=json.load(open("release/inventory.json"))["dependencies"]; print(x["opencode"]["version"]); print(x["unreal"]["release"])')
opencode_version=${agent_versions[0]}
unreal_version=${agent_versions[1]}
[[ $opencode_version =~ ^[0-9]+\.[0-9]+\.[0-9]+$ && $unreal_version =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo 'invalid agent inventory versions' >&2; exit 2; }
unreal_archive="unreal-agent-runner_${unreal_version}_linux_amd64.tar.gz"

install_dir=${1:-/opt/esf/agents}
work_dir=$(mktemp -d)
trap 'rm -rf "$work_dir"' EXIT

# npm ci verifies the tarball integrity recorded in agents/opencode/package-lock.json.
npm ci --prefix agents/opencode
opencode=agents/opencode/node_modules/@opencode/cli/bin/opencode.exe
python3 scripts/verify-agent-file.py opencode "$opencode"

gh release download "v${unreal_version}" --repo unreallabsai/unreal-agent \
  --pattern SHA256SUMS --pattern "$unreal_archive" \
  --dir "$work_dir"
python3 scripts/verify-agent-file.py unreal-sums "$work_dir/SHA256SUMS"
python3 scripts/verify-agent-file.py unreal-archive "$work_dir/$unreal_archive"
(
  cd "$work_dir"
  sha256sum -c SHA256SUMS --ignore-missing
  tar -xzf "$unreal_archive" unreal-agent-runner
)
python3 scripts/verify-agent-file.py unreal "$work_dir/unreal-agent-runner"

mkdir -p "$install_dir"
install -m 0755 "$opencode" "$install_dir/.opencode2.new"
install -m 0755 "$work_dir/unreal-agent-runner" "$install_dir/.unreal-agent-runner.new"
mv -f "$install_dir/.opencode2.new" "$install_dir/opencode2"
mv -f "$install_dir/.unreal-agent-runner.new" "$install_dir/unreal-agent-runner"
printf 'installed verified OpenCode %s and Unreal %s in %s\n' "$opencode_version" "$unreal_version" "$install_dir"
