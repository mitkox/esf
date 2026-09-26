#!/usr/bin/env python3
"""Check pinned release inputs and refuse an unqualified RC/final release."""

import argparse
import hashlib
import json
from pathlib import Path
import re
import sys
import tomllib

ROOT = Path(__file__).resolve().parents[1]


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--require-qualified", action="store_true")
    args = parser.parse_args()
    inventory = json.loads((ROOT / "release/inventory.json").read_text())
    embedded_inventory = ROOT / "internal/releaseinfo/inventory.json"
    dependencies = inventory["dependencies"]
    mod = (ROOT / "go.mod").read_text()
    npm_frontend = json.loads((ROOT / "internal/controlplane/web/package-lock.json").read_text())["packages"]
    npm_agent = json.loads((ROOT / "agents/opencode/package-lock.json").read_text())["packages"]
    python = tomllib.loads((ROOT / "uv.lock").read_text())
    example = tomllib.loads((ROOT / "internal/factory/example.toml").read_text())
    template = (ROOT / "deploy/cube/Dockerfile").read_text()
    issues = []

    def require(condition: bool, message: str) -> None:
        if not condition:
            issues.append(message)

    require(embedded_inventory.exists() and embedded_inventory.read_bytes() == (ROOT / "release/inventory.json").read_bytes(), "embedded factory inventory differs from release inventory")

    require(re.search(rf"(?m)^go {re.escape(inventory['toolchains']['go'])}$", mod) is not None, "Go toolchain does not match inventory")
    require(dependencies["cubesandbox"]["go_sdk"] in mod, "Cube SDK does not match inventory")
    for module, version in (("google.golang.org/grpc", "grpc"), ("go.opentelemetry.io/otel", "otel"), ("modernc.org/sqlite", "sqlite")):
        require(re.search(rf"(?m)^\s*{re.escape(module)} v{re.escape(dependencies[version])}(?:\s|$)", mod) is not None, f"{module} does not match inventory")
    frontend = dependencies["frontend"]
    for package, key in (("vite", "vite"), ("lucide-react", "lucide"), ("jsdom", "jsdom"), ("react", "react"), ("tailwindcss", "tailwind")):
        actual = npm_frontend.get(f"node_modules/{package}", {}).get("version")
        require(actual == frontend[key], f"{package}: lockfile {actual}, inventory {frontend[key]}")
    opencode = npm_agent.get("node_modules/@opencode/cli", {})
    require(opencode.get("version") == dependencies["opencode"]["version"], "OpenCode version differs from inventory")
    require(opencode.get("integrity") == dependencies["opencode"]["npm_integrity"], "OpenCode package integrity differs from inventory")
    example_agent = example["harnesses"]["opencode2"]
    require(example_agent.get("binary_sha256") == dependencies["opencode"]["linux_amd64_binary_sha256"], "example OpenCode digest differs from inventory")
    require(example_agent.get("preinstalled") is True, "example OpenCode agent is not template-installed")
    require(example_agent.get("credential_mode") == "cube_egress" and not example_agent.get("pass_env") and not example_agent.get("provider_files"), "example OpenCode credentials are agent-readable")
    require(example_agent.get("packages") == [] and not example_agent.get("catalog_cache"), "example OpenCode installs packages or uploads a catalog on every run")
    require(example["sandbox"].get("base_packages") == [], "example installs sandbox packages on every run")
    for base in ("node", "cube"):
        require(inventory["build_bases"][base] in template, f"Cube template {base} base differs from inventory")
    for name, field in (("opencode", "linux_amd64_binary_sha256"), ("unreal", "binary_sha256"), ("unreal", "linux_amd64_archive_sha256")):
        require(dependencies[name][field] in template, f"Cube template {name} {field} differs from inventory")
    require(hashlib.sha256(template.encode()).hexdigest() == inventory["templates"]["recipe_sha256"], "Cube template recipe differs from inventory")
    dspy = next((package.get("version") for package in python["package"] if package["name"] == "dspy"), None)
    require(dspy == dependencies["dspy"], "DSPy lock differs from inventory")
    if args.require_qualified:
        for name, status in inventory["qualification"].items():
            require(status == "passed", f"qualification {name} is {status}")
        for name, value in inventory["images"].items():
            require(isinstance(value, str) and value.startswith("sha256:"), f"image {name} has no digest")
        require(inventory["templates"]["template_sha256"] is not None, "template identity is missing")
        require(inventory["templates"]["cube_template_id"] is not None and inventory["templates"]["qualification"] == "passed", "Cube template is not qualified")
        require(dependencies["unreal"]["binary_sha256"] is not None, "Unreal checksum is missing")
        require(inventory["transitive_licenses"] == "reviewed", "transitive licenses are not reviewed")
    for issue in issues:
        print(f"inventory: {issue}", file=sys.stderr)
    if issues:
        return 1
    print("release inventory pins match lockfiles" + ("; qualification gates passed" if args.require_qualified else ""))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
