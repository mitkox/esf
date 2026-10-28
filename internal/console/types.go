package console

import (
	"strings"
	"time"

	"github.com/mitkox/esf/internal/factory"
	"github.com/mitkox/esf/internal/threatmon"
)

// The console API deliberately has its own data-transfer types rather than
// serialising internal runtime structs wholesale. Two reasons:
//
//  1. Secrets: catalog DTOs are explicit, so a future credential field in
//     factory.Config cannot leak by accident.
//  2. Stability: the console can evolve its presentation without changing the
//     evidence schema that auditors depend on.

// ── Shared ──────────────────────────────────────────────────────────────────

// WindowInfo describes the coverage window a response was computed over.
type WindowInfo struct {
	Key   string     `json:"key"`
	Label string     `json:"label"`
	Start *time.Time `json:"start,omitempty"`
	End   time.Time  `json:"end"`
}

// IndexInfo describes the evidence index a response was computed from.
type IndexInfo struct {
	Runs        int        `json:"runs"`
	Changes     int        `json:"changes"`
	RefreshedAt *time.Time `json:"refreshed_at,omitempty"`
	ScanMS      int64      `json:"scan_ms"`
	ScanErrors  int        `json:"scan_errors"`
	ScanError   string     `json:"scan_error,omitempty"`
	Stale       bool       `json:"stale"`
	OldestRunAt *time.Time `json:"oldest_run_at,omitempty"`
	NewestRunAt *time.Time `json:"newest_run_at,omitempty"`
}

// PageMeta describes list pagination.
type PageMeta struct {
	Limit      int    `json:"limit"`
	Offset     int    `json:"offset"`
	Total      int    `json:"total"`
	NextCursor string `json:"next_cursor,omitempty"`
}

// Facet is one filterable value and its count.
type Facet struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}

// Facets are the filter values available alongside a list response.
type Facets struct {
	States       []Facet `json:"states"`
	Harnesses    []Facet `json:"harnesses"`
	Repositories []Facet `json:"repositories"`
	Scopes       []Facet `json:"scopes"`
	Statuses     []Facet `json:"statuses,omitempty"`
}

// ── Runs ────────────────────────────────────────────────────────────────────

// CleanupSummary is the compact sandbox-cleanup outcome shown with a run.
type CleanupSummary struct {
	Attempted bool   `json:"attempted"`
	Outcome   string `json:"outcome,omitempty"`
	Verified  bool   `json:"verified"`
	Remaining int    `json:"remaining_sandboxes"`
	Error     string `json:"error,omitempty"`
}

// HardeningSummary is the security posture of one run, derived from its
// recorded hardening evidence. A missing evidence block is reported as
// "not measured", never as "clean".
type HardeningSummary struct {
	Measured         bool     `json:"measured"`
	Tripped          bool     `json:"tripped"`
	Findings         int      `json:"findings"`
	MaxSeverity      string   `json:"max_severity,omitempty"`
	EgressViolations int      `json:"egress_violations"`
	GateChecked      bool     `json:"gate_checked"`
	GateAllowed      bool     `json:"gate_allowed"`
	GateModified     []string `json:"gate_modified,omitempty"`
}

// IntakeSummary is the bounded intake-advisory result recorded on a run.
type IntakeSummary struct {
	Status      string  `json:"status,omitempty"`
	Ready       *bool   `json:"ready"`
	TaskType    string  `json:"task_type,omitempty"`
	Ambiguity   string  `json:"ambiguity,omitempty"`
	Model       string  `json:"model,omitempty"`
	DurationMS  int64   `json:"duration_ms,omitempty"`
	ErrorCode   string  `json:"error_code,omitempty"`
	ReadyProb   float64 `json:"ready_probability,omitempty"`
	ReadyConf   float64 `json:"ready_confidence,omitempty"`
	TaskTypeCf  float64 `json:"task_type_confidence,omitempty"`
	AmbiguityCf float64 `json:"ambiguity_confidence,omitempty"`
}

