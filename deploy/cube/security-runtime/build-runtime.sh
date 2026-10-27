#!/bin/sh
# Execute in the disposable Cube builder, never on the factory host.
set -eu
source_archive=${1:?pinned source archive required}
printf '%s  %s\n' 1c0fdac511147b99cc4f4285305c16962e0f7a46e192c7e5d99fecb9150fa0e7 "$source_archive" | sha256sum -c -
runtime_root=/opt/security-runtime
mkdir -p "$runtime_root/source" "$runtime_root/wheels"
tar -xzf "$source_archive" -C "$runtime_root/source"
python3 -m venv "$runtime_root/venv"
"$runtime_root/venv/bin/python" -m pip wheel --index-url https://pypi.org/simple \
  --wheel-dir "$runtime_root/wheels" "$runtime_root/source"
"$runtime_root/venv/bin/python" -m pip install --no-index \
  --find-links "$runtime_root/wheels" opencyber
"$runtime_root/venv/bin/python" -m pip freeze --all > "$runtime_root/requirements.lock"
(cd "$runtime_root/wheels" && sha256sum ./*.whl) > "$runtime_root/wheels.sha256"
