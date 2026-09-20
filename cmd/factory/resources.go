package main

// This file implements the AX-inspired resource-oriented CLI:
//
//	factory get <kind> [name]      list resources
//	factory describe <kind> <name> inspect one resource in depth
//	factory apply -f spec.toml     submit a run from a reviewed manifest
//	factory delete <kind> <name>   remove a resource that can safely be removed
//	factory review <run-id>        answer a paused run's review gate
//	factory attach <run-id>        run an operator command inside a sandbox
//	factory preview <run-id>       publish a sandbox port for human review
//
// The vocabulary is deliberately kubectl-shaped: one verb set across every
// resource kind, because an operator who has learned `get` and `describe` for a
// run should not have to learn a new shape for a change or a workspace. The
// behaviour is deliberately NOT kubectl-shaped where safety matters: runs are
// immutable evidence and cannot be deleted, and attach/preview are default
// closed in configuration.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
	"github.com/spf13/cobra"
	"go.temporal.io/sdk/client"

	"github.com/mitkox/esf/internal/factory"
	"github.com/mitkox/esf/internal/sandbox"
)

// runSpec is the on-disk manifest accepted by `factory apply`.
//
// It carries the caller-controlled subset of a RunRequest: task text, an
// approved repository, an approved revision, and references to operator-declared
// resources. It cannot express an executable, a host path or a credential,
// exactly like the CLI flags.
type runSpec struct {
	RunID               string   `json:"run_id" toml:"run_id"`
	ChangeID            string   `json:"change_id" toml:"change_id"`
	ParentRunID         string   `json:"parent_run_id" toml:"parent_run_id"`
	Scope               string   `json:"scope" toml:"scope"`
	Repository          string   `json:"repository" toml:"repository"`
	Revision            string   `json:"revision" toml:"revision"`
	Task                string   `json:"task" toml:"task"`
	TaskFile            string   `json:"task_file" toml:"task_file"`
	AgentHarness        string   `json:"agent_harness" toml:"agent_harness"`
	Model               string   `json:"model" toml:"model"`
	Workspace           string   `json:"workspace" toml:"workspace"`
	EgressPolicy        string   `json:"egress_policy" toml:"egress_policy"`
	VerificationProfile string   `json:"verification_profile" toml:"verification_profile"`
	SandboxTemplate     string   `json:"sandbox_template" toml:"sandbox_template"`
	Review              *bool    `json:"review" toml:"review"`
	AgentTimeout        string   `json:"agent_timeout" toml:"agent_timeout"`
	RunTimeout          string   `json:"total_timeout" toml:"total_timeout"`
	Tags                []string `json:"tags" toml:"tags"`
}

// resourceKinds lists every kind `get` and `describe` understand.
var resourceKinds = []string{
	"runs", "changes", "sandboxes",
	"workspaces", "models", "egress", "budgets", "scopes", "harnesses", "verifications",
}

// kindAliases accepts the singular form an operator naturally types.
var kindAliases = map[string]string{
	"run": "runs", "change": "changes", "sandbox": "sandboxes",
	"workspace": "workspaces", "model": "models",
	"budget": "budgets", "scope": "scopes",
	"harness": "harnesses", "verification": "verifications",
}

// normalizeKind maps a singular alias onto its canonical kind.
func normalizeKind(kind string) string {
	if canonical, ok := kindAliases[kind]; ok {
		return canonical
	}
	return kind
}

// configResourceKinds are the kinds that come from operator configuration
// rather than from durable state.
var configResourceKinds = map[string]bool{
	"workspaces": true, "models": true, "egress": true, "budgets": true,
	"scopes": true, "harnesses": true, "verifications": true,
}

// ── get ─────────────────────────────────────────────────────────────────────

func newGetCommand(configPath *string) *cobra.Command {
	var (
		scope  string
		asJSON bool
	)
	cmd := &cobra.Command{
		Use:   "get <kind> [name]",
		Short: "List resources (" + strings.Join(resourceKinds, ", ") + ")",
		Long: `List factory resources.

Kinds:
  runs          durable run records (from the artifact store)
  changes       durable work items and their activations
  sandboxes     live sandboxes in the deployment
  workspaces    declared execution environments
  models        declared model endpoints
  egress        declared network policies
  budgets       declared spending ceilings
  scopes        declared tenancy boundaries
  harnesses     declared agent harnesses
  verifications declared verification profiles`,
		Args:         cobra.RangeArgs(1, 2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			kind := normalizeKind(args[0])
			cfg, err := loadConfig(*configPath)
			if err != nil {
				return err
			}
			if configResourceKinds[kind] {
				return getConfigResource(cfg, kind, filterName(args), asJSON)
			}
			runtime, err := factory.NewRuntime(ctx, factory.RuntimeOptions{Config: cfg})
			if err != nil {
				return err
			}
			defer runtime.Close(ctx)

			switch kind {
			case "runs":
				return getRuns(ctx, runtime, scope, filterName(args), asJSON)
			case "changes":
				return getChanges(runtime, scope, filterName(args), asJSON)
			case "sandboxes":
				return getSandboxes(ctx, runtime, asJSON)
			default:
				return fmt.Errorf("unknown kind %q; expected one of %s", kind, strings.Join(resourceKinds, ", "))
			}
		},
	}
	cmd.Flags().StringVar(&scope, "scope", "", "filter by scope (runs and changes)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON")
	return cmd
}

