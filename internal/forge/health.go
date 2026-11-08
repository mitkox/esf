package forge

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// SubscriptionState is the forge's event-subscription state.
type SubscriptionState string

const (
	// SubscriptionUnknown means no subscription state was observed. It is not
	// evidence of a working subscription.
	SubscriptionUnknown SubscriptionState = "unknown"
	// SubscriptionActive means the forge reports the subscription enabled.
	SubscriptionActive SubscriptionState = "active"
	// SubscriptionDisabled means the forge reports the subscription disabled.
	SubscriptionDisabled SubscriptionState = "disabled"
	// SubscriptionProbationary means the forge reports the subscription in a
	// probationary state where deliveries may stop.
	SubscriptionProbationary SubscriptionState = "probationary"
)

// ParseSubscriptionState normalizes an operator- or adapter-supplied state.
// Unrecognized values are unknown, never active.
func ParseSubscriptionState(raw string) SubscriptionState {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(SubscriptionActive):
		return SubscriptionActive
	case string(SubscriptionDisabled):
		return SubscriptionDisabled
	case string(SubscriptionProbationary), "probation":
		return SubscriptionProbationary
	default:
		return SubscriptionUnknown
	}
}

// Valid reports whether the state is one this contract understands.
func (s SubscriptionState) Valid() bool {
	switch s {
	case SubscriptionUnknown, SubscriptionActive, SubscriptionDisabled, SubscriptionProbationary:
		return true
	default:
		return false
	}
}

// HealthStatus is the computed connector status.
type HealthStatus string

const (
	// HealthHealthy means the last successful sweep and a delivery are both
	// within the freshness window.
	HealthHealthy HealthStatus = "healthy"
	// HealthDegraded means the connector needs operator attention.
	HealthDegraded HealthStatus = "degraded"
	// HealthUnknown means there is not enough observation to judge.
	HealthUnknown HealthStatus = "unknown"
)

// HealthEvent names the connector activity behind one health observation.
type HealthEvent string

const (
	// HealthEventSweep is a reconciliation sweep attempt.
	HealthEventSweep HealthEvent = "sweep"
	// HealthEventDelivery is a webhook delivery or sweep delivery attempt.
	HealthEventDelivery HealthEvent = "delivery"
)

// HealthObservation is one connector liveness sample.
//
// Push timestamps are the last SUCCESSFUL sweep and the last authenticated
// delivery. A failed sweep must not advance the sweep timestamp: an operator
// reading a fresh timestamp would be told reconciliation is working when it is
// not. Event, Successful and Detail describe the attempt that produced this
// sample.
type HealthObservation struct {
	// Connector is the operator-facing connector name.
	Connector string
	// SweepInterval is the connector's reconciliation cadence. It sizes the
	// freshness window; zero uses the operator default.
	SweepInterval time.Duration
	// Event names the attempt behind this observation. Empty means the caller
	// reports only successful timestamps.
	Event HealthEvent
	// Successful reports the outcome of that attempt.
	Successful bool
	// Detail is bounded operator detail about that attempt.
	Detail string
	// LastSuccessfulSweep is the last reconciliation that completed and saved
	// its watermark.
	LastSuccessfulSweep time.Time
	// LastDelivery is the last authenticated webhook delivery.
	LastDelivery time.Time
	// Subscription is the forge-reported event-subscription state.
	Subscription SubscriptionState
	// SubscriptionDetail is bounded operator detail about that state.
	SubscriptionDetail string
	// QueueDepth is the pending work-item count per operator scope.
	QueueDepth map[string]int
}

