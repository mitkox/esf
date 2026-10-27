// cube-security-pilot is trusted host orchestration. Target, candidate scanner,
// assessment driver and OpenCyber execute exclusively in disposable Cube VMs.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/mitkox/esf/internal/sandbox"
	"github.com/mitkox/esf/internal/sandbox/cube"
	sa "github.com/mitkox/esf/internal/securityassessment"
	"github.com/pelletier/go-toml/v2"
)

type options struct {
	config, runtime, route, driver, driverSHA, guard, ca, fixture, report, scenario string
	repeat                                                                          int
	repair, recovery                                                                bool
	faultAfter                                                                      time.Duration
}

type runtimeReceipt struct {
	Schema       string   `json:"schema"`
	Source       string   `json:"source_revision"`
	Image        string   `json:"oci_index_sha256"`
	Catalog      string   `json:"recorded_catalog_sha256"`
	Template     string   `json:"template"`
	Status       string   `json:"template_status"`
	Inventory    string   `json:"runtime_inventory_sha256"`
	RootFS       string   `json:"rootfs_sha256"`
	ExternalHash bool     `json:"external_hash_match"`
	Operations   []string `json:"actual_operations_passed"`
}

type routeReceipt struct {
	Schema    string                          `json:"schema"`
	Qualified bool                            `json:"route_qualified"`
	Template  string                          `json:"template"`
	Runtime   string                          `json:"assessor_template"`
	Version   string                          `json:"cube_version"`
	Pending   bool                            `json:"pending_allocation"`
	Remaining []string                        `json:"remaining_owned_ids"`
	Checks    []struct{ Name, Status string } `json:"checks"`
	Hashes    map[string]string               `json:"sha256"`
}

type attempt struct {
	RunID            string         `json:"run_id"`
	ParentRunID      string         `json:"parent_run_id,omitempty"`
	Variant          string         `json:"variant"`
	Scenario         string         `json:"scenario"`
	CandidateDigest  string         `json:"candidate_digest"`
	ArtifactDigest   string         `json:"built_artifact_digest"`
	SourceSHA256     string         `json:"source_sha256"`
	Elapsed          time.Duration  `json:"elapsed_ns"`
	Requests         uint64         `json:"requests"`
	Manifest         string         `json:"private_manifest,omitempty"`
	Journal          string         `json:"ownership_journal,omitempty"`
	Assertions       []sa.Assertion `json:"assertions,omitempty"`
	Coverage         []sa.Coverage  `json:"coverage,omitempty"`
	Cleanup          sa.Cleanup     `json:"cleanup"`
	Verified         bool           `json:"evidence_verified"`
	Passed           bool           `json:"security_assertions_passed"`
	ExpectedObserved bool           `json:"expected_behavior_observed"`
	Error            string         `json:"error,omitempty"`
	FaultContext     string         `json:"fault_context,omitempty"`
}

type staging struct {
	Owner    string        `json:"owner"`
	Pending  bool          `json:"pending_allocation"`
	IDs      []string      `json:"sandbox_ids"`
	Cleanup  sa.Cleanup    `json:"cleanup"`
	Duration time.Duration `json:"elapsed_ns"`
}

type receipt struct {
	Schema             string            `json:"schema"`
	Started            time.Time         `json:"started"`
	Finished           time.Time         `json:"finished"`
	Scenario           string            `json:"scenario"`
	RepairExplicit     bool              `json:"repair_explicitly_requested"`
	RecoveryExplicit   bool              `json:"recovery_explicitly_requested"`
	Inputs             map[string]string `json:"input_sha256"`
	Stages             []staging         `json:"candidate_staging"`
	Attempts           []attempt         `json:"attempts"`
	BehaviorObserved   bool              `json:"requested_behavior_observed"`
	Decision           string            `json:"decision"`
	IntegrationEnabled bool              `json:"integration_enabled"`
	Measurements       map[string]any    `json:"measurements"`
	MissingGates       []string          `json:"missing_gates"`
	Error              string            `json:"error,omitempty"`
}

