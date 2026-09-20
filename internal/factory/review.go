package factory

// ReviewSignalName is the Temporal signal that ends a paused run's review gate.
//
// The signal carries a decision and an optional note. It is a signal rather
// than a query-plus-poll because the workflow must be able to block for an
// arbitrarily long time without consuming a worker slot or a sandbox.
const ReviewSignalName = "review-decision"

// ReviewDecision is the payload of a review decision.
//
// It is deliberately small: a decision, an operator note, and who decided. The
// note is carried into the change record as the rework instruction; it is never
// injected into a prompt automatically, because a human note is untrusted text
// and must go through the same review as any other task text.
type ReviewDecision struct {
	// Decision is HumanResultApproved or HumanResultRejected.
	Decision string `json:"decision"`
	// Note explains a rejection. It is recorded on the change as ReworkNote.
	Note string `json:"note,omitempty"`
	// Actor identifies the human. Best-effort attribution, not authentication.
	Actor string `json:"actor,omitempty"`
}
