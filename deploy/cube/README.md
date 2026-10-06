# Versioned CubeSandbox template

The opt-in Pi v1 candidate uses `Dockerfile.pi-v1` and a separate candidate
inventory. See [Pi installation and qualification](../../docs/harness-pi.md).

`Dockerfile` builds the Linux/amd64 ESF v0.6.1 template from a pinned
CubeSandbox base. It includes Git, CA certificates, Python, the command-output
limiter tools, OpenCode 2.0.24, and Unreal 0.3.1. The Docker build verifies
both executable digests against the release inventory. Provider credentials
are absent from the image; CubeEgress injects them at runtime.

Build and attest the candidate image through the `Candidate images` workflow.
Record its immutable digest, the package versions in
`/opt/esf/apt-package-versions.txt`, and the resulting READY Cube template ID
in the post-build qualification manifest. The source inventory records only
immutable build inputs. Set the template's DNS resolver explicitly for the
deployment; a healthy DNS service does not correct a template using another
resolver. Create the Cube template from the **digest**, not a
mutable tag, using the operator's `cubemastercli tpl create-from-image` flow:

```sh
cubemastercli tpl create-from-image \
  --image ghcr.io/mitkox/esf-cube-template@sha256:<qualified-digest> \
  --writable-layer-size 1G \
  --expose-port 49999 --expose-port 49983 --probe 49983
cubemastercli tpl watch --job-id <job-id>
```

Set `cube.template_id` only after the build reports READY. Configure agents
with `preinstalled = true` and their in-template paths. Run `factory agents
verify` to check both hashes in a disposable sandbox. Verify `git`, CA trust,
`sha256sum`, `head`, `mkfifo`, and Python as part of template qualification.
Then run the full Cube 0.7.2 lifecycle and agent acceptance
matrix on both VM and Kubernetes. Templates and image digests change only with
requalification. Linux/arm64 agent packages and runner checksums need their
own inventory and acceptance results before an arm64 template is supported.

Cube's [template guide](https://github.com/TencentCloud/CubeSandbox/blob/master/docs/guide/templates.md)
describes the OCI image build, snapshot, and READY lifecycle.