func filterName(args []string) string {
	if len(args) > 1 {
		return args[1]
	}
	return ""
}

// getRuns lists runs from the durable artifact store, newest first.
//
// In-flight runs that have not written a manifest yet are intentionally not
// listed: this view is the durable record, and `factory status <run-id>`
// answers the live question. Mixing the two would make "the run is not here"
// ambiguous.
func getRuns(ctx context.Context, runtime *factory.Runtime, scope, name string, asJSON bool) error {
	ids, err := factory.ListRunIDs(runtime.Artifacts)
	if err != nil {
		return err
	}
	var manifests []factory.RunManifest
	for _, id := range ids {
		if name != "" && id != name {
			continue
		}
		manifest, err := factory.ReadManifest(runtime.Artifacts, id)
		if err != nil {
			continue
		}
		if scope != "" && manifest.Scope != scope {
			continue
		}
		manifests = append(manifests, manifest)
	}
	if asJSON {
		if manifests == nil {
			manifests = []factory.RunManifest{}
		}
		return printJSON(manifests)
	}
	if len(manifests) == 0 {
		fmt.Println("no runs")
		return nil
	}
	fmt.Printf("%-28s %-8s %-20s %-10s %-6s %s\n", "RUN", "SCOPE", "RESULT", "AGENT", "COND", "CHANGE")
	for _, m := range manifests {
		trueCount, falseCount, _ := factory.ConditionCounts(m.Conditions)
		fmt.Printf("%-28s %-8s %-20s %-10s %-6s %s\n",
			truncate(m.RunID, 28), orDash(m.Scope), truncate(string(m.FactoryResult), 20),
			truncate(string(m.AgentResult), 10),
			fmt.Sprintf("%d/%d", trueCount, trueCount+falseCount), orDash(m.ChangeID))
	}
	return nil
}

// getChanges lists durable work items with their aggregate spend.
func getChanges(runtime *factory.Runtime, scope, name string, asJSON bool) error {
	store, err := factory.NewChangeStore(runtime.Artifacts.Root())
	if err != nil {
		return err
	}
	var changes []factory.Change
	if name != "" {
		c, err := store.Load(name)
		if err != nil {
			return err
		}
		changes = []factory.Change{c}
	} else {
		changes, err = store.List()
		if err != nil {
			return err
		}
	}
	filtered := changes[:0]
	for _, c := range changes {
		if scope != "" && c.Scope != scope {
			continue
		}
		filtered = append(filtered, c)
	}
	if asJSON {
		if filtered == nil {
			filtered = []factory.Change{}
		}
		return printJSON(filtered)
	}
	if len(filtered) == 0 {
		fmt.Println("no changes")
		return nil
	}
	fmt.Printf("%-28s %-10s %-6s %-10s %-8s %s\n", "CHANGE", "STATUS", "TRIES", "LAST", "COST", "REPO")
	for _, c := range filtered {
		fmt.Printf("%-28s %-10s %-6d %-10s %-8.4f %s\n",
			truncate(c.ChangeID, 28), c.Status, c.Attempts, orDash(string(c.LastResult)),
			c.TotalCostUSD, truncate(c.Repository, 40))
	}
	return nil
}

// getSandboxes lists live sandboxes, including ones the factory does not own:
// a leak is defined by absence from the factory's records, so the full list is
// the useful view.
func getSandboxes(ctx context.Context, runtime *factory.Runtime, asJSON bool) error {
	infos, err := runtime.Provider.List(ctx)
	if err != nil {
		return err
	}
	if asJSON {
		if infos == nil {
			infos = []sandbox.Info{}
		}
		return printJSON(infos)
	}
	if len(infos) == 0 {
		fmt.Println("no live sandboxes")
		return nil
	}
	fmt.Printf("%-36s %-10s %-10s %s\n", "SANDBOX", "STATE", "ORIGIN", "RUN")
	for _, info := range infos {
		fmt.Printf("%-36s %-10s %-10s %s\n",
			truncate(info.ID, 36), info.State, orDash(info.Metadata["origin"]), orDash(info.Metadata["run_id"]))
	}
	return nil
}

// configRow is one row of a configuration listing.
type configRow struct {
	Name   string `json:"name"`
	Detail string `json:"detail"`
}

