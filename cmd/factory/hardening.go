package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"go.temporal.io/sdk/client"

	"github.com/mitkox/esf/internal/factory"
	artifacts "github.com/mitkox/esf/internal/factoryartifacts"
)

// newCancelCommand stops one run.
//
// The mid-flight control is deliberately first-class: during an incident an
// operator must not have to learn the workflow engine's CLI to stop a run.
func newCancelCommand(configPath *string) *cobra.Command {
	return &cobra.Command{
		Use:          "cancel <run-id>",
		Short:        "Cancel a run's workflow",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			runID := args[0]
			temporalClient, err := connectRuntime(ctx, *configPath)
			if err != nil {
				return err
			}
			defer temporalClient.Close()

			if err := factory.CancelRun(ctx, temporalClient, runID); err != nil {
				return err
			}
			fmt.Printf("cancelled run %s\n", runID)
			fmt.Println("Cleanup runs on the cancellation path; `factory status` reports the CANCELLED outcome and verified cleanup.")
			return nil
		},
	}
}

// newHaltCommand stops every running run matching a filter.
//
// It defaults to a dry run. Stopping many runs at once is a containment action
// with real cost, so the operator sees exactly what would stop and must pass
// --yes to act.
func newHaltCommand(configPath *string) *cobra.Command {
	var (
		harness    string
		scope      string
		repository string
		yes        bool
		asJSON     bool
	)
	cmd := &cobra.Command{
		Use:          "halt",
		Short:        "Cancel running runs, optionally filtered by harness, scope or repository",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			temporalClient, err := connectRuntime(ctx, *configPath)
			if err != nil {
				return err
			}
			defer temporalClient.Close()

			filter := factory.HaltFilter{Harness: harness, Scope: scope, Repository: repository}
			cfg, err := loadConfig(*configPath)
			if err != nil {
				return err
			}
			dc, err := factory.TemporalDataConverter(cfg)
			if err != nil {
				return err
			}
			if !yes {
				runs, err := factory.ListOpenRuns(ctx, temporalClient, filter, dc)
				if err != nil {
					return err
				}
				if asJSON {
					return printJSON(runs)
				}
				if len(runs) == 0 {
					fmt.Println("No running runs match.")
					return nil
				}
				printOpenRuns(runs)
				fmt.Printf("\nDry run: %d run(s) would be cancelled. Re-run with --yes to stop them.\n", len(runs))
				return nil
			}

			runs, err := factory.HaltRuns(ctx, temporalClient, filter, dc)
			if asJSON {
				if printErr := printJSON(runs); printErr != nil {
					return printErr
				}
			} else {
				fmt.Printf("Cancellation requested for %d run(s).\n", len(runs))
			}
			return err
		},
	}
	cmd.Flags().StringVar(&harness, "harness", "", "only runs using this agent harness")
	cmd.Flags().StringVar(&scope, "scope", "", "only runs in this scope")
	cmd.Flags().StringVar(&repository, "repo", "", "only runs whose repository contains this substring")
	cmd.Flags().BoolVar(&yes, "yes", false, "actually cancel the matching runs (default is a dry run)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print machine-readable output")
	return cmd
}

// newThreatsCommand aggregates hardening evidence across runs.
//
// The per-run view is the factory's strength and also its blind spot: a pattern
// shared by many runs is invisible in any single manifest. This command is the
// aggregate view.
func newThreatsCommand(configPath *string) *cobra.Command {
	var (
		limit  int
		asJSON bool
	)
	cmd := &cobra.Command{
		Use:          "threats",
		Short:        "Aggregate hardening evidence across runs",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(*configPath)
			if err != nil {
				return err
			}
			store, err := artifacts.NewLocal(cfg.Storage.DataDir)
			if err != nil {
				return err
			}
			report, err := factory.BuildThreatReport(store, limit)
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(report)
			}
			printThreatReport(report)
			return nil
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 20, "maximum per-run entries to list (0 for all)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print machine-readable output")
	return cmd
}

// connectRuntime dials Temporal for an operational command without building a
// runtime.
//
// Containment must not depend on policy compliance: a worker refuses to start
// on an unacknowledged open egress policy, but an operator still has to be able
// to stop the runs already in flight.
func connectRuntime(_ context.Context, configPath string) (client.Client, error) {
	cfg, err := loadConfig(configPath)
	if err != nil {
		return nil, err
	}
	return factory.NewTemporalClient(cfg, nil)
}

func printOpenRuns(runs []factory.OpenRun) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "RUN ID\tHARNESS\tSCOPE\tREPOSITORY\tSTARTED")
	for _, run := range runs {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", run.RunID, dash(run.Harness), dash(run.Scope), dash(run.Repository), run.StartedAt.Format(time.RFC3339))
	}
	_ = w.Flush()
}

