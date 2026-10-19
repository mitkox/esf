package factory

import (
	"fmt"
	"time"

	"github.com/mitkox/esf/internal/harnessprofile"
	"github.com/mitkox/esf/internal/inferencebudget"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

type executionOverrides struct {
	Probe         bool
	ProfileDigest string
	SelectedRun   string
	Parent        string
	BudgetAccount string
	Receipt       *ImprovementReceipt
}

type ImprovementAdmission struct {
	StartedAt     time.Time              `json:"started_at"`
	Request       RunRequest             `json:"request"`
	Policy        ImprovementPolicy      `json:"policy"`
	PolicyName    string                 `json:"policy_name"`
	Fingerprint   string                 `json:"fingerprint"`
	Binding       harnessprofile.Binding `json:"binding"`
	ProfileDigest string                 `json:"profile_digest"`
	LibraryDigest string                 `json:"library_digest"`
	LimitMicros   int64                  `json:"limit_micros"`
	Enrolled      bool                   `json:"enrolled"`
	Reason        string                 `json:"reason,omitempty"`
	Deadline      time.Time              `json:"deadline"`
}

type ProbeRequest struct {
	Admission ImprovementAdmission `json:"admission"`
	Stage     string               `json:"stage"`
	Profile   string               `json:"profile"`
}
type SessionRequest struct {
	Admission ImprovementAdmission `json:"admission"`
	Stage     string               `json:"stage"`
	Profiles  []string             `json:"profiles"`
	Runs      []string             `json:"runs"`
}
type SessionResult struct {
	Profile  string `json:"profile,omitempty"`
	Evidence string `json:"evidence"`
	Error    string `json:"error,omitempty"`
}
type ImprovementFinish struct {
	Admission ImprovementAdmission `json:"admission"`
	Receipt   ImprovementReceipt   `json:"receipt"`
}

var improvementStages = []string{"base", "general", "domain", "general-probe", "domain-probe", "compose", "composed", "reflect"}

func improvementID(parent, stage string) string { return parent + "-h2-" + stage }

// This workflow is a separate, guarded entry point. A public RunRequest cannot
// disable review or write a probe's result into the Change index.
func ImprovementProbeWorkflow(ctx workflow.Context, in ProbeRequest) (RunManifest, error) {
	info := workflow.GetInfo(ctx)
	if info.ParentWorkflowExecution == nil || info.ParentWorkflowExecution.ID != WorkflowIDForRun(in.Admission.Request.RunID) {
		return RunManifest{}, fmt.Errorf("improvement probes require their owning parent workflow")
	}
	a := &Activities{}
	vctx := workflow.WithActivityOptions(ctx, noRetry())
	if e := workflow.ExecuteActivity(vctx, a.CheckImprovementAdmission, in.Admission).Get(ctx, nil); e != nil {
		return RunManifest{}, e
	}
	req := in.Admission.Request
	req.RunID = improvementID(req.RunID, in.Stage)
	req.ImprovementPolicy = ""
	req.ChangeID = ""
	req.ParentRunID = in.Admission.Request.RunID
	return softwareChangeWorkflow(ctx, req, executionOverrides{Probe: true, ProfileDigest: in.Profile, Parent: in.Admission.Request.RunID, BudgetAccount: req.RunID})
}

func improvementWorkflow(ctx workflow.Context, req RunRequest) (RunManifest, error) {
	return improvementWorkflowStrategy(ctx, req, "harness2")
}
func improvementWorkflowStrategy(ctx workflow.Context, req RunRequest, strategy string) (RunManifest, error) {
	if strategy != "harness2" && strategy != "baseline" && strategy != "static" && strategy != "repeated-baseline" {
		return RunManifest{}, fmt.Errorf("unknown comparison strategy")
	}
	a := &Activities{}
	var ad ImprovementAdmission
	ac := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{StartToCloseTimeout: time.Minute, RetryPolicy: &temporal.RetryPolicy{MaximumAttempts: 1}})
	if e := workflow.ExecuteActivity(ac, a.AdmitImprovement, req).Get(ctx, &ad); e != nil {
		return RunManifest{}, e
	}
	if !ad.Enrolled {
		req.ImprovementPolicy = ""
		return softwareChangeWorkflow(ctx, req, executionOverrides{Receipt: &ImprovementReceipt{Schema: "esf-improvement/v1", Policy: ad.PolicyName, PolicyDigest: ad.Policy.Digest(), Mode: ad.Policy.Mode, Upstream: harnessprofile.Upstream, Fallback: ad.Reason}})
	}
	if strategy == "baseline" || strategy == "repeated-baseline" {
		ad.ProfileDigest = ""
		ad.Request.HarnessProfile = ""
	}
	status := RunStatus{RunID: req.RunID, State: StateRunning, CurrentStep: "improvement.baseline", StartedAt: workflow.Now(ctx)}
	if e := workflow.SetQueryHandler(ctx, "status", func() (RunStatus, error) { return status, nil }); e != nil {
		return RunManifest{}, e
	}
	r := ImprovementReceipt{Schema: "esf-improvement/v1", Policy: ad.PolicyName, PolicyDigest: ad.Policy.Digest(), Mode: ad.Policy.Mode, Upstream: harnessprofile.Upstream, LibraryDigest: ad.LibraryDigest}
	r.StartedAt = ad.StartedAt
	// Disconnected cleanup revokes all stage accounts and reconciles every
	// known child/session sandbox, including ambiguous creation responses.
	defer func() {
		dc, _ := workflow.NewDisconnectedContext(ctx)
		dc = workflow.WithActivityOptions(dc, workflow.ActivityOptions{StartToCloseTimeout: 3 * time.Minute, RetryPolicy: &temporal.RetryPolicy{MaximumAttempts: 3}})
		_ = workflow.ExecuteActivity(dc, a.CleanupImprovement, ad).Get(dc, nil)
	}()
	stageCtx, cancel := workflow.WithCancel(ctx)
	workflow.Go(stageCtx, func(tc workflow.Context) {
		if workflow.NewTimer(tc, ad.Deadline.Sub(workflow.Now(tc))-ad.Policy.CleanupAllowance.Std()).Get(tc, nil) == nil {
			cancel()
		}
	})
	defer cancel()
	probe := func(stage, profile string) workflow.ChildWorkflowFuture {
		status.CurrentStep = "improvement." + stage
		cc := workflow.WithChildOptions(stageCtx, workflow.ChildWorkflowOptions{WorkflowID: WorkflowIDForRun(improvementID(req.RunID, stage)), WaitForCancellation: true, ParentClosePolicy: enumspb.PARENT_CLOSE_POLICY_REQUEST_CANCEL, RetryPolicy: &temporal.RetryPolicy{MaximumAttempts: 1}})
		return workflow.ExecuteChildWorkflow(cc, ImprovementProbeWorkflow, ProbeRequest{ad, stage, profile})
	}
	var base, composed RunManifest
	baseErr := probe("base", ad.ProfileDigest).Get(stageCtx, &base)
	if baseErr != nil || fatalImprovementProbe(base) {
		r.Error = "baseline infrastructure or security outcome requires reconciliation"
	}
	r.Stages = append(r.Stages, ImprovementStage{ID: improvementID(req.RunID, "base"), Role: "baseline", Run: base.RunID, Error: errorText(baseErr)})
	session := func(stage string, profiles, runs []string) (SessionResult, error) {
		status.CurrentStep = "improvement." + stage
		opts := agentRetry()
		opts.StartToCloseTimeout = ad.Policy.MaxDuration.Std()
		sc := workflow.WithActivityOptions(stageCtx, opts)
		var out SessionResult
		e := workflow.ExecuteActivity(sc, a.RunImprovementSession, SessionRequest{ad, stage, profiles, runs}).Get(stageCtx, &out)
		if e != nil {
			r.Error = "improvement session failed; reconcile execution and required evidence"
		}
		r.Stages = append(r.Stages, ImprovementStage{ID: improvementID(req.RunID, stage), Role: stage, Profile: out.Profile, Error: errorText(e)})
		if e == nil && out.Error != "" {
			e = fmt.Errorf("%s", out.Error)
			r.Error = "improvement session did not retain complete evidence"
		}
		return out, e
	}
	var pipelineErr error
	if strategy == "repeated-baseline" {
		for _, stage := range []string{"general-probe", "domain-probe", "composed"} {
			var m RunManifest
			e := probe(stage, ad.ProfileDigest).Get(stageCtx, &m)
			r.Stages = append(r.Stages, ImprovementStage{ID: improvementID(req.RunID, stage), Role: "repeated-baseline", Run: m.RunID, Error: errorText(e)})
			if e != nil || fatalImprovementProbe(m) {
				r.Error = "comparison probe failed required execution boundaries"
				break
			}
		}
		r.Fallback = "comparison: repeated-baseline"
	} else if strategy == "baseline" || strategy == "static" {
		r.Fallback = "comparison: " + strategy
	} else if baseErr == nil && base.CleanupResult.Verified && !fatalImprovementProbe(base) {
		general, ge := session("general", []string{ad.ProfileDigest}, []string{base.RunID})
		domain, de := session("domain", []string{ad.ProfileDigest}, []string{base.RunID})
		if ge == nil && de == nil {
			// At most two VMs are live: both probes start only after editor VMs
			// have been destroyed, and composition waits for both probes.
			gf := probe("general-probe", general.Profile)
			df := probe("domain-probe", domain.Profile)
			var gp, dp RunManifest
			gerr := gf.Get(stageCtx, &gp)
			derr := df.Get(stageCtx, &dp)
			r.Stages = append(r.Stages, ImprovementStage{ID: improvementID(req.RunID, "general-probe"), Role: "probe", Run: gp.RunID, Profile: general.Profile, Error: errorText(gerr)}, ImprovementStage{ID: improvementID(req.RunID, "domain-probe"), Role: "probe", Run: dp.RunID, Profile: domain.Profile, Error: errorText(derr)})
			if gerr == nil && derr == nil && gp.CleanupResult.Verified && dp.CleanupResult.Verified && !fatalImprovementProbe(gp) && !fatalImprovementProbe(dp) {
				co, ce := session("compose", []string{ad.ProfileDigest, general.Profile, domain.Profile}, []string{base.RunID, gp.RunID, dp.RunID})
				if ce == nil {
					ce = probe("composed", co.Profile).Get(stageCtx, &composed)
					r.Stages = append(r.Stages, ImprovementStage{ID: improvementID(req.RunID, "composed"), Role: "composed", Run: composed.RunID, Profile: co.Profile, Error: errorText(ce)})
					if ce != nil || fatalImprovementProbe(composed) {
						r.Error = "composed execution failed required infrastructure or security boundaries"
					}
				}
				pipelineErr = ce
			} else {
				pipelineErr = fmt.Errorf("probe failure or cleanup unverified")
				r.Error = "probe infrastructure or security outcome requires reconciliation"
			}
		} else {
			pipelineErr = fmt.Errorf("proposal stage failed")
		}
	} else {
		pipelineErr = fmt.Errorf("baseline execution or cleanup failed")
	}
	if ad.Policy.Mode == "shadow" {
		r.Fallback = "shadow preserves baseline"
	}
	if pipelineErr != nil {
		r.Fallback = errorText(pipelineErr)
	}
	// Selection eligibility, gateway usage and patch identity are checked in
	// an activity using durable evidence, not a model's assessment.
	status.CurrentStep = "improvement.select"
	if r.Error != "" {
		r.FailureKind = "boundary"
	}
	var selected ImprovementReceipt
	dc, _ := workflow.NewDisconnectedContext(ctx)
	dc = workflow.WithActivityOptions(dc, workflow.ActivityOptions{StartToCloseTimeout: 3 * time.Minute, RetryPolicy: &temporal.RetryPolicy{MaximumAttempts: 1}})
	if e := workflow.ExecuteActivity(dc, a.SelectImprovement, ImprovementFinish{ad, r}).Get(dc, &selected); e != nil {
		return RunManifest{}, e
	}
	if strategy == "harness2" && ctx.Err() == nil && selected.SelectedRun != "" && selected.Error == "" {
		// Reflection creates a draft only. Its failure is recorded and cannot
		// publish memory or trigger another solver execution.
		_, _ = session("reflect", []string{selected.ProfileDigest}, []string{selected.SelectedRun})
	}
	if r.Error != "" {
		selected.Error = r.Error
		selected.FailureKind = "boundary"
	}
	selected.Stages = r.Stages
	if e := workflow.ExecuteActivity(dc, a.PersistImprovement, ImprovementFinish{ad, selected}).Get(dc, &selected); e != nil {
		return RunManifest{}, e
	}
	if ctx.Err() != nil {
		return RunManifest{}, ctx.Err()
	}
	if selected.Error != "" || selected.SelectedRun == "" {
		var out RunManifest
		e := workflow.ExecuteActivity(dc, a.FinalizeImprovementFailure, ImprovementFinish{ad, selected}).Get(dc, &out)
		return out, e
	}
	finalReq := ad.Request
	finalReq.ImprovementPolicy = ""
	remaining := ad.Deadline.Sub(workflow.Now(ctx))
	if remaining <= ad.Policy.CleanupAllowance.Std() {
		selected.Error = "insufficient verification and cleanup allowance"
		selected.FailureKind = "boundary"
		var out RunManifest
		e := workflow.ExecuteActivity(dc, a.FinalizeImprovementFailure, ImprovementFinish{ad, selected}).Get(dc, &out)
		return out, e
	}
	finalReq.TotalTimeout = remaining
	out, e := softwareChangeWorkflow(ctx, finalReq, executionOverrides{SelectedRun: selected.SelectedRun, Parent: req.RunID, ProfileDigest: selected.ProfileDigest, BudgetAccount: improvementID(req.RunID, "composed"), Receipt: &selected})
	if out.FactoryResult == StateInfrastructureFailed || out.FactoryResult == StateEgressUnverified || out.FactoryResult == StateGateTampered || out.FactoryResult == StateQuarantined || !out.CleanupResult.Verified {
		if ce := workflow.ExecuteActivity(dc, a.SuspendImprovement, ad.Policy).Get(dc, nil); ce != nil {
			return out, ce
		}
	}
	return out, e
}

func fatalImprovementProbe(m RunManifest) bool {
	switch m.FactoryResult {
	case StateSucceeded, StateVerificationFailed, StateAgentFailed:
		return !m.CleanupResult.Verified
	default:
		return true
	}
}
func errorText(e error) string {
	if e == nil {
		return ""
	}
	s := e.Error()
	if len(s) > 1000 {
		s = s[:1000]
	}
	return s
}

// Retained as a named type for manifests and gateway contract tests.
var _ inferencebudget.Account