// getConfigResource lists one operator-declared resource family.
func getConfigResource(cfg factory.Config, kind, name string, asJSON bool) error {
	var rows []configRow
	switch kind {
	case "workspaces":
		for _, n := range sortedKeys(cfg.Workspaces) {
			w := cfg.Workspaces[n]
			rows = append(rows, configRow{n, fmt.Sprintf("template=%s snapshot=%s digest=%s", orDash(w.Template), orDash(w.SnapshotID), w.Digest())})
		}
	case "models":
		for _, n := range sortedKeys(cfg.Models) {
			m := cfg.Models[n]
			rows = append(rows, configRow{n, fmt.Sprintf("%s/%s key=%s digest=%s", m.Provider, m.Model, orDash(m.APIKeyEnv), m.Digest())})
		}
	case "egress":
		for _, n := range sortedKeys(cfg.Egress) {
			e := cfg.Egress[n]
			rows = append(rows, configRow{n, fmt.Sprintf("%s digest=%s", e.Summary(), e.Digest())})
		}
	case "budgets":
		for _, n := range sortedKeys(cfg.Budgets) {
			b := cfg.Budgets[n]
			rows = append(rows, configRow{n, fmt.Sprintf("cost<=%.4f tokens<=%d wall<=%s", b.MaxCostUSD, b.MaxTokens, b.MaxWallClock.Std())})
		}
	case "scopes":
		for _, n := range sortedKeys(cfg.Scopes) {
			s := cfg.Scopes[n]
			rows = append(rows, configRow{n, fmt.Sprintf("egress=%s model=%s workspace=%s budget=%s", orDash(s.Egress), orDash(s.Model), orDash(s.Workspace), orDash(s.Budget))})
		}
	case "harnesses":
		for _, n := range sortedKeys(cfg.Harnesses) {
			h := cfg.Harnesses[n]
			rows = append(rows, configRow{n, fmt.Sprintf("type=%s model=%s", h.Type, orDash(h.Model))})
		}
	case "verifications":
		for _, n := range sortedKeys(cfg.Verification) {
			p := cfg.Verification[n]
			rows = append(rows, configRow{n, fmt.Sprintf("%d gate(s)", len(p.Steps))})
		}
	default:
		return fmt.Errorf("unsupported config kind %q", kind)
	}
	if name != "" {
		filtered := rows[:0]
		for _, r := range rows {
			if r.Name == name {
				filtered = append(filtered, r)
			}
		}
		rows = filtered
	}
	if asJSON {
		if rows == nil {
			rows = []configRow{}
		}
		return printJSON(rows)
	}
	if len(rows) == 0 {
		fmt.Printf("no %s declared\n", kind)
		return nil
	}
	fmt.Printf("%-24s %s\n", resourceHeader(kind), "DETAIL")
	for _, r := range rows {
		fmt.Printf("%-24s %s\n", truncate(r.Name, 24), r.Detail)
	}
	return nil
}

// resourceHeader renders a table header for a kind without mangling words that
// end in "s" for reasons other than plurality (egress).
func resourceHeader(kind string) string {
	switch kind {
	case "egress":
		return "EGRESS"
	case "verifications":
		return "VERIFICATION"
	default:
		return strings.ToUpper(strings.TrimSuffix(kind, "s"))
	}
}

// ── describe ────────────────────────────────────────────────────────────────

func newDescribeCommand(configPath *string) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:          "describe <kind> <name>",
		Short:        "Show one resource in depth",
		Args:         cobra.ExactArgs(2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			kind, name := normalizeKind(args[0]), args[1]
			cfg, err := loadConfig(*configPath)
			if err != nil {
				return err
			}
			if configResourceKinds[kind] {
				return getConfigResource(cfg, kind, name, asJSON)
			}
			runtime, err := factory.NewRuntime(ctx, factory.RuntimeOptions{Config: cfg})
			if err != nil {
				return err
			}
			defer runtime.Close(ctx)

			switch kind {
			case "runs":
				return describeRun(runtime, name, asJSON)
			case "changes":
				return describeChange(runtime, name, asJSON)
			case "sandboxes":
				return describeSandbox(ctx, runtime, name, asJSON)
			default:
				return fmt.Errorf("unknown kind %q; expected one of %s", kind, strings.Join(resourceKinds, ", "))
			}
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON")
	return cmd
}

