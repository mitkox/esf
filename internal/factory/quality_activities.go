package factory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mitkox/esf/internal/agentharness"
	"github.com/mitkox/esf/internal/assurance"
	"github.com/mitkox/esf/internal/repository"
	"github.com/mitkox/esf/internal/sandbox"
	"github.com/mitkox/esf/internal/sandbox/cube"
	"github.com/mitkox/esf/internal/verification"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.temporal.io/sdk/temporal"
)

type QualityRouteInput struct {
	Request     RunRequest
	WorkflowID  string
	ExecutionID string
}
type QualityRouteOutput struct {
	Controlled bool
	Record     assurance.Run
}
type QualityRunInput struct {
	RunID       string
	ExecutionID string
}
type QualityAuthorOutput struct {
	Baseline     repository.Baseline  `json:"baseline"`
	Bundle       string               `json:"bundle"`
	BaselineTree string               `json:"baseline_tree"`
	Proposal     string               `json:"proposal"`
	Agent        agentharness.Result  `json:"agent"`
	Evidence     []assurance.Evidence `json:"evidence"`
	StartedAt    time.Time            `json:"started_at"`
	CompletedAt  time.Time            `json:"completed_at"`
}
type QualityGateInput struct {
	RunID       string
	ExecutionID string
	Control     assurance.Control
	Attempt     int
}
type QualityFinalizeInput struct {
	RunID   string
	Failure string
	Cleanup CleanupResult
	Outcome RunState
}

func (a *Activities) QualityRoute(ctx context.Context, in QualityRouteInput) (QualityRouteOutput, error) {
	if !a.cfg.Quality.Enabled() {
		if in.Request.AdmissionID != "" {
			return QualityRouteOutput{}, fmt.Errorf("quality admission unavailable on this worker")
		}
		return QualityRouteOutput{}, nil
	}
	if in.Request.AdmissionID == "" {
		if a.quality != nil {
			if _, err := a.quality.Store.Get(ctx, in.Request.RunID); err == nil {
				return QualityRouteOutput{}, temporal.NewNonRetryableApplicationError("run ID is reserved by controlled admission", "QualityAdmission", nil)
			} else if !errors.Is(err, assurance.ErrNotFound) {
				return QualityRouteOutput{}, err
			}
		}
		_, policies, err := a.cfg.resolveQualityRepository(in.Request)
		if err != nil {
			return QualityRouteOutput{}, temporal.NewNonRetryableApplicationError(err.Error(), "QualityAdmission", err)
		}
		if len(policies) == 0 {
			if in.Request.AdmissionID != "" {
				return QualityRouteOutput{}, fmt.Errorf("unexpected quality admission")
			}
			return QualityRouteOutput{}, nil
		}
	}
	if a.quality == nil || in.Request.AdmissionID == "" {
		return QualityRouteOutput{}, temporal.NewNonRetryableApplicationError("controlled repository requires authenticated quality admission", "QualityAdmission", nil)
	}
	r, err := a.quality.Store.Get(ctx, in.Request.RunID)
	if err != nil {
		return QualityRouteOutput{}, err
	}
	digest := qualityRequestDigest(in.Request, r.Requester.ID)
	if r.Decision != nil {
		if r.AdmissionID != in.Request.AdmissionID || r.RequestDigest != digest || r.ExecutionID != in.ExecutionID {
			return QualityRouteOutput{}, assurance.ErrConflict
		}
		return QualityRouteOutput{true, r}, nil
	}
	r, err = a.quality.Store.Bind(ctx, r.ID, in.Request.AdmissionID, digest, in.WorkflowID, in.ExecutionID)
	return QualityRouteOutput{true, r}, err
}

func (a *Activities) qualityRecord(ctx context.Context, in QualityRunInput) (assurance.Run, QualityDependencies, *Activities, error) {
	if a.quality == nil {
		return assurance.Run{}, QualityDependencies{}, nil, fmt.Errorf("quality runtime is unavailable")
	}
	r, err := a.quality.Store.Get(ctx, in.RunID)
	if err != nil {
		return r, QualityDependencies{}, nil, err
	}
	if r.ExecutionID != in.ExecutionID || in.ExecutionID == "" {
		return r, QualityDependencies{}, nil, assurance.ErrForbidden
	}
	if r.Snapshot.Evaluator != assurance.EvaluatorVersion {
		return r, QualityDependencies{}, nil, fmt.Errorf("frozen evaluator version is unavailable on this worker")
	}
	var d QualityDependencies
	if err = json.Unmarshal(r.Snapshot.Dependencies, &d); err != nil {
		return r, d, nil, err
	}
	for name, h := range d.Harnesses {
		for field := range map[string]bool{"binary": true, "catalog": true} {
			if digest := d.HostFiles[name+"/"+field]; digest != "" {
				p, e := a.quality.Content.VerifiedPath(digest)
				if e != nil {
					return r, d, nil, e
				}
				if field == "binary" {
					h.Binary = p
				} else {
					h.CatalogCache = p
				}
			}
		}
		d.Harnesses[name] = h
	}
	frozen := *a
	frozen.cfg.Harnesses = d.Harnesses
	frozen.cfg.Sandbox = d.Sandbox
	frozen.cfg.Limits = d.Limits
	frozen.cfg.FactoryVersion = d.FactoryVersion
	frozen.profiles = verification.Profiles{Profiles: d.Profiles}
	frozen.repos = repository.New(d.Resources.RepositoriesAllowed)
	frozen.harnesses, err = frozen.cfg.BuildHarnesses()
	return r, d, &frozen, err
}
func qualityCached[T any](r assurance.Run, key string) (T, bool, error) {
	var v T
	b, ok := r.Checkpoints[key]
	if !ok {
		return v, false, nil
	}
	err := json.Unmarshal(b, &v)
	return v, true, err
}
func (a *Activities) qualityEvidence(kind string, b []byte, producer, candidate, control, origin string, redact bool) (assurance.Evidence, error) {
	if redact {
		b = []byte(a.redactor.Redact(string(b)))
	} else if !bytes.Equal(b, []byte(a.redactor.Redact(string(b)))) {
		return assurance.Evidence{}, fmt.Errorf("%s requires sanitization; authoritative bytes rejected (digest %s)", kind, assurance.Digest(b))
	}
	if !redact && qualityJSONHasSecrets(a.redactor, b) {
		return assurance.Evidence{}, fmt.Errorf("%s requires sanitization; authoritative bytes rejected (digest %s)", kind, assurance.Digest(b))
	}
	digest, err := a.quality.Content.Put(b)
	if err != nil {
		return assurance.Evidence{}, err
	}
	return assurance.Evidence{Kind: kind, Digest: digest, Size: int64(len(b)), Producer: producer, Origin: origin, CandidateDigest: candidate, ControlID: control, Redacted: redact}, nil
}