func printThreatReport(report factory.ThreatReport) {
	fmt.Printf("Runs inspected:        %d\n", report.Runs)
	fmt.Printf("Runs with findings:    %d\n", report.RunsWithFindings)
	fmt.Printf("Quarantined:           %d\n", report.Tripped)
	fmt.Printf("Blocked by agent:      %d\n", report.Blocked)
	fmt.Printf("Gate tampering:        %d\n", report.GateTampered)
	fmt.Printf("Egress violations:     %d\n", report.EgressViolations)
	fmt.Printf("Alerts not delivered:  %d\n", report.AlertsUndelivered)

	if len(report.Rules) > 0 {
		fmt.Println("\nFindings by rule:")
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "RULE\tSEVERITY\tCATEGORY\tRUNS\tFINDINGS")
		for _, rule := range report.Rules {
			fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%d\n", rule.RuleID, dash(rule.Severity), dash(rule.Category), rule.Runs, rule.Findings)
		}
		_ = w.Flush()
	}

	if len(report.EgressObservations) > 0 {
		fmt.Println("\nMeasured egress posture:")
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "RUN\tPOLICY\tEXPECT DENY\tINTERNET\tMETADATA\tUID\tVIOLATIONS")
		for _, obs := range report.EgressObservations {
			fmt.Fprintf(w, "%s\t%s\t%t\t%s\t%s\t%d\t%d\n", obs.RunID, dash(obs.Policy), obs.ExpectDeny,
				reachability(obs.CanaryReachable, obs.CanaryDecided), reachability(obs.MetadataReachable, obs.MetadataDecided), obs.AgentUID, obs.Violations)
		}
		_ = w.Flush()
	}

	if len(report.Entries) > 0 {
		fmt.Println("\nRecent runs:")
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "RUN\tRESULT\tFINDINGS\tMAX\tTRIPPED\tBLOCKED\tGATES\tEGRESS")
		for _, entry := range report.Entries {
			gates := "-"
			if len(entry.GateModified) > 0 {
				gates = strings.Join(entry.GateModified, ",")
			}
			fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%t\t%t\t%s\t%d\n", entry.RunID, entry.Result, entry.Findings,
				dash(entry.MaxSeverity), entry.Tripped, entry.Blocked, gates, entry.EgressIssues)
		}
		_ = w.Flush()
	}
}

func reachability(reachable, decided bool) string {
	if !decided {
		return "unknown"
	}
	if reachable {
		return "reachable"
	}
	return "blocked"
}

func dash(v string) string {
	if strings.TrimSpace(v) == "" {
		return "-"
	}
	return v
}

// printHardeningSummary renders the defense-in-depth record for one run.
//
// It is printed even when everything is clean: "the boundary was measured and
// held" is a different claim from "no hardening evidence was recorded", and an
// operator must be able to tell them apart.
func printHardeningSummary(h *factory.HardeningEvidence) {
	if h == nil {
		fmt.Printf("HARDENING:      no hardening evidence recorded\n")
		return
	}
	if p := h.EgressProbe; p != nil {
		internet := strings.ToUpper(reachability(p.Canary.Reachable, p.Canary.Decided))
		metadata := strings.ToUpper(reachability(p.Metadata.Reachable, p.Metadata.Decided))
		line := fmt.Sprintf("policy=%s expect_deny=%t internet=%s metadata=%s uid=%d", dash(p.Policy), p.ExpectDeny, internet, metadata, p.AgentUID)
		if len(p.Violations) > 0 {
			fmt.Printf("EGRESS:         VIOLATED (%s)\n", line)
			for _, v := range p.Violations {
				fmt.Printf("                - %s\n", v)
			}
		} else {
			fmt.Printf("EGRESS:         measured (%s)\n", line)
		}
	} else {
		fmt.Printf("EGRESS:         not measured (probe disabled)\n")
	}
	if b := h.Behavior; b != nil {
		if b.Tripped {
			fmt.Printf("BEHAVIOR:       TRIPPED — %d finding(s), max %s\n", len(b.Findings), b.MaxSeverity)
			for _, finding := range b.Findings {
				fmt.Printf("                - %s %s (%s:%d)\n", finding.Severity, finding.RuleID, finding.Stream, finding.Line)
			}
		} else if len(b.Findings) > 0 {
			fmt.Printf("BEHAVIOR:       %d finding(s) below the trip threshold, max %s\n", len(b.Findings), b.MaxSeverity)
		} else {
			fmt.Printf("BEHAVIOR:       clean (%d lines scanned, ruleset %s)\n", b.ScannedLines, b.RulesetVersion)
		}
	} else {
		fmt.Printf("BEHAVIOR:       not scanned (monitor disabled)\n")
	}
	if h.BehaviorError != "" {
		fmt.Printf("BEHAVIOR:       scan problem: %s\n", h.BehaviorError)
	}
	if g := h.GateIntegrity; g != nil {
		switch {
		case len(g.Modified) > 0:
			fmt.Printf("GATE INTEGRITY: VIOLATED — the tree modified %s\n", strings.Join(g.Modified, ", "))
		case !g.Checked:
			fmt.Printf("GATE INTEGRITY: not checked\n")
		default:
			fmt.Printf("GATE INTEGRITY: unchanged (%s, %d gate program(s) checked)\n", dash(g.Method), len(g.GatePaths))
		}
	} else {
		fmt.Printf("GATE INTEGRITY: not checked\n")
	}
	for _, alert := range h.Alerts {
		state := "delivered"
		if !alert.Delivered {
			state = "NOT DELIVERED"
		}
		fmt.Printf("ALERT:          %s [%s] %s", alert.Kind, alert.Severity, state)
		if alert.Error != "" {
			fmt.Printf(" (%s)", alert.Error)
		}
		fmt.Println()
	}
}