// describeRun prints the manifest, its conditions, the sandbox inventory and
// the audit trail. It is the one command that answers "what actually happened".
func describeRun(runtime *factory.Runtime, runID string, asJSON bool) error {
	manifest, err := factory.ReadManifest(runtime.Artifacts, runID)
	if err != nil {
		return err
	}
	store, err := runtime.Artifacts.ForRun(runID)
	if err != nil {
		return err
	}
	audit, _ := factory.ReadAudit(store)
	inventory, _ := store.Read(factory.ArtifactInventory)

	if asJSON {
		payload := map[string]any{
			"manifest":   manifest,
			"audit":      audit,
			"inventory":  json.RawMessage(inventory),
			"artifacts":  store.Dir(),
			"conditions": manifest.Conditions,
		}
		return printJSON(payload)
	}

	fmt.Printf("Run:         %s\n", manifest.RunID)
	fmt.Printf("Change:      %s\n", orDash(manifest.ChangeID))
	fmt.Printf("Scope:       %s\n", orDash(manifest.Scope))
	fmt.Printf("Result:      %s\n", manifest.FactoryResult)
	fmt.Printf("Repository:  %s @ %s\n", manifest.Repository, manifest.RequestedRevision)
	fmt.Printf("Baseline:    %s\n", orDash(manifest.BaselineSHA))
	fmt.Printf("Sandbox:     %s (template %s)\n", orDash(manifest.SandboxID), orDash(manifest.SandboxTemplate))
	if manifest.Workspace != "" {
		fmt.Printf("Workspace:   %s @ %s\n", manifest.Workspace, manifest.WorkspaceDigest)
	}
	if manifest.EgressPolicy != "" {
		fmt.Printf("Egress:      %s (%s)\n", manifest.EgressPolicy, manifest.EgressSummary)
	}
	if manifest.Model != "" {
		fmt.Printf("Model:       %s/%s (%s)\n", orDash(manifest.ModelProvider), manifest.Model, orDash(manifest.ModelDigest))
	}
	if manifest.ResourceDigest != "" {
		fmt.Printf("Resources:   %s\n", manifest.ResourceDigest)
	}
	fmt.Printf("Agent:       %s (%s)\n", manifest.AgentResult, orDash(manifest.AgentHarness))
	fmt.Printf("Verified:    %s\n", manifest.VerificationResult)
	fmt.Printf("Cleanup:     %s\n", manifest.CleanupResult.Outcome)
	if manifest.ReviewConfigured {
		fmt.Printf("Review:      %s (suspend %s)\n", orDash(manifest.ReviewOutcome), orDash(string(manifest.SuspendResult)))
	}
	for _, preview := range manifest.Previews {
		fmt.Printf("Preview:     %d -> %s\n", preview.Port, preview.URL)
	}
	if len(manifest.BudgetExceeded) > 0 {
		fmt.Printf("Budget:      EXCEEDED (%s)\n", strings.Join(manifest.BudgetExceeded, "; "))
	}

	if len(manifest.Conditions) > 0 {
		fmt.Println("\nConditions:")
		for _, c := range manifest.Conditions {
			detail := c.Reason
			if c.Message != "" {
				detail = orDash(c.Reason) + " — " + c.Message
			}
			fmt.Printf("  %-22s %-7s %s\n", c.Type, c.Status, detail)
		}
	}
	if len(audit) > 0 {
		fmt.Println("\nAudit:")
		for _, e := range audit {
			fmt.Printf("  %s %-16s %-8s %s %s\n", e.At.Format(time.RFC3339), e.Action, e.Outcome, e.Actor, e.Error)
		}
	}
	if len(inventory) > 0 {
		fmt.Printf("\nInventory:   %s/%s\n", store.Location(), factory.ArtifactInventory)
	}
	fmt.Printf("Artifacts:   %s\n", store.Dir())
	return nil
}

// describeChange prints a work item and the lineage of its activations.
func describeChange(runtime *factory.Runtime, changeID string, asJSON bool) error {
	store, err := factory.NewChangeStore(runtime.Artifacts.Root())
	if err != nil {
		return err
	}
	change, err := store.Load(changeID)
	if err != nil {
		return err
	}
	if asJSON {
		return printJSON(change)
	}
	fmt.Printf("Change:      %s\n", change.ChangeID)
	fmt.Printf("Status:      %s\n", change.Status)
	fmt.Printf("Scope:       %s\n", orDash(change.Scope))
	fmt.Printf("Repository:  %s @ %s\n", change.Repository, change.RequestedRevision)
	fmt.Printf("Task hash:   %s\n", orDash(change.TaskHash))
	fmt.Printf("Attempts:    %d\n", change.Attempts)
	fmt.Printf("Spend:       %.4f USD, %d tokens (recorded)\n", change.TotalCostUSD, change.TotalTokens)
	if change.ReworkNote != "" {
		fmt.Printf("Rework note: %s\n", change.ReworkNote)
	}
	fmt.Println("\nActivations:")
	fmt.Printf("  %-28s %-10s %-10s %s\n", "RUN", "REASON", "RESULT", "PARENT")
	for _, a := range change.Activations {
		result := ""
		if manifest, err := factory.ReadManifest(runtime.Artifacts, a.RunID); err == nil {
			result = string(manifest.FactoryResult)
		}
		fmt.Printf("  %-28s %-10s %-10s %s\n", truncate(a.RunID, 28), a.Reason, orDash(result), orDash(a.ParentRunID))
	}
	return nil
}

func describeSandbox(ctx context.Context, runtime *factory.Runtime, sandboxID string, asJSON bool) error {
	infos, err := runtime.Provider.List(ctx)
	if err != nil {
		return err
	}
	for _, info := range infos {
		if info.ID != sandboxID {
			continue
		}
		if asJSON {
			return printJSON(info)
		}
		fmt.Printf("Sandbox:     %s\n", info.ID)
		fmt.Printf("Template:    %s\n", info.Template)
		fmt.Printf("State:       %s\n", info.State)
		fmt.Printf("Started:     %s\n", info.StartedAt.Format(time.RFC3339))
		for _, k := range sortedStringKeys(info.Metadata) {
			fmt.Printf("  %-12s %s\n", k, info.Metadata[k])
		}
		return nil
	}
	return fmt.Errorf("sandbox %s not found", sandboxID)
}

// ── apply ───────────────────────────────────────────────────────────────────

