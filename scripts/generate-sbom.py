#!/usr/bin/env python3
"""Generate a CycloneDX inventory from the frozen Go, npm, and Python graphs."""

import json
import hashlib
from pathlib import Path
import subprocess
import sys
import tomllib
from urllib.parse import quote
import uuid

ROOT = Path(__file__).resolve().parents[1]


def go_modules():
    output = subprocess.check_output(["go", "list", "-m", "-json", "all"], cwd=ROOT, text=True)
    decoder = json.JSONDecoder()
    while output.strip():
        module, consumed = decoder.raw_decode(output.lstrip())
        output = output.lstrip()[consumed:]
        if module.get("Main"):
            continue
        actual = module.get("Replace", module)
        version = actual.get("Version", module.get("Version"))
        if version:
            yield "golang", module["Path"], version


def npm_packages(lock_path):
    packages = json.loads((ROOT / lock_path).read_text())["packages"]
    for location, package in packages.items():
        if not location or not package.get("version"):
            continue
        name = location.rsplit("node_modules/", 1)[-1]
        yield "npm", name, package["version"]


def python_packages():
    lock = tomllib.loads((ROOT / "uv.lock").read_text())
    for package in lock["package"]:
        if package.get("version"):
            yield "pypi", package["name"], package["version"]


def main():
    if len(sys.argv) != 2:
        raise SystemExit("usage: generate-sbom.py OUTPUT")
    components = {}
    sources = (
        go_modules(),
        npm_packages("internal/controlplane/web/package-lock.json"),
        npm_packages("agents/opencode/package-lock.json"),
        python_packages(),
    )
    for source in sources:
        for ecosystem, name, version in source:
            purl = f"pkg:{ecosystem}/{quote(name, safe='/')}@{quote(version, safe='.+-')}"
            components[purl] = {"type": "library", "bom-ref": purl, "name": name, "version": version, "purl": purl}
    ordered = [components[key] for key in sorted(components)]
    component_digest = hashlib.sha256(json.dumps(ordered, sort_keys=True, separators=(",", ":")).encode()).hexdigest()
    bom = {
        "bomFormat": "CycloneDX",
        "specVersion": "1.6",
        "serialNumber": f"urn:uuid:{uuid.uuid5(uuid.NAMESPACE_URL, 'https://github.com/mitkox/esf/sbom/' + component_digest)}",
        "version": 1,
        "metadata": {"component": {"type": "application", "name": "esf", "version": json.loads((ROOT / "release/inventory.json").read_text())["release"].removeprefix("v")}},
        "components": ordered,
    }
    Path(sys.argv[1]).write_text(json.dumps(bom, indent=2, sort_keys=True) + "\n")


if __name__ == "__main__":
    main()