func (a *Activities) qualitySandbox(ctx context.Context, r assurance.Run, d QualityDependencies, slot string) (sandbox.Sandbox, error) {
	infos, err := a.provider.List(ctx)
	if err != nil {
		return nil, err
	}
	var owned []sandbox.Info
	for _, info := range infos {
		if info.Metadata["quality"] == "v1" && info.Metadata["run_id"] == r.ID && info.Metadata["workflow_run_id"] == r.ExecutionID && info.Metadata["quality_slot"] == slot {
			owned = append(owned, info)
		}
	}
	if len(owned) > 1 {
		return nil, fmt.Errorf("ambiguous quality sandbox ownership")
	}
	if len(owned) == 1 {
		return a.provider.Reattach(ctx, owned[0].ID)
	}
	key := "sandbox/" + slot + "/intent"
	if _, ok := r.Checkpoints[key]; ok {
		return nil, fmt.Errorf("quality sandbox creation outcome unresolved")
	}
	if _, err = a.quality.Store.Checkpoint(ctx, r.ID, key, slot); err != nil {
		return nil, err
	}
	template := d.Resources.WorkspaceConfig.Template
	if template == "" {
		template = d.Request.SandboxTemplate
	}
	return a.provider.Create(ctx, sandbox.Spec{Template: template, Metadata: map[string]string{"origin": "factory", "quality": "v1", "run_id": r.ID, "workflow_run_id": r.ExecutionID, "quality_slot": slot}, IdleTimeout: d.Limits.TotalTimeout.Std(), Network: d.Resources.Egress})
}
func (a *Activities) qualityPrepareTools(ctx context.Context, sb sandbox.Sandbox, d QualityDependencies) error {
	if err := installBasePackages(ctx, sb, d.Resources.WorkspaceConfig.EffectiveBasePackages(d.Sandbox.BasePackages), a.log); err != nil {
		return err
	}
	if script := d.Resources.WorkspaceConfig.SetupScript; script != "" {
		exec, err := sb.Execute(ctx, sandbox.Command{Script: script, Timeout: 10 * time.Minute, Description: "quality workspace preparation"})
		if err != nil {
			return err
		}
		if !exec.Succeeded() {
			return fmt.Errorf("quality workspace setup failed")
		}
	}
	return nil
}
func qualityCommand(ctx context.Context, sb sandbox.Sandbox, dir string, argv ...string) (string, error) {
	result, err := sb.Execute(ctx, sandbox.Command{Argv: argv, Dir: dir, Timeout: 5 * time.Minute, Env: map[string]string{"GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_GLOBAL": "/dev/null", "GIT_TERMINAL_PROMPT": "0"}})
	if err != nil {
		return "", err
	}
	if !result.Succeeded() {
		return "", fmt.Errorf("quality command %s failed (exit %d)", argv[0], result.ExitCode)
	}
	return result.Stdout, nil
}
func qualityGit(ctx context.Context, sb sandbox.Sandbox, args ...string) (string, error) {
	prefix := []string{"git", "-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false", "-c", "core.attributesFile=/dev/null", "-c", "core.autocrlf=false", "-c", "diff.external=", "-C", DefaultRepositoryDir}
	return qualityCommand(ctx, sb, DefaultRepositoryDir, append(prefix, args...)...)
}
func qualitySourceCheck(ctx context.Context, sb sandbox.Sandbox) error {
	tree, err := qualityGit(ctx, sb, "ls-files", "--stage", "-z")
	if err != nil {
		return err
	}
	for _, entry := range strings.Split(tree, "\x00") {
		if strings.HasPrefix(entry, "160000 ") {
			return fmt.Errorf("controlled source transport does not support submodules")
		}
	}
	files, err := qualityGit(ctx, sb, "ls-files", "-z")
	if err != nil {
		return err
	}
	for _, name := range strings.Split(files, "\x00") {
		if name == ".gitmodules" {
			return fmt.Errorf("controlled source transport does not support submodules")
		}
		if path.Base(name) == ".gitattributes" {
			b, err := sb.ReadFile(ctx, path.Join(DefaultRepositoryDir, name))
			if err != nil {
				return err
			}
			if strings.Contains(string(b), "filter=") || strings.Contains(string(b), "filter =") {
				return fmt.Errorf("controlled source transport does not support Git filters or LFS")
			}
		}
	}
	return nil
}
func qualityTree(ctx context.Context, sb sandbox.Sandbox) (string, error) {
	tree, err := qualityGit(ctx, sb, "write-tree")
	if err != nil {
		return "", err
	}
	entries, err := qualityGit(ctx, sb, "ls-tree", "-r", "-z", "--full-tree", strings.TrimSpace(tree))
	if err != nil {
		return "", err
	}
	return assurance.Digest([]byte(entries)), nil
}

