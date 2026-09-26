#!/usr/bin/env python3
"""Verify a staged Linux/amd64 agent against the release inventory."""

import hashlib
import json
from pathlib import Path
import sys

if len(sys.argv) != 3 or sys.argv[1] not in {"opencode", "unreal", "unreal-archive", "unreal-sums"}:
    raise SystemExit("usage: verify-agent-file.py {opencode|unreal|unreal-archive|unreal-sums} FILE")

root = Path(__file__).resolve().parents[1]
dependencies = json.loads((root / "release/inventory.json").read_text())["dependencies"]
key = {
    "opencode": ("opencode", "linux_amd64_binary_sha256"),
    "unreal": ("unreal", "binary_sha256"),
    "unreal-archive": ("unreal", "linux_amd64_archive_sha256"),
    "unreal-sums": ("unreal", "sha256sums_sha256"),
}[sys.argv[1]]
expected = dependencies[key[0]][key[1]]
digest = hashlib.file_digest(open(sys.argv[2], "rb"), "sha256").hexdigest()
if digest != expected:
    raise SystemExit(f"{sys.argv[1]} digest mismatch: {digest}")
print(f"{sys.argv[1]} sha256 verified: {digest}")
