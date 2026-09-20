// Command factory is the software factory's command-line entry point.
//
// The MVP deliberately has no web UI. Operators drive the factory with:
//
//	factory run      submit a task and optionally wait for the verified patch
//	factory status   inspect a run
//	factory logs     show a run's evidence
//	factory worker   run the Temporal worker
//	factory doctor   verify Cube, Temporal and configuration before running
//	factory sandboxes list Cube sandboxes to find leaks
//
// The CLI is also the control plane's security boundary: it accepts only an
// approved repository, an exact revision and task text. It never accepts an
// executable, a host path or an environment override.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"

	"github.com/mitkox/esf/internal/artifacts"
	"github.com/mitkox/esf/internal/factory"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := rootCommand().ExecuteContext(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "factory: %v\n", err)
		os.Exit(1)
	}
}

func rootCommand() *cobra.Command {
	var configPath string

	root := &cobra.Command{
		Use:   "factory",
		Short: "Self-hosted agentic software factory control plane",
		Long: `A durable, policy-controlled software engineering production system.

One task in, one isolated CubeSandbox microVM, one coding agent, deterministic
verification, one verified patch out.`,
		SilenceUsage: true,
	}
	root.PersistentFlags().StringVar(&configPath, "config", "", "path to factory.toml (defaults to $FACTORY_CONFIG or ./factory.toml)")

	root.AddCommand(
		newRunCommand(&configPath),
		newGetCommand(&configPath),
		newDescribeCommand(&configPath),
		newApplyCommand(&configPath),
		newDeleteCommand(&configPath),
		newReviewCommand(&configPath),
		newAttachCommand(&configPath),
		newPreviewCommand(&configPath),
		newStatusCommand(&configPath),
		newLogsCommand(&configPath),
		newWorkerCommand(&configPath),
		newDoctorCommand(&configPath),
		newSandboxesCommand(&configPath),
		newInitCommand(),
	)
	return root
}

func resolveConfigPath(flagValue string) string {
	if strings.TrimSpace(flagValue) != "" {
		return flagValue
	}
	if env := strings.TrimSpace(os.Getenv("FACTORY_CONFIG")); env != "" {
		return env
	}
	if _, err := os.Stat("factory.toml"); err == nil {
		return "factory.toml"
	}
	return ""
}

func loadConfig(flagValue string) (factory.Config, error) {
	path := resolveConfigPath(flagValue)
	cfg, err := factory.LoadConfig(path)
	if err != nil {
		return factory.Config{}, err
	}
	return cfg, nil
}

// ── run ─────────────────────────────────────────────────────────────────────