// ConnectorHealth is the read-only connector health projection.
//
// It carries no credential, token-file path, or forge response body. Queue
// depths are per operator scope and are bounded by the caller.
type ConnectorHealth struct {
	Connector           string            `json:"connector"`
	Status              HealthStatus      `json:"status"`
	Reason              string            `json:"reason,omitempty"`
	Detail              string            `json:"detail,omitempty"`
	LastSuccessfulSweep *time.Time        `json:"last_successful_sweep,omitempty"`
	LastDelivery        *time.Time        `json:"last_delivery,omitempty"`
	Subscription        SubscriptionState `json:"subscription"`
	SubscriptionDetail  string            `json:"subscription_detail,omitempty"`
	QueueDepth          map[string]int    `json:"queue_depth,omitempty"`
	EvaluatedAt         time.Time         `json:"evaluated_at"`
}

const (
	// healthStalenessIntervals is how many sweep intervals may pass without a
	// fresh observation before health is degraded.
	healthStalenessIntervals = 3
	// defaultHealthSweepInterval sizes the freshness window when the caller
	// does not report the connector's configured interval. It matches the
	// operator default.
	defaultHealthSweepInterval = time.Minute
)

// EvaluateHealth computes connector liveness from one observation.
//
// Reconciliation is primary: a failed sweep attempt degrades immediately, and a
// successful sweep older than the freshness window degrades even when a newer
// delivery exists. A delivery may be older than the newest sweep and still be
// within the same window: a quiet healthy connector is not degraded just because
// a later sweep succeeded. A successful sweep with no delivery at all is
// degraded because the delivery path has never been proven.
func EvaluateHealth(now time.Time, observation HealthObservation) ConnectorHealth {
	health := ConnectorHealth{
		Connector:    strings.TrimSpace(observation.Connector),
		Subscription: observation.Subscription,
		EvaluatedAt:  now.UTC(),
	}
	if !health.Subscription.Valid() {
		health.Subscription = SubscriptionUnknown
	}
	if !observation.LastSuccessfulSweep.IsZero() {
		sweep := observation.LastSuccessfulSweep.UTC()
		health.LastSuccessfulSweep = &sweep
	}
	if !observation.LastDelivery.IsZero() {
		delivery := observation.LastDelivery.UTC()
		health.LastDelivery = &delivery
	}
	health.SubscriptionDetail = boundedHealthText(observation.SubscriptionDetail)
	health.QueueDepth = copyQueueDepth(observation.QueueDepth)

	switch health.Subscription {
	case SubscriptionDisabled:
		health.Status = HealthDegraded
		health.Reason = "subscription_disabled"
		health.Detail = "the forge reports the event subscription is disabled"
		return health
	case SubscriptionProbationary:
		health.Status = HealthDegraded
		health.Reason = "subscription_probationary"
		health.Detail = "the forge reports the event subscription is probationary; deliveries may stop"
		return health
	}

	if observation.Event == HealthEventSweep && !observation.Successful {
		health.Status = HealthDegraded
		health.Reason = "sweep_failed"
		health.Detail = boundedHealthText(observation.Detail)
		if health.Detail == "" {
			health.Detail = "the reconciliation sweep attempt failed"
		}
		return health
	}

	window := healthStalenessWindow(observation.SweepInterval)
	switch {
	case observation.LastSuccessfulSweep.IsZero() && observation.LastDelivery.IsZero():
		health.Status = HealthUnknown
		health.Reason = "no_observations"
		health.Detail = "no successful sweep or delivery has been observed"
	case observation.LastSuccessfulSweep.IsZero():
		health.Status = HealthDegraded
		health.Reason = "no_successful_sweep"
		health.Detail = "a delivery was observed but no successful reconciliation sweep"
	case now.Sub(observation.LastSuccessfulSweep.UTC()) > window:
		health.Status = HealthDegraded
		health.Reason = "stale_sweep"
		health.Detail = "no successful reconciliation sweep within the freshness window"
	case observation.LastDelivery.IsZero():
		health.Status = HealthDegraded
		health.Reason = "no_delivery_since_sweep"
		health.Detail = "no delivery has been authenticated since the last successful sweep"
	case now.Sub(observation.LastDelivery.UTC()) > window:
		health.Status = HealthDegraded
		health.Reason = "no_recent_delivery"
		health.Detail = "no authenticated delivery within the freshness window"
	default:
		health.Status = HealthHealthy
		health.Reason = "recent_delivery"
		health.Detail = "a delivery arrived within the freshness window"
	}
	return health
}

