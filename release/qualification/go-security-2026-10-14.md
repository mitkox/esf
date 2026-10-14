# Go security patch, 14 October 2026

The PR40 Linux security gate reported 13 reachable standard-library findings with
Go 1.27.1. Go 1.27.2 removes those findings; the same scan then identified 5 reachable
HTTP/2 findings in `golang.org/x/net v0.59.0`. The minimal complete remediation
pins Go 1.27.2 and x/net 0.60.0. No other module versions changed. CI still runs the
same pinned govulncheck 1.8.0 command without exclusions.

All active toolchain pins, supported-version documentation and both release
inventory copies agree. The three Go Docker builders retain Debian Bookworm and
pin the official 1.27.2-bookworm index
`sha256:5cf287a799e6b94384bad13d16b14904c531f51ba65792237e122ce42b392f61`.
The official Go download listing publishes Linux amd64 archive SHA256
`ecbadb99091a3f46e31f5f934b068b1864eafa7995211b39eaddf76996045fe5`.
The locally installed toolchain reports `go version go1.27.2 linux/amd64`.
The archive checksum is recorded upstream metadata, not a claim that this task
reinstalled the host toolchain.

Validation:

- `go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...`: No vulnerabilities found.
- `go build ./...` and `go vet ./...`: passed.
- `scripts/verify-release-inventory.py`: passed.
- Compiled `internal/turnexec` and `internal/sandbox/cube` tests ran inside
  disposable Cube VMs: 11 and 13 top-level tests passed respectively.
- Historical model receipts are unchanged; no model calls were repeated for
  this patch. Hosted CI and Azure acceptance remain separate gates.

Sources: [official Go downloads](https://go.dev/dl/?mode=json&include=all),
[Go HTTP/2 advisory](https://pkg.go.dev/vuln/GO-2026-6617),
[official Docker image definition](https://github.com/docker-library/golang/tree/8380885ec449224702b989d8792d2e1470a46e89/1.27/bookworm).