func main() {
	var o options
	flag.StringVar(&o.config, "config", "factory.toml", "trusted local factory TOML; never copied into a VM")
	flag.StringVar(&o.runtime, "runtime-qualification", "", "native runtime qualification receipt")
	flag.StringVar(&o.route, "route-proof", "", "completed endpoint-only route qualification receipt")
	flag.StringVar(&o.driver, "driver-path", "", "absolute path to the static assessment driver")
	flag.StringVar(&o.driverSHA, "driver-sha256", "", "operator-held driver SHA256")
	flag.StringVar(&o.guard, "guard-socket", "", "absolute scoped root guard socket")
	flag.StringVar(&o.ca, "ca-file", "", "optional public Cube preview CA")
	flag.StringVar(&o.fixture, "fixture", "testdata/security-assessment/tenant-api/server.py", "exact seeded synthetic fixture source")
	flag.StringVar(&o.report, "report", "", "fresh private JSON receipt path")
	flag.StringVar(&o.scenario, "scenario", "normal", "normal, startup-failure, cancel or timeout")
	flag.IntVar(&o.repeat, "repeat", 3, "normal before/after pairs, 1 to 3")
	flag.BoolVar(&o.repair, "repair", false, "explicitly stage and assess the fixed synthetic candidate after each vulnerable run")
	flag.BoolVar(&o.recovery, "recovery", false, "explicitly run a fresh repaired candidate after an injected failure and verified cleanup")
	flag.DurationVar(&o.faultAfter, "fault-after", 3*time.Second, "bounded cancellation or timeout injection delay; does not identify execution phase")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, o); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func readBounded(name string, limit int64) ([]byte, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(b)) > limit {
		return nil, errors.New("input unavailable or exceeds bound")
	}
	return b, nil
}