// healthStalenessWindow is how old the last successful sweep or delivery may be
// before health is degraded. It is three sweep intervals so one slow or failed
// attempt does not degrade a connector that is otherwise working.
func healthStalenessWindow(sweepInterval time.Duration) time.Duration {
	if sweepInterval <= 0 {
		sweepInterval = defaultHealthSweepInterval
	}
	return healthStalenessIntervals * sweepInterval
}

// Degraded reports whether the connector needs operator attention.
func (h ConnectorHealth) Degraded() bool { return h.Status == HealthDegraded }

func copyQueueDepth(input map[string]int) map[string]int {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]int, len(input))
	for scope, depth := range input {
		scope = boundedHealthText(scope)
		if scope == "" {
			continue
		}
		if depth < 0 {
			depth = 0
		}
		out[scope] = depth
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// HealthDirName is the durable connector-health directory under the factory
// data directory.
const HealthDirName = "connector-health"

const (
	healthSnapshotFileName  = "health.json"
	maxHealthSnapshotBytes  = 1 << 20
	maxHealthConnectors     = 128
	maxHealthTextBytes      = 512
	maxHealthConnectorBytes = 128
)

// HealthSnapshot is the last observation per connector.
type HealthSnapshot struct {
	Connectors []ConnectorHealth `json:"connectors"`
	UpdatedAt  time.Time         `json:"updated_at"`
}

// SaveConnectorHealth merges one connector report into the durable health
// snapshot. The write is atomic and owner-only.
func SaveConnectorHealth(dataDir string, report ConnectorHealth) error {
	if strings.TrimSpace(dataDir) == "" {
		return fmt.Errorf("connector health: data directory is required")
	}
	report = normalizeConnectorHealth(report)
	if err := validateConnectorHealth(report); err != nil {
		return err
	}
	snapshot, err := LoadHealthSnapshot(dataDir)
	if err != nil {
		return err
	}
	replaced := false
	for i, existing := range snapshot.Connectors {
		if existing.Connector == report.Connector {
			snapshot.Connectors[i] = report
			replaced = true
			break
		}
	}
	if !replaced {
		if len(snapshot.Connectors) >= maxHealthConnectors {
			return fmt.Errorf("connector health: snapshot exceeds %d connectors", maxHealthConnectors)
		}
		snapshot.Connectors = append(snapshot.Connectors, report)
	}
	sort.Slice(snapshot.Connectors, func(i, j int) bool {
		return snapshot.Connectors[i].Connector < snapshot.Connectors[j].Connector
	})
	snapshot.UpdatedAt = time.Now().UTC()

	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return fmt.Errorf("connector health: encode snapshot: %w", err)
	}
	data = append(data, '\n')
	if len(data) > maxHealthSnapshotBytes {
		return fmt.Errorf("connector health: snapshot exceeds %d bytes", maxHealthSnapshotBytes)
	}

	dir := filepath.Join(dataDir, HealthDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("connector health: create %s: %w", dir, err)
	}
	temp, err := os.CreateTemp(dir, ".connector-health-*")
	if err != nil {
		return fmt.Errorf("connector health: create temporary snapshot: %w", err)
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return fmt.Errorf("connector health: set snapshot mode: %w", err)
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return fmt.Errorf("connector health: write snapshot: %w", err)
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return fmt.Errorf("connector health: sync snapshot: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("connector health: close snapshot: %w", err)
	}
	if err := os.Rename(tempName, filepath.Join(dir, healthSnapshotFileName)); err != nil {
		return fmt.Errorf("connector health: replace snapshot: %w", err)
	}
	directory, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("connector health: open snapshot directory: %w", err)
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return fmt.Errorf("connector health: sync snapshot directory: %w", err)
	}
	return nil
}

