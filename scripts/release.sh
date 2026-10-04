#!/usr/bin/env bash

set -euo pipefail
umask 022

version=${1:-}
output_dir=${2:-dist}

if [[ ! "$version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$ ]]; then
  echo "usage: $0 vMAJOR.MINOR.PATCH [output-directory]" >&2
  exit 2
fi

inventory_version=$(python3 -c 'import json; print(json.load(open("release/inventory.json"))["release"])')
if [[ "${version%%-*}" != "$inventory_version" ]]; then
  echo "release version does not match the input inventory" >&2
  exit 1
fi
if [[ "$version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+-(test|rc)\.[1-9][0-9]*$ ]]; then
  # A prerelease is a validation artifact. Pending deployment qualifications
  # remain visible in the embedded inventory and do not imply production use.
  python3 scripts/verify-release-inventory.py
else
  echo "build an RC, qualify those assets, then use promote-release.py for the final release" >&2
  exit 1
fi
if [[ "$version" != *-test.* && -n $(git status --porcelain --untracked-files=normal) ]]; then
  echo "release builds require a clean checkout" >&2
  exit 1
fi

mkdir -p "$output_dir"
if find "$output_dir" -mindepth 1 -maxdepth 1 -print -quit | grep -q .; then
  echo "release output directory must be empty: $output_dir" >&2
  exit 1
fi

build_dir=$(mktemp -d)
trap 'rm -rf "$build_dir"' EXIT

release_name=${version#v}
binary_release=${release_name%%-*}
commit=$(git rev-parse HEAD)
go_toolchain=$(go env GOVERSION)
targets=(
  linux/amd64
  linux/arm64
  darwin/amd64
  darwin/arm64
)

for target in "${targets[@]}"; do
  goos=${target%/*}
  goarch=${target#*/}
  target_dir="$build_dir/${goos}_${goarch}"

  mkdir -p "$target_dir"
  CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
    go build -buildvcs=false -trimpath -ldflags="-s -w -X main.version=v$binary_release" \
    -o "$target_dir/machinist" ./cmd/machinist
  CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
    go build -buildvcs=false -trimpath -ldflags="-s -w -X github.com/mitkox/esf/internal/factory.Version=$binary_release -X main.buildCommit=$commit" \
    -o "$target_dir/factory" ./cmd/factory
  cp -L LICENSE README.md factory.example.toml "$target_dir/"
  chmod 0644 "$target_dir/LICENSE" "$target_dir/README.md" "$target_dir/factory.example.toml"
  chmod 0755 "$target_dir/machinist" "$target_dir/factory"
  touch -t 198001010000 "$target_dir/LICENSE" "$target_dir/README.md" "$target_dir/factory.example.toml" "$target_dir/machinist" "$target_dir/factory"
  for component in factory machinist combined; do
    archive="$output_dir/esf_${component}_${release_name}_${goos}_${goarch}.tar.gz"
    case "$component" in
      factory) files=(LICENSE README.md factory.example.toml factory) ;;
      machinist) files=(LICENSE README.md machinist) ;;
      combined) files=(LICENSE README.md factory.example.toml factory machinist) ;;
    esac
    COPYFILE_DISABLE=1 tar --format=ustar --owner=0 --group=0 --numeric-owner -C "$target_dir" -cf - "${files[@]}" | gzip -n > "$archive"
  done
done

printf '{\n  "release": "%s",\n  "binary_release": "v%s",\n  "commit": "%s",\n  "go_toolchain": "%s",\n  "machinist_upstream": "39435164faf1ff7fad49e41c38a7eb1a00538f21",\n  "targets": ["linux/amd64", "linux/arm64", "darwin/amd64", "darwin/arm64"]\n}\n' \
  "$version" "$binary_release" "$commit" "$go_toolchain" > "$output_dir/release-manifest.json"
cp release/inventory.json "$output_dir/dependency-inventory.json"
python3 scripts/generate-sbom.py "$output_dir/sbom.cdx.json"

(
  cd "$output_dir"
  shasum -a 256 esf_*.tar.gz release-manifest.json dependency-inventory.json sbom.cdx.json > checksums.txt
)
