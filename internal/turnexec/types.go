// Package turnexec executes one sf-owned turn. It does not schedule workflows,
// decide permissions, publish repositories, or accept client-selected commands.
package turnexec

import (
	"context"
	"encoding/json"
	"github.com/mitkox/esf/internal/sandbox"
	"time"
)

type Request struct {
	ProfileSHA  string            `json:"profile_sha,omitempty"`
	MaxCostUSD  *float64          `json:"max_cost_usd,omitempty"`
	PolicySHA   string            `json:"policy_sha,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	Mode        string            `json:"mode,omitempty"`
	ExecutionID string            `json:"execution_id"`
	Profile     string            `json:"profile"`
	SpecSHA     string            `json:"spec_sha"`
	Prompt      string            `json:"prompt"`
	OwnerToken  string            `json:"owner_token"`
	Manifest    string            `json:"manifest,omitempty"`
	Checkpoint  string            `json:"checkpoint,omitempty"`
	SessionID   string            `json:"session_id,omitempty"`
}
type Permission struct {
	ID              string `json:"id"`
	OperationDigest string `json:"operation_digest"`
	Description     string `json:"description"`
	Decision        string `json:"decision,omitempty"`
}
type Status struct {
	Permissions    []Permission `json:"permissions,omitempty"`
	ExecutionID    string       `json:"execution_id"`
	State          string       `json:"status"`
	LeaseExpiresAt time.Time    `json:"lease_expires_at"`
	Checkpoint     string       `json:"checkpoint,omitempty"`
	Error          string       `json:"error,omitempty"`
}
type Event struct {
	Seq  int    `json:"seq"`
	Type string `json:"type"`
	Data any    `json:"data"`
}
type Result struct {
	Result     map[string]any `json:"result"`
	Checkpoint string         `json:"checkpoint,omitempty"`
	Error      string         `json:"error,omitempty"`
}
type File struct {
	Path    string `json:"path"`
	Content []byte `json:"content_base64"`
}
type Manifest struct {
	Files            []File `json:"files"`
	HarnessSessionID string `json:"harness_session_id,omitempty"`
}

// Profiles are operator-only configuration. Env contains environment variable
// REFERENCES on the service host, never literal credentials or client input.
// Tool and connector boundaries must be enforced in the template/network profile.
type MCPServer struct {
	URL          string `json:"url"`
	BearerEnvRef string `json:"bearer_env_ref"`
}
type HarnessArtifact struct {
	File   string `json:"file"`
	SHA256 string `json:"sha256"`
}
type Profile struct {
	ModelBaseURL           string               `json:"model_base_url,omitempty"`
	MaxCostUSD             float64              `json:"max_cost_usd,omitempty"`
	HarnessArtifact        *HarnessArtifact     `json:"harness_artifact,omitempty"`
	TurnPolicy             *TurnPolicy          `json:"turn_policy,omitempty"`
	SelectionOnly          bool                 `json:"selection_only,omitempty"`
	MCPServers             map[string]MCPServer `json:"mcp_servers,omitempty"`
	InteractivePermissions bool                 `json:"interactive_permissions,omitempty"`
	Template               string               `json:"template"`
	Command                []string             `json:"command"`
	EnvRefs                map[string]string    `json:"env_refs,omitempty"`
	Network                sandbox.Network      `json:"network"`
	TimeoutSeconds         int                  `json:"timeout_seconds"`
	Harness                string               `json:"harness"`
}
type record struct {
	PendingPermission *Permission           `json:"pending_permission,omitempty"`
	Decisions         map[string]Permission `json:"decisions,omitempty"`
	Status            Status                `json:"status"`
	Request           Request               `json:"request"`
	OwnerHash         string                `json:"owner_hash"`
	Fingerprint       string                `json:"fingerprint"`
	ProfileSHA        string                `json:"profile_sha"`
	SandboxID         string                `json:"sandbox_id,omitempty"`
	Events            []Event               `json:"events"`
	EventBytes        int                   `json:"event_bytes"`
	Result            *Result               `json:"result,omitempty"`
	StartedAt         time.Time             `json:"started_at"`
	cancel            context.CancelFunc
}

func terminal(state string) bool { return state != "queued" && state != "running" }
func digestJSON(v any) string    { b, _ := json.Marshal(v); return digest(b) }
