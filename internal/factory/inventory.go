package factory

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"time"

	"github.com/mitkox/esf/internal/sandbox"
)

// The sandbox inventory is the factory's answer to a problem AX solves with a
// metadata service: an agent should DISCOVER the facts of its environment,
// rather than have them baked into a prompt by whoever assembled the request.
//
// AX runs a small metadata server inside the sandbox. ESF deliberately does not
// add a listening service: a long-lived process with a control surface is
// exactly the kind of privileged component that widens a sandbox's threat
// model, and the factory's own security posture is that nothing inside a
// sandbox is trusted to serve anything. The same goal — one authoritative
// source of environment facts — is achieved with a file written by the factory
// before the agent starts:
//
//	/workspace/.factory/inventory.json   (path derived from the repository dir)
//
// The file is the harness contract. A harness reads it instead of parsing
// prompt text, which means an operator can change egress policy, model or
// workspace without editing a single prompt, and a prompt can no longer drift
// from the environment it describes.
//
// SECURITY: the inventory contains names and digests, never credential values.
// The task text stays out of it too: the file records the PATH of the task
// file, so untrusted text is delivered exactly once, by the existing
// stdin-file mechanism, and never duplicated into a second place an agent
// could misread.

// InventoryContractVersion identifies the harness-facing inventory schema.
//
// A harness that depends on the inventory should assert this value: the
// contract is versioned so that changing it is a visible, reviewable act rather
// than a silent break for every harness at once.
const InventoryContractVersion = "factory/v1"

// InventoryDirName is the directory, beside the repository, holding
// factory-owned metadata inside a sandbox.
const InventoryDirName = ".factory"

// InventoryFileName is the fixed name of the inventory document.
const InventoryFileName = "inventory.json"

// Inventory is the authoritative description of a run's environment as the
// agent sees it.
type Inventory struct {
	// ContractVersion is InventoryContractVersion.
	ContractVersion string `json:"contract_version"`
	// RunID and ChangeID identify the execution and its durable work item.
	RunID    string `json:"run_id"`
	ChangeID string `json:"change_id,omitempty"`
	// Scope is the tenancy boundary the run executes under.
	Scope string `json:"scope,omitempty"`
	// FactoryVersion is the ESF release that produced the inventory.
	FactoryVersion string `json:"factory_version"`

	// TaskFile is the in-sandbox path of the task text. The text itself is
	// never copied here.
	TaskFile string `json:"task_file"`
	// Repository describes the checked-out revision.
	Repository InventoryRepository `json:"repository"`
	// Workspace describes the pre-warmed environment.
	Workspace InventoryWorkspace `json:"workspace"`
	// Egress describes the network policy in effect.
	Egress InventoryEgress `json:"egress"`
	// Model describes the endpoint the harness is expected to use. Credentials
	// are absent by construction.
	Model InventoryModel `json:"model"`
	// Limits are the ceilings the factory will enforce.
	Limits InventoryLimits `json:"limits"`
	// Sandbox identifies the microVM.
	Sandbox InventorySandbox `json:"sandbox"`
	// WrittenAt is when the factory wrote the file.
	WrittenAt time.Time `json:"written_at"`
}

// InventoryRepository is the repository state the agent starts from.
type InventoryRepository struct {
	URL         string `json:"url"`
	Kind        string `json:"kind,omitempty"`
	Revision    string `json:"revision"`
	BaselineSHA string `json:"baseline_sha,omitempty"`
	Directory   string `json:"directory"`
}

// InventoryWorkspace names the environment the run was prepared with.
type InventoryWorkspace struct {
	Name   string `json:"name,omitempty"`
	Digest string `json:"digest,omitempty"`
	// SnapshotID is set when the sandbox was created from a pre-warmed
	// snapshot, so a harness can tell a warm start from a cold one.
	SnapshotID string `json:"snapshot_id,omitempty"`
}

// InventoryEgress is the effective network policy.
//
// It is duplicated from the resolved resources on purpose: the harness may need
// to know whether it can reach a registry, and this is the only place the
// answer is authoritative for the sandbox it is running in.
type InventoryEgress struct {
	Policy        string   `json:"policy,omitempty"`
	Digest        string   `json:"digest,omitempty"`
	Summary       string   `json:"summary,omitempty"`
	AllowOut      []string `json:"allow_out,omitempty"`
	DenyOut       []string `json:"deny_out,omitempty"`
	AllowInternet *bool    `json:"allow_internet,omitempty"`
}