func newRunCommand(configPath *string) *cobra.Command {
	var (
		repo      string
		localPath string
		rev       string
		task      string
		taskFile  string
		agent     string
		model     string
		profile   string
		template  string
		runID     string
		wait      bool
		agentTO   time.Duration
		scope     string
		workspace string
		egress    string
		changeID  string
		parentRun string
		review    bool
	)

	cmd := &cobra.Command{
		Use:   "run",
		Short: "Submit a task and produce a verified patch",
		Long: `Submit one task to the factory.

The factory creates a fresh CubeSandbox microVM, clones the repository at the
exact revision, runs the coding agent inside the microVM, applies deterministic
verification, extracts the patch, and destroys the microVM.

The agent is selected by NAME from operator configuration. This command cannot
execute an arbitrary program.`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()

			prompt := task
			if taskFile != "" {
				data, err := os.ReadFile(taskFile)
				if err != nil {
					return fmt.Errorf("read task file: %w", err)
				}
				prompt = string(data)
			}
			if strings.TrimSpace(prompt) == "" {
				return errors.New("a task is required (use --task or --task-file)")
			}
			if repo == "" && localPath == "" {
				return errors.New("a repository is required (use --repo or --local-path)")
			}
			if rev == "" {
				return errors.New("--rev is required: runs must be reproducible")
			}

			cfg, err := loadConfig(*configPath)
			if err != nil {
				return err
			}
			if agent == "" {
				agent = firstHarnessName(cfg)
			}
			if profile == "" {
				profile = firstProfileName(cfg)
			}
			if runID == "" {
				runID = newRunID()
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

			req := factory.RunRequest{
				RunID:               runID,
				ChangeID:            changeID,
				ParentRunID:         parentRun,
				Scope:               scope,
				Repository:          repo,
				LocalPath:           localPath,
				Revision:            rev,
				Task:                prompt,
				SandboxTemplate:     template,
				AgentHarness:        agent,
				AgentModel:          model,
				Workspace:           workspace,
				EgressPolicy:        egress,
				VerificationProfile: profile,
				AgentTimeout:        cfg.Limits.AgentTimeout.Std(),
				VerificationTimeout: cfg.Limits.VerificationTimeout.Std(),
				TotalTimeout:        cfg.Limits.TotalTimeout.Std(),
			}
			// --model accepts either a declared model NAME or a raw model id, as it
			// always has. Only a declared name is resolved as a resource and pinned
			// in evidence; a raw id is passed through to the harness unchanged.
			if _, declared := cfg.Models[model]; declared {
				req.Model = model
			}
			if cmd.Flags().Changed("review") {
				req.Review = &review
			}
			if agentTO > 0 {
				req.AgentTimeout = agentTO
			}
			if req.SandboxTemplate == "" {
				req.SandboxTemplate = cfg.Cube.TemplateID
			}

			workflowID := factory.WorkflowIDForRun(runID)
			fmt.Printf("Run ID:      %s\n", runID)
			fmt.Printf("Workflow ID: %s\n", workflowID)
			fmt.Printf("Repository:  %s @ %s\n", displayRepo(repo, localPath), rev)
			fmt.Printf("Agent:       %s\n", agent)
			fmt.Printf("Verification:%s\n", profile)
			fmt.Printf("Template:    %s\n", req.SandboxTemplate)
			fmt.Println()

			run, err := temporalClient.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
				ID:        workflowID,
				TaskQueue: cfg.Temporal.TaskQueue,
			}, factory.SoftwareChangeWorkflow, req)
			if err != nil {
				return fmt.Errorf("start workflow: %w", err)
			}

			if !wait {
				fmt.Println("Submitted. Use `factory status` / `factory logs` to follow it.")
				return nil
			}

			fmt.Println("Waiting for the run to complete...")
			var manifest factory.RunManifest
			if err := run.Get(ctx, &manifest); err != nil {
				// The workflow failed. The manifest may still exist because
				// finalization uses a disconnected context; report what we know.
				manifest = reportFailure(ctx, runtime, temporalClient, runID, err)
			}
			printRunResult(runtime, manifest, runID)
			if manifest.FactoryResult != factory.StateSucceeded {
				return fmt.Errorf("factory result: %s", manifest.FactoryResult)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&repo, "repo", "", "approved repository URL (remote)")
	cmd.Flags().StringVar(&localPath, "local-path", "", "host repository path (packed into the sandbox; used by tests)")
	cmd.Flags().StringVar(&rev, "rev", "", "exact revision to check out (required)")
	cmd.Flags().StringVar(&task, "task", "", "task text")
	cmd.Flags().StringVar(&taskFile, "task-file", "", "read task text from a file")
	cmd.Flags().StringVar(&agent, "agent", "", "agent harness name (from configuration)")
	cmd.Flags().StringVar(&model, "model", "", "optional model override for the agent")
	cmd.Flags().StringVar(&profile, "verification", "", "verification profile name")
	cmd.Flags().StringVar(&template, "sandbox-template", "", "CubeSandbox template id (defaults to configuration)")
	cmd.Flags().StringVar(&runID, "run-id", "", "explicit run id (default: generated)")
	cmd.Flags().BoolVar(&wait, "wait", true, "wait for the run to finish")
	cmd.Flags().DurationVar(&agentTO, "agent-timeout", 0, "override the agent timeout")
	cmd.Flags().StringVar(&scope, "scope", "", "tenancy scope (defaults to the default scope)")
	cmd.Flags().StringVar(&workspace, "workspace", "", "named pre-warmed workspace")
	cmd.Flags().StringVar(&egress, "egress", "", "named egress policy")
	cmd.Flags().StringVar(&changeID, "change", "", "attach the run to a durable change (default: the run itself)")
	cmd.Flags().StringVar(&parentRun, "parent-run", "", "run this one reworks (lineage evidence)")
	cmd.Flags().BoolVar(&review, "review", false, "pause for human review before finalizing")
	return cmd
}

