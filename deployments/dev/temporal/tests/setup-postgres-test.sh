#!/bin/sh
# Exercise initialization and restored-database safety without a live database.
set -eu
script=${1:-"$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)/scripts/setup-postgres.sh"}
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT HUP INT TERM
mkdir "$work/bin"
export CALL_LOG="$work/calls" POSTGRES_SEEDS=test-db POSTGRES_USER=test-user
export PATH="$work/bin:$PATH"
cat >"$work/bin/nc" <<'EOF'
#!/bin/sh
printf 'nc %s\n' "$*" >>"$CALL_LOG"
EOF
cat >"$work/bin/temporal-sql-tool" <<'EOF'
#!/bin/sh
printf 'sql %s\n' "$*" >>"$CALL_LOG"
case "${FAIL_AT:-}:$*" in
  create:*'--db temporal create') exit 73 ;;
  update:*'--db temporal update-schema '*) exit 42 ;;
esac
EOF
chmod +x "$work/bin/nc" "$work/bin/temporal-sql-tool"

# Misconfiguration must fail before any database command or network probe.
for mode in missing invalid; do
  : >"$CALL_LOG"
  unset TEMPORAL_SCHEMA_MODE
  if [ "$mode" = invalid ]; then export TEMPORAL_SCHEMA_MODE=invalid; fi
  if sh "$script" >"$work/output" 2>&1; then
    echo "FAIL: accepted $mode schema mode" >&2; exit 1
  fi
  test ! -s "$CALL_LOG"
done

# A restored database gets only updates, with no metadata reinitialization.
: >"$CALL_LOG"
TEMPORAL_SCHEMA_MODE=upgrade sh "$script" >"$work/output" 2>&1
test "$(grep -c '^sql .*update-schema ' "$CALL_LOG")" -eq 2
if grep -Eq 'setup-schema|--db [^ ]+ create$' "$CALL_LOG"; then
  echo 'FAIL: upgrade attempted database initialization' >&2; exit 1
fi
grep -q -- '--db temporal update-schema ' "$CALL_LOG"
grep -q -- '--db temporal_visibility update-schema ' "$CALL_LOG"

# The explicit fresh-install path still initializes both stores.
: >"$CALL_LOG"
TEMPORAL_SCHEMA_MODE=create sh "$script" >"$work/output" 2>&1
test "$(grep -c 'setup-schema -v 0.0$' "$CALL_LOG")" -eq 2
test "$(grep -c '^sql .* create$' "$CALL_LOG")" -eq 2
test "$(grep -c '^sql .*update-schema ' "$CALL_LOG")" -eq 2

# An initialization failure (such as an existing database) never falls through
# to setup-schema. An upgrade failure never advances to the second database.
for operation in create update; do
  : >"$CALL_LOG"
  mode=create
  if [ "$operation" = update ]; then mode=upgrade; fi
  if TEMPORAL_SCHEMA_MODE="$mode" FAIL_AT="$operation" sh "$script" >"$work/output" 2>&1; then
    echo "FAIL: ignored $operation failure" >&2; exit 1
  fi
  test "$(grep -c '^sql ' "$CALL_LOG")" -eq 1
  if grep -q 'setup-schema' "$CALL_LOG"; then
    echo 'FAIL: initialized schema after a failed database command' >&2; exit 1
  fi
done
echo 'Temporal schema safety: 6 scenarios passed'
