"""Generate the installed userspace identity inside the Cube builder."""
import hashlib
import importlib.metadata
import json
from pathlib import Path
import platform

root = Path("/opt/security-runtime")
source_archive = Path("/tmp/opencyber-source.tar.gz")
source_digest = hashlib.sha256(source_archive.read_bytes()).hexdigest()
if source_digest != "1c0fdac511147b99cc4f4285305c16962e0f7a46e192c7e5d99fecb9150fa0e7":
    raise RuntimeError("source archive does not match the selected source pin")
wheels = sorted((root/"wheels").glob("*.whl"))
if not wheels:
    raise RuntimeError("builder wheel artifacts required; do not regenerate the inventory after packaging")
manifest = {
    "schema": "esf.opencyber-userspace/v1",
    "source_commit": "09692d89e3cb9dce3506cd4e6fc92c9e4c3c16b6",
    "source_archive_sha256": source_digest,
    "python_base_index": "docker.io/library/python@sha256:73aa5faee130c60da030e105ec971ccf57473fbd22ce73bd6fc9ba99a40b838d",
    "python_base_amd64": "docker.io/library/python@sha256:17cbbc787c42807b179b559a5356f87804f01812cbcabd57f2becd3605ce65cd",
    "python": platform.python_version(),
    "recorded_catalog_sha256": "6ff0d628d1e93da78345b05ad63cdcd27adade55de29938ead97c4a7560b6660",
    "compact_catalog_sha256": "1c50da349bf4e7e162e34f3554840d7190926ddd559c7b0f23628ea91bb92837",
    "packages": {d.metadata["Name"]:d.version for d in importlib.metadata.distributions()},
    "wheels": {p.name:hashlib.sha256(p.read_bytes()).hexdigest() for p in wheels},
    "scope": "native HTTP/API, supplied JSONL logs, selected evidence; no external executable or browser/hardware qualification",
}
(root/"runtime.json").write_text(json.dumps(manifest,sort_keys=True,indent=2)+"\n",encoding="utf-8")