func (a *Activities) QualityAuthor(ctx context.Context, in QualityRunInput) (QualityAuthorOutput, error) {
	stop := startActivityHeartbeat(ctx)
	defer stop()
	r, d, frozen, err := a.qualityRecord(ctx, in)
	if err != nil {
		return QualityAuthorOutput{}, err
	}
	if cached, ok, err := qualityCached[QualityAuthorOutput](r, "author-completed"); ok {
		return cached, err
	}
	if _, ok := r.Checkpoints["author-started"]; ok {
		return QualityAuthorOutput{}, fmt.Errorf("author execution outcome unresolved; new run required")
	}
	if _, err = a.quality.Store.Checkpoint(ctx, r.ID, "author-started", r.ExecutionID); err != nil {
		return QualityAuthorOutput{}, err
	}
	out := QualityAuthorOutput{StartedAt: time.Now().UTC()}
	sb, err := a.qualitySandbox(ctx, r, d, "author")
	if err != nil {
		return out, err
	}
	if _, err = frozen.PrepareSandbox(ctx, PrepareSandboxInput{RunID: r.ID, SandboxID: sb.ID(), Harness: d.Request.AgentHarness, BasePackages: d.Resources.WorkspaceConfig.BasePackages, SetupScript: d.Resources.WorkspaceConfig.SetupScript, WorkspaceResolved: true, ModelEndpoint: agentharness.ModelEndpoint{Provider: d.Resources.ModelProvider, Model: d.Resources.ModelID, BaseURL: d.Resources.ModelBaseURL, APIKeyEnv: d.Resources.ModelAPIKeyEnv}}); err != nil {
		return out, err
	}
	prepared, err := frozen.PrepareRepository(ctx, PrepareRepositoryInput{RunID: r.ID, SandboxID: sb.ID(), Request: d.Request})
	if err != nil {
		return out, err
	}
	out.Baseline = prepared.Baseline
	if err = qualitySourceCheck(ctx, sb); err != nil {
		return out, err
	}
	out.BaselineTree, err = qualityTree(ctx, sb)
	if err != nil {
		return out, err
	}
	if _, err = qualityGit(ctx, sb, "bundle", "create", "/workspace/.factory/quality-source.bundle", "HEAD"); err != nil {
		return out, err
	}
	bundle, err := sb.ReadFile(ctx, "/workspace/.factory/quality-source.bundle")
	if err != nil {
		return out, err
	}
	e, err := a.qualityEvidence("baseline", bundle, "factory", "", "", "factory-observed", false)
	if err != nil {
		return out, err
	}
	out.Bundle = e.Digest
	out.Evidence = append(out.Evidence, e)
	if _, err = a.quality.Store.Checkpoint(ctx, r.ID, "baseline", out); err != nil {
		return out, err
	}
	// Only prerequisites that can be established before authoring are allowed.
	for _, p := range r.Snapshot.Policies {
		for _, q := range p.Spec.Qualifications {
			if q.Phase == "author" {
				if err = checkQualityQualification(ctx, sb, d, q, d.Request.AgentHarness); err != nil {
					return out, err
				}
				b, _ := json.Marshal(q)
				e, err := a.qualityEvidence("qualification", b, "factory", "", q.ID, "factory-observed", false)
				if err != nil {
					return out, err
				}
				out.Evidence = append(out.Evidence, e)
			}
		}
	}
	if _, err = frozen.WriteInventory(ctx, WriteInventoryInput{RunID: r.ID, SandboxID: sb.ID(), Request: d.Request, Resources: d.Resources, BaselineSHA: out.Baseline.SHA, Template: sb.Template()}); err != nil {
		return out, err
	}
	planBytes, _ := json.Marshal(map[string]any{"snapshot_digest": r.Snapshot.Digest, "policies": r.Snapshot.Policies, "provisional": true, "source_revision": out.Baseline.SHA})
	if err = sb.WriteFile(ctx, "/workspace/.factory/quality-plan.json", planBytes); err != nil {
		return out, err
	}
	if _, err = frozen.ApplyRuntimeNetwork(ctx, ApplyRuntimeNetworkInput{RunID: r.ID, SandboxID: sb.ID(), Harness: d.Request.AgentHarness}); err != nil {
		return out, err
	}
	model := d.Request.AgentModel
	if d.Resources.ModelID != "" {
		model = d.Resources.ModelID
	}
	timeout, err := boundedTimeout(d.Request.AgentTimeout, d.Limits.AgentTimeout.Std(), 30*time.Minute)
	if err != nil {
		return out, err
	}
	agent, err := frozen.RunAgent(ctx, RunAgentInput{RunID: r.ID, SandboxID: sb.ID(), Harness: d.Request.AgentHarness, Model: model, ModelAPIKeyEnv: d.Resources.ModelAPIKeyEnv, Prompt: d.Request.Task + "\n\nThe factory quality plan is available at /workspace/.factory/quality-plan.json.", RepositoryDir: DefaultRepositoryDir, Timeout: timeout})
	if err != nil {
		return out, err
	}
	out.Agent = agent.Result
	if agent.EvidenceError != "" {
		return out, fmt.Errorf("author evidence persistence: %s", agent.EvidenceError)
	}
	for kind, data := range map[string]string{"author-stdout": out.Agent.Stdout, "author-stderr": out.Agent.Stderr} {
		e, err := a.qualityEvidence(kind, []byte(data), r.ID+"/author", "", "", "agent-reported", true)
		if err != nil {
			return out, err
		}
		out.Evidence = append(out.Evidence, e)
	}
	out.Agent.Stdout = ""
	out.Agent.Stderr = ""
	resultBytes, _ := json.Marshal(out.Agent)
	e, err = a.qualityEvidence("author-result", resultBytes, "factory", "", "", "factory-observed", false)
	if err != nil {
		return out, err
	}
	out.Evidence = append(out.Evidence, e)
	// HEAD may have moved: capture against the recorded original commit.
	if _, err = qualityGit(ctx, sb, "add", "-A", "--", "."); err != nil {
		return out, err
	}
	proposal, err := qualityGit(ctx, sb, "diff", "--cached", out.Baseline.SHA, "--binary", "--no-color", "--no-ext-diff", "--no-textconv")
	if err != nil {
		return out, err
	}
	e, err = a.qualityEvidence("patch", []byte(proposal), "factory", "", "", "factory-observed", false)
	if err != nil {
		return out, err
	}
	out.Proposal = e.Digest
	out.Evidence = append(out.Evidence, e)
	out.CompletedAt = time.Now().UTC()
	if _, err = a.quality.Store.Checkpoint(ctx, r.ID, "author-completed", out); err != nil {
		return out, err
	}
	return out, nil
}