// RunSummary is the small, stable projection of a run used by every list.
type RunSummary struct {
	RunID               string           `json:"run_id"`
	ChangeID            string           `json:"change_id,omitempty"`
	ParentRunID         string           `json:"parent_run_id,omitempty"`
	Scope               string           `json:"scope,omitempty"`
	Repository          string           `json:"repository"`
	RepositoryKind      string           `json:"repository_kind,omitempty"`
	RequestedRevision   string           `json:"requested_revision"`
	BaselineSHA         string           `json:"baseline_sha,omitempty"`
	ResultingSHA        string           `json:"resulting_sha_if_any,omitempty"`
	AgentHarness        string           `json:"agent_harness"`
	AgentVersion        string           `json:"agent_version,omitempty"`
	VerificationProfile string           `json:"verification_profile"`
	SandboxID           string           `json:"sandbox_id,omitempty"`
	FactoryResult       string           `json:"factory_result"`
	AgentResult         string           `json:"agent_result,omitempty"`
	VerificationResult  string           `json:"verification_result,omitempty"`
	CleanupResult       CleanupSummary   `json:"cleanup_result"`
	Hardening           HardeningSummary `json:"hardening"`
	StartedAt           *time.Time       `json:"started_at,omitempty"`
	CompletedAt         *time.Time       `json:"completed_at,omitempty"`
	DurationMS          int64            `json:"duration_ms"`
	ReviewConfigured    bool             `json:"review_configured"`
	ReviewOutcome       string           `json:"review_outcome,omitempty"`
	ReviewNote          string           `json:"review_note,omitempty"`
	BudgetExceeded      []string         `json:"budget_exceeded,omitempty"`
	TokensIn            *int64           `json:"tokens_in"`
	TokensOut           *int64           `json:"tokens_out"`
	InferenceCost       *float64         `json:"inference_cost"`
	Intake              *IntakeSummary   `json:"intake,omitempty"`
	Error               string           `json:"error,omitempty"`
}

// RunActions are the CLI commands an operator can copy from the console. The
// console is read-only in this round: these strings are instructions, never
// executed by the server.
type RunActions struct {
	ApproveCommand string `json:"approve_command"`
	RejectCommand  string `json:"reject_command"`
	CancelCommand  string `json:"cancel_command"`
	Describe       string `json:"describe_command"`
	Logs           string `json:"logs_command"`
}

// TaskDetail is the human-facing task specification recorded with a run.
type TaskDetail struct {
	Task                string     `json:"task"`
	Truncated           bool       `json:"truncated"`
	TaskHash            string     `json:"task_hash,omitempty"`
	AgentHarness        string     `json:"agent_harness,omitempty"`
	AgentModel          string     `json:"agent_model,omitempty"`
	Model               string     `json:"model,omitempty"`
	EgressPolicy        string     `json:"egress_policy,omitempty"`
	Scope               string     `json:"scope,omitempty"`
	VerificationProfile string     `json:"verification_profile,omitempty"`
	Workspace           string     `json:"workspace,omitempty"`
	RecordedAt          *time.Time `json:"recorded_at,omitempty"`
}

// VerificationStep is one deterministic gate.
type VerificationStep struct {
	ID             string    `json:"id"`
	Description    string    `json:"description,omitempty"`
	Argv           []string  `json:"argv"`
	ExitCode       int       `json:"exit_code"`
	Passed         bool      `json:"passed"`
	Mandatory      bool      `json:"mandatory"`
	TimedOut       bool      `json:"timed_out"`
	DurationMS     int64     `json:"duration_ms"`
	StartedAt      time.Time `json:"started_at,omitempty"`
	CompletedAt    time.Time `json:"completed_at,omitempty"`
	StdoutArtifact string    `json:"stdout_artifact,omitempty"`
	StderrArtifact string    `json:"stderr_artifact,omitempty"`
}

// VerificationDetail is the gate run recorded for one run.
type VerificationDetail struct {
	Available   bool               `json:"available"`
	Passed      bool               `json:"passed"`
	Profile     string             `json:"profile,omitempty"`
	DurationMS  int64              `json:"duration_ms"`
	StartedAt   time.Time          `json:"started_at,omitempty"`
	CompletedAt time.Time          `json:"completed_at,omitempty"`
	Steps       []VerificationStep `json:"steps"`
}