// reportFailure recovers as much result information as possible after a failed
// workflow, so the operator still sees the evidence that was produced.
func reportFailure(ctx context.Context, runtime *factory.Runtime, c client.Client, runID string, cause error) factory.RunManifest {
	fmt.Fprintf(os.Stderr, "\nworkflow error: %v\n", cause)
	manifest, err := factory.ReadManifest(runtime.Artifacts, runID)
	if err == nil {
		return manifest
	}
	// No manifest: fall back to the live workflow status.
	status, serr := factory.QueryRunStatus(ctx, c, factory.WorkflowIDForRun(runID), "")
	if serr == nil {
		return factory.RunManifest{
			RunID:         runID,
			FactoryResult: status.State,
			AgentResult:   status.AgentOutcome,
			Error:         status.Error,
		}
	}
	return factory.RunManifest{
		RunID:         runID,
		FactoryResult: factory.StateInfrastructureFailed,
		Error:         cause.Error(),
	}
}

func printRunResult(runtime *factory.Runtime, manifest factory.RunManifest, runID string) {
	fmt.Println()
	fmt.Printf("FACTORY RESULT: %s\n", manifest.FactoryResult)
	fmt.Printf("AGENT RESULT:   %s\n", manifest.AgentResult)
	if manifest.ChangeID != "" {
		fmt.Printf("CHANGE:         %s\n", manifest.ChangeID)
	}
	if manifest.Scope != "" {
		fmt.Printf("SCOPE:          %s\n", manifest.Scope)
	}
	if manifest.Workspace != "" {
		fmt.Printf("WORKSPACE:      %s\n", manifest.Workspace)
	}
	if manifest.EgressPolicy != "" {
		fmt.Printf("EGRESS:         %s (%s)\n", manifest.EgressPolicy, manifest.EgressSummary)
	}
	if manifest.Model != "" {
		fmt.Printf("MODEL:          %s/%s\n", manifest.ModelProvider, manifest.Model)
	}
	if manifest.ReviewConfigured {
		fmt.Printf("REVIEW:         %s (sandbox %s)\n", manifest.ReviewOutcome, manifest.SuspendResult)
		for _, preview := range manifest.Previews {
			fmt.Printf("PREVIEW:        %d -> %s\n", preview.Port, preview.URL)
		}
	}
	if len(manifest.BudgetExceeded) > 0 {
		fmt.Printf("BUDGET:         EXCEEDED (%s)\n", strings.Join(manifest.BudgetExceeded, "; "))
	}
	if manifest.VerificationResult != "" {
		if manifest.VerificationResult == factory.OutcomeSuccess {
			fmt.Printf("VERIFICATION:   PASSED\n")
		} else {
			fmt.Printf("VERIFICATION:   %s\n", manifest.VerificationResult)
		}
	}
	switch manifest.CleanupResult.Outcome {
	case factory.OutcomeSuccess:
		fmt.Printf("CLEANUP:        PASSED\n")
	case factory.OutcomeSkipped:
		fmt.Printf("CLEANUP:        NOT NEEDED (no sandbox created)\n")
	default:
		fmt.Printf("CLEANUP:        %s (%s)\n", manifest.CleanupResult.Outcome, manifest.CleanupResult.Error)
	}
	if manifest.Error != "" {
		fmt.Printf("ERROR:          %s\n", manifest.Error)
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

	// A gate that rewrites tracked files is worth shouting about: the
	// deliverable patch is captured before verification, so such a change is
	// deliberately NOT in changes.patch.
	if manifest.VerificationMutatedTree {
		fmt.Printf("\nWARNING: a verification gate modified the working tree.\n")
		fmt.Printf("         The deliverable patch is captured BEFORE verification, so that\n")
		fmt.Printf("         change is not in changes.patch.\n")
		if manifest.PostVerificationPatch != "" {
			fmt.Printf("         Evidence: %s/%s\n", runID, manifest.PostVerificationPatch)
		}
	}

	store, err := runtime.Artifacts.ForRun(runID)
	if err == nil {
		patchPath := filepath.Join(store.Dir(), factory.ArtifactPatch)
		if _, statErr := os.Stat(patchPath); statErr == nil {
			fmt.Printf("\nPatch:\n%s\n", patchPath)
			fmt.Printf("Artifacts:\n%s\n", store.Dir())
			return
		}
		fmt.Printf("\nArtifacts:\n%s\n", store.Dir())
	}
}

// ── status ──────────────────────────────────────────────────────────────────

func newStatusCommand(configPath *string) *cobra.Command {
	var (
		asJSON bool
		watch  bool
	)
	cmd := &cobra.Command{
		Use:          "status <run-id>",
		Short:        "Show a run's status and result",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			runID := args[0]

			cfg, err := loadConfig(*configPath)
			if err != nil {
				return err
			}
			runtime, err := factory.NewRuntime(ctx, factory.RuntimeOptions{Config: cfg})
			if err != nil {
				return err
			}
			defer runtime.Close(ctx)

			// A completed run's authoritative record is its durable manifest.
			if manifest, err := factory.ReadManifest(runtime.Artifacts, runID); err == nil {
				if asJSON {
					return printJSON(manifest)
				}
				printRunResult(runtime, manifest, runID)
				return nil
			}

			// Otherwise ask Temporal about the in-flight workflow.
			c, err := runtime.TemporalClient()
			if err != nil {
				return err
			}
			defer c.Close()

			workflowID := factory.WorkflowIDForRun(runID)
			if watch {
				return watchRunStatus(ctx, c, workflowID, runID)
			}
			status, err := factory.QueryRunStatus(ctx, c, workflowID, "")
			if err == nil {
				if asJSON {
					return printJSON(status)
				}
				printRunStatus(status)
				return nil
			}

			desc, derr := factory.DescribeWorkflow(ctx, c, workflowID, "")
			if derr != nil {
				return fmt.Errorf("run %s not found (no manifest and no workflow)", runID)
			}
			if asJSON {
				return printJSON(desc)
			}
			fmt.Printf("Run ID:      %s\n", runID)
			fmt.Printf("Workflow:    %s\n", desc.WorkflowID)
			fmt.Printf("Status:      %s\n", desc.Status)
			fmt.Printf("Started:     %s\n", desc.StartTime)
			if desc.CloseTime != "" {
				fmt.Printf("Closed:      %s\n", desc.CloseTime)
			}
			fmt.Println("\nNo manifest yet; the run may still be in progress.")
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON")
	cmd.Flags().BoolVar(&watch, "watch", false, "stream condition transitions until the run finishes")
	return cmd
}

// printRunStatus renders a live workflow status, including conditions.
func printRunStatus(status factory.RunStatus) {
	fmt.Printf("Run ID:      %s\n", status.RunID)
	if status.ChangeID != "" {
		fmt.Printf("Change:      %s\n", status.ChangeID)
	}
	if status.Scope != "" {
		fmt.Printf("Scope:       %s\n", status.Scope)
	}
	fmt.Printf("State:       %s\n", status.State)
	fmt.Printf("Step:        %s\n", status.CurrentStep)
	if status.SandboxID != "" {
		fmt.Printf("Sandbox:     %s\n", status.SandboxID)
	}
	if status.BaselineSHA != "" {
		fmt.Printf("Baseline:    %s\n", status.BaselineSHA)
	}
	fmt.Printf("Agent:       %s\n", status.AgentOutcome)
	if len(status.Conditions) > 0 {
		fmt.Println("Conditions:")
		for _, c := range status.Conditions {
			fmt.Printf("  %-22s %-7s %s\n", c.Type, c.Status, c.Reason)
		}
	}
}

// watchRunStatus streams condition transitions until the workflow closes.
//
// It polls the workflow query rather than opening a long-lived connection: the
// status query is cheap, and a watcher that survives a dropped connection
// (which polling does) is more useful in a terminal than a stream that dies
// with the network.
func watchRunStatus(ctx context.Context, c client.Client, workflowID, runID string) error {
	seen := map[factory.ConditionType]factory.ConditionStatus{}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		status, err := factory.QueryRunStatus(ctx, c, workflowID, "")
		if err != nil {
			// The workflow may have closed between polls; the manifest is then
			// the record to read, and the caller can re-run `status`.
			return fmt.Errorf("run %s is no longer queryable (it may have finished): %w", runID, err)
		}
		for _, cond := range status.Conditions {
			if seen[cond.Type] == cond.Status {
				continue
			}
			seen[cond.Type] = cond.Status
			fmt.Printf("%s %-22s %-7s %s\n", time.Now().Format("15:04:05"), cond.Type, cond.Status, cond.Reason)
		}
		if status.State == factory.StatePaused {
			fmt.Printf("%s %-22s %-7s %s\n", time.Now().Format("15:04:05"), "PAUSED", "", "awaiting `factory review "+runID+" --approve|--reject`")
		}
		if isTerminalRunState(status.State) {
			fmt.Printf("%s final state: %s\n", time.Now().Format("15:04:05"), status.State)
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// isTerminalRunState reports whether a state ends a run.
func isTerminalRunState(state factory.RunState) bool {
	switch state {
	case factory.StateAgentFailed, factory.StateVerificationFailed, factory.StateSucceeded,
		factory.StateCancelled, factory.StateInfrastructureFailed, factory.StateInvalidRequest:
		return true
	default:
		return false
	}
}

// ── logs ────────────────────────────────────────────────────────────────────

func newLogsCommand(configPath *string) *cobra.Command {
	var tail int
	cmd := &cobra.Command{
		Use:          "logs <run-id>",
		Short:        "Show a run's evidence",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			runID := args[0]

			cfg, err := loadConfig(*configPath)
			if err != nil {
				return err
			}
			store, err := artifacts.NewLocal(cfg.Storage.DataDir)
			if err != nil {
				return err
			}
			runStore, err := store.ForRun(runID)
			if err != nil {
				return err
			}
			paths, err := runStore.List(".")
			if err != nil {
				return err
			}
			if len(paths) == 0 {
				return fmt.Errorf("no artifacts for run %s", runID)
			}
			sort.Strings(paths)

			fmt.Printf("Artifacts for run %s (%s):\n\n", runID, runStore.Location())
			for _, p := range paths {
				fmt.Printf("  %s\n", p)
			}

			for _, name := range []string{factory.ArtifactAgentStdout, factory.ArtifactAgentStderr} {
				data, err := runStore.Read(name)
				if err != nil || len(data) == 0 {
					continue
				}
				fmt.Printf("\n── %s ──\n%s\n", name, tailLines(string(data), tail))
			}
			_ = ctx
			return nil
		},
	}
	cmd.Flags().IntVar(&tail, "tail", 40, "lines of agent output to show per stream")
	return cmd
}

// ── worker ──────────────────────────────────────────────────────────────────

func newWorkerCommand(configPath *string) *cobra.Command {
	return &cobra.Command{
		Use:          "worker",
		Short:        "Run the Temporal worker (the factory's activity executor)",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			cfg, err := loadConfig(*configPath)
			if err != nil {
				return err
			}
			runtime, err := factory.NewRuntime(ctx, factory.RuntimeOptions{Config: cfg})
			if err != nil {
				return err
			}
			defer runtime.Close(ctx)
			return runtime.RunWorker(ctx)
		},
	}
}

// ── doctor ──────────────────────────────────────────────────────────────────

func newDoctorCommand(configPath *string) *cobra.Command {
	return &cobra.Command{
		Use:          "doctor",
		Short:        "Verify configuration, CubeSandbox and Temporal before running",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 2*time.Minute)
			defer cancel()

			cfg, err := loadConfig(*configPath)
			if err != nil {
				return err
			}
			fmt.Println("Configuration:")
			fmt.Printf("  data dir:        %s\n", cfg.Storage.DataDir)
			fmt.Printf("  cube api:        %s\n", cfg.Cube.APIURL)
			fmt.Printf("  cube template:   %s\n", cfg.Cube.TemplateID)
			fmt.Printf("  cube api key:    %s\n", redacted(cfg.Cube.APIKey))
			fmt.Printf("  temporal:        %s (namespace %s)\n", cfg.Temporal.HostPort, cfg.Temporal.Namespace)
			fmt.Printf("  task queue:      %s\n", cfg.Temporal.TaskQueue)
			fmt.Printf("  remote repos:    %v\n", cfg.Repositories.Allowed)
			fmt.Println()

			problems := 0

			fmt.Println("CubeSandbox:")
			if runtime, err := factory.NewRuntime(ctx, factory.RuntimeOptions{Config: cfg}); err == nil {
				defer runtime.Close(ctx)
				if err := runtime.Provider.Ping(ctx); err != nil {
					fmt.Printf("  [FAIL] %v\n", err)
					problems++
				} else {
					fmt.Printf("  [ok]   provider %s reachable, template %s exists\n",
						runtime.Provider.Name(), cfg.Cube.TemplateID)
					if infos, err := runtime.Provider.List(ctx); err == nil {
						fmt.Printf("  [ok]   %d live sandbox(es)\n", len(infos))
						for _, info := range infos {
							origin := info.Metadata["origin"]
							if origin == "factory" {
								fmt.Printf("         - %s (factory run %s)\n", info.ID, info.Metadata["run_id"])
							}
						}
					}
				}
			} else {
				fmt.Printf("  [FAIL] %v\n", err)
				problems++
			}
			fmt.Println()

			fmt.Println("Temporal:")
			if runtime, err := factory.NewRuntime(ctx, factory.RuntimeOptions{Config: cfg}); err == nil {
				defer runtime.Close(ctx)
				if c, err := runtime.TemporalClient(); err != nil {
					fmt.Printf("  [FAIL] %v\n", err)
					problems++
				} else {
					defer c.Close()
					_, err := c.DescribeWorkflowExecution(ctx, "factory-doctor-probe", "")
					// A not-found error still proves the frontend answered.
					if err != nil && strings.Contains(err.Error(), "Unavailable") {
						fmt.Printf("  [FAIL] %v\n", err)
						problems++
					} else {
						fmt.Printf("  [ok]   %s reachable\n", cfg.Temporal.HostPort)
					}
				}
			}
			fmt.Println()

			fmt.Println("Harnesses:")
			if harnesses, err := cfg.BuildHarnesses(); err == nil {
				for _, name := range harnesses.Names() {
					h, _ := harnesses.Resolve(name)
					fmt.Printf("  [ok]   %s (model %s)\n", name, h.Model())
				}
			} else {
				fmt.Printf("  [FAIL] %v\n", err)
				problems++
			}

			if problems > 0 {
				return fmt.Errorf("%d problem(s) found", problems)
			}
			fmt.Println("\nFactory doctor: all checks passed.")
			return nil
		},
	}
}