func (a *Activities) qualityReconstruct(ctx context.Context, r assurance.Run, d QualityDependencies, slot string, author QualityAuthorOutput) (sandbox.Sandbox, string, error) {
	sb, err := a.qualitySandbox(ctx, r, d, slot)
	if err != nil {
		return nil, "", err
	}
	if err = a.qualityPrepareTools(ctx, sb, d); err != nil {
		return nil, "", err
	}
	bundle, err := a.quality.Content.Read(author.Bundle)
	if err != nil {
		return nil, "", err
	}
	patchBytes, err := a.quality.Content.Read(author.Proposal)
	if err != nil {
		return nil, "", err
	}
	if err = sb.WriteFile(ctx, "/workspace/.factory/source.bundle", bundle); err != nil {
		return nil, "", err
	}
	if err = sb.WriteFile(ctx, "/workspace/.factory/candidate.patch", patchBytes); err != nil {
		return nil, "", err
	}
	if _, err = qualityCommand(ctx, sb, "/workspace", "git", "-c", "core.hooksPath=/dev/null", "clone", "--no-checkout", "/workspace/.factory/source.bundle", DefaultRepositoryDir); err != nil {
		return nil, "", err
	}
	if _, err = qualityGit(ctx, sb, "checkout", "--detach", "--force", author.Baseline.SHA); err != nil {
		return nil, "", err
	}
	tree, err := qualityTree(ctx, sb)
	if err != nil {
		return nil, "", err
	}
	if tree != author.BaselineTree {
		return nil, "", fmt.Errorf("baseline tree mismatch")
	}
	if len(patchBytes) > 0 {
		if _, err = qualityGit(ctx, sb, "apply", "--index", "--binary", "/workspace/.factory/candidate.patch"); err != nil {
			return nil, "", err
		}
	}
	if err = qualitySourceCheck(ctx, sb); err != nil {
		return nil, "", err
	}
	tree, err = qualityTree(ctx, sb)
	return sb, tree, err
}

