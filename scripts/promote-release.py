#!/usr/bin/env python3
"""Rename a verified RC release set without rebuilding any archive bytes."""

import hashlib
import gzip
import json
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tarfile

from release_qualification import verify_qualification


def main():
    if len(sys.argv) != 6:
        raise SystemExit("usage: promote-release.py RC_VERSION FINAL_VERSION RC_DIRECTORY OUTPUT_DIRECTORY QUALIFICATION_MANIFEST")
    source_version, final_version = sys.argv[1:3]
    source, output = map(Path, sys.argv[3:5])
    root = Path(__file__).resolve().parents[1]
    inventory = json.loads((root / "release/inventory.json").read_text())
    problems = verify_qualification(root, inventory, Path(sys.argv[5]), source)
    if problems:
        raise SystemExit("RC is not qualified: " + "; ".join(problems))
    if not re.fullmatch(r"v[0-9]+\.[0-9]+\.[0-9]+-rc\.[0-9]+", source_version):
        raise SystemExit("source must be an RC release")
    if final_version != source_version.split("-", 1)[0]:
        raise SystemExit("final version must match the RC base version")
    manifest = json.loads((source / "release-manifest.json").read_text())
    commit = subprocess.check_output(["git", "rev-parse", "HEAD"], text=True).strip()
    if manifest["release"] != source_version or manifest["commit"] != commit or manifest["binary_release"] != final_version:
        raise SystemExit("RC manifest is incompatible with this final tag")
    if output.exists() and any(output.iterdir()):
        raise SystemExit("output directory must be empty")
    output.mkdir(parents=True, exist_ok=True)
    source_name, final_name = source_version[1:], final_version[1:]
    archives = sorted(source.glob(f"esf_*_{source_name}_*.tar.gz"))
    if len(archives) != 12:
        raise SystemExit(f"expected 12 qualified RC archives, got {len(archives)}")
    for archive in archives:
        destination = output / archive.name.replace(f"_{source_name}_", f"_{final_name}_")
        shutil.copyfile(archive, destination)
        if hashlib.sha256(archive.read_bytes()).digest() != hashlib.sha256(destination.read_bytes()).digest():
            raise SystemExit(f"archive changed during promotion: {archive.name}")
    for name in ("dependency-inventory.json", "sbom.cdx.json"):
        shutil.copyfile(source / name, output / name)
    # Retain the attested RC qualification unchanged. The final provenance binds
    # renamed archives; promoted_from records their qualified source names.
    shutil.copyfile(sys.argv[5], output / "qualification-manifest.json")
    qualification_path = Path(sys.argv[5])
    qualification = json.loads(qualification_path.read_text())
    for name in qualification["evidence"]:
        destination = output / name
        destination.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(qualification_path.parent / name, destination)
    # GitHub release attachments are regular files; retain the full nested
    # evidence bundle in a deterministic archive as well as the workflow asset.
    with (output / "qualification-evidence.tar.gz").open("wb") as raw:
        with gzip.GzipFile(fileobj=raw, mode="wb", mtime=0, filename="") as compressed:
            with tarfile.open(fileobj=compressed, mode="w", format=tarfile.USTAR_FORMAT) as archive:
                for name in ["qualification-manifest.json", *sorted(qualification["evidence"])]:
                    content = output / name
                    info = tarfile.TarInfo(name)
                    info.size = content.stat().st_size
                    info.mode = 0o644
                    with content.open("rb") as stream:
                        archive.addfile(info, stream)
    manifest["release"] = final_version
    manifest["promoted_from"] = source_version
    (output / "release-manifest.json").write_text(json.dumps(manifest, indent=2) + "\n")
    names = sorted([path.name for path in output.glob("esf_*.tar.gz")]) + ["release-manifest.json", "dependency-inventory.json", "sbom.cdx.json", "qualification-manifest.json", "qualification-evidence.tar.gz"]
    with (output / "checksums.txt").open("w") as checksums:
        for name in names:
            digest = hashlib.sha256((output / name).read_bytes()).hexdigest()
            checksums.write(f"{digest}  {name}\n")


if __name__ == "__main__":
    main()
