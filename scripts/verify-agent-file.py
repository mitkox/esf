#!/usr/bin/env python3
"""Verify a staged Linux/amd64 agent against the release inventory."""

import hashlib
import json
from pathlib import Path
import sys

if len(sys.argv) != 3 or sys.argv[1] not in {"opencode", "unreal", "unreal-archive", "unreal-sums", "pi", "pi-node"}:
    raise SystemExit("usage: verify-agent-file.py {opencode|unreal|unreal-archive|unreal-sums|pi|pi-node} FILE")

root = Path(__file__).resolve().parents[1]
dependencies = json.loads((root / "release/inventory.json").read_text())["dependencies"]
key = {
    "opencode": ("opencode", "linux_amd64_binary_sha256"),
    "unreal": ("unreal", "binary_sha256"),
    "unreal-archive": ("unreal", "linux_amd64_archive_sha256"),
    "unreal-sums": ("unreal", "sha256sums_sha256"),
}.get(sys.argv[1])
if key:
    expected = dependencies[key[0]][key[1]]
else:
    pi = json.loads((root / "agents/pi/inventory.json").read_text())
    expected = pi["runner" if sys.argv[1] == "pi" else "runtime"]["sha256"]
sha256 = hashlib.sha256()
with open(sys.argv[2], "rb") as artifact:
    for chunk in iter(lambda: artifact.read(1 << 20), b""):
        sha256.update(chunk)
digest = sha256.hexdigest()
if digest != expected:
    raise SystemExit(f"{sys.argv[1]} digest mismatch: {digest}")
print(f"{sys.argv[1]} sha256 verified: {digest}")