func newApplyCommand(configPath *string) *cobra.Command {
	var (
		file    string
		wait    bool
		asJSON  bool
		runIDIn string
	)
	cmd := &cobra.Command{
		Use:   "apply -f <spec.toml|spec.json>",
		Short: "Submit a run from a reviewed manifest",
		Long: `Submit a run from a manifest file.

The manifest carries only what a caller is allowed to choose: task text, an
approved repository, an exact revision, and references to operator-declared
resources (workspace, model, egress policy, scope). It cannot express an
executable, a host path or a credential.

Submitted runs are idempotent by run ID: re-applying the same manifest returns
the existing workflow instead of starting a second one.`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			if file == "" {
				return fmt.Errorf("--f is required")
			}
			spec, err := readRunSpec(file)
			if err != nil {
				return err
			}
			if runIDIn != "" {
				spec.RunID = runIDIn
			}
			if spec.RunID == "" {
				spec.RunID = newRunID()
			}
			if spec.TaskFile != "" {
				data, err := os.ReadFile(spec.TaskFile)
				if err != nil {
					return fmt.Errorf("read task file: %w", err)
				}
				spec.Task = string(data)
			}
			cfg, err := loadConfig(*configPath)
			if err != nil {
				return err
			}
			if spec.AgentHarness == "" {
				spec.AgentHarness = firstHarnessName(cfg)
			}
			if spec.VerificationProfile == "" {
				spec.VerificationProfile = firstProfileName(cfg)
			}
			req, err := runRequestFromSpec(cfg, spec)
			if err != nil {
				return err
			}
			runtime, err := factory.NewRuntime(ctx, factory.RuntimeOptions{Config: cfg})
			if err != nil {
				return err
			}
			defer runtime.Close(ctx)
			temporalClient, err := runtime.TemporalClient()
			if err != nil {
				return err
			}
			defer temporalClient.Close()

			workflowID := factory.WorkflowIDForRun(req.RunID)
			run, err := temporalClient.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
				ID:        workflowID,
				TaskQueue: cfg.Temporal.TaskQueue,
			}, factory.SoftwareChangeWorkflow, req)
			if err != nil {
				// Idempotent apply: an existing workflow is the desired state.
				if strings.Contains(err.Error(), "already started") || strings.Contains(err.Error(), "AlreadyStarted") {
					fmt.Printf("run %s already exists (workflow %s)\n", req.RunID, workflowID)
					if !wait {
						return nil
					}
					run = temporalClient.GetWorkflow(ctx, workflowID, "")
				} else {
					return fmt.Errorf("start workflow: %w", err)
				}
			} else {
				// The effective change is the run itself when none was named, which
				// is exactly what the change store will record.
				effectiveChange := req.ChangeID
				if effectiveChange == "" {
					effectiveChange = req.RunID
				}
				fmt.Printf("applied run %s (change %s, scope %s)\n", req.RunID, effectiveChange, orDash(req.Scope))
			}
			if !wait {
				return nil
			}
			var manifest factory.RunManifest
			if err := run.Get(ctx, &manifest); err != nil {
				manifest = reportFailure(ctx, runtime, temporalClient, req.RunID, err)
			}
			if asJSON {
				return printJSON(manifest)
			}
			printRunResult(runtime, manifest, req.RunID)
			if manifest.FactoryResult != factory.StateSucceeded {
				return fmt.Errorf("factory result: %s", manifest.FactoryResult)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&file, "f", "f", "", "path to a run manifest (.toml or .json)")
	cmd.Flags().BoolVar(&wait, "wait", false, "wait for the run to finish")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the final manifest as JSON")
	cmd.Flags().StringVar(&runIDIn, "run-id", "", "override the run id in the manifest")
	return cmd
}

// readRunSpec loads a manifest, accepting TOML or JSON by extension.
//
// Unknown fields are rejected: a typo in a reviewed manifest must fail loudly,
// not silently drop the setting it was meant to change.
func readRunSpec(path string) (runSpec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return runSpec{}, fmt.Errorf("read %s: %w", path, err)
	}
	var spec runSpec
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		decoder := json.NewDecoder(strings.NewReader(string(data)))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&spec); err != nil {
			return runSpec{}, fmt.Errorf("parse %s: %w", path, err)
		}
	default:
		decoder := toml.NewDecoder(strings.NewReader(string(data)))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&spec); err != nil {
			return runSpec{}, fmt.Errorf("parse %s: %w", path, err)
		}
	}
	return spec, nil
}

