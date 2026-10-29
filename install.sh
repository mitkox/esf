#!/bin/sh

set -eu

repository=${ESF_REPOSITORY:-mitkox/esf}
version=${ESF_VERSION:-}
install_dir=${ESF_INSTALL_DIR:-}
component=${ESF_COMPONENT:-factory}

fail() {
  printf 'ESF installer: %s\n' "$*" >&2
  exit 1
}

command -v curl >/dev/null 2>&1 || fail "curl is required"
command -v tar >/dev/null 2>&1 || fail "tar is required"
command -v gh >/dev/null 2>&1 || fail "GitHub CLI is required to verify release provenance"
case $component in
  factory) ;;
  machinist|combined)
    fail "the Machinist component was removed in ESF 0.6.2; install the factory archive (the default) and use the factory command" ;;
  *) fail "ESF_COMPONENT must be factory" ;;
esac

case $(uname -s) in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *) fail "unsupported operating system: $(uname -s)" ;;
esac

case $(uname -m) in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) fail "unsupported architecture: $(uname -m)" ;;
esac

if [ -z "$version" ]; then
  metadata=$(curl -fsSL -H 'Accept: application/vnd.github+json' \
    -H 'User-Agent: esf-installer' \
    "https://api.github.com/repos/$repository/releases/latest") || \
    fail "could not find a published release"
  version=$(printf '%s\n' "$metadata" | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1)
fi

printf '%s\n' "$version" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$' ||
  fail "invalid release version: $version"

if [ -z "$install_dir" ]; then
  if [ -w /usr/local/bin ]; then
    install_dir=/usr/local/bin
  else
    install_dir=${HOME:?HOME is required}/.local/bin
  fi
fi

release_name=${version#v}
archive_name="esf_factory_${release_name}_${os}_${arch}.tar.gz"
release_url="https://github.com/$repository/releases/download/$version"
temporary_dir=$(mktemp -d)
trap 'rm -rf "$temporary_dir"' EXIT HUP INT TERM

curl -fsSL "$release_url/checksums.txt" -o "$temporary_dir/checksums.txt" || \
  fail "could not download checksums for $version"
curl -fsSL "$release_url/$archive_name" -o "$temporary_dir/$archive_name" || \
  fail "could not download $archive_name"

expected=$(awk -v name="$archive_name" '$2 == name || $2 == "*" name { print $1; exit }' "$temporary_dir/checksums.txt")
[ -n "$expected" ] || fail "release checksums do not contain $archive_name"

if command -v sha256sum >/dev/null 2>&1; then
  actual=$(sha256sum "$temporary_dir/$archive_name" | awk '{print $1}')
elif command -v shasum >/dev/null 2>&1; then
  actual=$(shasum -a 256 "$temporary_dir/$archive_name" | awk '{print $1}')
else
  fail "sha256sum or shasum is required"
fi
[ "$actual" = "$expected" ] || fail "checksum mismatch for $archive_name"
gh attestation verify "$temporary_dir/$archive_name" -R "$repository" \
  --signer-workflow "$repository/.github/workflows/release.yml" \
  --source-ref "refs/tags/$version" >/dev/null || fail "release provenance verification failed"

binaries='factory'
for binary in $binaries; do
  tar -xzf "$temporary_dir/$archive_name" -C "$temporary_dir" "$binary" || fail "archive lacks $binary"
done
mkdir -p "$install_dir"
for binary in $binaries; do
  staged="$install_dir/.$binary.esf-update.$$"
  install -m 0755 "$temporary_dir/$binary" "$staged"
  mv -f "$staged" "$install_dir/$binary"
done

printf 'installed ESF factory %s to %s\n' "$version" "$install_dir"
case ":$PATH:" in
  *":$install_dir:"*) ;;
  *) printf 'add %s to PATH before running factory\n' "$install_dir" ;;
esac
