package factory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/mitkox/esf/internal/assurance"
	"github.com/mitkox/esf/internal/repository"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
)

type qualityPeerKey struct{}

func isAlreadyStarted(err error) bool {
	var e *serviceerror.WorkflowExecutionAlreadyStarted
	return errors.As(err, &e)
}

func (r *Runtime) serveQuality(ctx context.Context, c client.Client) (func(), error) {
	path := r.Config.qualitySocket()
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return nil, err
	}
	if err := qualityCheckDirectory(filepath.Dir(path)); err != nil {
		return nil, err
	}
	if st, err := os.Lstat(path); err == nil {
		if st.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("quality socket path already exists and is not a socket")
		}
		conn, e := net.DialTimeout("unix", path, time.Second)
		if e == nil {
			conn.Close()
			return nil, fmt.Errorf("quality socket already has a server")
		}
		if err = os.Remove(path); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	l, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if gid := r.Config.Quality.SocketGID; gid != nil {
		if err = os.Chown(path, -1, *gid); err != nil {
			l.Close()
			return nil, err
		}
	}
	if err = os.Chmod(path, 0660); err != nil {
		l.Close()
		return nil, err
	}
	srv := &http.Server{Handler: r.qualityHandler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 30 * time.Second, ConnContext: func(ctx context.Context, conn net.Conn) context.Context {
		uid, err := qualityPeerUID(conn)
		if err != nil {
			return ctx
		}
		return context.WithValue(ctx, qualityPeerKey{}, uid)
	}}
	serviceCtx, cancel := context.WithCancel(ctx)
	go func() {
		if err := srv.Serve(l); err != nil && err != http.ErrServerClosed {
			r.Log.Error("quality socket stopped", "error", err)
			cancel()
		}
	}()
	outboxDone := make(chan struct{})
	go func() { defer close(outboxDone); r.qualityOutbox(serviceCtx, c) }()
	return func() {
		cancel()
		shutdown, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		_ = srv.Shutdown(shutdown)
		<-outboxDone
	}, nil
}