// InventoryModel is the model endpoint selection. It never carries a secret.
type InventoryModel struct {
	Name     string `json:"name,omitempty"`
	Provider string `json:"provider,omitempty"`
	ID       string `json:"id,omitempty"`
	Digest   string `json:"digest,omitempty"`
	// APIKeyEnv names the environment variable the harness should read. The
	// value is not present, and is only populated when the harness was
	// explicitly granted it.
	APIKeyEnv string `json:"api_key_env,omitempty"`
	BaseURL   string `json:"base_url,omitempty"`
}

// InventoryLimits are the ceilings the factory enforces for this run.
type InventoryLimits struct {
	AgentTimeout        string `json:"agent_timeout"`
	VerificationTimeout string `json:"verification_timeout"`
	TotalTimeout        string `json:"total_timeout"`
}

// InventorySandbox identifies the microVM.
type InventorySandbox struct {
	ID       string `json:"id,omitempty"`
	Template string `json:"template,omitempty"`
	Provider string `json:"provider,omitempty"`
}

// InventoryPathFor returns the inventory path for a repository directory.
//
// The inventory lives beside the repository rather than inside it: putting it
// in the checkout would make it part of the deliverable and would tempt a gate
// into committing it.
func InventoryPathFor(repositoryDir string) string {
	return path.Join(metadataDirFor(repositoryDir), InventoryDirName, InventoryFileName)
}

// TaskFilePathFor returns the conventional in-sandbox path of the task text.
func TaskFilePathFor(repositoryDir string) string {
	return path.Join(metadataDirFor(repositoryDir), InventoryDirName, "task.txt")
}

// metadataDirFor returns the directory that holds factory metadata beside the
// repository. A relative repository directory resolves under /workspace, which
// is the only sane in-sandbox anchor; an absolute one uses its real parent.
func metadataDirFor(repositoryDir string) string {
	dir := path.Dir(repositoryDir)
	if dir == "." {
		return "/workspace"
	}
	return dir
}

// BuildInventory assembles the inventory from resolved run facts.
func BuildInventory(req RunRequest, resolved ResolvedResources, in InventoryInput) Inventory {
	return Inventory{
		ContractVersion: InventoryContractVersion,
		RunID:           req.RunID,
		ChangeID:        req.ChangeID,
		Scope:           resolved.Scope,
		FactoryVersion:  in.FactoryVersion,
		TaskFile:        TaskFilePathFor(req.EffectiveRepositoryDir()),
		Repository: InventoryRepository{
			URL:         req.Repository,
			Kind:        req.RepositoryKind,
			Revision:    req.Revision,
			BaselineSHA: in.BaselineSHA,
			Directory:   req.EffectiveRepositoryDir(),
		},
		Workspace: InventoryWorkspace{
			Name:       resolved.Workspace,
			Digest:     resolved.WorkspaceDigest,
			SnapshotID: resolved.WorkspaceConfig.SnapshotID,
		},
		Egress: InventoryEgress{
			Policy:        resolved.EgressPolicy,
			Digest:        resolved.EgressDigest,
			Summary:       resolved.EgressSummary,
			AllowOut:      resolved.Egress.AllowOut,
			DenyOut:       resolved.Egress.DenyOut,
			AllowInternet: resolved.Egress.AllowInternet,
		},
		Model: InventoryModel{
			Name:      resolved.Model,
			Provider:  resolved.ModelProvider,
			ID:        resolved.ModelID,
			Digest:    resolved.ModelDigest,
			APIKeyEnv: resolved.ModelAPIKeyEnv,
			BaseURL:   resolved.ModelBaseURL,
		},
		Limits: InventoryLimits{
			AgentTimeout:        req.AgentTimeout.String(),
			VerificationTimeout: req.VerificationTimeout.String(),
			TotalTimeout:        req.TotalTimeout.String(),
		},
		Sandbox: InventorySandbox{
			ID:       in.SandboxID,
			Template: in.Template,
			Provider: in.Provider,
		},
		WrittenAt: in.WrittenAt,
	}
}

// InventoryInput carries the run-time facts that are not part of the request.
type InventoryInput struct {
	FactoryVersion string
	SandboxID      string
	Template       string
	Provider       string
	BaselineSHA    string
	WrittenAt      time.Time
}

// WriteInventory writes the inventory into a live sandbox.
//
// It returns the marshalled document so the caller can persist the same bytes
// as evidence without re-serialising them — the record must be exactly what the
// agent saw, not a re-encoding that might differ.
func WriteInventoryDocument(ctx context.Context, sb sandbox.Sandbox, repositoryDir string, inv Inventory) ([]byte, error) {
	data, err := json.MarshalIndent(inv, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode sandbox inventory: %w", err)
	}
	target := InventoryPathFor(repositoryDir)
	if err := sb.WriteFile(ctx, target, data); err != nil {
		return nil, fmt.Errorf("write sandbox inventory to %s: %w", target, err)
	}
	return data, nil
}
