#!/bin/sh
set -eu
/usr/local/bin/envd -port 49983 -isnotfc >/var/log/envd.log 2>&1 &
if [ "$#" -gt 0 ]; then
  exec "$@"
fi
wait