// runRequestFromSpec validates a manifest into a RunRequest.
func runRequestFromSpec(cfg factory.Config, spec runSpec) (factory.RunRequest, error) {
	req := factory.RunRequest{
		RunID:               spec.RunID,
		ChangeID:            spec.ChangeID,
		ParentRunID:         spec.ParentRunID,
		Scope:               spec.Scope,
		Repository:          spec.Repository,
		Revision:            spec.Revision,
		Task:                spec.Task,
		AgentHarness:        spec.AgentHarness,
		AgentModel:          spec.Model,
		Workspace:           spec.Workspace,
		EgressPolicy:        spec.EgressPolicy,
		VerificationProfile: spec.VerificationProfile,
		SandboxTemplate:     spec.SandboxTemplate,
		Review:              spec.Review,
		AgentTimeout:        cfg.Limits.AgentTimeout.Std(),
		VerificationTimeout: cfg.Limits.VerificationTimeout.Std(),
		TotalTimeout:        cfg.Limits.TotalTimeout.Std(),
	}
	// `model` names a declared model when one exists, and is otherwise passed to
	// the harness as a raw model id. A manifest cannot invent a model resource,
	// but it can still pin an exact model id for a harness that accepts one.
	if _, declared := cfg.Models[spec.Model]; declared {
		req.Model = spec.Model
	}
	if spec.SandboxTemplate == "" {
		req.SandboxTemplate = cfg.Cube.TemplateID
	}
	if spec.AgentTimeout != "" {
		d, err := time.ParseDuration(spec.AgentTimeout)
		if err != nil {
			return factory.RunRequest{}, fmt.Errorf("agent_timeout: %w", err)
		}
		req.AgentTimeout = d
	}
	if spec.RunTimeout != "" {
		d, err := time.ParseDuration(spec.RunTimeout)
		if err != nil {
			return factory.RunRequest{}, fmt.Errorf("total_timeout: %w", err)
		}
		req.TotalTimeout = d
	}
	if err := req.Validate(); err != nil {
		return factory.RunRequest{}, err
	}
	return req, nil
}

// ── delete ──────────────────────────────────────────────────────────────────

func newDeleteCommand(configPath *string) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "delete <kind> <name>",
		Short: "Remove a resource that can safely be removed (sandboxes, changes)",
		Long: `Delete a resource.

Only two kinds can be deleted:
  sandboxes   destroys a live sandbox (requires it to be factory-owned)
  changes     abandons a change; its runs and evidence are retained

Runs cannot be deleted. A run is immutable evidence, and an operator tool that
can erase the record of what an agent did is a liability, not a feature.`,
		Args:         cobra.ExactArgs(2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			kind, name := normalizeKind(args[0]), args[1]
			cfg, err := loadConfig(*configPath)
			if err != nil {
				return err
			}
			runtime, err := factory.NewRuntime(ctx, factory.RuntimeOptions{Config: cfg})
			if err != nil {
				return err
			}
			defer runtime.Close(ctx)

			switch kind {
			case "sandboxes":
				infos, err := runtime.Provider.List(ctx)
				if err != nil {
					return err
				}
				for _, info := range infos {
					if info.ID != name {
						continue
					}
					if info.Metadata["origin"] != "factory" && !force {
						return fmt.Errorf("sandbox %s is not factory-owned; refusing to destroy it without --force", name)
					}
					if err := runtime.Provider.Destroy(ctx, name); err != nil {
						return err
					}
					fmt.Printf("destroyed sandbox %s\n", name)
					return nil
				}
				return fmt.Errorf("sandbox %s not found", name)
			case "changes":
				store, err := factory.NewChangeStore(runtime.Artifacts.Root())
				if err != nil {
					return err
				}
				change, err := store.Load(name)
				if err != nil {
					return err
				}
				change.Status = factory.ChangeAbandoned
				change.UpdatedAt = time.Now().UTC()
				if err := store.Save(change); err != nil {
					return err
				}
				fmt.Printf("abandoned change %s (runs and evidence retained)\n", name)
				return nil
			case "runs":
				return fmt.Errorf("runs cannot be deleted: a run is immutable evidence")
			default:
				return fmt.Errorf("cannot delete kind %q", kind)
			}
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "destroy a sandbox the factory does not own")
	return cmd
}

// ── review ──────────────────────────────────────────────────────────────────

func newReviewCommand(configPath *string) *cobra.Command {
	var (
		approve bool
		reject  bool
		note    string
	)
	cmd := &cobra.Command{
		Use:   "review <run-id> (--approve | --reject) [--note text]",
		Short: "Answer a paused run's review gate",
		Long: `Answer a paused run's human review gate.

A run reaches the gate when review is enabled (globally, per scope, or per run).
The decision is recorded in the run's manifest and drives the change lifecycle:
approval closes the change, rejection returns it to OPEN for a rework activation
that references the reviewed run as its parent.`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			runID := args[0]
			switch {
			case approve && reject:
				return fmt.Errorf("--approve and --reject are mutually exclusive")
			case !approve && !reject:
				return fmt.Errorf("one of --approve or --reject is required; a gate must not be closed by accident")
			}
			decision := factory.HumanResultRejected
			if approve {
				decision = factory.HumanResultApproved
			}
			if reject && strings.TrimSpace(note) == "" {
				return fmt.Errorf("--reject requires --note explaining what must change: the note becomes the rework instruction")
			}

			cfg, err := loadConfig(*configPath)
			if err != nil {
				return err
			}
			runtime, err := factory.NewRuntime(ctx, factory.RuntimeOptions{Config: cfg})
			if err != nil {
				return err
			}
			defer runtime.Close(ctx)
			temporalClient, err := runtime.TemporalClient()
			if err != nil {
				return err
			}
			defer temporalClient.Close()

			workflowID := factory.WorkflowIDForRun(runID)
			payload := factory.ReviewDecision{Decision: decision, Note: note}
			entry := factory.AuditEntry{
				Action:  factory.AuditReviewDecision,
				RunID:   runID,
				Outcome: factory.AuditOutcomeAttempted,
				Detail:  map[string]any{"decision": decision, "note": note},
			}
			if err := auditOperatorAction(ctx, runtime, runID, entry); err != nil {
				return fmt.Errorf("record review decision attempt: %w", err)
			}
			if err := temporalClient.SignalWorkflow(ctx, workflowID, "", factory.ReviewSignalName, payload); err != nil {
				entry.Outcome = string(factory.OutcomeFailed)
				entry.Error = err.Error()
				if auditErr := auditOperatorAction(ctx, runtime, runID, entry); auditErr != nil {
					return fmt.Errorf("signal review decision: %v; record audit outcome: %w", err, auditErr)
				}
				return fmt.Errorf("signal review decision: %w", err)
			}
			entry.Outcome = string(factory.OutcomeSuccess)
			if err := auditOperatorAction(ctx, runtime, runID, entry); err != nil {
				return fmt.Errorf("record review decision outcome: %w", err)
			}
			fmt.Printf("review decision sent: %s\n", decision)
			return nil
		},
	}
	cmd.Flags().BoolVar(&approve, "approve", false, "approve the change")
	cmd.Flags().BoolVar(&reject, "reject", false, "reject the change (requires --note)")
	cmd.Flags().StringVar(&note, "note", "", "reviewer note; becomes the rework instruction on rejection")
	return cmd
}

