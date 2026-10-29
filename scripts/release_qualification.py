"""Verify post-build evidence without putting an image's digest inside itself."""

import hashlib
import json
import math
from pathlib import Path
import re
import subprocess

GATES = (
    "vm", "kubernetes", "soak_24h", "restore", "security", "performance",
    "temporal_replay", "agent_acceptance", "cube_lifecycle", "migration_drill",
)
TARGETS = ("vm", "kubernetes")
PLATFORMS = ("linux_amd64", "linux_arm64", "darwin_amd64", "darwin_arm64")
IMAGE_PLATFORMS = ("linux/amd64", "linux/arm64")
SHA256 = re.compile(r"[0-9a-f]{64}\Z")
HARNESS2_COMMIT = "1cfe254e8d25b15f37048c3730134686abe60335"


def verify_harness_improvement(section, directory, verified_evidence, commit):
    """Optional capability receipt; binary qualification cannot enable it implicitly."""
    if section is None:
        return []
    if not isinstance(section, dict) or type(section.get("enabled")) is not bool:
        return ["harness improvement activation receipt is malformed"]
    if not section["enabled"]:
        return []
    issues = []
    def require(value, message):
        if not value:
            issues.append("harness improvement: " + message)
    require(section.get("upstream_commit") == HARNESS2_COMMIT, "upstream pin differs")
    require(section.get("mode") in ("static", "adaptive"), "unsupported activation mode")
    for name in ("profile_digest", "route_digest"):
        require(isinstance(section.get(name), str) and
                re.fullmatch(r"sha256:[0-9a-f]{64}", section[name]), name + " missing")
    required = ("quality_report", "independent_approval", "component_activation", "budget_enforcement",
                "scope_isolation", "cleanup")
    for name in required:
        require(isinstance(section.get(name), str) and section[name] in verified_evidence,
                name + " lacks verified evidence")
    if issues:
        return issues
    try:
        report = json.loads((directory / section["quality_report"]).read_text())
        if not isinstance(report, dict) or not isinstance(report.get("input"), dict):
            return ["harness improvement: malformed quality report"]
        require(report.get("schema") == "esf-harness-quality/v1" and report.get("passed") is True,
                "quality report did not pass")
        inputs = report.get("input", {})
        require(inputs.get("source_commit") == commit, "quality report source differs")
        require(inputs.get("split") == "qualification", "quality report used a non-held-out partition")
        require(inputs.get("profile") == section["profile_digest"], "profile differs from quality report")
        require(inputs.get("route_digest") == section["route_digest"], "model route differs from quality report")
        require(inputs.get("mode") == section["mode"], "mode differs from quality report")
        if section["mode"] == "adaptive":
            require(inputs.get("policy_digest") == section.get("policy_digest") and
                    re.fullmatch(r"sha256:[0-9a-f]{64}", str(section.get("policy_digest"))),
                    "policy differs from quality report")
        approval = json.loads((directory / section["independent_approval"]).read_text())
        if not isinstance(approval, dict):
            return ["harness improvement: malformed independent approval"]
        require(approval.get("schema") == "esf-harness-approval/v1" and
                approval.get("profile") == section["profile_digest"] and bool(approval.get("signature")),
                "independent approval binding is missing")
    except (OSError, ValueError, TypeError, KeyError) as exc:
        issues.append("harness improvement evidence cannot be read: " + type(exc).__name__)
    return issues


def sha256(path: Path) -> str:
    with path.open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