// ── sandboxes ───────────────────────────────────────────────────────────────

func newSandboxesCommand(configPath *string) *cobra.Command {
	return &cobra.Command{
		Use:          "sandboxes",
		Short:        "List CubeSandboxes (use this to find leaks)",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			cfg, err := loadConfig(*configPath)
			if err != nil {
				return err
			}
			runtime, err := factory.NewRuntime(ctx, factory.RuntimeOptions{Config: cfg})
			if err != nil {
				return err
			}
			defer runtime.Close(ctx)

			infos, err := runtime.Provider.List(ctx)
			if err != nil {
				return err
			}
			if len(infos) == 0 {
				fmt.Println("No live sandboxes.")
				return nil
			}
			fmt.Printf("%d live sandbox(es):\n\n", len(infos))
			factoryCount := 0
			for _, info := range infos {
				origin := info.Metadata["origin"]
				if origin == "factory" {
					factoryCount++
				}
				fmt.Printf("  %-34s template=%s state=%s origin=%s run=%s\n",
					info.ID, info.Template, info.State, originOf(origin), info.Metadata["run_id"])
			}
			if factoryCount > 0 {
				return fmt.Errorf("%d sandbox(es) were created by the factory and are still alive", factoryCount)
			}
			return nil
		},
	}
}

