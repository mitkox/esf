#!/usr/bin/env python3
"""Check candidate Pi artifacts without changing historical release records."""
import hashlib
import json
from pathlib import Path

root = Path(__file__).resolve().parents[1]
inventory = json.loads((root / "agents/pi/inventory.json").read_text())
lock_path = root / "agents/pi/package-lock.json"
lock = json.loads(lock_path.read_text())
assert hashlib.sha256(lock_path.read_bytes()).hexdigest() == inventory["package_lock_sha256"], "Pi lock digest mismatch"
for name in ("pi-durable", "pi-ai", "chord"):
    package = lock["packages"]["node_modules/@earendil-works/" + name]
    assert package["version"] == inventory["pi_durable"]["version"] and package["integrity"].startswith("sha512-"), "Pi package not pinned"
assert inventory["pi_durable"]["git_commit"] == "7c10bd4337495ee613f2224843ecdf349b80d1df"
assert hashlib.sha256((root / "internal/agentharness/pi-catalog.json").read_bytes()).hexdigest() == inventory["model_catalog_sha256"], "Pi catalog pin mismatch"
digest = hashlib.sha256((root / "agents/pi/dist/pi-runner.mjs").read_bytes()).hexdigest()
assert digest == inventory["runner"]["sha256"], "Pi runner inventory mismatch"
assert (root / "agents/pi/dist/SHA256SUMS").read_text() == digest + "  pi-runner.mjs\n"
dockerfile = (root / "deploy/cube/Dockerfile.pi-v1").read_text()
for entry in (inventory["runtime"], inventory["runner"]):
    assert entry["sha256"] in dockerfile, "Pi template pin mismatch"
assert inventory["runtime"]["image"] in dockerfile
assert hashlib.sha256((root / "agents/pi/dist/THIRD_PARTY_NOTICES.txt").read_bytes()).hexdigest() == inventory["third_party_notices_sha256"], "Pi license notices mismatch"
print("Pi candidate inventory and pinned dependency graph verified")