func (r *Runtime) qualityHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		uid, ok := req.Context().Value(qualityPeerKey{}).(uint32)
		if !ok {
			http.Error(w, "peer identity unavailable", http.StatusForbidden)
			return
		}
		actor := assurance.Actor{ID: "uid:" + strconv.FormatUint(uint64(uid), 10), UID: uid, Kind: "human", Roles: append([]string{}, r.Config.Quality.Roles[strconv.FormatUint(uint64(uid), 10)]...)}
		if len(actor.Roles) == 0 {
			_ = r.Quality.Store.AuditFailure(req.Context(), "service", uuid.NewString(), actor, "unbound UID")
			http.Error(w, "UID has no quality role", http.StatusForbidden)
			return
		}
		result, err := r.qualityRequest(req, actor)
		if err != nil {
			reason := r.Redactor.Redact(err.Error())
			_ = r.Quality.Store.AuditFailure(req.Context(), "service", uuid.NewString(), actor, reason)
			code := http.StatusBadRequest
			if errors.Is(err, assurance.ErrForbidden) {
				code = http.StatusForbidden
			}
			if errors.Is(err, assurance.ErrNotFound) {
				code = http.StatusNotFound
			}
			if errors.Is(err, assurance.ErrConflict) || errors.Is(err, assurance.ErrTerminal) {
				code = http.StatusConflict
			}
			w.WriteHeader(code)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": reason})
			return
		}
		_ = json.NewEncoder(w).Encode(result)
	})
}
func decodeQuality(req *http.Request, v any) error {
	d := json.NewDecoder(io.LimitReader(req.Body, 1<<20))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return fmt.Errorf("expected exactly one JSON document")
	}
	return nil
}
func (r *Runtime) qualityRequest(req *http.Request, actor assurance.Actor) (any, error) {
	ctx := req.Context()
	parts := strings.Split(strings.Trim(req.URL.Path, "/"), "/")
	if len(parts) < 2 || parts[0] != "v1" {
		return nil, assurance.ErrNotFound
	}
	store := r.Quality.Store
	if req.Method == "GET" {
		switch parts[1] {
		case "changes":
			changes, err := r.qualityChanges(ctx)
			if err != nil {
				return nil, err
			}
			if len(parts) == 2 {
				return changes, nil
			}
			for _, c := range changes {
				if c.ChangeID == parts[2] {
					return c, nil
				}
			}
			return nil, assurance.ErrNotFound
		case "policies":
			return r.Quality.Policies, nil
		case "outbox":
			return store.Pending(ctx)
		case "runs":
			if len(parts) == 2 {
				return store.List(ctx)
			}
			record, err := store.Get(ctx, parts[2])
			if err != nil {
				return nil, err
			}
			if len(parts) == 3 {
				return record, nil
			}
			switch parts[3] {
			case "events":
				return store.Events(ctx, record.ID)
			case "evidence":
				return record.Evidence, nil
			case "attestation":
				if len(record.Attestation) == 0 {
					return nil, fmt.Errorf("candidate has no approved attestation")
				}
				return record.Attestation, nil
			case "status":
				return summarizeQuality(record), nil
			case "manifest":
				if len(record.Manifest) == 0 {
					return nil, fmt.Errorf("run has no terminal manifest")
				}
				return record.Manifest, nil
			}
		case "capa":
			if len(parts) == 2 {
				return store.Objects(ctx, "capa")
			}
			return store.GetCAPA(ctx, parts[2])
		case "nc":
			runs, err := store.List(ctx)
			if err != nil {
				return nil, err
			}
			out := []assurance.NonConformance{}
			for _, run := range runs {
				out = append(out, run.NonConformances...)
			}
			return out, nil
		case "objects":
			if len(parts) != 3 {
				return nil, assurance.ErrNotFound
			}
			data, err := r.Quality.Content.Read(parts[2])
			if err != nil {
				return nil, err
			}
			return map[string]any{"digest": parts[2], "data": data}, nil
		}
	}
	if req.Method != "POST" {
		return nil, assurance.ErrNotFound
	}
	switch parts[1] {
	case "runs":
		if len(parts) == 2 {
			if !assurance.HasRole(actor, []string{"submitter", "maintainer", "quality-authority"}) {
				return nil, assurance.ErrForbidden
			}
			var in RunRequest
			if err := decodeQuality(req, &in); err != nil {
				return nil, err
			}
			return r.admitQuality(ctx, actor, in)
		}
		if len(parts) != 4 {
			return nil, assurance.ErrNotFound
		}
		switch parts[3] {
		case "approval":
			var in assurance.ApprovalInput
			if err := decodeQuality(req, &in); err != nil {
				return nil, err
			}
			in.Reason = r.Redactor.Redact(in.Reason)
			return store.Approve(ctx, parts[2], actor, in)
		case "exception":
			var in assurance.ExceptionInput
			if err := decodeQuality(req, &in); err != nil {
				return nil, err
			}
			in.Reason = r.Redactor.Redact(in.Reason)
			return store.GrantException(ctx, parts[2], actor, in)
		case "evaluate":
			record, err := store.Get(ctx, parts[2])
			if err != nil {
				return nil, err
			}
			var in struct {
				Paths []string `json:"paths"`
			}
			if err = decodeQuality(req, &in); err != nil {
				return nil, err
			}
			return assurance.Evaluate(record.Snapshot, in.Paths)
		}
	case "capa":
		roles := r.Config.Quality.CAPARoles
		if len(roles) == 0 {
			roles = []string{"quality-authority"}
		}
		if !assurance.HasRole(actor, roles) {
			return nil, assurance.ErrForbidden
		}
		if len(parts) == 2 {
			var in struct {
				ID               string `json:"id"`
				RunID            string `json:"run_id"`
				NonConformanceID string `json:"non_conformance_id"`
			}
			if err := decodeQuality(req, &in); err != nil {
				return nil, err
			}
			return store.OpenCAPA(ctx, in.ID, in.RunID, in.NonConformanceID, actor)
		}
		if len(parts) == 3 {
			var in assurance.CAPAAction
			if err := decodeQuality(req, &in); err != nil {
				return nil, err
			}
			in.Reason = r.Redactor.Redact(in.Reason)
			in.RootCause = r.Redactor.Redact(in.RootCause)
			in.AcceptanceCriteria = r.Redactor.Redact(in.AcceptanceCriteria)
			in.CorrectiveActions = r.Redactor.Redact(in.CorrectiveActions)
			in.PreventiveActions = r.Redactor.Redact(in.PreventiveActions)
			if in.Action == "verify" || in.Action == "close" {
				c, err := store.GetCAPA(ctx, parts[2])
				if err != nil {
					return nil, err
				}
				if in.Action == "close" {
					for _, e := range c.VerificationEvidence {
						if err = r.Quality.Content.Verify(e); err != nil {
							return nil, fmt.Errorf("CAPA effectiveness evidence integrity: %w", err)
						}
					}
				} else {
					available := map[string]assurance.Evidence{}
					for _, id := range append(append([]string{}, c.CorrectiveRuns...), c.PreventiveRuns...) {
						run, err := store.Get(ctx, id)
						if err != nil {
							return nil, err
						}
						for _, e := range run.Evidence {
							available[e.Digest] = e
						}
					}
					for _, digest := range in.EvidenceDigests {
						e, ok := available[digest]
						if !ok {
							return nil, fmt.Errorf("unknown CAPA effectiveness evidence")
						}
						if err = r.Quality.Content.Verify(e); err != nil {
							return nil, fmt.Errorf("CAPA effectiveness evidence integrity: %w", err)
						}
					}
				}
			}
			return store.AdvanceCAPA(ctx, parts[2], actor, in)
		}
	case "imports":
		if len(parts) != 4 || !assurance.HasRole(actor, []string{"quality-authority"}) {
			return nil, assurance.ErrForbidden
		}
		if parts[2] != "requirement" && parts[2] != "change" && parts[2] != "acknowledgment" {
			return nil, fmt.Errorf("unsupported import kind")
		}
		var body json.RawMessage
		if err := decodeQuality(req, &body); err != nil {
			return nil, err
		}
		if string(body) != r.Redactor.Redact(string(body)) || qualityJSONHasSecrets(r.Redactor, body) {
			return nil, fmt.Errorf("import requires secret sanitization")
		}
		return map[string]bool{"imported": true}, store.ImportAs(ctx, parts[2], parts[3], body, actor)
	}
	return nil, assurance.ErrNotFound
}

