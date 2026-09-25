// Package sandbox defines the factory's execution-fabric abstraction.
//
// The package deliberately exposes a small surface: create a sandbox, run
// commands and move files in and out of it, then destroy it. CubeSandbox is one
// implementation (see internal/sandbox/cube); tests use an in-memory fake.
//
// The abstraction exists to keep *factory* semantics — run lifecycle, evidence,
// policy — independent of the sandbox vendor. It is not a mirror of any vendor
// SDK: callers should not expect every Cube feature here.
package sandbox

import (
	"context"
	"time"
)

// Spec describes a sandbox to create.
//
// Every field is operator- or factory-derived. Nothing here is supplied by an
// API client, so a caller can never choose a host path, an executable, or a
// sandbox template.
type Spec struct {
	// Template is an operator-approved sandbox template identifier.
	Template string
	// Metadata is attached to the sandbox for auditing and leak detection.
	Metadata map[string]string
	// Env is injected into the sandbox at creation.
	//
	// SECURITY: values placed here are visible to the coding agent. Only
	// operator-approved, per-run credentials may be placed here, and task text
	// must never be used to derive them.
	Env map[string]string
	// IdleTimeout bounds how long the sandbox may live without activity.
	IdleTimeout time.Duration
	// Limits are the requested per-run resource limits. Zero values mean the
	// provider default applies.
	Limits Limits
	// Network expresses the desired egress policy. A zero value means "use the
	// deployment default", which the factory never weakens.
	Network Network
}

// Limits expresses per-run resource ceilings.
//
// Providers that cannot enforce a field ignore it; the factory still applies
// its own wall-clock timeouts, which are the authoritative limit.
type Limits struct {
	CPUMilli  int
	MemoryMiB int
	DiskMiB   int
}

// Network expresses an egress policy request.
//
// The MVP sets no fields: the effective policy of the deployment (public egress
// allowed, internal CIDRs denied) is what the factory wants. The type exists so
// a future allowlist-based policy can be layered in without changing call
// sites.
type Network struct {
	// AllowOut lists explicitly permitted destinations (host, CIDR or domain).
	AllowOut []string `json:"allow_out,omitempty"`
	// DenyOut lists explicitly denied destinations.
	DenyOut []string `json:"deny_out,omitempty"`
	// AllowInternet, when non-nil, requests that public egress be enabled or
	// disabled.
	AllowInternet *bool `json:"allow_internet,omitempty"`
	// Rules are L7 policies evaluated by a provider-side egress proxy. Secrets
	// in inject directives must never be exposed to the sandbox process.
	Rules []NetworkRule `json:"rules,omitempty"`
}

// NetworkRule is a provider-neutral L7 egress rule. Match fields are ANDed.
// Rules are evaluated in order and should therefore put narrow rules first.
type NetworkRule struct {
	Name   string
	Match  NetworkMatch
	Action NetworkAction
}

// NetworkMatch identifies an outbound HTTP request.
type NetworkMatch struct {
	SNI    string
	Host   string
	Method []string
	Path   string
	Scheme string
	Port   int
}

// NetworkAction allows or denies a matching request and may inject headers at
// the trusted egress proxy. Injected secret values are operator-side data.
type NetworkAction struct {
	Allow  bool
	Audit  string
	Inject []HeaderInjection
}

// HeaderInjection describes a header written by the trusted egress proxy.
// Format must contain ${SECRET}; an empty format means the raw secret.
type HeaderInjection struct {
	Header string
	Secret string
	Format string
}

// Info is a point-in-time description of a live sandbox, used for leak
// detection and cleanup verification.
type Info struct {
	ID         string
	Template   string
	State      string
	StartedAt  time.Time
	Metadata   map[string]string
	ProviderID string
}

// Command is a single command to execute inside a sandbox.
//
// Argv is the preferred form and is executed WITHOUT shell interpretation:
// implementations must quote each element so no element can inject shell
// syntax. Script is an explicit escape hatch for multi-statement setup owned by
// the factory itself — never by an API client.
type Command struct {
	// Argv is the command and its arguments. Mutually exclusive with Script.
	Argv []string
	// Script is a shell script supplied by factory code (not by a caller).
	Script string
	// Dir is the working directory.
	Dir string
	// Env is extra environment for this command only.
	Env map[string]string
	// Stdin is data piped to the command's standard input.
	//
	// Providers whose data plane cannot stream raw stdin may ignore this; use
	// StdinPath when the payload must be delivered reliably.
	Stdin []byte
	// StdinPath redirects the command's standard input from a file that
	// already exists inside the sandbox.
	//
	// This is the factory's preferred way to deliver untrusted text (such as a
	// task prompt) to an agent: the text is written with WriteFile and never
	// appears in the command string at all, so there is no interpolation to get
	// wrong. Shell-based providers render it as a quoted redirection.
	StdinPath string
	// Timeout bounds this single command. Zero means the caller's context is
	// the only bound.
	Timeout time.Duration
	// Description is recorded in evidence; it never affects execution.
	Description string
}

