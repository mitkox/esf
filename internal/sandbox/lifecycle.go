package sandbox

import "context"

// Suspender is implemented by providers that can checkpoint a live sandbox and
// later bring it back without rebuilding it.
//
// Suspend exists for cost control, not for convenience: an agent run is idle
// most of the time (model latency, network, a human review gate), and holding a
// microVM through that idle period is the single largest avoidable cost in a
// factory. Providers that cannot checkpoint simply omit this interface; the
// factory detects support by type assertion and records a SKIPPED outcome
// rather than pretending the sandbox was suspended.
type Suspender interface {
	// Suspend checkpoints the sandbox identified by sandboxID. It must be
	// idempotent: suspending an already-suspended sandbox succeeds.
	Suspend(ctx context.Context, sandboxID string) error
	// Resume brings a suspended sandbox back. It must be idempotent for a
	// running sandbox, because a retried activity may resume twice.
	Resume(ctx context.Context, sandboxID string) error
}

// Previewer is implemented by providers that can expose a port inside a live
// sandbox over the deployment's ingress.
//
// Preview is deliberately an operator capability, not a task capability: a
// caller must never be able to publish a sandbox to the network on its own.
// The returned URL is recorded as evidence and is the only address a human
// should use to reach a running sandbox.
type Previewer interface {
	// PreviewURL returns an HTTP(S) URL that reaches the given port inside the
	// sandbox, creating the mapping if the provider requires it.
	PreviewURL(ctx context.Context, sandboxID string, port int) (string, error)
}

// Stater is implemented by providers that can report a sandbox's lifecycle
// state without transitioning it.
//
// It exists because some provider SDKs auto-resume a suspended sandbox on
// connect. An operator asking "is this run still awake?" or "show me the app"
// must not be the reason a checkpointed sandbox starts costing money again, so
// the factory checks state through a read-only path before it connects.
type Stater interface {
	SandboxState(ctx context.Context, sandboxID string) (string, error)
}

// StatePaused is the provider state reported for a checkpointed sandbox.
const StatePaused = "paused"

// StateRunning is the provider state reported for a live sandbox.
const StateRunning = "running"
