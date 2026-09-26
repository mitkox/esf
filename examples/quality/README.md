# Complete R2 and R3 fixtures

These harnesses deliberately produce a fixed patch and fixed review verdict.
Use them only in a registered scratch repository to exercise the control plane.
For real work, replace both harnesses with qualified author/reviewer tools and
replace the syntax profile with the required tests, scanners and SBOM tooling.

## Prepare

1. Copy `author.sh` and `reviewer.py` to `/opt/esf/quality-fixtures` on the worker
   host. Keep them operator-owned; the worker reads and freezes their bytes.
2. Create `/srv/esf/quality-fixture` as an ordinary Git repository with one
   initial committed file. Record `git rev-parse HEAD` in both request JSONs.
   Both runs can use the same baseline; delivered patches are not automatically
   applied to the host repository.
3. Copy `factory.toml` and `policy.yaml` together to the worker configuration
   directory. Replace the Cube template/proxy, paths, socket group and actual
   UID bindings. The worker needs durable private storage and a worker-owned
   `/run/esf` directory with client-group traversal permission.
4. The Cube template must support installing `git` and `python3`. The fixture
   scripts are pushed into each relevant fresh sandbox.
5. Validate and start a dedicated worker on the fixture task queue:

```sh
factory quality policy validate /etc/esf/quality/policy.yaml
factory --config /etc/esf/quality/factory.toml worker
```

## R2

From the submitter OS account:

```sh
factory quality --socket /run/esf/quality.sock submit -f r2.json
factory quality --socket /run/esf/quality.sock status fixture-r2-001
factory quality --socket /run/esf/quality.sock evidence fixture-r2-001
factory quality --socket /run/esf/quality.sock attestation fixture-r2-001 > r2.statement.json
```

The author commits `quality-fixture.txt`. Its change matches no explicit rule
and becomes R2. Syntax verification and independent review run in fresh VMs.
After cleanup, the gates-only decision becomes approved and is committed with
the manifest and statement. Export may finish afterward.

## R3

From the submitter OS account:

```sh
factory quality --socket /run/esf/quality.sock submit -f r3.json
factory quality --socket /run/esf/quality.sock show fixture-r3-001
```

The author commits `auth/quality-fixture.txt`. The explicit `auth/**` rule selects
R3. After syntax verification, independent review and destruction of all VMs,
the run waits for an independent maintainer.

From the maintainer OS account:

```sh
factory quality --socket /run/esf/quality.sock approve fixture-r3-001 \
  --operation fixture-r3-approval --reason "Inspected candidate and fixture receipts"
factory quality --socket /run/esf/quality.sock attestation fixture-r3-001 > r3.statement.json
```

The submitter cannot approve their own R3 run, even if assigned the maintainer
role. Rejection or expiry produces a rejected quality decision and no readiness
statement. Use a new run ID after remediation; reusing an ID with different
request bytes fails.

The automated equivalent is `TestQualityCubeR2R3` in the integration suite. It
uses real CubeSandbox, Temporal, SQLite and peer-authenticated socket approval,
and verifies workflow history replay.
