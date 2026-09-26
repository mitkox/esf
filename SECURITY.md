# Security

## Reporting

Report vulnerabilities privately through
[GitHub Security Advisories](https://github.com/mitkox/esf/security/advisories/new).
Do not include real credentials or private repository data in reports, and do
not open public issues for unpatched vulnerabilities.

## Supported versions

ESF is under active development. Only the latest release and the latest
commit on `main` receive security fixes. Go 1.26.6 is the minimum supported
toolchain. Dependency and toolchain minimums may increase when a security fix
requires it.

## Trust model

Machinist runs configured coding commands with the operating-system permissions,
tools, environment, and credentials of the worker host user. Command prompts,
executor commands, repository mappings, and worker configuration are trusted
operator policy. A submitted work prompt is untrusted input, but the selected
command can still act on it using every capability available to its process.

The control plane binds only to loopback. Browser requests use a random CSRF
token, while CLI and worker requests use a shared bearer token. A managed worker
may connect to a non-loopback control plane only over HTTPS. Do not expose the
control plane through an untrusted proxy or network boundary.

Repository mappings constrain Machinist assignment and path resolution. They do
not sandbox a command from other files or tools available to the worker OS user.
Use OS permissions, repository permissions, and narrowly scoped credentials to
enforce capability boundaries.

## Local data

Machinist state defaults to `~/.machinist`. Protect this directory because it
may contain task prompts, command output, control-plane state, bearer tokens,
repository paths, and unpublished work. Configuration and token files should
remain readable only by the worker host user.

Provider CLIs own their credentials. Machinist does not request or persist
provider API tokens.

See [ARCHITECTURE.md](ARCHITECTURE.md) for the implemented security boundaries.

## Factory security

The factory runs coding agents in CubeSandbox microVMs. Temporal authentication,
encrypted workflow payloads, evidence storage, credential provisioning and
operational boundaries are described in [the deployment guide](docs/production-deployment.md).

Optional QMS uses Linux Unix-socket peer credentials and operator-owned UID role
bindings. Run it under a dedicated worker identity, restrict the socket and
private SQLite/object directories, and keep host administrators trusted. Sharing
an OS account shares approval identity. Temporal signals and actor names supplied
by clients confer no approval authority.

Candidate source, task prompts and evidence can contain unpublished work even
when they contain no detected credentials. Keep the database, object store,
exports and integration-test artifacts private. Secret detection is a defense
in depth, not a guarantee that arbitrary content is safe to publish.

V1 readiness statements are unsigned and must be obtained from the trusted
authority. They do not authorize deployment or establish regulatory compliance.
See [quality operations](docs/quality-operations.md) for setup and
[the assurance ADR](docs/adr/0004-assurance-control-plane.md) for limitations,
including reviewer filesystem isolation and the single-host trust model.
