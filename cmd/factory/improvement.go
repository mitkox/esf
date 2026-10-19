package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/mitkox/esf/internal/factory"
	"github.com/mitkox/esf/internal/harnessprofile"
	"github.com/mitkox/esf/internal/inferencebudget"
	"github.com/spf13/cobra"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
)

func improvementReusePolicy(req factory.RunRequest) enumspb.WorkflowIdReusePolicy {
	if req.ImprovementPolicy != "" {
		return enumspb.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE
	}
	return enumspb.WORKFLOW_ID_REUSE_POLICY_UNSPECIFIED
}

func readImprovementJSON(path string, v any) error {
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	d := json.NewDecoder(io.LimitReader(f, 4<<20))
	d.DisallowUnknownFields()
	if e = d.Decode(v); e != nil {
		return e
	}
	var extra any
	if e = d.Decode(&extra); e != io.EOF {
		return errors.New("trailing JSON data")
	}
	return nil
}
func newHarnessProfileCommand(configPath *string) *cobra.Command {
	root := &cobra.Command{Use: "profile", Short: "Import, qualify, inspect, and promote immutable harness profiles"}
	var file string
	importCmd := &cobra.Command{Use: "import", Short: "Store an unapproved candidate; never enables it", RunE: func(cmd *cobra.Command, _ []string) error {
		cfg, e := loadConfig(*configPath)
		if e != nil {
			return e
		}
		f, e := os.Open(file)
		if e != nil {
			return e
		}
		defer f.Close()
		p, e := harnessprofile.Decode(f)
		if e != nil {
			return e
		}
		s, e := cfg.ProfileStore()
		if e != nil {
			return e
		}
		defer s.Close()
		d, e := s.Put(p)
		if e != nil {
			return e
		}
		fmt.Fprintln(cmd.OutOrStdout(), d)
		return nil
	}}
	importCmd.Flags().StringVar(&file, "file", "", "candidate JSON file")
	_ = importCmd.MarkFlagRequired("file")
	inspect := &cobra.Command{Use: "inspect NAME_OR_DIGEST", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		cfg, e := loadConfig(*configPath)
		if e != nil {
			return e
		}
		d := args[0]
		if v, ok := cfg.HarnessProfiles[d]; ok {
			d = v
		}
		s, e := cfg.ProfileStore()
		if e != nil {
			return e
		}
		defer s.Close()
		p, e := s.Get(d)
		if e != nil {
			return e
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(p)
	}}
	var input, output string
	qualify := &cobra.Command{Use: "qualify", Short: "Evaluate frozen paired trials; missing evidence cannot pass", RunE: func(cmd *cobra.Command, _ []string) error {
		var in harnessprofile.QualityInput
		if e := readImprovementJSON(input, &in); e != nil {
			return e
		}
		r := harnessprofile.Qualify(in)
		b, e := json.MarshalIndent(r, "", "  ")
		if e != nil {
			return e
		}
		if output != "" {
			if e = os.WriteFile(output, append(b, '\n'), 0600); e != nil {
				return e
			}
		} else {
			fmt.Fprintln(cmd.OutOrStdout(), string(b))
		}
		if !r.Passed {
			return fmt.Errorf("quality qualification failed: %s", strings.Join(r.Problems, "; "))
		}
		return nil
	}}
	qualify.Flags().StringVar(&input, "input", "", "frozen trial input JSON")
	qualify.Flags().StringVar(&output, "output", "", "report destination")
	_ = qualify.MarkFlagRequired("input")
	var digest, report, approval string
	promote := &cobra.Command{Use: "promote", Short: "Retain a reviewed promotion receipt; configure its digest separately", RunE: func(cmd *cobra.Command, _ []string) error {
		cfg, e := loadConfig(*configPath)
		if e != nil {
			return e
		}
		s, e := cfg.ProfileStore()
		if e != nil {
			return e
		}
		defer s.Close()
		p, e := s.Get(digest)
		if e != nil {
			return e
		}
		var r harnessprofile.QualityReport
		var a harnessprofile.Approval
		if e = readImprovementJSON(report, &r); e != nil {
			return e
		}
		if e = readImprovementJSON(approval, &a); e != nil {
			return e
		}
		if e = s.Promote(p, r, a, cfg.Improvement.ApprovalKeys, time.Now()); e != nil {
			return e
		}
		fmt.Fprintln(cmd.OutOrStdout(), "Promotion retained:", digest)
		return nil
	}}
	promote.Flags().StringVar(&digest, "candidate", "", "candidate digest")
	promote.Flags().StringVar(&report, "report", "", "quality report")
	promote.Flags().StringVar(&approval, "approval", "", "independent signed reviewer receipt")
	for _, f := range []string{"candidate", "report", "approval"} {
		_ = promote.MarkFlagRequired(f)
	}
	root.AddCommand(importCmd, inspect, qualify, promote)
	var reviewer, expires string
	payload := &cobra.Command{Use: "approval-payload", Short: "Emit exact bytes for an independent reviewer's Ed25519 signature", RunE: func(cmd *cobra.Command, _ []string) error {
		cfg, e := loadConfig(*configPath)
		if e != nil {
			return e
		}
		s, e := cfg.ProfileStore()
		if e != nil {
			return e
		}
		defer s.Close()
		p, e := s.Get(digest)
		if e != nil {
			return e
		}
		var r harnessprofile.QualityReport
		if e = readImprovementJSON(report, &r); e != nil {
			return e
		}
		expiry, e := time.Parse(time.RFC3339, expires)
		if e != nil {
			return e
		}
		a := harnessprofile.Approval{Schema: "esf-harness-approval/v1", Reviewer: reviewer, Profile: digest, Qualification: harnessprofile.Hash(r), Binding: p.Binding, Expires: expiry}
		_, e = cmd.OutOrStdout().Write(a.SigningBytes())
		return e
	}}
	payload.Flags().StringVar(&digest, "candidate", "", "candidate digest")
	payload.Flags().StringVar(&report, "report", "", "qualification report")
	payload.Flags().StringVar(&reviewer, "reviewer", "", "trusted independent reviewer key ID")
	payload.Flags().StringVar(&expires, "expires", "", "approval expiry in RFC3339")
	for _, f := range []string{"candidate", "report", "reviewer", "expires"} {
		_ = payload.MarkFlagRequired(f)
	}
	root.AddCommand(payload)
	return root
}