func (a *Activities) QualityCandidate(ctx context.Context, in QualityRunInput) (assurance.Run, error) {
	stop := startActivityHeartbeat(ctx)
	defer stop()
	r, d, _, err := a.qualityRecord(ctx, in)
	if err != nil {
		return r, err
	}
	if r.Candidate != nil {
		return r, nil
	}
	author, ok, err := qualityCached[QualityAuthorOutput](r, "author-completed")
	if err != nil || !ok {
		return r, fmt.Errorf("author checkpoint unavailable")
	}
	sb, tree, err := a.qualityReconstruct(ctx, r, d, "reconstruct", author)
	if err != nil {
		return r, err
	}
	names, err := qualityGit(ctx, sb, "diff", "--cached", "--name-only", "--no-renames", "-z", author.Baseline.SHA)
	if err != nil {
		return r, err
	}
	var paths []string
	for _, p := range strings.Split(names, "\x00") {
		if p != "" {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	candidate := assurance.Candidate{RepositoryID: r.Snapshot.RepositoryID, BaselineSHA: author.Baseline.SHA, BaselineTree: author.BaselineTree, BaselineBundle: author.Bundle, PatchDigest: author.Proposal, TreeDigest: tree, Paths: paths}
	candidate.Digest = assurance.Hash(candidate)
	classifier := a.quality.Classifier
	if classifier == nil {
		classifier = assurance.DeterministicClassifier{}
	}
	plan, err := classifier.Classify(ctx, r.Snapshot, candidate)
	if err != nil {
		return r, err
	}
	// Risk-dependent author prerequisites were not present at admission. Fail
	// rather than retroactively pretending the execution was qualified.
	for _, q := range plan.Qualifications {
		if q.Phase == "author" {
			found := false
			for _, e := range author.Evidence {
				if e.Kind == "qualification" && e.ControlID == q.ID {
					found = true
				}
			}
			if !found {
				return r, fmt.Errorf("late author prerequisite %s requires a new controlled run", q.ID)
			}
		}
	}
	for _, g := range plan.Controls {
		if g.Type == "independent-review" {
			if g.Harness == d.Request.AgentHarness {
				return r, fmt.Errorf("reviewer must use a distinct qualified configuration")
			}
			if g.DifferentModel && (d.Harnesses[g.Harness].Model == "" || author.Agent.Model == "" || d.Harnesses[g.Harness].Model == author.Agent.Model) {
				return r, fmt.Errorf("policy requires reviewer model diversity")
			}
		}
	}
	return a.quality.Store.Transition(ctx, r.ID, "candidate", candidate, func(record *assurance.Run, _ time.Time) error {
		record.Candidate = &candidate
		record.Plan = &plan
		for _, e := range author.Evidence {
			e.CandidateDigest = candidate.Digest
			record.Evidence = append(record.Evidence, e)
		}
		return nil
	})
}

func checkQualityQualification(ctx context.Context, sb sandbox.Sandbox, d QualityDependencies, q assurance.Qualification, harness string) error {
	if q.Harness != "" && q.Harness != harness {
		return fmt.Errorf("qualification %s: wrong harness", q.ID)
	}
	h := d.Harnesses[harness]
	model := h.Model
	if harness == d.Request.AgentHarness {
		if d.Resources.ModelID != "" {
			model = d.Resources.ModelID
		} else if d.Request.AgentModel != "" {
			model = d.Request.AgentModel
		}
	}
	if q.Model != "" && q.Model != model {
		return fmt.Errorf("qualification %s: model not approved", q.ID)
	}
	if q.Template != "" && q.Template != sb.Template() {
		return fmt.Errorf("qualification %s: template not approved", q.ID)
	}
	if q.BinarySHA256 != "" {
		executable := q.Executable
		if executable == "" {
			switch h.Type {
			case "opencode":
				executable = "/usr/local/bin/opencode2"
			case "unreal":
				executable = agentharness.UnrealBinaryPath
			case "generic":
				executable = h.Executable
				if h.Binary != "" {
					executable = "/usr/local/bin/" + harness
				}
			}
		}
		if !path.IsAbs(executable) {
			return fmt.Errorf("qualification %s requires an absolute executable path", q.ID)
		}
		measured, err := qualityCommand(ctx, sb, "/workspace", "sha256sum", "--", executable)
		if err != nil {
			return fmt.Errorf("qualification %s unavailable: %w", q.ID, err)
		}
		fields := strings.Fields(measured)
		if len(fields) == 0 || strings.ToLower(fields[0]) != strings.TrimPrefix(strings.ToLower(q.BinarySHA256), "sha256:") {
			return fmt.Errorf("qualification %s: executable digest mismatch", q.ID)
		}
	}
	return nil
}

type qualityGateSink struct {
	a        *Activities
	run      assurance.Run
	gate     string
	evidence []assurance.Evidence
	err      error
}

func (s *qualityGateSink) Write(name string, b []byte) error {
	e, err := s.a.qualityEvidence("gate-log", b, "factory", s.run.Candidate.Digest, s.gate, "repository-reported", true)
	if err != nil {
		s.err = errors.Join(s.err, err)
		return err
	}
	s.evidence = append(s.evidence, e)
	return nil
}

func (a *Activities) QualityGate(ctx context.Context, in QualityGateInput) (assurance.GateAttempt, error) {
	r, err := a.quality.Store.Get(ctx, in.RunID)
	if err != nil {
		return assurance.GateAttempt{}, err
	}
	if r.ExecutionID != in.ExecutionID {
		return assurance.GateAttempt{}, assurance.ErrForbidden
	}
	var executor assurance.GateExecutor = activityGateExecutor{a}
	return executor.Execute(ctx, r, in.Control, strconv.Itoa(in.Attempt))
}

type activityGateExecutor struct{ activities *Activities }

func (e activityGateExecutor) Execute(ctx context.Context, r assurance.Run, c assurance.Control, attempt string) (assurance.GateAttempt, error) {
	n, err := strconv.Atoi(attempt)
	if err != nil || n < 1 || n > 3 {
		return assurance.GateAttempt{}, fmt.Errorf("invalid gate attempt")
	}
	return e.activities.executeQualityGate(ctx, QualityGateInput{r.ID, r.ExecutionID, c, n})
}

func (a *Activities) executeQualityGate(ctx context.Context, in QualityGateInput) (assurance.GateAttempt, error) {
	stop := startActivityHeartbeat(ctx)
	defer stop()
	r, d, frozen, err := a.qualityRecord(ctx, QualityRunInput{in.RunID, in.ExecutionID})
	if err != nil {
		return assurance.GateAttempt{}, err
	}
	id := fmt.Sprintf("%s-attempt-%d", in.Control.ID, in.Attempt)
	for _, g := range r.Gates {
		if g.ID == id {
			return g, nil
		}
	}
	if r.Plan == nil || r.Candidate == nil {
		return assurance.GateAttempt{}, fmt.Errorf("quality candidate required")
	}
	found := false
	for _, g := range r.Plan.Controls {
		if g.ID == in.Control.ID && assurance.Hash(g) == assurance.Hash(in.Control) {
			found = true
		}
	}
	if !found {
		return assurance.GateAttempt{}, assurance.ErrForbidden
	}
	g := assurance.GateAttempt{ID: id, ControlID: in.Control.ID, CandidateDigest: r.Candidate.Digest, PlanDigest: r.Plan.Digest, Status: "error", Executor: assurance.Actor{ID: r.ID + "/" + id, Kind: "factory"}, StartedAt: time.Now().UTC()}
	if _, ok := r.Checkpoints["gate-started/"+id]; ok {
		g.Reason = "gate outcome unresolved; refusing to turn an unknown result into a pass"
		g.CompletedAt = time.Now().UTC()
		_, err = a.quality.Store.RecordGate(ctx, r.ID, g)
		return g, err
	}
	if _, err = a.quality.Store.Checkpoint(ctx, r.ID, "gate-started/"+id, id); err != nil {
		return g, err
	}
	execute := func() error {
		if in.Control.Type == "provider-snapshot" || in.Control.Type == "provider-ack" {
			body := d.ProviderFacts[in.Control.ID]
			if in.Control.Type == "provider-ack" {
				var e error
				body, e = a.qualityAcknowledgment(ctx, r, in.Control)
				if e != nil {
					return fmt.Errorf("required provider acknowledgment unavailable: %w", e)
				}
				var ack struct {
					CandidateDigest string `json:"candidate_digest"`
					PlanDigest      string `json:"plan_digest"`
					Acknowledged    bool   `json:"acknowledged"`
				}
				if e = json.Unmarshal(body, &ack); e != nil {
					return e
				}
				if !ack.Acknowledged || ack.CandidateDigest != r.Candidate.Digest || ack.PlanDigest != r.Plan.Digest {
					return fmt.Errorf("provider acknowledgment does not bind this candidate and plan")
				}
			}
			if len(body) == 0 {
				return fmt.Errorf("required provider snapshot unavailable")
			}
			e, err := a.qualityEvidence("provider-fact", body, "operator-import", r.Candidate.Digest, in.Control.ID, "provider-reported", false)
			if err != nil {
				return err
			}
			g.Evidence = append(g.Evidence, e)
			g.Status = "passed"
			return nil
		}
		author, ok, err := qualityCached[QualityAuthorOutput](r, "author-completed")
		if err != nil || !ok {
			return fmt.Errorf("author checkpoint unavailable")
		}
		sb, tree, err := a.qualityReconstruct(ctx, r, d, "gate-"+id, author)
		if err != nil {
			var network net.Error
			g.Retryable = ctx.Err() == nil && (errors.Is(err, cube.ErrUnavailable) || errors.As(err, &network)) && in.Attempt < 3
			return err
		}
		if tree != r.Candidate.TreeDigest {
			return fmt.Errorf("candidate tree mismatch")
		}
		for _, name := range in.Control.Outputs {
			tracked, e := qualityGit(ctx, sb, "ls-files", "--", name)
			if e != nil {
				return e
			}
			if strings.TrimSpace(tracked) != "" {
				return fmt.Errorf("gate output overlaps candidate source: %s", name)
			}
		}
		phase := "verification"
		harness := ""
		if in.Control.Type == "independent-review" {
			phase = "review"
			harness = in.Control.Harness
		}
		if in.Control.Type == "independent-review" {
			h, e := frozen.harnesses.Resolve(harness)
			if e != nil {
				return e
			}
			if e = h.Provision(ctx, sb); e != nil {
				return e
			}
		}
		for _, q := range r.Plan.Qualifications {
			if q.Phase == phase {
				if err = checkQualityQualification(ctx, sb, d, q, harness); err != nil {
					return err
				}
				body, _ := json.Marshal(q)
				e, err := a.qualityEvidence("qualification", body, "factory", r.Candidate.Digest, in.Control.ID, "factory-observed", false)
				if err != nil {
					return err
				}
				g.Evidence = append(g.Evidence, e)
			}
		}
		if in.Control.Type == "verification" {
			profile, ok := d.Profiles[in.Control.Profile]
			if !ok {
				return fmt.Errorf("frozen gate profile unavailable")
			}
			sink := &qualityGateSink{a: a, run: r, gate: in.Control.ID}
			result, err := (&verification.Runner{Sink: sink, ArtifactPrefix: id}).Run(ctx, sb, profile, DefaultRepositoryDir)
			if err != nil {
				return err
			}
			if sink.err != nil {
				return sink.err
			}
			g.Evidence = append(g.Evidence, sink.evidence...)
			b, _ := json.Marshal(result)
			e, err := a.qualityEvidence("gate-result", b, "factory", r.Candidate.Digest, in.Control.ID, "factory-observed", false)
			if err != nil {
				return err
			}
			g.Evidence = append(g.Evidence, e)
			if result.Passed {
				g.Status = "passed"
			} else {
				g.Status = "failed"
				g.Reason = "mandatory verification steps failed: " + strings.Join(result.FailingSteps(), ",")
			}
		} else {
			g.Executor.Kind = "agent"
			g.Executor.Roles = []string{"independent-reviewer"}
			h, err := frozen.harnesses.Resolve(in.Control.Harness)
			if err != nil {
				return err
			}
			if _, err = frozen.ApplyRuntimeNetwork(ctx, ApplyRuntimeNetworkInput{RunID: r.ID, SandboxID: sb.ID(), Harness: in.Control.Harness}); err != nil {
				return err
			}
			prompt := fmt.Sprintf("Review the supplied change against the task. Do not edit source. Return ONLY JSON with candidate_digest, plan_digest, verdict (approve or changes_requested), and findings (array of strings). Candidate digest: %s\nPlan digest: %s\nTask:\n%s", r.Candidate.Digest, r.Plan.Digest, d.Request.Task)
			if in.Control.ReportFile != "" {
				prompt += "\nWrite the JSON report to " + in.Control.ReportFile + ". This report file is the only permitted write."
			}
			result, err := h.Run(ctx, sb, agentharness.Task{RunID: r.ID + "-" + id, Prompt: prompt, PromptPath: "/workspace/.factory/review-prompt.txt", RepositoryDir: DefaultRepositoryDir, Model: d.Harnesses[in.Control.Harness].Model, Timeout: d.Limits.AgentTimeout.Std()})
			if err != nil {
				return err
			}
			for kind, data := range map[string]string{"review-stdout": result.Stdout, "review-stderr": result.Stderr} {
				e, err := a.qualityEvidence(kind, []byte(data), g.Executor.ID, r.Candidate.Digest, in.Control.ID, "agent-reported", true)
				if err != nil {
					return err
				}
				g.Evidence = append(g.Evidence, e)
			}
			if !result.Succeeded() {
				g.Status = "failed"
				g.Reason = "reviewer process failed"
				g.NonWaivable = true
				return nil
			}
			var report assurance.ReviewReport
			reportBytes := []byte(result.Stdout)
			if in.Control.ReportFile != "" {
				reportBytes, err = sb.ReadFile(ctx, in.Control.ReportFile)
				if err != nil {
					return fmt.Errorf("review report missing: %w", err)
				}
			}
			decoder := json.NewDecoder(bytes.NewReader(reportBytes))
			decoder.DisallowUnknownFields()
			if err = decoder.Decode(&report); err != nil {
				return fmt.Errorf("invalid review report: %w", err)
			}
			var extra any
			if decoder.Decode(&extra) != io.EOF {
				return fmt.Errorf("review report contains trailing content")
			}
			if report.CandidateDigest != r.Candidate.Digest || report.PlanDigest != r.Plan.Digest || (report.Verdict != "approve" && report.Verdict != "changes_requested") || report.Findings == nil {
				return fmt.Errorf("review report does not match candidate and plan")
			}
			b, _ := json.Marshal(report)
			e, err := a.qualityEvidence("review-report", b, g.Executor.ID, r.Candidate.Digest, in.Control.ID, "repository-reported", false)
			if err != nil {
				return err
			}
			g.Evidence = append(g.Evidence, e)
			g.Status = "passed"
			if report.Verdict != "approve" {
				g.Status = "failed"
				g.Reason = "review requested changes"
			}
		}
		for kind, name := range in.Control.Outputs {
			b, err := sb.ReadFile(ctx, path.Join(DefaultRepositoryDir, name))
			if err != nil {
				return fmt.Errorf("missing gate output %s: %w", kind, err)
			}
			e, err := a.qualityEvidence(kind, b, g.Executor.ID, r.Candidate.Digest, in.Control.ID, "repository-reported", false)
			if err != nil {
				return err
			}
			g.Evidence = append(g.Evidence, e)
		}
		for _, name := range in.Control.Outputs {
			if _, err = qualityCommand(ctx, sb, DefaultRepositoryDir, "rm", "-f", "--", name); err != nil {
				return err
			}
		}
		for _, q := range r.Plan.Qualifications {
			if q.Phase == phase {
				if err = checkQualityQualification(ctx, sb, d, q, harness); err != nil {
					return err
				}
			}
		}
		if _, err = qualityGit(ctx, sb, "add", "-A", "--", "."); err != nil {
			return err
		}
		after, err := qualityTree(ctx, sb)
		if err != nil {
			return err
		}
		if after != tree {
			g.Status = "failed"
			g.Reason = "gate or reviewer mutated the candidate source tree"
			g.NonWaivable = true
		}
		return nil
	}
	if err = execute(); err != nil {
		g.Status = "error"
		g.Reason = a.redactor.Redact(err.Error())
	}
	g.CompletedAt = time.Now().UTC()
	_, err = a.quality.Store.RecordGate(ctx, r.ID, g)
	if err == nil {
		if m := a.telemetry.Metrics(); m != nil {
			m.QualityGateDuration.Record(ctx, g.CompletedAt.Sub(g.StartedAt).Seconds(), metric.WithAttributes(attribute.String("control", g.ControlID), attribute.String("outcome", g.Status), attribute.String("risk", r.Plan.Risk)))
		}
	}
	return g, err
}

func (a *Activities) QualityCleanup(ctx context.Context, in QualityRunInput) (CleanupResult, error) {
	result := CleanupResult{Attempted: true, FinishedAt: time.Now().UTC(), Outcome: OutcomeError}
	infos, err := a.provider.List(ctx)
	if err != nil {
		return result, err
	}
	var errs []error
	for _, s := range infos {
		if s.Metadata["quality"] == "v1" && s.Metadata["run_id"] == in.RunID && s.Metadata["workflow_run_id"] == in.ExecutionID {
			if err = a.provider.Destroy(ctx, s.ID); err != nil {
				errs = append(errs, err)
			}
		}
	}
	if len(errs) > 0 {
		return result, errors.Join(errs...)
	}
	infos, err = a.provider.List(ctx)
	if err != nil {
		return result, err
	}
	for _, s := range infos {
		if s.Metadata["quality"] == "v1" && s.Metadata["run_id"] == in.RunID && s.Metadata["workflow_run_id"] == in.ExecutionID {
			result.Remaining++
		}
	}
	if result.Remaining > 0 {
		return result, fmt.Errorf("quality sandboxes remain")
	}
	result.Verified = true
	result.Outcome = OutcomeSuccess
	return result, nil
}

func (a *Activities) QualityState(ctx context.Context, id string) (assurance.Run, error) {
	if a.quality == nil {
		return assurance.Run{}, fmt.Errorf("quality unavailable")
	}
	return a.quality.Store.Get(ctx, id)
}
func (a *Activities) QualityApproval(ctx context.Context, id string) (assurance.Run, error) {
	r, err := a.quality.Store.Get(ctx, id)
	if err != nil {
		return r, err
	}
	if r.Approval == nil {
		return a.quality.Store.OpenApproval(ctx, id)
	}
	return a.quality.Store.ResolveApproval(ctx, id)
}
func (a *Activities) QualityCAPAState(ctx context.Context, id string) (assurance.CAPA, error) {
	return a.quality.Store.GetCAPA(ctx, id)
}

func (a *Activities) QualityFinalize(ctx context.Context, in QualityFinalizeInput) (RunManifest, error) {
	r, err := a.quality.Store.Get(ctx, in.RunID)
	if err != nil {
		return RunManifest{}, err
	}
	if r.Decision != nil {
		var m RunManifest
		err = json.Unmarshal(r.Manifest, &m)
		return m, err
	}
	cleanupBytes, _ := json.Marshal(in.Cleanup)
	e, err := a.qualityEvidence("cleanup", cleanupBytes, "factory", "", "", "factory-observed", false)
	if err != nil {
		return RunManifest{}, err
	}
	_, err = a.quality.Store.Transition(ctx, r.ID, "cleanup-evidence", e, func(record *assurance.Run, _ time.Time) error {
		record.Evidence = append(record.Evidence, e)
		return nil
	})
	if err != nil {
		return RunManifest{}, err
	}
	r, err = a.quality.Store.CommitResult(ctx, r.ID, a.quality.Content, func(record assurance.Run, decision assurance.Decision, attestation string) (json.RawMessage, error) {
		var d QualityDependencies
		if err := json.Unmarshal(record.Snapshot.Dependencies, &d); err != nil {
			return nil, err
		}
		author, _, _ := qualityCached[QualityAuthorOutput](record, "author-completed")
		state := in.Outcome
		if state == "" || state == StateSucceeded {
			state = StateSucceeded
			if decision.Result != "approved" {
				state = StateQualityFailed
			}
		}
		agentOutcome := OutcomeSkipped
		if !author.StartedAt.IsZero() {
			agentOutcome = OutcomeFailed
			if author.Agent.Succeeded() {
				agentOutcome = OutcomeSuccess
			}
		}
		verifyOutcome := OutcomeSkipped
		for _, g := range record.Gates {
			if verifyOutcome == OutcomeSkipped {
				verifyOutcome = OutcomeSuccess
			}
			if g.Status != "passed" {
				verifyOutcome = OutcomeFailed
			}
		}
		summary := summarizeQuality(record)
		summary.Decision = &decision
		summary.AttestationDigest = attestation
		m := RunManifest{RunID: record.ID, ChangeID: d.Request.ChangeID, ParentRunID: d.Request.ParentRunID, Scope: d.Resources.Scope, FactoryVersion: d.FactoryVersion, WorkflowID: record.WorkflowID, WorkflowRunID: record.ExecutionID, Repository: d.Request.Repository, RepositoryKind: d.Request.RepositoryKind, RequestedRevision: d.Request.Revision, BaselineSHA: author.Baseline.SHA, TaskHash: repository.HashTask(d.Request.Task), AgentHarness: d.Request.AgentHarness, AgentVersion: author.Agent.Version, VerificationProfile: d.Request.VerificationProfile, StartedAt: record.CreatedAt, CompletedAt: decision.At, Duration: decision.At.Sub(record.CreatedAt), AgentResult: agentOutcome, AgentExitCode: author.Agent.ExitCode, VerificationResult: verifyOutcome, FactoryResult: state, CleanupResult: in.Cleanup, Artifacts: "runs/" + record.ID, Error: strings.Join(decision.Reasons, "; "), Quality: summary, TokensIn: author.Agent.TokensIn, TokensOut: author.Agent.TokensOut, InferenceCost: author.Agent.CostUSD}
		if m.ChangeID == "" {
			m.ChangeID = m.RunID
		}
		if m.Repository == "" {
			m.Repository = "local:" + d.Request.LocalPath
		}
		if record.Candidate != nil {
			m.Patch = ArtifactPatch
		}
		if record.Approval != nil && len(record.Approval.Rules) > 0 {
			m.ReviewConfigured = true
			m.ReviewOutcome = record.Approval.Status
			m.HumanResult = record.Approval.Status
		}
		return json.Marshal(m)
	}, a.redactor.Redact(in.Failure), in.Cleanup.Verified)
	if err != nil {
		return RunManifest{}, err
	}
	var m RunManifest
	err = json.Unmarshal(r.Manifest, &m)
	if metrics := a.telemetry.Metrics(); metrics != nil && r.Approval != nil && !r.Approval.RequestedAt.IsZero() {
		metrics.QualityApprovalWait.Record(ctx, r.Decision.At.Sub(r.Approval.RequestedAt).Seconds(), metric.WithAttributes(attribute.String("outcome", r.Approval.Status)))
	}
	return m, err
}