// Execution is the recorded outcome of a Command.
type Execution struct {
	ExitCode    int
	Stdout      string
	Stderr      string
	StartedAt   time.Time
	CompletedAt time.Time
	Duration    time.Duration
	// TimedOut reports that the command was killed for exceeding its timeout
	// (or the caller's context deadline).
	TimedOut bool
}

// Succeeded reports whether the command exited zero and was not killed.
func (e Execution) Succeeded() bool { return e.ExitCode == 0 && !e.TimedOut }

// Sandbox is a live, isolated execution environment.
//
// Implementations are not required to be safe for concurrent use by multiple
// goroutines for mutating calls; the factory runs one sandbox per workflow and
// executes commands sequentially.
type Sandbox interface {
	// ID is the provider's identifier for this sandbox.
	ID() string
	// Template is the template the sandbox was created from.
	Template() string
	// Execute runs one command and returns its outcome. A non-zero exit code is
	// reported in Execution, not as an error: a failing test is a result, not a
	// transport failure. An error means the command could not be run at all.
	Execute(ctx context.Context, cmd Command) (Execution, error)
	// WriteFile writes data to path inside the sandbox, creating parents.
	WriteFile(ctx context.Context, path string, data []byte) error
	// ReadFile reads a file from inside the sandbox.
	ReadFile(ctx context.Context, path string) ([]byte, error)
	// Destroy terminates the sandbox. It is idempotent.
	Destroy(ctx context.Context) error
}

// Capabilities advertises optional features a provider supports.
//
// The MVP implements none of the speculative-execution features. They are
// declared here so Phase 2 can detect support rather than assume it.
type Capabilities struct {
	Snapshot bool
	Clone    bool
	Rollback bool
	// CloneMultiple reports whether one call can produce several clones, which
	// Phase 2's fan-out relies on.
	CloneMultiple bool

	// Suspend reports that the provider implements Suspender, so the factory may
	// checkpoint an idle sandbox instead of paying for it through a review gate.
	Suspend bool
	// Resume reports that the provider implements Suspender and can bring a
	// checkpointed sandbox back.
	Resume bool
	// Preview reports that the provider implements Previewer, so an operator may
	// expose a port in a running sandbox for human review.
	Preview bool
}

// Snapshotter is the Phase 2 snapshot seam. It is declared but NOT implemented
// in the MVP; no code path calls it.
type Snapshotter interface {
	Snapshot(ctx context.Context, sandboxID, name string) (string, error)
	Clone(ctx context.Context, sandboxID, snapshotID string, n int) ([]Sandbox, error)
	Rollback(ctx context.Context, sandboxID, snapshotID string) error
}

// NetworkUpdater is implemented by providers that can atomically replace a
// running sandbox's egress policy. The factory uses it to allow setup traffic
// first, then lock the agent phase down before untrusted execution begins.
type NetworkUpdater interface {
	UpdateNetwork(ctx context.Context, network Network) error
}

// Provider creates, reattaches to, and destroys sandboxes.
type Provider interface {
	// Name identifies the provider in evidence (for example "cubesandbox").
	Name() string
	// Capabilities reports optional features.
	Capabilities() Capabilities
	// Create starts a new sandbox.
	Create(ctx context.Context, spec Spec) (Sandbox, error)
	// Reattach returns a handle to an existing sandbox.
	//
	// Durable workflows need this: each Temporal activity is a separate
	// invocation, so a sandbox created by one activity must be reachable from
	// the next by ID alone.
	Reattach(ctx context.Context, sandboxID string) (Sandbox, error)
	// Destroy terminates the sandbox with the given ID. It is idempotent and
	// must succeed if the sandbox is already gone.
	Destroy(ctx context.Context, sandboxID string) error
	// List returns live sandboxes. The factory uses it to verify cleanup and to
	// detect leaks; it must never be used to mutate state.
	List(ctx context.Context) ([]Info, error)
	// Ping verifies the provider is reachable.
	Ping(ctx context.Context) error
}