// PatchFile is one changed file in a patch.
type PatchFile struct {
	Path      string `json:"path"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
}

// PatchDetail summarises a run's deliverable patch.
type PatchDetail struct {
	Available bool        `json:"available"`
	Path      string      `json:"path,omitempty"`
	Bytes     int64       `json:"bytes"`
	Files     []PatchFile `json:"files"`
	Additions int         `json:"additions"`
	Deletions int         `json:"deletions"`
	Binary    bool        `json:"binary"`
	Truncated bool        `json:"truncated"`
}

// ArtifactInfo is one evidence file in a run directory.
type ArtifactInfo struct {
	Path         string `json:"path"`
	Bytes        int64  `json:"bytes"`
	Kind         string `json:"kind"`
	Previewable  bool   `json:"previewable"`
	Truncated    bool   `json:"truncated,omitempty"`
	ErrorMessage string `json:"error,omitempty"`
}

// RunDetail is everything the console shows for one run.
type RunDetail struct {
	Run          RunSummary            `json:"run"`
	Manifest     *factory.RunManifest  `json:"manifest"`
	Task         *TaskDetail           `json:"task"`
	Verification VerificationDetail    `json:"verification"`
	Patch        PatchDetail           `json:"patch"`
	Artifacts    []ArtifactInfo        `json:"artifacts"`
	Audit        []factory.AuditEntry  `json:"audit"`
	Change       *ChangeSummary        `json:"change"`
	RelatedRuns  []RunSummary          `json:"related_runs"`
	Actions      RunActions            `json:"actions"`
	Live         *LiveRun              `json:"live,omitempty"`
	Warnings     []string              `json:"warnings"`
	Conditions   []factory.Condition   `json:"conditions"`
	Security     *RunSecurityDetail    `json:"security,omitempty"`
	Previews     []factory.PreviewLink `json:"previews,omitempty"`
	Budget       []string              `json:"budget_exceeded,omitempty"`
	GeneratedAt  time.Time             `json:"generated_at"`
}

// RunSecurityDetail exposes the raw hardening evidence for one run.
type RunSecurityDetail struct {
	EgressProbe   *factory.EgressProbeResult `json:"egress_probe,omitempty"`
	Behavior      *threatmon.Report          `json:"behavior,omitempty"`
	BehaviorError string                     `json:"behavior_error,omitempty"`
	GateIntegrity *factory.GateIntegrity     `json:"gate_integrity,omitempty"`
	Alerts        []factory.AlertRecord      `json:"alerts,omitempty"`
}

// ── Changes ─────────────────────────────────────────────────────────────────

// ActivationSummary is one run activation of a change.
type ActivationSummary struct {
	RunID       string    `json:"run_id"`
	ParentRunID string    `json:"parent_run_id,omitempty"`
	Reason      string    `json:"reason"`
	CreatedAt   time.Time `json:"created_at"`
}

// ChangeSummary is the durable work item projection.
type ChangeSummary struct {
	ChangeID          string              `json:"change_id"`
	Scope             string              `json:"scope,omitempty"`
	Repository        string              `json:"repository"`
	RequestedRevision string              `json:"requested_revision"`
	TaskPreview       string              `json:"task_preview"`
	TaskHash          string              `json:"task_hash"`
	Status            string              `json:"status"`
	Attempts          int                 `json:"attempts"`
	TotalCostUSD      float64             `json:"total_cost_usd"`
	TotalTokens       int64               `json:"total_tokens"`
	LastRunID         string              `json:"last_run_id,omitempty"`
	LastResult        string              `json:"last_result,omitempty"`
	CreatedAt         time.Time           `json:"created_at"`
	UpdatedAt         time.Time           `json:"updated_at"`
	ReworkNote        string              `json:"rework_note,omitempty"`
	Activations       []ActivationSummary `json:"activations"`
	HasEvidence       bool                `json:"has_evidence"`
}

// ReviewContext tells the change detail page whether a human gate applies.
type ReviewContext struct {
	Configured     bool   `json:"configured"`
	GlobalEnabled  bool   `json:"global_enabled"`
	Pending        bool   `json:"pending"`
	LatestOutcome  string `json:"latest_outcome,omitempty"`
	ApproveCommand string `json:"approve_command"`
	RejectCommand  string `json:"reject_command"`
}

// ChangeDetail is one change with its activations.
type ChangeDetail struct {
	Change      ChangeSummary `json:"change"`
	Runs        []RunSummary  `json:"runs"`
	Review      ReviewContext `json:"review"`
	GeneratedAt time.Time     `json:"generated_at"`
	Warnings    []string      `json:"warnings"`
}

// ── List responses ──────────────────────────────────────────────────────────

// RunsResponse is the paginated run ledger.
type RunsResponse struct {
	Runs        []RunSummary `json:"runs"`
	Page        PageMeta     `json:"page"`
	Facets      Facets       `json:"facets"`
	Window      WindowInfo   `json:"window"`
	GeneratedAt time.Time    `json:"generated_at"`
	Index       IndexInfo    `json:"index"`
}

// ChangesResponse is the paginated change portfolio.
type ChangesResponse struct {
	Changes     []ChangeSummary `json:"changes"`
	Page        PageMeta        `json:"page"`
	Facets      Facets          `json:"facets"`
	Window      WindowInfo      `json:"window"`
	GeneratedAt time.Time       `json:"generated_at"`
}

// ReviewItem is one item awaiting a human decision.
type ReviewItem struct {
	Kind           string                `json:"kind"`
	Run            *RunSummary           `json:"run,omitempty"`
	Change         *ChangeSummary        `json:"change,omitempty"`
	WaitingSince   time.Time             `json:"waiting_since"`
	WaitingMS      int64                 `json:"waiting_ms"`
	ApproveCommand string                `json:"approve_command"`
	RejectCommand  string                `json:"reject_command"`
	Previews       []factory.PreviewLink `json:"previews,omitempty"`
	Live           *LiveRun              `json:"live,omitempty"`
	Detail         string                `json:"detail,omitempty"`
}

// ReviewsResponse is the human review queue.
type ReviewsResponse struct {
	Items         []ReviewItem `json:"items"`
	Page          PageMeta     `json:"page"`
	Total         int          `json:"total"`
	LiveAvailable bool         `json:"live_available"`
	LiveError     string       `json:"live_error,omitempty"`
	GeneratedAt   time.Time    `json:"generated_at"`
	PendingReview int          `json:"pending_review"`
	PausedRuns    int          `json:"paused_runs"`
}

// ── Security ────────────────────────────────────────────────────────────────

// SecurityPosture is the operator-configured defense-in-depth posture.
type SecurityPosture struct {
	EgressProbeEnabled        bool   `json:"egress_probe_enabled"`
	BehaviorMonitorEnabled    bool   `json:"behavior_monitor_enabled"`
	TripSeverity              string `json:"trip_severity,omitempty"`
	RequireNonRoot            bool   `json:"require_non_root"`
	AcknowledgeOpenEgress     bool   `json:"acknowledge_open_egress"`
	AllowGateSelfModification bool   `json:"allow_gate_self_modification"`
	AlertWebhookConfigured    bool   `json:"alert_webhook_configured"`
	EgressCanaryURL           string `json:"egress_canary_url,omitempty"`
}

// EgressAggregate counts measured network observation outcomes.
type EgressAggregate struct {
	Probed          int `json:"probed"`
	CanaryReachable int `json:"canary_reachable"`
	MetadataReach   int `json:"metadata_reachable"`
	DeniedExpected  int `json:"denied_expected"`
	Inconclusive    int `json:"inconclusive"`
	Violations      int `json:"violations"`
}

// SecurityEntry is one run in the cross-run security view, enriched with the
// context an operator needs to act on it.
type SecurityEntry struct {
	RunID        string    `json:"run_id"`
	Result       string    `json:"result"`
	Repository   string    `json:"repository,omitempty"`
	Harness      string    `json:"agent_harness,omitempty"`
	Scope        string    `json:"scope,omitempty"`
	StartedAt    time.Time `json:"started_at,omitempty"`
	FinishedAt   time.Time `json:"finished_at,omitempty"`
	Findings     int       `json:"findings"`
	MaxSeverity  string    `json:"max_severity,omitempty"`
	Tripped      bool      `json:"tripped"`
	Blocked      bool      `json:"blocked"`
	GateModified []string  `json:"gate_modified,omitempty"`
	EgressIssues int       `json:"egress_issues"`
}

// AlertSummary is one security alert recorded in evidence.
type AlertSummary struct {
	RunID     string    `json:"run_id"`
	Kind      string    `json:"kind"`
	Severity  string    `json:"severity"`
	Summary   string    `json:"summary,omitempty"`
	Delivered bool      `json:"delivered"`
	Error     string    `json:"error,omitempty"`
	At        time.Time `json:"at"`
}

// SecurityResponse is the governance security page payload.
type SecurityResponse struct {
	Window             WindowInfo                  `json:"window"`
	GeneratedAt        time.Time                   `json:"generated_at"`
	Inspected          int                         `json:"inspected"`
	RunsWithFindings   int                         `json:"runs_with_findings"`
	Tripped            int                         `json:"tripped"`
	Blocked            int                         `json:"blocked"`
	GateTampered       int                         `json:"gate_tampered"`
	EgressViolations   int                         `json:"egress_violations"`
	AlertsUndelivered  int                         `json:"alerts_undelivered"`
	Rules              []factory.RuleTally         `json:"rules"`
	EgressObservations []factory.EgressObservation `json:"egress_observations"`
	Entries            []SecurityEntry             `json:"entries"`
	Egress             EgressAggregate             `json:"egress"`
	Posture            SecurityPosture             `json:"posture"`
	Alerts             []AlertSummary              `json:"alerts"`
	Stops              []RunSummary                `json:"stops"`
	Index              IndexInfo                   `json:"index"`
}

// ── Operations ──────────────────────────────────────────────────────────────

// CleanupFailure is a run whose sandbox cleanup did not verify.
type CleanupFailure struct {
	RunID        string    `json:"run_id"`
	SandboxID    string    `json:"sandbox_id,omitempty"`
	Outcome      string    `json:"outcome,omitempty"`
	Verified     bool      `json:"verified"`
	Remaining    int       `json:"remaining_sandboxes"`
	Error        string    `json:"error,omitempty"`
	FinishedAt   time.Time `json:"finished_at,omitempty"`
	Repository   string    `json:"repository,omitempty"`
	FactoryState string    `json:"factory_result,omitempty"`
}

// CleanupAggregate is the evidence-derived sandbox lifecycle summary.
type CleanupAggregate struct {
	RunsWithSandbox int              `json:"runs_with_sandbox"`
	Verified        int              `json:"verified"`
	Failed          int              `json:"failed"`
	RemainingTotal  int              `json:"remaining_total"`
	RecentFailures  []CleanupFailure `json:"recent_failures"`
}

// LiveSandbox is one sandbox observed from the Cube API.
type LiveSandbox struct {
	ID               string    `json:"id"`
	Template         string    `json:"template,omitempty"`
	State            string    `json:"state,omitempty"`
	StartedAt        time.Time `json:"started_at,omitempty"`
	RunID            string    `json:"run_id,omitempty"`
	WorkflowRunID    string    `json:"workflow_run_id,omitempty"`
	FactoryOwned     bool      `json:"factory_owned"`
	AttributionKnown bool      `json:"attribution_known"`
	Leaked           bool      `json:"leaked"`
	Active           bool      `json:"active"`
}

// LiveSandboxState is the live Cube inventory, with honest availability.
type LiveSandboxState struct {
	Available bool      `json:"available"`
	Error     string    `json:"error,omitempty"`
	CheckedAt time.Time `json:"checked_at,omitempty"`
	// AttributionAvailable reports whether open-workflow state could be read.
	// Without it a factory-owned sandbox cannot be called a leak: it may simply
	// belong to a workflow the console cannot query right now.
	AttributionAvailable bool          `json:"attribution_available"`
	Note                 string        `json:"note,omitempty"`
	Total                int           `json:"total"`
	FactoryOwned         int           `json:"factory_owned"`
	Leaked               int           `json:"leaked"`
	Active               int           `json:"active"`
	Sandboxes            []LiveSandbox `json:"sandboxes"`
}

// CubeInfo is the redacted Cube configuration.
type CubeInfo struct {
	APIURL             string `json:"api_url,omitempty"`
	TemplateID         string `json:"template_id,omitempty"`
	Proxy              string `json:"proxy,omitempty"`
	Version            string `json:"version,omitempty"`
	CredentialsPresent bool   `json:"credentials_configured"`
}

// TemporalInfo is the redacted Temporal configuration.
type TemporalInfo struct {
	HostPort       string `json:"host_port,omitempty"`
	Namespace      string `json:"namespace,omitempty"`
	TaskQueue      string `json:"task_queue,omitempty"`
	TLS            bool   `json:"tls"`
	APIKeyEnv      string `json:"api_key_env,omitempty"`
	PayloadKeyring bool   `json:"payload_keyring_configured"`
}

// ObservabilityInfo is the redacted telemetry configuration.
type ObservabilityInfo struct {
	OTLPConfigured bool   `json:"otel_configured"`
	ServiceName    string `json:"service_name,omitempty"`
}

// StorageInfo is the evidence store description.
type StorageInfo struct {
	DataDir     string `json:"data_dir"`
	RunCount    int    `json:"run_count"`
	ChangeCount int    `json:"change_count"`
}

// InfrastructureInfo groups the configured infrastructure.
type InfrastructureInfo struct {
	Cube          CubeInfo          `json:"cube"`
	Temporal      TemporalInfo      `json:"temporal"`
	Observability ObservabilityInfo `json:"observability"`
	Storage       StorageInfo       `json:"storage"`
}

// Probe is one bounded reachability check. It is labelled a probe because a
// TCP handshake is not a health claim.
type Probe struct {
	ID        string     `json:"id"`
	Label     string     `json:"label"`
	Target    string     `json:"target"`
	State     string     `json:"state"`
	Detail    string     `json:"detail,omitempty"`
	LatencyMS int64      `json:"latency_ms"`
	CheckedAt *time.Time `json:"checked_at,omitempty"`
}

// DataHealth describes evidence completeness and freshness.
type DataHealth struct {
	Index           IndexInfo `json:"index"`
	StorageReadable bool      `json:"storage_readable"`
	StorageError    string    `json:"storage_error,omitempty"`
	Notes           []string  `json:"notes,omitempty"`
}

// OperationsResponse is the operations page payload.
type OperationsResponse struct {
	GeneratedAt    time.Time             `json:"generated_at"`
	Environment    string                `json:"environment"`
	Cleanup        CleanupAggregate      `json:"cleanup"`
	LiveSandboxes  LiveSandboxState      `json:"live_sandboxes"`
	Infrastructure InfrastructureInfo    `json:"infrastructure"`
	Harnesses      []HarnessCatalog      `json:"harnesses"`
	Verification   []VerificationCatalog `json:"verification_profiles"`
	Limits         LimitsCatalog         `json:"limits"`
	Review         ReviewCatalog         `json:"review"`
	Hardening      HardeningCatalog      `json:"hardening"`
	Probes         []Probe               `json:"probes"`
	DataHealth     DataHealth            `json:"data_health"`
	Warnings       []string              `json:"warnings"`
}

// ── Catalog ─────────────────────────────────────────────────────────────────

type HarnessCatalog struct {
	Name           string   `json:"name"`
	Type           string   `json:"type,omitempty"`
	Model          string   `json:"model,omitempty"`
	Provider       string   `json:"provider,omitempty"`
	ThinkingLevel  string   `json:"thinking_level,omitempty"`
	TimeoutSeconds int64    `json:"timeout_seconds"`
	Preinstalled   bool     `json:"preinstalled"`
	Binary         string   `json:"binary,omitempty"`
	BinaryPinned   bool     `json:"binary_pinned"`
	RuntimePinned  bool     `json:"runtime_pinned"`
	Packages       []string `json:"packages,omitempty"`
	PassEnvNames   []string `json:"pass_env_names,omitempty"`
	PromptMode     string   `json:"prompt_mode,omitempty"`
	CatalogCache   bool     `json:"catalog_cache_configured"`
	ProviderFiles  bool     `json:"provider_files_configured"`
}

type ModelCatalog struct {
	Name      string   `json:"name"`
	Provider  string   `json:"provider,omitempty"`
	Model     string   `json:"model,omitempty"`
	BaseURL   string   `json:"base_url,omitempty"`
	APIKeyEnv string   `json:"api_key_env,omitempty"`
	Scopes    []string `json:"scopes,omitempty"`
}

type EgressCatalog struct {
	Name          string   `json:"name"`
	AllowInternet *bool    `json:"allow_internet"`
	AllowOut      []string `json:"allow_out,omitempty"`
	DenyOut       []string `json:"deny_out,omitempty"`
	Summary       string   `json:"summary,omitempty"`
}

type WorkspaceCatalog struct {
	Name               string   `json:"name"`
	Template           string   `json:"template,omitempty"`
	BasePackages       []string `json:"base_packages,omitempty"`
	SnapshotConfigured bool     `json:"snapshot_configured"`
	RepositoryCache    bool     `json:"repository_cache"`
	SetupScriptPresent bool     `json:"setup_script_configured"`
}

type BudgetCatalog struct {
	Name           string  `json:"name"`
	MaxCostUSD     float64 `json:"max_cost_usd"`
	MaxTokens      int64   `json:"max_tokens"`
	MaxWallClockMS int64   `json:"max_wall_clock_ms"`
}

type ScopeCatalog struct {
	Name         string   `json:"name"`
	Repositories []string `json:"repositories,omitempty"`
	Harnesses    []string `json:"harnesses,omitempty"`
	Egress       string   `json:"egress,omitempty"`
	Model        string   `json:"model,omitempty"`
	Workspace    string   `json:"workspace,omitempty"`
	Budget       string   `json:"budget,omitempty"`
	Review       *bool    `json:"review"`
}

type VerificationStepCatalog struct {
	ID          string   `json:"id"`
	Argv        []string `json:"argv"`
	Description string   `json:"description,omitempty"`
	Mandatory   bool     `json:"mandatory"`
	TimeoutMS   int64    `json:"timeout_ms"`
	Dir         string   `json:"dir,omitempty"`
}

type VerificationCatalog struct {
	Name  string                    `json:"name"`
	Steps []VerificationStepCatalog `json:"steps"`
}

type LimitsCatalog struct {
	CommandOutputBytes    int64 `json:"command_output_bytes"`
	ArtifactBytes         int64 `json:"artifact_bytes"`
	WorkerConcurrency     int   `json:"worker_concurrency"`
	AgentTimeoutMS        int64 `json:"agent_timeout_ms"`
	VerificationTimeoutMS int64 `json:"verification_timeout_ms"`
	TotalTimeoutMS        int64 `json:"total_timeout_ms"`
	SandboxIdleTimeoutMS  int64 `json:"sandbox_idle_timeout_ms"`
	CPUMilli              int   `json:"cpu_milli"`
	MemoryMiB             int   `json:"memory_mib"`
	DiskMiB               int   `json:"disk_mib"`
}

type ReviewCatalog struct {
	Enabled      bool  `json:"enabled"`
	TimeoutMS    int64 `json:"timeout_ms"`
	Suspend      bool  `json:"suspend"`
	PreviewPorts []int `json:"preview_ports,omitempty"`
}

type HardeningCatalog struct {
	AcknowledgeOpenEgress     bool   `json:"acknowledge_open_egress"`
	EgressProbeEnabled        bool   `json:"egress_probe_enabled"`
	EgressCanaryURL           string `json:"egress_canary_url,omitempty"`
	MetadataProbeURL          string `json:"metadata_probe_url,omitempty"`
	BehaviorMonitorEnabled    bool   `json:"behavior_monitor_enabled"`
	TripSeverity              string `json:"trip_severity,omitempty"`
	RequireNonRoot            bool   `json:"require_non_root"`
	AllowGateSelfModification bool   `json:"allow_gate_self_modification"`
	AlertWebhookConfigured    bool   `json:"alert_webhook_configured"`
	AlertTimeoutMS            int64  `json:"alert_timeout_ms"`
}

type IntakeCatalog struct {
	Enabled   bool   `json:"enabled"`
	Model     string `json:"model,omitempty"`
	TimeoutMS int64  `json:"timeout_ms"`
}

type SandboxCatalog struct {
	AllowAttach     bool     `json:"allow_attach"`
	AllowPreview    bool     `json:"allow_preview"`
	BasePackages    []string `json:"base_packages,omitempty"`
	SetupScript     bool     `json:"setup_script_configured"`
	RuntimeAllowOut []string `json:"runtime_allow_out,omitempty"`
}

type CatalogResponse struct {
	Environment    string                `json:"environment"`
	ConfigPath     string                `json:"config_path,omitempty"`
	SchemaVersion  int                   `json:"schema_version"`
	FactoryVersion string                `json:"factory_version"`
	ConsoleVersion string                `json:"console_version"`
	GeneratedAt    time.Time             `json:"generated_at"`
	Cube           CubeInfo              `json:"cube"`
	Temporal       TemporalInfo          `json:"temporal"`
	Storage        StorageInfo           `json:"storage"`
	Limits         LimitsCatalog         `json:"limits"`
	Sandbox        SandboxCatalog        `json:"sandbox"`
	Harnesses      []HarnessCatalog      `json:"harnesses"`
	Models         []ModelCatalog        `json:"models"`
	Egress         []EgressCatalog       `json:"egress"`
	Workspaces     []WorkspaceCatalog    `json:"workspaces"`
	Budgets        []BudgetCatalog       `json:"budgets"`
	Scopes         []ScopeCatalog        `json:"scopes"`
	Verification   []VerificationCatalog `json:"verification_profiles"`
	Review         ReviewCatalog         `json:"review"`
	Hardening      HardeningCatalog      `json:"hardening"`
	Intake         IntakeCatalog         `json:"intake"`
	Observability  ObservabilityInfo     `json:"observability"`
	Repositories   []string              `json:"repositories"`
	Warnings       []string              `json:"warnings"`
}

// ── Search & session ────────────────────────────────────────────────────────

type SearchResult struct {
	Kind     string `json:"kind"`
	ID       string `json:"id"`
	Title    string `json:"title"`
	Subtitle string `json:"subtitle"`
	Status   string `json:"status,omitempty"`
	Href     string `json:"href"`
}

type SearchResponse struct {
	Query      string         `json:"query"`
	Results    []SearchResult `json:"results"`
	DurationMS int64          `json:"duration_ms"`
	Index      IndexInfo      `json:"index"`
}

type OperatorInfo struct {
	Name string `json:"name"`
	Role string `json:"role"`
}

type Capabilities struct {
	Actions       bool `json:"actions"`
	LiveTemporal  bool `json:"live_temporal"`
	LiveSandboxes bool `json:"live_sandboxes"`
	Search        bool `json:"search"`
	Artifacts     bool `json:"artifacts"`
}

type SessionResponse struct {
	Authenticated  bool         `json:"authenticated"`
	Environment    string       `json:"environment"`
	Version        string       `json:"version"`
	FactoryVersion string       `json:"factory_version"`
	ServerTime     time.Time    `json:"server_time"`
	UptimeSeconds  int64        `json:"uptime_seconds"`
	Operator       OperatorInfo `json:"operator"`
	Capabilities   Capabilities `json:"capabilities"`
}

// ── Overview ────────────────────────────────────────────────────────────────

type KPI struct {
	Value     float64  `json:"value"`
	Previous  float64  `json:"previous"`
	DeltaPct  *float64 `json:"delta_pct"`
	Sample    int      `json:"sample"`
	Available bool     `json:"available"`
	Note      string   `json:"note,omitempty"`
}

type InFlightKPI struct {
	Value     int    `json:"value"`
	Paused    int    `json:"paused"`
	Available bool   `json:"available"`
	Note      string `json:"note,omitempty"`
}

type ReviewsKPI struct {
	Value        int   `json:"value"`
	OldestWaitMS int64 `json:"oldest_wait_ms"`
	PausedLive   int   `json:"paused_live"`
	// Note carries the scope caveat: pending decisions are a queue, not a
	// windowed sample, so they must not look as if they were filtered by the
	// dashboard window.
	Note string `json:"note,omitempty"`
}

type SeriesPoint struct {
	Date          string  `json:"date"`
	Label         string  `json:"label"`
	Succeeded     int     `json:"succeeded"`
	Failed        int     `json:"failed"`
	Security      int     `json:"security"`
	Other         int     `json:"other"`
	Total         int     `json:"total"`
	CostUSD       float64 `json:"cost_usd"`
	P50DurationMS int64   `json:"p50_duration_ms"`
}

type Series struct {
	Granularity string        `json:"granularity"`
	Points      []SeriesPoint `json:"points"`
}

type CountShare struct {
	Value string  `json:"value"`
	Count int     `json:"count"`
	Share float64 `json:"share"`
}

type Breakdown struct {
	Name          string  `json:"name"`
	Runs          int     `json:"runs"`
	SuccessRate   float64 `json:"success_rate"`
	P50DurationMS int64   `json:"p50_duration_ms"`
	CostUSD       float64 `json:"cost_usd"`
}

type AttentionItem struct {
	ID         string    `json:"id"`
	Kind       string    `json:"kind"`
	Severity   string    `json:"severity"`
	Title      string    `json:"title"`
	Detail     string    `json:"detail"`
	RunID      string    `json:"run_id,omitempty"`
	ChangeID   string    `json:"change_id,omitempty"`
	Repository string    `json:"repository,omitempty"`
	At         time.Time `json:"at"`
	Href       string    `json:"href"`
}

type HealthCheck struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	// State is ok, warn, error, unknown or disabled. "disabled" is an explicit
	// operator choice and is reported separately from a passing check.
	State     string `json:"state"`
	Detail    string `json:"detail,omitempty"`
	LatencyMS int64  `json:"latency_ms"`
}

type HealthSummary struct {
	Status string        `json:"status"`
	Checks []HealthCheck `json:"checks"`
	Probes []Probe       `json:"probes"`
	Index  IndexInfo     `json:"index"`
}

// SecuritySummary is the compact security card on the overview.
type SecuritySummary struct {
	RunsScanned       int                 `json:"runs_scanned"`
	RunsWithFindings  int                 `json:"runs_with_findings"`
	Tripped           int                 `json:"tripped"`
	Blocked           int                 `json:"blocked"`
	GateTampered      int                 `json:"gate_tampered"`
	EgressViolations  int                 `json:"egress_violations"`
	AlertsUndelivered int                 `json:"alerts_undelivered"`
	StopsInWindow     int                 `json:"stops_in_window"`
	TopRules          []factory.RuleTally `json:"top_rules"`
}

type CatalogCounts struct {
	Harnesses      int `json:"harnesses"`
	Repositories   int `json:"repositories"`
	Verification   int `json:"verification_profiles"`
	Budgets        int `json:"budgets"`
	Scopes         int `json:"scopes"`
	EgressPolicies int `json:"egress_policies"`
}

type Overview struct {
	Window       WindowInfo      `json:"window"`
	GeneratedAt  time.Time       `json:"generated_at"`
	Index        IndexInfo       `json:"index"`
	KPIs         OverviewKPIs    `json:"kpis"`
	Series       Series          `json:"series"`
	Outcomes     []CountShare    `json:"outcomes"`
	Harnesses    []Breakdown     `json:"harnesses"`
	Repositories []Breakdown     `json:"repositories"`
	Scopes       []Breakdown     `json:"scopes"`
	Attention    []AttentionItem `json:"attention"`
	RecentRuns   []RunSummary    `json:"recent_runs"`
	Security     SecuritySummary `json:"security"`
	Health       HealthSummary   `json:"health"`
	Catalog      CatalogCounts   `json:"catalog"`
}

type OverviewKPIs struct {
	Runs                KPI         `json:"runs"`
	SuccessRate         KPI         `json:"success_rate"`
	InFlight            InFlightKPI `json:"in_flight"`
	Reviews             ReviewsKPI  `json:"reviews"`
	SecurityStops       KPI         `json:"security_stops"`
	CleanupVerifiedRate KPI         `json:"cleanup_verified_rate"`
	CostUSD             KPI         `json:"cost_usd"`
	Tokens              KPI         `json:"tokens"`
	P50DurationMS       KPI         `json:"p50_duration_ms"`
	P95DurationMS       KPI         `json:"p95_duration_ms"`
}

// ── Live orchestration ──────────────────────────────────────────────────────

// LiveRun is one workflow currently executing in Temporal.
type LiveRun struct {
	RunID            string              `json:"run_id"`
	WorkflowID       string              `json:"workflow_id"`
	Harness          string              `json:"harness,omitempty"`
	Scope            string              `json:"scope,omitempty"`
	Repository       string              `json:"repository,omitempty"`
	StartedAt        time.Time           `json:"started_at"`
	State            string              `json:"state,omitempty"`
	CurrentStep      string              `json:"current_step,omitempty"`
	ChangeID         string              `json:"change_id,omitempty"`
	SandboxID        string              `json:"sandbox_id,omitempty"`
	SandboxDestroyed bool                `json:"sandbox_destroyed"`
	Conditions       []factory.Condition `json:"conditions,omitempty"`
	QueryError       string              `json:"query_error,omitempty"`
}

// ── Internal helpers ────────────────────────────────────────────────────────

const maxErrorText = 400

func truncateText(value string, limit int) (string, bool) {
	value = strings.TrimSpace(value)
	if limit <= 0 || len(value) <= limit {
		return value, false
	}
	return value[:limit] + "…", true
}

func millis(d time.Duration) int64 {
	if d <= 0 {
		return 0
	}
	return d.Milliseconds()
}

func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}