// ── init ────────────────────────────────────────────────────────────────────

func newInitCommand() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:          "init",
		Short:        "Write an example factory.toml",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			const target = "factory.toml"
			if !force {
				if _, err := os.Stat(target); err == nil {
					return fmt.Errorf("%s already exists (use --force to overwrite)", target)
				}
			}
			if err := os.WriteFile(target, []byte(exampleConfig()), 0o644); err != nil {
				return err
			}
			fmt.Printf("Wrote %s\n", target)
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing file")
	return cmd
}

// ── helpers ─────────────────────────────────────────────────────────────────

func printJSON(v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}

func newRunID() string {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("run-%d", time.Now().UnixNano())
	}
	return "run-" + hex.EncodeToString(buf)
}

func displayRepo(repo, localPath string) string {
	if repo != "" {
		return repo
	}
	return "local:" + localPath
}

func firstHarnessName(cfg factory.Config) string {
	names := make([]string, 0, len(cfg.Harnesses))
	for name := range cfg.Harnesses {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return ""
	}
	return names[0]
}

func firstProfileName(cfg factory.Config) string {
	names := make([]string, 0, len(cfg.Verification))
	for name := range cfg.Verification {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return ""
	}
	return names[0]
}

func tailLines(text string, n int) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if n > 0 && len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func redacted(secret string) string {
	if secret == "" {
		return "unset"
	}
	return "[REDACTED]"
}

