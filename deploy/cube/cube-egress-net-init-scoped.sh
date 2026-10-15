#!/usr/bin/env bash
# Operator wrapper for Cube's reserved table/marks; leaves VPN/sysctl state alone.
set -euo pipefail
base=/usr/local/services/cubetoolbox/scripts/cube-egress/cube-proxy-iptables-init.sh
iface=${CUBE_INGRESS_IFACE:-cube-dev}
CUBE_L7_MARK_HTTP=0xCE010000
CUBE_L7_MARK_HTTPS=0xCE020000
CUBE_L7_MARK_MASK=0xFFFF0000
if [[ -f /etc/cubeegress/l7-marks.conf ]]; then
  # shellcheck source=/dev/null
  . /etc/cubeegress/l7-marks.conf
fi
mask=$(printf '0x%x' "$((CUBE_L7_MARK_MASK))")
remove_scoped() {
  local value mark
  for value in "$CUBE_L7_MARK_HTTP" "$CUBE_L7_MARK_HTTPS"; do
    mark=$(printf '0x%x' "$((value))")
    while ip -4 rule del iif "$iface" fwmark "$mark/$mask" table 100 2>/dev/null; do :; done
  done
}
case ${1:-up} in
  up)
    # The official down/up pair also makes repeated activation deterministic.
    remove_scoped
    "$base" down
    "$base" up
    for value in "$CUBE_L7_MARK_HTTP" "$CUBE_L7_MARK_HTTPS"; do
      mark=$(printf '0x%x' "$((value))")
      priority=$(ip -4 -o rule show | awk -v mark="$mark/$mask" '$2=="from" && $3=="all" && $4=="fwmark" && $5==mark && $6=="lookup" && $7=="100" {sub(":$", "", $1); print $1}')
      [[ $priority =~ ^[0-9]+$ ]] || { echo 'Expected exactly one unscoped Cube route' >&2; exit 1; }
      ip -4 rule del priority "$priority" fwmark "$mark/$mask" table 100
      if ! ip -4 rule add priority "$priority" iif "$iface" fwmark "$mark/$mask" table 100; then
        ip -4 rule add priority "$priority" fwmark "$mark/$mask" table 100
        exit 1
      fi
    done
    ;;
  down) remove_scoped; "$base" down ;;
  status) "$base" status ;;
  *) echo 'Usage: esf-net-init.sh up|down|status' >&2; exit 2 ;;
esac