// ── attach ──────────────────────────────────────────────────────────────────

func newAttachCommand(configPath *string) *cobra.Command {
	var (
		sandboxID string
		timeout   time.Duration
		dir       string
	)
	cmd := &cobra.Command{
		Use:   "attach <run-id> [--sandbox <id>] -- <command> [args...]",
		Short: "Run an operator command inside a run's sandbox",
		Long: `Run a command inside a live sandbox, with the factory's own privileges.

This is remote code execution against an environment that holds credentials, so
it is DEFAULT CLOSED: the operator must set [sandbox] allow_attach = true, and
every invocation is recorded in the run's audit trail. It is not a TTY: the
command runs to completion and its streams are printed. Use it to inspect a
failed run, not as an interactive shell.

The sandbox is never resumed implicitly. A suspended sandbox must be resumed
deliberately, because waking one restarts its cost.`,
		Args:         cobra.MinimumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			runID := args[0]
			argv := args[1:]
			if cmd.ArgsLenAtDash() == 0 {
				argv = args
			}
			if len(argv) == 0 {
				return fmt.Errorf("a command is required after --")
			}
			cfg, err := loadConfig(*configPath)
			if err != nil {
				return err
			}
			if !cfg.Sandbox.AllowAttach {
				return fmt.Errorf("attach is disabled: set [sandbox] allow_attach = true to enable it (it is audited)")
			}
			runtime, err := factory.NewRuntime(ctx, factory.RuntimeOptions{Config: cfg})
			if err != nil {
				return err
			}
			defer runtime.Close(ctx)

			target, err := resolveSandboxID(ctx, runtime, runID, sandboxID)
			if err != nil {
				return err
			}
			if err := ensureAwake(ctx, runtime.Provider, target); err != nil {
				return err
			}
			sb, err := runtime.Provider.Reattach(ctx, target)
			if err != nil {
				return err
			}
			if timeout <= 0 {
				timeout = 5 * time.Minute
			}
			entry := factory.AuditEntry{
				Action:    factory.AuditAttach,
				RunID:     runID,
				SandboxID: target,
				Outcome:   factory.AuditOutcomeAttempted,
				Detail: map[string]any{
					// The argv is recorded by length and first element only:
					// the arguments may contain a token an operator pasted.
					"argv_len": len(argv),
					"program":  argv[0],
				},
			}
			if err := auditOperatorAction(ctx, runtime, runID, entry); err != nil {
				return fmt.Errorf("record attach attempt: %w", err)
			}
			execution, err := sb.Execute(ctx, sandbox.Command{
				Argv:        argv,
				Dir:         dir,
				Timeout:     timeout,
				Description: "operator attach",
			})
			entry.Outcome = string(factory.OutcomeSuccess)
			entry.Detail["exit"] = execution.ExitCode
			if err != nil {
				entry.Outcome = string(factory.OutcomeError)
				entry.Error = err.Error()
				if auditErr := auditOperatorAction(ctx, runtime, runID, entry); auditErr != nil {
					return fmt.Errorf("attach: %v; record audit outcome: %w", err, auditErr)
				}
				return err
			}
			if execution.ExitCode != 0 {
				entry.Outcome = string(factory.OutcomeFailed)
			}
			if err := auditOperatorAction(ctx, runtime, runID, entry); err != nil {
				return fmt.Errorf("record attach outcome: %w", err)
			}

			if execution.Stdout != "" {
				fmt.Print(execution.Stdout)
			}
			if execution.Stderr != "" {
				fmt.Fprint(os.Stderr, execution.Stderr)
			}
			if execution.ExitCode != 0 {
				return fmt.Errorf("command exited %d", execution.ExitCode)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&sandboxID, "sandbox", "", "sandbox id (defaults to the run's recorded sandbox)")
	cmd.Flags().DurationVar(&timeout, "timeout", 5*time.Minute, "command timeout")
	cmd.Flags().StringVar(&dir, "dir", "", "working directory inside the sandbox")
	return cmd
}

// ── preview ─────────────────────────────────────────────────────────────────

func newPreviewCommand(configPath *string) *cobra.Command {
	var (
		sandboxID string
		port      int
	)
	cmd := &cobra.Command{
		Use:   "preview <run-id> --port <n>",
		Short: "Publish a sandbox port through the deployment ingress",
		Long: `Publish a port inside a run's sandbox as an HTTP URL.

Preview is DEFAULT CLOSED ([sandbox] allow_preview = true) and audited, because
it exposes a sandbox that may hold credentials and unreviewed code. A suspended
sandbox is never resumed implicitly.`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			runID := args[0]
			if port <= 0 {
				return fmt.Errorf("--port is required")
			}
			cfg, err := loadConfig(*configPath)
			if err != nil {
				return err
			}
			if !cfg.Sandbox.AllowPreview {
				return fmt.Errorf("preview is disabled: set [sandbox] allow_preview = true to enable it (it is audited)")
			}
			runtime, err := factory.NewRuntime(ctx, factory.RuntimeOptions{Config: cfg})
			if err != nil {
				return err
			}
			defer runtime.Close(ctx)

			target, err := resolveSandboxID(ctx, runtime, runID, sandboxID)
			if err != nil {
				return err
			}
			previewer, ok := runtime.Provider.(sandbox.Previewer)
			if !ok || !runtime.Provider.Capabilities().Preview {
				return fmt.Errorf("sandbox provider %s does not support preview", runtime.Provider.Name())
			}
			if err := ensureAwake(ctx, runtime.Provider, target); err != nil {
				return err
			}
			entry := factory.AuditEntry{
				Action:    factory.AuditPreview,
				RunID:     runID,
				SandboxID: target,
				Outcome:   factory.AuditOutcomeAttempted,
				Detail:    map[string]any{"port": port},
			}
			if err := auditOperatorAction(ctx, runtime, runID, entry); err != nil {
				return fmt.Errorf("record preview attempt: %w", err)
			}
			url, err := previewer.PreviewURL(ctx, target, port)
			entry.Outcome = string(factory.OutcomeSuccess)
			entry.Detail["url"] = url
			if err != nil {
				entry.Outcome = string(factory.OutcomeFailed)
				entry.Error = err.Error()
				if auditErr := auditOperatorAction(ctx, runtime, runID, entry); auditErr != nil {
					return fmt.Errorf("preview: %v; record audit outcome: %w", err, auditErr)
				}
				return err
			}
			if err := auditOperatorAction(ctx, runtime, runID, entry); err != nil {
				return fmt.Errorf("record preview outcome: %w", err)
			}
			fmt.Println(url)
			return nil
		},
	}
	cmd.Flags().StringVar(&sandboxID, "sandbox", "", "sandbox id (defaults to the run's recorded sandbox)")
	cmd.Flags().IntVar(&port, "port", 0, "in-sandbox port to publish")
	return cmd
}