func originOf(origin string) string {
	if origin == "" {
		return "unmanaged"
	}
	return origin
}

// ensure the enumspb import is used for a compile-time assertion that the
// Temporal API version this code was written against is present.
var _ = enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING
var _ = artifacts.ErrInvalidPath

func exampleConfig() string {
	return `# Factory operator configuration.
#
# This file is OPERATOR POLICY. A task can choose only: task text, an approved
# repository, and an exact revision. It can never choose an executable, a host
# path, or a credential.

factory_version = "0.1.0"

[cube]
# Discovered on this host: the Cube API is NOT on the SDK default port 3000.
api_url         = "http://127.0.0.1:4000"
template_id     = "tpl-your-ready-template"
proxy_node_ip   = "192.0.2.10"
proxy_port_http = 80
idle_timeout    = "60m"
request_timeout = "60s"

[temporal]
host_port  = "127.0.0.1:7233"
namespace  = "default"
task_queue = "factory"

[storage]
data_dir = ".factory"

[sandbox]
# Installed in EVERY sandbox before the agent harness runs. These belong to the
# factory, not to a harness: repository preparation needs git whatever the agent
# is, so a harness must not have to remember to declare it.
base_packages = ["git", "ca-certificates"]

[limits]
agent_timeout        = "30m"
verification_timeout = "15m"
total_timeout        = "90m"
sandbox_idle_timeout = "60m"

[observability]
# Empty disables trace export; the factory then uses no-op tracers.
otel_endpoint = ""
service_name  = "factory"

[repositories]
# Remote repositories must match one of these prefixes. Empty disallows all
# remote repositories; local sources remain available for tests.
allowed = []

# ── Agent harnesses ──────────────────────────────────────────────────────────
# The harness name is what a request may select. The executable and arguments
# are operator configuration and are never client-supplied.

[harnesses.opencode2]
type        = "opencode"
binary      = "/home/USER/.npm-global/lib/node_modules/@opencode/cli/bin/opencode.exe"
model       = "<provider>/<model>"
timeout     = "30m"
packages    = ["git", "ca-certificates"]
prompt_mode = "stdin_file"

# Only these host environment variables reach the sandbox agent. Everything else
# on the host is dropped, so unrelated secrets cannot leak into a run.
pass_env = ["FACTORY_AGENT_TOKEN"]

# Pre-stage the agent's model catalog. A coding agent that must fetch a model
# catalog from the network at start-up is neither reproducible nor reliable:
# the run would depend on an external service being reachable and fast at that
# instant. Staging it makes model resolution deterministic and offline.
# catalog_cache = "/home/USER/.cache/opencode/models.json"

# Provider session state, staged so the agent can authenticate.
#
# SECURITY: this makes the file readable by the agent INSIDE the sandbox. It is
# opt-in operator configuration, never derived from task text, never logged, and
# never written into the task prompt. Its contents are also registered with the
# evidence redactor. The cleaner alternative — an egress proxy that injects the
# credential header so it never enters the sandbox — is Phase 2 work.
# provider_files = { "/root/.local/share/opencode/auth.json" = "/home/USER/.local/share/opencode/auth.json" }

# A deterministic conformance agent used to prove the factory contract without a
# language model. It receives the task on stdin and exits zero on success, like
# any other harness. Hosted by the end-to-end acceptance test.
[harnesses.conformance]
type        = "generic"
executable  = "/bin/sh"
args        = ["/workspace/repository/.factory-agent.sh"]
timeout     = "5m"
prompt_mode = "stdin_file"

# ── Deterministic verification ───────────────────────────────────────────────
# Verification is ALWAYS a structured argv. There is no shell-string form: an
# API client must never be able to smuggle shell syntax into a gate.

[verification.default]
name = "default"

[[verification.default.steps]]
id          = "build"
argv        = ["./build.sh"]
mandatory   = true
timeout     = "10m"
description = "repository build script"

[[verification.default.steps]]
id          = "unit-tests"
argv        = ["./test.sh"]
mandatory   = true
timeout     = "10m"
description = "repository test script"
`
}
