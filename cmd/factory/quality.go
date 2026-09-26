package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/mitkox/esf/internal/assurance"
	"github.com/mitkox/esf/internal/factory"
	"github.com/spf13/cobra"
)

func submitQuality(ctx context.Context, cfg factory.Config, req factory.RunRequest, wait bool) error {
	c := factory.NewQualityClient(cfg.QualitySocket())
	var run assurance.Run
	if err := c.Call(ctx, "POST", "/v1/runs", req, &run); err != nil {
		return err
	}
	if !wait {
		return printJSON(run)
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for run.Decision == nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
		if err := c.Call(ctx, "GET", "/v1/runs/"+run.ID, nil, &run); err != nil {
			return err
		}
	}
	if err := printJSON(run.Manifest); err != nil {
		return err
	}
	if run.Decision.Result != "approved" {
		return fmt.Errorf("quality rejected: %s", strings.Join(run.Decision.Reasons, "; "))
	}
	return nil
}

func newQualityCommand(configPath *string) *cobra.Command {
	root := &cobra.Command{Use: "quality", Short: "Inspect and operate the authenticated assurance control plane"}
	var socket string
	root.PersistentFlags().StringVar(&socket, "socket", "", "worker Unix socket (avoids loading worker configuration)")
	root.PersistentFlags().Bool("json", true, "emit machine-readable JSON")
	clientFor := func() (*factory.QualityClient, error) {
		if socket != "" {
			return factory.NewQualityClient(socket), nil
		}
		cfg, err := loadConfig(*configPath)
		if err != nil {
			return nil, err
		}
		return factory.NewQualityClient(cfg.QualitySocket()), nil
	}
	query := func(use, short string, min, max int, endpoint func([]string) string) *cobra.Command {
		return &cobra.Command{Use: use, Short: short, Args: cobra.RangeArgs(min, max), RunE: func(cmd *cobra.Command, args []string) error {
			c, err := clientFor()
			if err != nil {
				return err
			}
			var data json.RawMessage
			if err = c.Call(cmd.Context(), "GET", endpoint(args), nil, &data); err != nil {
				return err
			}
			if strings.HasSuffix(endpoint(args), "/attestation") {
				_, err = cmd.OutOrStdout().Write(data)
				return err
			}
			return printJSON(data)
		}}
	}
	root.AddCommand(query("runs", "List controlled runs", 0, 0, func([]string) string { return "/v1/runs" }), query("status <run-id>", "Explain the decision and missing controls", 1, 1, func(a []string) string { return "/v1/runs/" + a[0] + "/status" }), query("show <run-id>", "Read the full frozen run record", 1, 1, func(a []string) string { return "/v1/runs/" + a[0] }), query("evidence <run-id>", "Read evidence receipts", 1, 1, func(a []string) string { return "/v1/runs/" + a[0] + "/evidence" }), query("events <run-id>", "Read the append-only audit history", 1, 1, func(a []string) string { return "/v1/runs/" + a[0] + "/events" }), query("attestation <run-id>", "Export the stored unsigned in-toto statement", 1, 1, func(a []string) string { return "/v1/runs/" + a[0] + "/attestation" }), query("nc", "List non-conformances", 0, 0, func([]string) string { return "/v1/nc" }), query("outbox", "Inspect pending dispatch, exports and synchronization", 0, 0, func([]string) string { return "/v1/outbox" }), query("object <digest>", "Read immutable bytes as base64 JSON", 1, 1, func(a []string) string { return "/v1/objects/" + a[0] }))
	policy := query("policy", "List active operator policies", 0, 0, func([]string) string { return "/v1/policies" })
	policy.AddCommand(&cobra.Command{Use: "validate <policy.yaml>...", Short: "Strict offline policy validation", Args: cobra.MinimumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		var out []assurance.PolicyRef
		for _, p := range args {
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			policy, err := assurance.ParsePolicy(b)
			if err != nil {
				return err
			}
			out = append(out, assurance.PolicyRef{Name: policy.Metadata.Name, Version: policy.Metadata.Version, Digest: assurance.Hash(policy)})
		}
		return printJSON(out)
	}})
	root.AddCommand(policy)
	var paths []string
	evaluate := &cobra.Command{Use: "evaluate <run-id>", Short: "Evaluate hypothetical paths against a run's frozen policy (read-only)", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		c, err := clientFor()
		if err != nil {
			return err
		}
		var plan assurance.Plan
		if err = c.Call(cmd.Context(), "POST", "/v1/runs/"+args[0]+"/evaluate", map[string]any{"paths": paths}, &plan); err != nil {
			return err
		}
		return printJSON(plan)
	}}
	evaluate.Flags().StringSliceVar(&paths, "paths", nil, "repository-relative paths")
	root.AddCommand(evaluate)
	for _, action := range []string{"approve", "reject", "exception"} {
		var reason, operation, control string
		command := &cobra.Command{Use: action + " <run-id>", Short: "Commit an authenticated " + action + " decision", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
			if reason == "" {
				return errors.New("--reason is required")
			}
			if operation == "" {
				operation = uuid.NewString()
			}
			c, err := clientFor()
			if err != nil {
				return err
			}
			var run assurance.Run
			if err = c.Call(cmd.Context(), "GET", "/v1/runs/"+args[0], nil, &run); err != nil {
				return err
			}
			if run.Candidate == nil || run.Plan == nil || run.Approval == nil {
				return errors.New("run has no pending decision request")
			}
			var input any
			route := "approval"
			if action == "exception" {
				if control == "" {
					return errors.New("--control is required")
				}
				route = "exception"
				input = assurance.ExceptionInput{Operation: operation, ControlID: control, CandidateDigest: run.Candidate.Digest, PlanDigest: run.Plan.Digest, Reason: reason}
			} else {
				input = assurance.ApprovalInput{Operation: operation, RequestID: run.Approval.ID, CandidateDigest: run.Candidate.Digest, PlanDigest: run.Plan.Digest, Decision: action, Reason: reason}
			}
			if err = c.Call(cmd.Context(), "POST", "/v1/runs/"+args[0]+"/"+route, input, &run); err != nil {
				return err
			}
			return printJSON(run)
		}}
		command.Flags().StringVar(&reason, "reason", "", "decision rationale (required)")
		command.Flags().StringVar(&operation, "operation", "", "stable idempotency ID for retries")
		if action == "exception" {
			command.Flags().StringVar(&control, "control", "", "failed control ID")
		}
		root.AddCommand(command)
	}
	postFile := func(use, short string, count int, endpoint func([]string) string) *cobra.Command {
		var file string
		command := &cobra.Command{Use: use, Short: short, Args: cobra.ExactArgs(count), RunE: func(cmd *cobra.Command, args []string) error {
			if file == "" {
				return errors.New("--file is required")
			}
			b, err := os.ReadFile(file)
			if err != nil {
				return err
			}
			if !json.Valid(b) {
				return errors.New("file must contain one JSON value")
			}
			c, err := clientFor()
			if err != nil {
				return err
			}
			var result json.RawMessage
			if err = c.Call(cmd.Context(), "POST", endpoint(args), json.RawMessage(b), &result); err != nil {
				return err
			}
			return printJSON(result)
		}}
		command.Flags().StringVarP(&file, "file", "f", "", "JSON request file")
		return command
	}
	root.AddCommand(postFile("submit", "Admit a RunRequest JSON document", 0, func([]string) string { return "/v1/runs" }), postFile("import <requirement|change> <id>", "Freeze an enterprise fact as an immutable local snapshot", 2, func(a []string) string { return "/v1/imports/" + a[0] + "/" + a[1] }))
	capa := query("capa [id]", "Inspect CAPA records", 0, 1, func(a []string) string {
		if len(a) == 0 {
			return "/v1/capa"
		}
		return "/v1/capa/" + a[0]
	})
	capa.AddCommand(postFile("open", "Open CAPA for a non-conformance", 0, func([]string) string { return "/v1/capa" }), postFile("act <id>", "Plan, approve, link, verify or close CAPA", 1, func(a []string) string { return "/v1/capa/" + a[0] }))
	root.AddCommand(capa)
	return root
}