// LoadHealthSnapshot reads the durable connector-health snapshot. A missing
// snapshot is an empty snapshot, not an error.
func LoadHealthSnapshot(dataDir string) (HealthSnapshot, error) {
	snapshot := HealthSnapshot{Connectors: []ConnectorHealth{}}
	if strings.TrimSpace(dataDir) == "" {
		return snapshot, fmt.Errorf("connector health: data directory is required")
	}
	path := filepath.Join(dataDir, HealthDirName, healthSnapshotFileName)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return snapshot, nil
	}
	if err != nil {
		return snapshot, fmt.Errorf("connector health: read snapshot: %w", err)
	}
	if len(data) > maxHealthSnapshotBytes {
		return snapshot, fmt.Errorf("connector health: snapshot exceeds %d bytes", maxHealthSnapshotBytes)
	}
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return HealthSnapshot{Connectors: []ConnectorHealth{}}, fmt.Errorf("connector health: decode snapshot: %w", err)
	}
	if snapshot.Connectors == nil {
		snapshot.Connectors = []ConnectorHealth{}
	}
	if len(snapshot.Connectors) > maxHealthConnectors {
		return HealthSnapshot{Connectors: []ConnectorHealth{}}, fmt.Errorf("connector health: snapshot exceeds %d connectors", maxHealthConnectors)
	}
	for i, report := range snapshot.Connectors {
		snapshot.Connectors[i] = normalizeConnectorHealth(report)
	}
	sort.Slice(snapshot.Connectors, func(i, j int) bool {
		return snapshot.Connectors[i].Connector < snapshot.Connectors[j].Connector
	})
	return snapshot, nil
}

func normalizeConnectorHealth(report ConnectorHealth) ConnectorHealth {
	report.Connector = strings.TrimSpace(report.Connector)
	report.Reason = boundedHealthText(report.Reason)
	report.Detail = boundedHealthText(report.Detail)
	report.SubscriptionDetail = boundedHealthText(report.SubscriptionDetail)
	if !report.Subscription.Valid() {
		report.Subscription = SubscriptionUnknown
	}
	if !report.Status.valid() {
		report.Status = HealthUnknown
	}
	report.QueueDepth = copyQueueDepth(report.QueueDepth)
	if report.EvaluatedAt.IsZero() {
		report.EvaluatedAt = time.Now().UTC()
	} else {
		report.EvaluatedAt = report.EvaluatedAt.UTC()
	}
	if report.LastSuccessfulSweep != nil {
		sweep := report.LastSuccessfulSweep.UTC()
		report.LastSuccessfulSweep = &sweep
	}
	if report.LastDelivery != nil {
		delivery := report.LastDelivery.UTC()
		report.LastDelivery = &delivery
	}
	return report
}

func validateConnectorHealth(report ConnectorHealth) error {
	switch {
	case report.Connector == "":
		return fmt.Errorf("connector health: connector name is required")
	case len(report.Connector) > maxHealthConnectorBytes:
		return fmt.Errorf("connector health: connector name exceeds %d bytes", maxHealthConnectorBytes)
	case strings.ContainsAny(report.Connector, "\x00\r\n\t/\\"):
		return fmt.Errorf("connector health: connector name contains unsafe characters")
	}
	return nil
}

func (s HealthStatus) valid() bool {
	switch s {
	case HealthHealthy, HealthDegraded, HealthUnknown:
		return true
	default:
		return false
	}
}

// boundedHealthText makes a display field valid UTF-8, removes control
// characters, and bounds it so a health snapshot cannot grow without limit.
func boundedHealthText(value string) string {
	value = strings.ToValidUTF8(strings.TrimSpace(value), "\uFFFD")
	value = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, value)
	value = strings.TrimSpace(value)
	if len(value) <= maxHealthTextBytes {
		return value
	}
	cut := maxHealthTextBytes
	for cut > 0 && (value[cut]&0xC0) == 0x80 {
		cut--
	}
	return strings.TrimSpace(value[:cut])
}
