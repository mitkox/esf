# Versioned CubeSandbox template

The opt-in Pi v1 candidate uses `Dockerfile.pi-v1` and a separate candidate
inventory. See [Pi installation and qualification](../../docs/harness-pi.md).

`Dockerfile` builds the Linux/amd64 ESF v1.0.0-beta.1 template from a pinned
CubeSandbox base (`cubesandbox-base` 2026.16). It includes Git, CA certificates,
Python, the command-output limiter tools, OpenCode 2.2.5, and Unreal 0.3.1.
The Docker build verifies the portable glibc x64-baseline OpenCode package and
every executable digest against the release inventory. Provider credentials
are absent from the image; CubeEgress injects them at runtime.

The pinned vendor base ships `envd` built with Go 1.25.4, and the published
Unreal agent runner is built with Go 1.27.0. Both carry Go Standard Library
advisories that have fixes only in newer toolchains. Because no newer
`cubesandbox-base` tag exists, this recipe rebuilds both binaries from their
pinned upstream sources with the pinned Go 1.27.3 toolchain and copies the
rebuilt static binaries over the vendor payload. Every source archive and
resulting binary is checksum-pinned in `Dockerfile`; the rebuild does not
modify the rest of the vendor layer. Provenance:

| Payload | Upstream source | Rebuilt with |
| --- | --- | --- |
| `envd` | `github.com/e2b-dev/infra` tag `2026.16`, commit `b8ca332f…` | Go 1.27.3, `CGO_ENABLED=0` |
| `unreal-agent-runner` | `github.com/unreallabsai/unreal-agent` tag `v0.3.1`, commit `55747f5f…` | Go 1.27.3, `CGO_ENABLED=0` |

The upstream Unreal release archive/binary pins remain authoritative for the
operator install path (`scripts/install-agents.sh`); the template payload uses
the rebuilt binary and is verified by its own digest. If a rebuilt `envd` ever
fails Cube sandbox lifecycle compatibility, revert this stage to the vendor
base, record `envd` as inherited platform risk with an owner and GA gate, and
requalify before release.

Build and attest the candidate image through the `Candidate images` workflow.
Record its immutable digest, the package versions in
`/opt/esf/apt-package-versions.txt`, the `envd`/Unreal source and binary pins,
and the resulting READY Cube template ID in the post-build qualification
manifest. The source inventory records only immutable build inputs. Set the
template's DNS resolver explicitly for the deployment; a healthy DNS service
does not correct a template using another resolver. Create the Cube template
from the **digest**, not a mutable tag, using the operator's
`cubemastercli tpl create-from-image` flow. Pass `--enable-inject-envd=false` so
the template keeps the rebuilt `envd` instead of replacing it with the CLI's
embedded binary:

```sh
cubemastercli tpl create-from-image \
  --image ghcr.io/mitkox/esf-cube-template@sha256:<qualified-digest> \
  --writable-layer-size 1G \
  --expose-port 49999 --expose-port 49983 --probe 49983 \
  --enable-inject-envd=false
cubemastercli tpl watch --job-id <job-id>
```

Set `cube.template_id` only after the build reports READY. Configure agents
with `preinstalled = true` and their in-template paths. Run `factory agents
verify` to check both hashes in a disposable sandbox. Verify `git`, CA trust,
`sha256sum`, `head`, `mkfifo`, and Python as part of template qualification.
Then run the full Cube 0.7.3 lifecycle and agent acceptance matrix on both VM
and Kubernetes. Templates and image digests change only with requalification.
Linux/arm64 agent packages and runner checksums need their own inventory and
acceptance results before an arm64 template is supported.

Cube's [template guide](https://github.com/TencentCloud/CubeSandbox/blob/master/docs/guide/templates.md)
describes the OCI image build, snapshot, and READY lifecycle.
