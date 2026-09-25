# Offline DSPy implementation-brief lab

This optional lab generates a structured implementation brief with a **generative**
model, submits it with the original task to ESF's existing agent harness, and
scores the resulting run evidence. TypeSafe/Jev is a decision model and cannot
generate these briefs; this lab needs a separate generative-model credential.
It never runs as part of the factory worker or promotes a prompt automatically.
Run its evaluations against a worker with intake disabled: generated briefs may
contain repository context that the Jev intake pilot is not intended to receive.

Install the pinned DSPy 3.4.0 package in a separate virtual environment:

```sh
python3.12 -m venv /opt/factory/brief-lab-venv
/opt/factory/brief-lab-venv/bin/pip install /path/to/esf/tools/brief_lab
```

Supply the selected generative provider's key through your secret manager into
the lab process environment. Do not put it in a fixture, command argument, Git,
or a generated task file. The TypeSafe key is not used here.

## Generate one brief

Create an operator-reviewed context file containing only repository facts you
want sent to the generative model. Then run:

```sh
/opt/factory/brief-lab-venv/bin/python -m esf_brief_lab \
  --model '<provider>/<model>' generate \
  --task-file /secure/task.txt --context-file /secure/context.txt \
  --output /secure/task-with-brief.txt
factory run --local-path /path/to/repo --rev FULL_SHA \
  --task-file /secure/task-with-brief.txt --agent opencode2 \
  --verification default
```

The generated task file includes the original task verbatim and marks the brief
as advisory. Review it before submission. A saved DSPy program can be loaded
with `generate --program /secure/brief-program.json`.

## Optimize against factory evidence

Create an owner-only JSONL fixture file outside Git. Each line contains:

```json
{"id":"greeting-1","task":"Fix the greeting and satisfy the acceptance test","repository_context":"Python project; greeting.py exports greeting()","local_path":"/absolute/path/to/disposable/repo","revision":"FULL_COMMIT_SHA","agent":"opencode2","verification":"acceptance","acceptance_gates":["greeting-test"]}
```

Use at least four distinct task texts. Each fixture must point to a disposable local
repository and exact revision; its named verification profile must contain the
listed acceptance gates. Check that those gates fail on the unchanged baseline;
otherwise they cannot distinguish a useful patch from unrelated changes. Runs
that produce no patch score zero. The optimizer invokes `factory run` repeatedly, so
configure a real Cube/Temporal deployment and spending limits first. It uses
GEPA with a bounded metric-call count, keeps a task-level holdout invisible to
GEPA, and compares direct agent use, the unoptimized brief, and the optimized
brief on that holdout:

```sh
/opt/factory/brief-lab-venv/bin/python -m esf_brief_lab \
  --model '<provider>/<model>' optimize \
  --fixtures /secure/brief-fixtures.jsonl \
  --factory /usr/local/bin/factory \
  --factory-config /etc/factory/factory.toml \
  --max-metric-calls 16 --max-runs 24 \
  --output /secure/brief-program-v1.json
```

The score requires ESF success and every listed acceptance gate to pass. It
penalizes longer runs and reported inference cost; missing cost is shown as
unknown. A human rejection scores zero when available. The tool saves a
candidate only if it beats both baselines on the untouched holdout mean. The operator decides
whether to use the saved program for future `generate` calls. A timed-out CLI
may leave its Temporal workflow running; inspect the reported run ID before
retrying.