// ── helpers ─────────────────────────────────────────────────────────────────

// resolveSandboxID finds the sandbox for a run, preferring the manifest's
// durable record and falling back to the live workflow status.
func resolveSandboxID(ctx context.Context, runtime *factory.Runtime, runID, explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if manifest, err := factory.ReadManifest(runtime.Artifacts, runID); err == nil && manifest.SandboxID != "" {
		return manifest.SandboxID, nil
	}
	temporalClient, err := runtime.TemporalClient()
	if err != nil {
		return "", fmt.Errorf("no manifest for run %s and Temporal is unavailable: %w", runID, err)
	}
	defer temporalClient.Close()
	status, err := factory.QueryRunStatus(ctx, temporalClient, factory.WorkflowIDForRun(runID), "")
	if err != nil || status.SandboxID == "" {
		return "", fmt.Errorf("run %s has no recorded sandbox", runID)
	}
	return status.SandboxID, nil
}

// ensureAwake refuses to operate on a suspended sandbox.
//
// The check exists because connecting to a suspended sandbox resumes it, so
// "look, don't touch" would otherwise be a spending decision made by accident.
func ensureAwake(ctx context.Context, provider sandbox.Provider, sandboxID string) error {
	stater, ok := provider.(sandbox.Stater)
	if !ok {
		return fmt.Errorf("sandbox provider %s cannot report lifecycle state; refusing an action that could implicitly resume it", provider.Name())
	}
	state, err := stater.SandboxState(ctx, sandboxID)
	if err != nil {
		return err
	}
	if state == sandbox.StatePaused {
		return fmt.Errorf("sandbox %s is suspended; resume the run first so waking it is a deliberate decision", sandboxID)
	}
	return nil
}

// auditOperatorAction records an operator action in the run's audit trail.
func auditOperatorAction(ctx context.Context, runtime *factory.Runtime, runID string, entry factory.AuditEntry) error {
	store, err := runtime.Artifacts.ForRun(runID)
	if err != nil {
		return err
	}
	if err := factory.AppendAudit(store, entry, runtime.Redactor); err != nil {
		return err
	}
	_ = ctx
	return nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedStringKeys(m map[string]string) []string { return sortedKeys(m) }

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + "…"
}
