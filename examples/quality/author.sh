#!/bin/sh
set -eu
if [ "${1:-}" = "--version" ]; then
  echo fixture-author-v1
  exit 0
fi
prompt=$(cat)
case "$prompt" in
  *R3*) target=auth/quality-fixture.txt ;;
  *) target=quality-fixture.txt ;;
esac
mkdir -p "$(dirname "$target")"
printf 'Deterministic quality fixture\n' > "$target"
git add -- "$target"
git -c user.name=QualityFixture -c user.email=quality-fixture@example.invalid \
  commit -m 'Controlled fixture change'