func save(name string, value any) error {
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.OpenFile(name+".new", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, a := f.Write(append(b, '\n'))
	err = errors.Join(a, f.Sync(), f.Close())
	if err != nil {
		return err
	}
	if err = os.Rename(f.Name(), name); err != nil {
		return err
	}
	d, err := os.Open(filepath.Dir(name))
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

func writePrivate(name string, b []byte) error {
	f, err := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, a := f.Write(b)
	return errors.Join(a, f.Sync(), f.Close())
}

func nonce() (string, error) {
	var b [16]byte
	_, err := rand.Read(b[:])
	return hex.EncodeToString(b[:]), err
}

func run(ctx context.Context, o options) (retErr error) {
	if o.report == "" || o.repeat < 1 || o.repeat > 3 || o.faultAfter <= 0 || o.faultAfter > time.Minute ||
		(o.scenario != "normal" && o.scenario != "startup-failure" && o.scenario != "cancel" && o.scenario != "timeout") ||
		(o.recovery && (o.scenario == "normal" || !o.repair)) {
		return errors.New("invalid pilot options; recovery requires an explicit repair and a failure scenario")
	}
	if !filepath.IsAbs(o.report) || !filepath.IsAbs(o.guard) || !filepath.IsAbs(o.driver) {
		return errors.New("report, guard and driver paths must be absolute")
	}
	private := o.report + ".private"
	if err := os.Mkdir(private, 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(o.report, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	r := receipt{Schema: "esf.cube-security-pilot/v1", Started: time.Now().UTC(), Scenario: o.scenario, RepairExplicit: o.repair, RecoveryExplicit: o.recovery, Inputs: map[string]string{}, Decision: "incomplete", Measurements: map[string]any{}, MissingGates: []string{
		"full lifecycle failure matrix, including invalid credential, missing log, capped analysis and tool failure",
		"interrupted controller restart/reconciliation and lost acknowledgement qualification",
		"independent review and eligible human approval",
		"representative delivery sample, incremental findings, triage cost and latency thresholds",
		"production/customer acceptance and optional configured AI inference",
	}}
	defer func() {
		r.Finished = time.Now().UTC()
		if retErr != nil {
			r.Error = "pilot_incomplete: " + retErr.Error()
		}
		measure(&r)
		retErr = errors.Join(retErr, save(o.report, r))
	}()
	if err = save(o.report, r); err != nil {
		return err
	}
	configBytes, err := readBounded(o.config, 1<<20)
	if err != nil {
		return err
	}
	var config struct {
		Cube cube.Config `toml:"cube"`
	}
	if err = toml.Unmarshal(configBytes, &config); err != nil {
		return errors.New("invalid trusted factory configuration")
	}
	if err = config.Cube.Validate(); err != nil {
		return err
	}
	r.Inputs["factory_config"] = sa.SHA256(configBytes)
	var rt runtimeReceipt
	var proof routeReceipt
	for _, input := range []struct {
		name, path string
		value      any
	}{{"runtime_qualification", o.runtime, &rt}, {"route_proof", o.route, &proof}} {
		b, e := readBounded(input.path, 1<<20)
		if e != nil {
			return e
		}
		if e = json.Unmarshal(b, input.value); e != nil {
			return e
		}
		r.Inputs[input.name] = sa.SHA256(b)
	}
	if rt.Schema != "esf.opencyber-runtime-qualification/v1" || rt.Source != "09692d89e3cb9dce3506cd4e6fc92c9e4c3c16b6" || rt.Catalog != "6ff0d628d1e93da78345b05ad63cdcd27adade55de29938ead97c4a7560b6660" || rt.Status != "READY" || !rt.ExternalHash {
		return errors.New("selected native runtime qualification missing or mismatched")
	}
	for _, needed := range []string{"stdio initialize", "tools/list", "catalog alignment", "engagement create", "supplied-log timeline", "selected export", "selected evidence verify", "audit verify"} {
		found := false
		for _, v := range rt.Operations {
			found = found || v == needed
		}
		if !found {
			return errors.New("native runtime operation not qualified: " + needed)
		}
	}
	if proof.Schema != "esf.cube-security-route/v1" || !proof.Qualified || proof.Pending || len(proof.Remaining) > 0 || proof.Template != config.Cube.TemplateID || proof.Runtime != rt.Template || proof.Version != config.Cube.Version {
		return errors.New("endpoint-only route proof does not qualify configured templates")
	}
	for _, needed := range []string{"positive-control", "tcp-reachable-control", "tcp-guard-denial", "udp-reachable-control", "udp-guard-denial", "host-management-denial", "direct-preview-denial", "metadata-denial", "target-isolation-control-9443", "target-isolation-control-4000", "target-assessor-preview-denial", "target-management-denial", "target-endpoint-denial", "assessor-filesystem-isolation", "cleanup", "route-cleanup"} {
		found := false
		for _, v := range proof.Checks {
			if v.Name == needed && v.Status == "pass" {
				found = true
			}
		}
		if !found {
			return errors.New("required route control missing: " + needed)
		}
	}
	if o.ca != "" {
		b, e := readBounded(o.ca, 1<<20)
		if e != nil {
			return e
		}
		rest := b
		certs := 0
		for len(strings.TrimSpace(string(rest))) > 0 {
			block, remaining := pem.Decode(rest)
			if block == nil || block.Type != "CERTIFICATE" {
				return errors.New("CA file must contain only public certificates")
			}
			certs++
			rest = remaining
		}
		if certs == 0 {
			return errors.New("CA file empty")
		}
		r.Inputs["preview_ca"] = sa.SHA256(b)
		// The Cube provider's bounded reader uses system trust; Go includes this
		// explicit public CA along with its system certificate directories.
		if err = os.Setenv("SSL_CERT_FILE", o.ca); err != nil {
			return err
		}
	}
	source, err := readBounded(o.fixture, 1<<20)
	if err != nil {
		return err
	}
	if sa.SHA256(source) != proof.Hashes["target.py"] {
		return errors.New("fixture differs from route-qualified seeded source")
	}
	r.Inputs["seeded_source"] = sa.SHA256(source)
	fixed, err := repairSource(source)
	if err != nil {
		return err
	}
	tools := []string{"oc_engagement_create", "oc_http_request", "oc_log_timeline", "oc_evidence_export", "oc_evidence_verify", "oc_audit_verify"}
	versions := map[string]string{}
	for _, name := range tools {
		versions[name] = "0.5.0"
	}
	if !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(rt.RootFS) {
		return errors.New("runtime qualification must pin the Cube rootfs artifact")
	}
	profile := sa.Profile{Schema: sa.ProfileSchema, ID: "synthetic-tenant-api-v1", TargetURL: "https://attempt.esf.invalid:9445", Runtime: sa.RuntimePin{SourceRevision: rt.Source, ImageDigest: "sha256:" + rt.Image, CatalogDigest: rt.Catalog, TemplateID: rt.Template, Tools: versions, Assets: map[string]string{"runtime-inventory": rt.Inventory, "cube-rootfs-sha256": rt.RootFS}}, Limits: sa.DefaultLimits(), RequiredTools: tools, RequiredChecks: append([]string(nil), sa.FixedAssessmentChecks...)}
	if err = profile.Validate(); err != nil {
		return err
	}
	trusted := sa.CubeExecutorConfig{Cube: config.Cube, GuardSocket: o.guard, DriverBinaryPath: o.driver, DriverSHA256: o.driverSHA, TargetEntrypoint: "server.py", PrivateJournalRoot: filepath.Join(private, "attempts"), PreviewCAFile: o.ca, RuntimeInventorySHA256: rt.Inventory}
	executor, err := sa.NewCubeExecutor(trusted)
	if err != nil {
		return err
	}
	r.Inputs["driver"] = o.driverSHA
	provider, err := cube.New(config.Cube)
	if err != nil {
		return err
	}
	seed, err := stageCandidate(ctx, provider, config.Cube, source, private, "vulnerable", &r, o.report)
	if err != nil {
		return err
	}
	if err = save(filepath.Join(private, "vulnerable-candidate.json"), seed.Manifest); err != nil {
		return err
	}
	if err = writePrivate(filepath.Join(private, "vulnerable-candidate.tar"), seed.Archive); err != nil {
		return err
	}
	var repaired sa.CandidateExport
	if o.repair {
		repaired, err = stageCandidate(ctx, provider, config.Cube, fixed, private, "repaired", &r, o.report)
		if err != nil {
			return err
		}
		if seed.Manifest.Digest == repaired.Manifest.Digest || seed.Manifest.ArchiveSHA256 == repaired.Manifest.ArchiveSHA256 {
			return errors.New("repair did not replace candidate identity")
		}
		if err = save(filepath.Join(private, "repaired-candidate.json"), repaired.Manifest); err != nil {
			return err
		}
		if err = writePrivate(filepath.Join(private, "repaired-candidate.tar"), repaired.Archive); err != nil {
			return err
		}
	}
	count := o.repeat
	if o.scenario != "normal" {
		count = 1
	}
	for i := 0; i < count; i++ {
		token, e := nonce()
		if e != nil {
			return e
		}
		runID := "pilot-" + token + "-before"
		candidate := seed
		variant := "vulnerable"
		if o.scenario == "startup-failure" {
			candidate, e = stageCandidate(ctx, provider, config.Cube, []byte("raise SystemExit('explicit synthetic startup fault')\n"), private, "startup-failure", &r, o.report)
			if e != nil {
				return e
			}
			variant = "startup-failure"
		}
		a, e := assess(ctx, executor, sa.CubeAssessmentRequest{RunID: runID, ChangeID: "synthetic-tenant-repair", Profile: profile, Candidate: candidate}, variant, o.scenario, o.faultAfter, private)
		r.Attempts = append(r.Attempts, a)
		if err = save(o.report, r); err != nil {
			return err
		}
		if e != nil {
			return e
		}
		if !a.ExpectedObserved {
			return errors.New("before/failure behavior not demonstrated")
		}
		if o.repair && (o.scenario == "normal" || o.recovery) {
			a, e = assess(ctx, executor, sa.CubeAssessmentRequest{RunID: "pilot-" + token + "-after", ChangeID: "synthetic-tenant-repair", ParentRunID: runID, Profile: profile, Candidate: repaired}, "repaired", "normal", o.faultAfter, private)
			r.Attempts = append(r.Attempts, a)
			if err = save(o.report, r); err != nil {
				return err
			}
			if e != nil {
				return e
			}
			if !a.ExpectedObserved {
				return errors.New("replacement candidate repair not demonstrated")
			}
		}
	}
	r.BehaviorObserved = true
	if o.scenario == "normal" && o.repeat < 3 {
		r.MissingGates = append(r.MissingGates, "three repeatable vulnerable/repaired pairs")
	}
	if !o.repair {
		r.MissingGates = append(r.MissingGates, "explicit separate repaired candidate assessment")
	}
	return nil
}

func repairSource(source []byte) ([]byte, error) {
	needle := "            owner = self.path.rsplit(\"/\", 1)[-1]\n"
	if strings.Count(string(source), needle) != 1 || strings.Contains(string(source), "if owner != user:") {
		return nil, errors.New("synthetic repair requires the exact known vulnerable owner lookup")
	}
	replacement := needle + "            if owner != user:\n                self.respond(403, {\"error\": \"tenant access denied\"}, correlation)\n                return\n"
	return []byte(strings.Replace(string(source), needle, replacement, 1)), nil
}

// stageCandidate performs no target execution. The snapshot scanner and bounded
// archive export execute through the actual Cube provider inside an owned VM.
func stageCandidate(ctx context.Context, p *cube.Provider, cfg cube.Config, source []byte, private, variant string, r *receipt, reportPath string) (export sa.CandidateExport, retErr error) {
	ctx, cancelStage := context.WithTimeout(ctx, 2*time.Minute)
	defer cancelStage()
	token, err := nonce()
	if err != nil {
		return export, err
	}
	s := staging{Owner: "pilot-stage-" + token, Pending: true, Cleanup: sa.Cleanup{Status: sa.Incomplete}}
	started := time.Now()
	journal := filepath.Join(private, "stage-"+token+".json")
	if err = save(journal, s); err != nil {
		return export, err
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		cleanup, cleanupErr := cleanupStaging(cleanupCtx, p, cfg, s.Owner, s.IDs, s.Pending)
		s.Cleanup = cleanup
		s.Duration = time.Since(started)
		r.Stages = append(r.Stages, s)
		retErr = errors.Join(retErr, cleanupErr, save(journal, s), save(reportPath, r))
	}()
	deny := false
	sb, err := p.Create(ctx, sandbox.Spec{Template: cfg.TemplateID, Metadata: map[string]string{"esf.pilot.owner": s.Owner, "esf.pilot.variant": variant}, IdleTimeout: 5 * time.Minute, Network: sandbox.Network{AllowInternet: &deny}})
	if err != nil {
		return export, errors.New("candidate staging allocation acknowledgement unavailable")
	}
	s.Pending = false
	s.IDs = []string{sb.ID()}
	if err = save(journal, s); err != nil {
		return export, err
	}
	if err = sb.WriteFile(ctx, "/tmp/pilot-candidate/server.py", source); err != nil {
		return export, errors.New("candidate staging transfer incomplete")
	}
	export, err = sa.ExportCandidate(ctx, sb, "/tmp/pilot-candidate", sa.DefaultCandidatePolicy())
	if err != nil {
		return export, err
	}
	if string(export.Files["server.py"]) != string(source) {
		return export, errors.New("exported candidate differs from staged bytes")
	}
	return export, nil
}

func cleanupStaging(ctx context.Context, p *cube.Provider, cfg cube.Config, owner string, known []string, pending bool) (sa.Cleanup, error) {
	c := sa.Cleanup{Status: sa.Incomplete, OwnedIDs: append([]string(nil), known...), PendingAllocation: pending}
	items, err := p.List(ctx)
	if err != nil {
		return c, errors.New("candidate staging cleanup inventory unavailable")
	}
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	seen := map[string]bool{}
	for _, id := range known {
		seen[id] = true
	}
	for _, item := range items {
		if item.Metadata["esf.pilot.owner"] == owner {
			if !seen[item.ID] {
				c.OwnedIDs = append(c.OwnedIDs, item.ID)
				seen[item.ID] = true
			}
			req, e := http.NewRequestWithContext(ctx, http.MethodDelete, strings.TrimRight(cfg.APIURL, "/")+"/sandboxes/"+url.PathEscape(item.ID), nil)
			if e != nil {
				continue
			}
			if cfg.APIKey != "" {
				req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
			}
			response, e := client.Do(req)
			if e == nil {
				io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
				response.Body.Close()
			}
		}
	}
	items, err = p.List(ctx)
	if err != nil {
		return c, errors.New("candidate staging post-cleanup inventory unavailable")
	}
	for _, item := range items {
		if item.Metadata["esf.pilot.owner"] == owner || seen[item.ID] {
			c.RemainingIDs = append(c.RemainingIDs, item.ID)
		}
	}
	if pending || len(c.RemainingIDs) > 0 {
		return c, errors.New("candidate staging cleanup incomplete; retain stage journal")
	}
	c.Status = sa.Complete
	return c, nil
}

func assess(ctx context.Context, e *sa.CubeExecutor, in sa.CubeAssessmentRequest, variant, scenario string, delay time.Duration, private string) (a attempt, retErr error) {
	a = attempt{RunID: in.RunID, ParentRunID: in.ParentRunID, Variant: variant, Scenario: scenario, CandidateDigest: in.Candidate.Manifest.Digest, ArtifactDigest: in.Candidate.Manifest.ArchiveSHA256, SourceSHA256: sa.SHA256(in.Candidate.Files["server.py"])}
	attemptCtx := ctx
	stop := func() {}
	var timer *time.Timer
	if scenario == "timeout" {
		attemptCtx, stop = context.WithTimeout(ctx, delay)
	}
	if scenario == "cancel" {
		attemptCtx, stop = context.WithCancel(ctx)
		timer = time.AfterFunc(delay, stop)
		defer timer.Stop()
	}
	defer stop()
	out, err := e.Assess(attemptCtx, in)
	a.Elapsed = out.Duration
	a.Requests = out.Requests
	a.Journal = out.JournalPath
	a.Assertions = out.Manifest.Assertions
	a.Coverage = out.Manifest.Coverage
	a.Cleanup = out.Manifest.Cleanup
	a.Passed = out.Manifest.Passed()
	if err != nil {
		a.Error = err.Error()
	}
	if errors.Is(err, context.Canceled) {
		a.FaultContext = context.Canceled.Error()
	}
	if errors.Is(err, context.DeadlineExceeded) {
		a.FaultContext = context.DeadlineExceeded.Error()
	}
	if out.Manifest.Schema != "" {
		a.Manifest = filepath.Join(private, in.RunID+"-manifest.json")
		if retErr = save(a.Manifest, out.Manifest); retErr != nil {
			return a, retErr
		}
		if retErr = save(filepath.Join(private, in.RunID+"-verification-input.json"), struct {
			Expected sa.Identity       `json:"expected_identity"`
			Profile  sa.Profile        `json:"profile"`
			Hashes   map[string]string `json:"externally_held_evidence_sha256"`
		}{out.Expected, out.Profile, out.ExpectedHashes}); retErr != nil {
			return a, retErr
		}
		dir := filepath.Join(private, in.RunID+"-evidence")
		if retErr = os.Mkdir(dir, 0700); retErr != nil {
			return a, retErr
		}
		for name, b := range out.PrivateEvidence {
			if filepath.Base(name) != name || !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`).MatchString(name) {
				return a, errors.New("invalid private selected evidence name")
			}
			if retErr = writePrivate(filepath.Join(dir, name), b); retErr != nil {
				return a, retErr
			}
		}
		a.Verified = out.Manifest.Verify(out.Expected, out.PrivateEvidence, out.ExpectedHashes) == nil
	}
	if scenario != "normal" {
		// Cleanup lives in the durable journal even when the assessment ended
		// before a manifest existed. A generic failure is not a qualified fault.
		var j struct {
			Cleanup sa.Cleanup `json:"cleanup"`
		}
		b, readErr := readBounded(out.JournalPath, 1<<20)
		if readErr == nil && json.Unmarshal(b, &j) == nil {
			a.Cleanup = j.Cleanup
		}
		injected := injectedFailure(scenario, err, ctx.Err())
		a.ExpectedObserved = injected && !a.Passed && a.Cleanup.Status == sa.Complete && !a.Cleanup.PendingAllocation && len(a.Cleanup.RemainingIDs) == 0
		if !a.ExpectedObserved {
			return a, errors.New("injected failure or cleanup not demonstrated")
		}
		return a, nil
	}
	if err != nil {
		return a, err
	}
	if !a.Verified || a.Cleanup.Status != sa.Complete {
		return a, errors.New("assessment evidence or cleanup incomplete")
	}
	statuses := map[string]sa.AssertionStatus{}
	for _, check := range a.Assertions {
		statuses[check.ID] = check.Status
	}
	coverageComplete := len(a.Coverage) == len(sa.FixedAssessmentChecks)
	for _, check := range a.Coverage {
		coverageComplete = coverageComplete && check.Status == sa.Complete
	}
	if variant == "vulnerable" {
		a.ExpectedObserved = coverageComplete && statuses["tenant-control"] == sa.Passed && statuses["tenant-isolation"] == sa.Failed && statuses["evidence-integrity"] == sa.Passed && !a.Passed
	} else {
		a.ExpectedObserved = coverageComplete && a.Passed
	}
	return a, nil
}

func injectedFailure(scenario string, failure, parentFailure error) bool {
	if failure == nil || parentFailure != nil {
		return false
	}
	switch scenario {
	case "startup-failure":
		return strings.Contains(failure.Error(), "did not become ready")
	case "cancel":
		return errors.Is(failure, context.Canceled)
	case "timeout":
		return errors.Is(failure, context.DeadlineExceeded)
	}
	return false
}

func measure(r *receipt) {
	var durations []float64
	var requests uint64
	for _, a := range r.Attempts {
		durations = append(durations, a.Elapsed.Seconds())
		requests += a.Requests
	}
	sort.Float64s(durations)
	r.Measurements["total_http_requests"] = requests
	r.Measurements["attempt_seconds_sorted"] = durations
	if len(durations) > 0 {
		middle := len(durations) / 2
		median := durations[middle]
		if len(durations)%2 == 0 {
			median = (durations[middle-1] + durations[middle]) / 2
		}
		r.Measurements["median_attempt_seconds"] = median
	}
	r.Measurements["assessment_vm_peak_memory"] = "unavailable: executor exposes no sampled guest/process peak memory"
	r.Measurements["runtime_receipt_single_observation"] = "separate native-runtime qualification; not per-attempt memory or marginal cost"
	r.Measurements["fault_injection_phase"] = "elapsed delay from Assess invocation; runtime-phase cancellation not established by delay alone"
}
