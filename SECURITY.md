# Security

## Reporting

Report vulnerabilities privately through
[GitHub Security Advisories](https://github.com/mitkox/esf/security/advisories/new).
Do not include real credentials or private repository data in reports, and do
not open public issues for unpatched vulnerabilities.

## Supported versions

ESF is under active development. Only the latest release and the latest
commit on `master` receive security fixes. Go 1.27.3 is the pinned supported
toolchain. Dependency and toolchain minimums may increase when a security fix
requires it.

## Trust model

The factory worker enforces repository policy for CLI and direct workflow
submissions. Remote URLs must match the operator allowlist. `--local-path`
accepts only an exact canonical repository directory registered in
`repositories.local_paths`; local sources are disabled when that list is empty.
Registration is checked again before the worker reads or bundles host content.
It does not grant access to descendants, adjacent paths or symlink aliases.
Registered repositories, their parent directories and Git metadata must remain
under operator control. A local source exports all repository refs into Cube,
so only register repositories whose full history may be disclosed to the task.
Task submissions cannot supply an executable or redefine operator resources.
Operator configuration declares the repositories, harnesses, templates,
verification profiles, resource limits and egress policy. Task text and
repository content are untrusted.

Coding agents run inside dedicated CubeSandbox microVMs. Repository mappings and
harness allowlists constrain *assignment*; they are not a sandbox. Isolation,
credential injection and egress control are enforced around the microVM and by
the provider, not by trusting the agent process.

## Local data

Factory state defaults to `.factory/` in the selected workspace. It can contain
task prompts, patches, command output, run manifests, review notes, audit
records and credentials staged by the operator. Protect the evidence tree and
`factory.toml`; keep credentials in narrowly scoped, owner-only files outside
Git. Provider CLIs own their credentials; ESF does not request or persist
provider API tokens.

The inherited Machinist control plane, browser UI and `~/.machinist` state were
removed in 0.6.2. They are no longer part of the supported attack surface.

See [ARCHITECTURE.md](ARCHITECTURE.md) for the implemented security boundaries.

## Factory security

The factory runs coding agents in CubeSandbox microVMs. Temporal authentication,
encrypted workflow payloads, evidence storage, credential provisioning and
operational boundaries are described in [the deployment guide](docs/production-deployment.md).

Candidate source, task prompts and evidence can contain unpublished work even
when they contain no detected credentials. Keep the database, object store,
exports and integration-test artifacts private. Secret detection is a defense
in depth, not a guarantee that arbitrary content is safe to publish.

V1 readiness statements are unsigned and must be obtained from the trusted
authority. They do not authorize deployment or establish regulatory compliance.