func qualityRequestDigest(req RunRequest, actorID string) string {
	req.AdmissionID = ""
	return assurance.Hash(struct {
		Request RunRequest
		ActorID string
	}{req, actorID})
}
func (r *Runtime) admitQuality(ctx context.Context, actor assurance.Actor, req RunRequest) (assurance.Run, error) {
	if req.AdmissionID != "" {
		return assurance.Run{}, fmt.Errorf("admission IDs are service-issued")
	}
	if req.RunID == "" {
		req.RunID = "run-" + uuid.NewString()
	}
	if !assurance.ValidID(req.RunID) {
		return assurance.Run{}, fmt.Errorf("invalid run ID")
	}
	submissionDigest := qualityRequestDigest(req, actor.ID)
	if old, err := r.Quality.Store.Get(ctx, req.RunID); err == nil {
		if old.Requester.ID != actor.ID || old.SubmissionDigest != submissionDigest {
			return old, assurance.ErrConflict
		}
		return old, nil
	} else if !errors.Is(err, assurance.ErrNotFound) {
		return old, err
	}
	if req.ChangeID != "" && !assurance.ValidID(req.ChangeID) || req.ParentRunID != "" && !assurance.ValidID(req.ParentRunID) {
		return assurance.Run{}, fmt.Errorf("invalid change or parent run ID")
	}
	if req.AgentHarness == "" {
		names := r.Harnesses.Names()
		if len(names) > 0 {
			req.AgentHarness = names[0]
		}
	}
	req.AgentHarness = strings.ToLower(strings.TrimSpace(req.AgentHarness))
	if req.VerificationProfile == "" {
		var names []string
		for name := range r.Profiles.Profiles {
			names = append(names, name)
		}
		sort.Strings(names)
		if len(names) > 0 {
			req.VerificationProfile = names[0]
		}
	}
	if req.SandboxTemplate == "" {
		req.SandboxTemplate = r.Config.Cube.TemplateID
	}
	approvedTemplate := req.SandboxTemplate == r.Config.Cube.TemplateID
	for _, workspace := range r.Config.Workspaces {
		if workspace.Template != "" && workspace.Template == req.SandboxTemplate {
			approvedTemplate = true
		}
	}
	if !approvedTemplate {
		return assurance.Run{}, fmt.Errorf("controlled run requires an operator-declared sandbox template")
	}
	if err := req.Validate(); err != nil {
		return assurance.Run{}, err
	}
	if req.RepositoryDir != "" && req.RepositoryDir != DefaultRepositoryDir {
		return assurance.Run{}, fmt.Errorf("controlled runs use the fixed repository directory")
	}
	if _, err := boundedTimeout(req.AgentTimeout, r.Config.Limits.AgentTimeout.Std(), 30*time.Minute); err != nil {
		return assurance.Run{}, err
	}
	if _, err := boundedTimeout(req.TotalTimeout, r.Config.Limits.TotalTimeout.Std(), 90*time.Minute); err != nil {
		return assurance.Run{}, err
	}
	if string(r.Redactor.Redact(req.Task)) != req.Task {
		return assurance.Run{}, fmt.Errorf("task contains protected secret material")
	}
	if _, err := r.Harnesses.Resolve(req.AgentHarness); err != nil {
		return assurance.Run{}, err
	}
	if _, err := r.Profiles.Resolve(req.VerificationProfile); err != nil {
		return assurance.Run{}, err
	}
	kind := repository.SourceRemote
	if req.LocalPath != "" {
		kind = repository.SourceLocal
	}
	if err := r.Repos.Validate(repository.Request{Kind: kind, URL: req.Repository, LocalPath: req.LocalPath, Revision: req.Revision}); err != nil {
		return assurance.Run{}, err
	}
	if _, err := r.Config.ResolveResources(req); err != nil {
		return assurance.Run{}, err
	}
	digest := qualityRequestDigest(req, actor.ID)
	if old, err := r.Quality.Store.Get(ctx, req.RunID); err == nil {
		if old.RequestDigest != digest || old.Requester.ID != actor.ID {
			return old, assurance.ErrConflict
		}
		return old, nil
	} else if !errors.Is(err, assurance.ErrNotFound) {
		return old, err
	}
	if _, err := os.Stat(filepath.Join(r.Artifacts.Root(), "runs", req.RunID, ArtifactRun)); err == nil {
		return assurance.Run{}, fmt.Errorf("%w: run ID has legacy evidence", assurance.ErrConflict)
	} else if !os.IsNotExist(err) {
		return assurance.Run{}, err
	}
	snapshot, err := r.Config.qualitySnapshot(req, r.Quality.Policies)
	if err != nil {
		return assurance.Run{}, err
	}
	if snapshot.Digest == "" {
		return assurance.Run{}, fmt.Errorf("repository has no quality policies; use the legacy submission path for this registered repository")
	}
	if err = r.freezeQualityFiles(&snapshot); err != nil {
		return assurance.Run{}, err
	}
	if req.ParentRunID != "" {
		parent, err := r.Quality.Store.Get(ctx, req.ParentRunID)
		if err != nil {
			return assurance.Run{}, fmt.Errorf("controlled parent run: %w", err)
		}
		if parent.Snapshot.RepositoryID != snapshot.RepositoryID {
			return assurance.Run{}, fmt.Errorf("parent run belongs to another repository")
		}
	}
	if req.ChangeID != "" {
		runs, err := r.Quality.Store.List(ctx)
		if err != nil {
			return assurance.Run{}, err
		}
		for _, old := range runs {
			var prior RunRequest
			if err = json.Unmarshal(old.Request, &prior); err != nil {
				return assurance.Run{}, err
			}
			id := prior.ChangeID
			if id == "" {
				id = old.ID
			}
			if id == req.ChangeID && old.Snapshot.RepositoryID != snapshot.RepositoryID {
				return assurance.Run{}, fmt.Errorf("change belongs to another repository")
			}
		}
	}
	snapshotBytes, _ := json.Marshal(snapshot)
	if qualityJSONHasSecrets(r.Redactor, snapshotBytes) {
		return assurance.Run{}, fmt.Errorf("policy snapshot contains protected secret material")
	}
	if len(snapshotBytes) > 1<<20 {
		return assurance.Run{}, fmt.Errorf("frozen execution snapshot exceeds 1 MiB")
	}
	body, _ := json.Marshal(req)
	if qualityJSONHasSecrets(r.Redactor, body) {
		return assurance.Run{}, fmt.Errorf("request contains protected secret material")
	}
	return r.Quality.Store.Reserve(ctx, assurance.Run{ID: req.RunID, AdmissionID: uuid.NewString(), RequestDigest: digest, SubmissionDigest: submissionDigest, Request: body, Requester: actor, Snapshot: snapshot, WorkflowID: WorkflowIDForRun(req.RunID)})
}

// QualityClient uses a Unix socket, with identity established by the server.
// No bearer secret or asserted actor travels in its requests.
type QualityClient struct{ HTTP *http.Client }

func NewQualityClient(socket string) *QualityClient {
	return &QualityClient{HTTP: &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}, Timeout: 35 * time.Second}}
}
func (c *QualityClient) Call(ctx context.Context, method, path string, input, output any) error {
	var body io.Reader
	if input != nil {
		b, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = strings.NewReader(string(b))
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://quality"+path, body)
	if err != nil {
		return err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if resp.StatusCode == http.StatusNotFound {
			return fmt.Errorf("%w: quality service: %s", assurance.ErrNotFound, strings.TrimSpace(string(b)))
		}
		return fmt.Errorf("quality service: %s", strings.TrimSpace(string(b)))
	}
	if output == nil {
		_, err = io.Copy(io.Discard, resp.Body)
		return err
	}
	return json.NewDecoder(io.LimitReader(resp.Body, assurance.MaxObjectBytes*2)).Decode(output)
}
func (c Config) QualitySocket() string { return c.qualitySocket() }
