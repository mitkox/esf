#!/usr/bin/env bash

set -euo pipefail

release_dir=${1:-dist}
version=${2:-}

if [[ ! "$version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$ ]]; then
  echo "usage: $0 RELEASE_DIRECTORY vMAJOR.MINOR.PATCH" >&2
  exit 2
fi

release_name=${version#v}
binary_release=${release_name%%-*}
commit=$(git rev-parse HEAD)
targets=(
  linux/amd64
  linux/arm64
  darwin/amd64
  darwin/arm64
)

(
  cd "$release_dir"
  shasum -a 256 -c checksums.txt
)
grep -F '"release": "'"$version"'"' "$release_dir/release-manifest.json"
grep -F '"commit": "'"$commit"'"' "$release_dir/release-manifest.json"

verify_dir=$(mktemp -d)
trap 'rm -rf "$verify_dir"' EXIT
host_target="$(go env GOOS)/$(go env GOARCH)"

for target in "${targets[@]}"; do
  goos=${target%/*}
  goarch=${target#*/}
  target_dir="$verify_dir/${goos}_${goarch}"

  mkdir -p "$target_dir"
  for component in factory machinist combined; do
    archive="$release_dir/esf_${component}_${release_name}_${goos}_${goarch}.tar.gz"
    test -f "$archive"
    contents=$(tar -tzf "$archive" | LC_ALL=C sort)
    case "$component" in
      factory) expected=$(printf '%s\n' LICENSE README.md factory.example.toml factory | LC_ALL=C sort) ;;
      machinist) expected=$(printf '%s\n' LICENSE README.md machinist | LC_ALL=C sort) ;;
      combined) expected=$(printf '%s\n' LICENSE README.md factory.example.toml factory machinist | LC_ALL=C sort) ;;
    esac
    test "$contents" = "$expected"
    tar -C "$target_dir" -xzf "$archive"
  done
  test -x "$target_dir/machinist"
  test -x "$target_dir/factory"
  go version -m "$target_dir/machinist" | grep -F "GOOS=$goos"
  go version -m "$target_dir/machinist" | grep -F "GOARCH=$goarch"

  if [[ "$target" == "$host_target" ]]; then
    test "$("$target_dir/machinist" version)" = "v$binary_release"
    test "$("$target_dir/factory" version --json | python3 -c 'import json,sys; print(json.load(sys.stdin)["release"])')" = "$binary_release"
  fi
  if [[ "$goos" == linux ]]; then
    factory_bytes=$(stat -c%s "$release_dir/esf_factory_${release_name}_${goos}_${goarch}.tar.gz")
    combined_bytes=$(stat -c%s "$release_dir/esf_combined_${release_name}_${goos}_${goarch}.tar.gz")
    if (( factory_bytes * 100 > combined_bytes * 75 )); then
      echo "factory-only archive exceeds 75% of combined: $target" >&2
      exit 1
    fi
  fi
done