func newImproveCommand(configPath *string) *cobra.Command {
	var in factory.SuiteRequest
	var wait bool
	cmd := &cobra.Command{Use: "improve", Short: "Run an offline task suite through durable Harness2 improvement", RunE: func(cmd *cobra.Command, _ []string) error {
		cfg, e := loadConfig(*configPath)
		if e != nil {
			return e
		}
		if e = cfg.Validate(); e != nil {
			return e
		}
		if _, _, e = cfg.ReadImprovementSuite(in.Suite); e != nil {
			return e
		}
		c, e := factory.NewTemporalClient(cfg, nil)
		if e != nil {
			return e
		}
		defer c.Close()
		r, e := c.ExecuteWorkflow(cmd.Context(), client.StartWorkflowOptions{ID: "factory-experiment-" + in.ID, TaskQueue: cfg.Temporal.TaskQueue, WorkflowIDReusePolicy: enumspb.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE}, factory.ImprovementSuiteWorkflow, in)
		if e != nil {
			return e
		}
		fmt.Fprintln(cmd.OutOrStdout(), "Experiment:", in.ID, "Workflow:", r.GetID())
		if !wait {
			return nil
		}
		var result factory.SuiteResult
		if e = r.Get(cmd.Context(), &result); e != nil {
			return e
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
	}}
	cmd.Flags().StringVar(&in.ID, "experiment-id", "", "stable unique experiment ID")
	cmd.Flags().StringVar(&in.Suite, "suite", "", "operator-declared task suite name")
	cmd.Flags().StringVar(&in.Policy, "policy", "", "offline improvement policy name")
	cmd.Flags().StringVar(&in.Split, "partition", "development", "development, validation, or qualification")
	cmd.Flags().BoolVar(&in.Compare, "compare", false, "compare single baseline, static profile, Harness2 and four baseline attempts")
	cmd.Flags().IntVar(&in.Repeats, "repeats", 3, "repetitions per task and arm (1..3)")
	cmd.Flags().BoolVar(&wait, "wait", true, "wait for completion")
	for _, f := range []string{"experiment-id", "suite", "policy"} {
		_ = cmd.MarkFlagRequired(f)
	}
	return cmd
}

func newBudgetGatewayCommand(configPath *string) *cobra.Command {
	var listen, dir, upstream, keyEnv string
	cmd := &cobra.Command{Use: "budget-gateway", Short: "Serve the trusted, bounded text Chat Completions model route", RunE: func(cmd *cobra.Command, _ []string) error {
		cfg, e := loadConfig(*configPath)
		if e != nil {
			return e
		}
		host, _, e := net.SplitHostPort(listen)
		if e != nil {
			return e
		}
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return errors.New("gateway binds loopback; use a private TLS reverse proxy for Cube clients")
		}
		ledger, e := inferencebudget.Open(dir)
		if e != nil {
			return e
		}
		defer ledger.Close()
		g := cfg.Improvement
		if upstream != g.UpstreamURL {
			return errors.New("upstream differs from the frozen improvement route")
		}
		gateway := &inferencebudget.Gateway{Ledger: ledger, AdminKey: os.Getenv(g.GatewayAdminKeyEnv), TokenKey: []byte(os.Getenv(g.GatewayTokenKeyEnv)), Upstream: upstream, UpstreamKey: os.Getenv(keyEnv), RouteDigest: g.RouteDigest, PricingEvidence: g.PricingEvidence, InputPrice: g.InputPrice, OutputPrice: g.OutputPrice, Model: g.WireModel}
		if e = gateway.Validate(); e != nil {
			return e
		}
		server := &http.Server{Addr: listen, Handler: gateway, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 11 * time.Minute, IdleTimeout: time.Minute, MaxHeaderBytes: 16 << 10}
		go func() {
			<-cmd.Context().Done()
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			_ = server.Shutdown(ctx)
		}()
		fmt.Fprintln(cmd.OutOrStdout(), "Budget gateway:", listen)
		if e = server.ListenAndServe(); e != http.ErrServerClosed {
			return e
		}
		return nil
	}}
	cmd.Flags().StringVar(&listen, "listen", "127.0.0.1:8093", "private gateway listen address")
	cmd.Flags().StringVar(&dir, "state", "", "durable ledger directory")
	cmd.Flags().StringVar(&upstream, "upstream", "", "approved HTTPS upstream /v1 base URL")
	cmd.Flags().StringVar(&keyEnv, "upstream-key-env", "", "environment variable containing the upstream provider key")
	for _, f := range []string{"state", "upstream", "upstream-key-env"} {
		_ = cmd.MarkFlagRequired(f)
	}
	cmd.AddCommand(&cobra.Command{Use: "digest", Short: "Print the route digest from configured upstream, model and price evidence", RunE: func(cmd *cobra.Command, _ []string) error {
		cfg, e := loadConfig(*configPath)
		if e != nil {
			return e
		}
		g := cfg.Improvement
		fmt.Fprintln(cmd.OutOrStdout(), inferencebudget.RouteDigest(g.UpstreamURL, g.WireModel, g.InputPrice, g.OutputPrice, g.PricingEvidence))
		return nil
	}})
	return cmd
}