def verify_qualification(root: Path, inventory: dict, manifest_path: Path,
                         artifact_dir: Path | None = None) -> list[str]:
    issues = []

    def require(condition, message):
        if not condition:
            issues.append(message)

    try:
        manifest_path = manifest_path.resolve(strict=True)
        manifest = json.loads(manifest_path.read_text())
        assets = (artifact_dir or manifest_path.parent).resolve(strict=True)
        commit = subprocess.check_output(
            ["git", "rev-parse", "HEAD"], cwd=root, text=True).strip()
        require(not subprocess.check_output(
            ["git", "status", "--porcelain", "--untracked-files=normal"], cwd=root, text=True).strip(),
                "qualification source checkout is not clean")
        require(manifest.get("schema") == "esf-qualification/v1", "unsupported qualification schema")
        require(manifest.get("release") == inventory["release"], "qualification release mismatch")
        require(manifest.get("commit") == commit, "qualification source commit mismatch")
        require(manifest.get("inventory_sha256") == sha256(root / "release/inventory.json"),
                "qualification inventory checksum mismatch")

        def check_files(entries, directory, kind):
            require(isinstance(entries, dict) and bool(entries), f"{kind} checksums are missing")
            verified = set()
            if not isinstance(entries, dict):
                return verified
            for name, checksum in entries.items():
                if (not isinstance(name, str) or not name or "\\" in name
                        or Path(name).is_absolute() or ".." in Path(name).parts
                        or (kind == "evidence" and not name.startswith("evidence/"))
                        or not isinstance(checksum, str) or not SHA256.fullmatch(checksum)):
                    issues.append(f"invalid {kind} checksum entry")
                    continue
                original = directory / name
                path = original.resolve(strict=True)
                if (not path.is_relative_to(directory) or not path.is_file()
                        or any(part.is_symlink() for part in (original, *original.parents))):
                    issues.append(f"{kind} file escapes its directory: {name}")
                    continue
                if sha256(path) != checksum:
                    issues.append(f"{kind} checksum mismatch: {name}")
                    continue
                verified.add(name)
            return verified

        artifacts = check_files(manifest.get("artifacts"), assets, "artifact")
        release_manifest = json.loads((assets / "release-manifest.json").read_text())
        require(release_manifest.get("commit") == commit, "release assets source commit mismatch")
        require(release_manifest.get("binary_release") == inventory["release"], "release assets binary version mismatch")
        distribution = release_manifest.get("release", "")
        require(distribution == inventory["release"] or
                re.fullmatch(re.escape(inventory["release"]) + r"-rc\.[1-9][0-9]*", distribution),
                "release assets distribution version mismatch")
        expected = {f"esf_factory_{distribution.removeprefix('v')}_{platform}.tar.gz"
                    for platform in PLATFORMS}
        expected.update(("release-manifest.json", "dependency-inventory.json", "sbom.cdx.json", "checksums.txt"))
        require(expected <= artifacts, "qualification does not bind the complete release set")
        require((assets / "dependency-inventory.json").read_bytes() ==
                (root / "release/inventory.json").read_bytes(), "release assets inventory mismatch")

        evidence = check_files(manifest.get("evidence"), manifest_path.parent, "evidence")
        issues.extend(verify_harness_improvement(manifest.get("harness_improvement"),
                      manifest_path.parent, evidence, commit))
        def verified_refs(refs):
            return isinstance(refs, list) and bool(refs) and all(isinstance(ref, str) and ref in evidence for ref in refs)
        gates = manifest.get("gates", {})
        require(isinstance(gates, dict), "qualification gates are missing")
        for name in GATES:
            gate = gates.get(name, {}) if isinstance(gates, dict) else {}
            require(isinstance(gate, dict) and gate.get("status") == "passed", f"qualification {name} is not passed")
            refs = gate.get("evidence", []) if isinstance(gate, dict) else []
            require(verified_refs(refs),
                    f"qualification {name} has no verified evidence")

        for target in TARGETS:
            soak = gates.get("soak_24h", {}).get("targets", {}).get(target, {})
            duration = soak.get("duration_seconds", 0)
            require(isinstance(duration, (int, float)) and not isinstance(duration, bool)
                    and math.isfinite(duration) and duration >= 86400,
                    f"{target} soak is shorter than 24 hours")
            require(soak.get("unexpected_failures") == 0 and soak.get("unreconciled_sandboxes") == 0,
                    f"{target} soak has failures or missing cleanup evidence")
            trials = gates.get("performance", {}).get("targets", {}).get(target, {})
            for concurrency in ("1", "4", "8"):
                trial = trials.get(concurrency, {})
                require(type(trial.get("batches")) is int and trial["batches"] >= 30,
                        f"{target} concurrency {concurrency} needs 30 batches")
                require(trial.get("unexpected_failures") == 0 and trial.get("unreconciled_sandboxes") == 0,
                        f"{target} concurrency {concurrency} has failures or missing cleanup evidence")
                for metric in ("p95_duration_seconds", "peak_worker_rss_bytes"):
                    before, after = trial.get("baseline_" + metric), trial.get("candidate_" + metric)
                    valid = all(isinstance(v, (int, float)) and not isinstance(v, bool) and
                                math.isfinite(v) and v > 0 for v in (before, after))
                    require(valid and after <= before * 1.20,
                            f"{target} concurrency {concurrency} {metric} is missing or regressed by more than 20%")

        images = manifest.get("images", {})
        for name in inventory["images"]:
            image = images.get(name, {})
            digest = image.get("digest", "")
            require(isinstance(digest, str) and digest.startswith("sha256:") and SHA256.fullmatch(digest[7:]),
                    f"image {name} has no qualified digest")
            platforms = ("linux/amd64",) if name == "cube_template" else IMAGE_PLATFORMS
            for platform in platforms:
                refs = image.get("sboms", {}).get(platform, [])
                require(verified_refs(refs),
                        f"image {name} {platform} has no verified SBOM")
                measured = image.get("platforms", {}).get(platform, {})
                platform_digest = measured.get("digest", "")
                require(isinstance(platform_digest, str) and platform_digest.startswith("sha256:")
                        and SHA256.fullmatch(platform_digest[7:]),
                        f"image {name} {platform} has no manifest digest")
                require(verified_refs(measured.get("security_evidence")),
                        f"image {name} {platform} has no verified vulnerability assessment")
        template = manifest.get("template", {})
        require(bool(template.get("id")) and SHA256.fullmatch(template.get("sha256", "")),
                "qualified template identity is missing")
        require(template.get("recipe_sha256") == inventory["templates"]["recipe_sha256"],
                "qualified template recipe mismatch")
        require(manifest.get("transitive_licenses") == "reviewed", "transitive licenses are not reviewed")
    except (OSError, ValueError, TypeError, KeyError, AttributeError, subprocess.SubprocessError) as exc:
        issues.append(f"cannot verify qualification manifest: {type(exc).__name__}")
    return issues
