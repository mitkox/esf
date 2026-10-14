package turnexec

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mitkox/esf/internal/sandbox"
)

const MaxManifestBytes = 16 << 20
const maxEventBytes = 2 << 20

var idPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,128}$`)
var specPattern = regexp.MustCompile(`^(sha256:)?[a-f0-9]{64}$`)
var profilePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$`)
var shaPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
var envPattern = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

type Service struct {
	harnesses   map[string][]byte
	lock        *os.File
	mu          sync.Mutex
	root, token string
	profiles    map[string]Profile
	provider    sandbox.Provider
	records     map[string]*record
	lease       time.Duration
	closed      bool
	wg          sync.WaitGroup
}

// Open recovers interrupted turns by destroying their sandbox and recording a
// failed outcome. It never repeats potentially side-effecting execution.
// A deployment must run ONE service process per state directory.
func Open(ctx context.Context, root, token string, profiles map[string]Profile, provider sandbox.Provider) (*Service, error) {
	var err error
	root, err = filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if len(token) < 32 {
		return nil, errors.New("service token must contain at least 32 characters")
	}
	if len(profiles) == 0 || provider == nil {
		return nil, errors.New("provider and profiles required")
	}
	harnesses := map[string][]byte{}
	for name, p := range profiles {
		if p.HarnessArtifact != nil {
			if len(p.Command) == 0 || p.Command[0] != "/tmp/esf-harness" || !shaPattern.MatchString(p.HarnessArtifact.SHA256) {
				return nil, errors.New("harness artifact requires pinned SHA and fixed guest path")
			}
			info, err := os.Stat(p.HarnessArtifact.File)
			if err != nil {
				return nil, err
			}
			if !info.Mode().IsRegular() || info.Size() > 256<<20 {
				return nil, errors.New("invalid harness artifact")
			}
			body, err := os.ReadFile(p.HarnessArtifact.File)
			if err != nil {
				return nil, err
			}
			if digest(body) != p.HarnessArtifact.SHA256 {
				return nil, errors.New("harness artifact digest mismatch")
			}
			harnesses[name] = body
		}
		if p.ModelBaseURL != "" {
			u, e := url.Parse(p.ModelBaseURL)
			if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
				return nil, errors.New("model base URL must be credential-free HTTPS")
			}
		}
		if p.MaxCostUSD < 0 || p.MaxCostUSD > 100 {
			return nil, errors.New("profile cost cap must be between zero and 100 USD")
		}
		if err := validatePolicy(p); err != nil {
			return nil, err
		}
		if !profilePattern.MatchString(name) || p.Template == "" || len(p.Command) == 0 || p.TimeoutSeconds < 1 || p.TimeoutSeconds > 86400 || p.Harness == "" {
			return nil, fmt.Errorf("invalid profile %q", name)
		}
		if p.SelectionOnly && (p.Harness != "claude-code" || len(p.Command) != 1 || p.InteractivePermissions || len(p.MCPServers) > 0) {
			return nil, errors.New("selection-only profile requires a Claude executable without tools or MCP servers")
		}
		for name, server := range p.MCPServers {
			u, err := url.Parse(server.URL)
			if name == "sf_gate" || !profilePattern.MatchString(name) || err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" || !envPattern.MatchString(server.BearerEnvRef) {
				return nil, errors.New("invalid MCP server configuration; HTTPS and bearer environment reference required")
			}
			if value, ok := os.LookupEnv(server.BearerEnvRef); !ok || len(value) < 24 {
				return nil, errors.New("MCP bearer token missing or too short")
			}
		}
		if p.InteractivePermissions {
			for _, arg := range p.Command {
				if strings.Contains(arg, "skip-permissions") || arg == "bypassPermissions" || arg == "--permission-prompt-tool" {
					return nil, errors.New("interactive profile cannot bypass or override permission bridge")
				}
			}
		}
		for key, ref := range p.EnvRefs {
			if key == "HOME" || !envPattern.MatchString(key) || !envPattern.MatchString(ref) {
				return nil, errors.New("invalid environment reference")
			}
			if _, ok := os.LookupEnv(ref); !ok {
				return nil, fmt.Errorf("missing environment reference %s", ref)
			}
		}
	}
	for _, dir := range []string{"executions", "blobs"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0700); err != nil {
			return nil, err
		}
	}
	lock, err := os.OpenFile(filepath.Join(root, "service.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		return nil, errors.New("state directory already owned by another service")
	}
	opened := false
	defer func() {
		if !opened {
			lock.Close()
		}
	}()
	s := &Service{harnesses: harnesses, lock: lock, root: root, token: token, profiles: profiles, provider: provider, records: map[string]*record{}, lease: 60 * time.Second}
	files, err := os.ReadDir(filepath.Join(root, "executions"))
	if err != nil {
		return nil, err
	}
	for _, file := range files {
		if !strings.HasSuffix(file.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(root, "executions", file.Name()))
		if err != nil {
			return nil, err
		}
		var r record
		if err := json.Unmarshal(b, &r); err != nil {
			return nil, err
		}
		if !idPattern.MatchString(r.Status.ExecutionID) || file.Name() != r.Status.ExecutionID+".json" {
			return nil, errors.New("invalid persisted execution identity")
		}
		s.records[r.Status.ExecutionID] = &r
	}
	// Metadata also closes the create-to-persist crash window.
	if len(s.records) > 0 {
		live, err := provider.List(ctx)
		if err != nil {
			return nil, fmt.Errorf("reconcile execution sandboxes: %w", err)
		}
		for _, info := range live {
			if info.Metadata["esf.turn.service"] != digest([]byte(root)) {
				continue
			}
			if r := s.records[info.Metadata["esf.turn.id"]]; r != nil && (!terminal(r.Status.State) || cleanupUnverified(r)) {
				if err := provider.Destroy(ctx, info.ID); err != nil {
					return nil, err
				}
			}
		}
	}
	for _, r := range s.records {
		if !terminal(r.Status.State) || cleanupUnverified(r) {
			if r.SandboxID != "" {
				if err := provider.Destroy(ctx, r.SandboxID); err != nil {
					return nil, err
				}
			}
			r.Status.State = "failed"
			r.Status.Error = "execution interrupted by service restart; never replayed"
			clean := s.cleanupIdentity(r.Status.ExecutionID)
			if r.SandboxID != "" {
				live, err := provider.List(ctx)
				if err != nil {
					return nil, err
				}
				for _, v := range live {
					if v.ID == r.SandboxID {
						clean = false
					}
				}
			}
			r.Result = s.failure(r, "failed", r.Status.Error, clean)
			s.event(r, "state", map[string]any{"status": "failed"})
			if err := s.persist(r); err != nil {
				return nil, err
			}
		}
	}
	opened = true
	return s, nil
}
func digest(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }
func atomicWrite(file string, body []byte) error {
	f, err := os.CreateTemp(filepath.Dir(file), ".write-")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(body)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(name, file); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(file))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
func (s *Service) persist(r *record) error {
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(s.root, "executions", r.Status.ExecutionID+".json"), b)
}
func (s *Service) event(r *record, kind string, data any) {
	b, _ := json.Marshal(data)
	if (kind == "stdout" || kind == "stderr") && r.EventBytes+len(b) > maxEventBytes {
		if len(r.Events) == 0 || r.Events[len(r.Events)-1].Type != "output_limit" {
			r.Events = append(r.Events, Event{len(r.Events) + 1, "output_limit", map[string]any{"limit_bytes": maxEventBytes}})
		}
		return
	}
	r.Events = append(r.Events, Event{len(r.Events) + 1, kind, data})
	r.EventBytes += len(b)
}
func validateManifest(m Manifest) error {
	if len(m.Files) > 4096 || (m.HarnessSessionID != "" && !idPattern.MatchString(m.HarnessSessionID)) {
		return errors.New("manifest exceeds limits")
	}
	seen := map[string]bool{}
	total := 0
	for _, f := range m.Files {
		if f.Path == "" || f.Path == "." || strings.ContainsAny(f.Path, "\\\x00") || strings.HasPrefix(f.Path, "/") || path.Clean(f.Path) != f.Path || f.Path == ".." || strings.HasPrefix(f.Path, "../") || len(f.Path) > 1024 || seen[f.Path] {
			return errors.New("invalid manifest file path")
		}
		seen[f.Path] = true
		total += len(f.Content)
		if total > MaxManifestBytes {
			return errors.New("manifest exceeds byte limit")
		}
	}
	for p := range seen {
		for d := path.Dir(p); d != "."; d = path.Dir(d) {
			if seen[d] {
				return errors.New("file overlaps directory")
			}
		}
	}
	return nil
}
func (s *Service) putManifest(m Manifest) (string, error) {
	if err := validateManifest(m); err != nil {
		return "", err
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	if len(b) > MaxManifestBytes {
		return "", errors.New("encoded manifest exceeds byte limit")
	}
	sha := digest(b)
	return sha, atomicWrite(filepath.Join(s.root, "blobs", sha+".json"), b)
}
func (s *Service) manifest(sha string) (Manifest, error) {
	var m Manifest
	if !shaPattern.MatchString(sha) {
		return m, errors.New("invalid manifest digest")
	}
	b, err := os.ReadFile(filepath.Join(s.root, "blobs", sha+".json"))
	if err != nil {
		return m, err
	}
	if len(b) > MaxManifestBytes || digest(b) != sha {
		return m, errors.New("manifest integrity failure")
	}
	if err = json.Unmarshal(b, &m); err != nil {
		return m, err
	}
	return m, validateManifest(m)
}
func decode(w http.ResponseWriter, r *http.Request, v any, limit int64) error {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return errors.New("expected one JSON value")
	}
	return nil
}
func respond(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	expected := "Bearer " + s.token
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte(expected)) != 1 {
		respond(w, 401, map[string]string{"error": "unauthorized"})
		return
	}
	route := strings.TrimPrefix(r.URL.Path, "/v1/")
	if route == r.URL.Path {
		http.NotFound(w, r)
		return
	}
	if strings.HasPrefix(route, "profiles/") && r.Method == "GET" {
		name := strings.TrimPrefix(route, "profiles/")
		p, ok := s.profiles[name]
		if !ok {
			respond(w, 404, map[string]string{"error": "profile policy unavailable"})
			return
		}
		keys := []string{}
		for key := range p.EnvRefs {
			keys = append(keys, key)
		}
		respond(w, 200, map[string]any{"profile_sha": digestJSON(p), "policy_sha": policyDigest(p.TurnPolicy), "policy": p.TurnPolicy, "secret_env": keys, "interactive_permissions": p.InteractivePermissions, "model_base_url": p.ModelBaseURL})
		return
	}
	if route == "blobs" && r.Method == "POST" {
		var m Manifest
		if err := decode(w, r, &m, MaxManifestBytes); err != nil {
			respond(w, 400, map[string]string{"error": "invalid manifest"})
			return
		}
		sha, err := s.putManifest(m)
		if err != nil {
			respond(w, 400, map[string]string{"error": "invalid manifest"})
			return
		}
		respond(w, 201, map[string]string{"sha256": sha})
		return
	}
	if strings.HasPrefix(route, "blobs/") && r.Method == "GET" {
		m, err := s.manifest(strings.TrimPrefix(route, "blobs/"))
		if err != nil {
			respond(w, 404, map[string]string{"error": "manifest unavailable"})
			return
		}
		respond(w, 200, m)
		return
	}
	if route == "executions" && r.Method == "POST" {
		s.submit(w, r)
		return
	}
	parts := strings.Split(route, "/")
	if len(parts) < 2 || len(parts) > 3 || parts[0] != "executions" || !idPattern.MatchString(parts[1]) {
		http.NotFound(w, r)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rec := s.records[parts[1]]
	if rec == nil {
		http.NotFound(w, r)
		return
	}
	if len(parts) == 2 && r.Method == "GET" {
		respond(w, 200, rec.Status)
		return
	}
	if len(parts) != 3 {
		http.NotFound(w, r)
		return
	}
	switch parts[2] {
	case "events":
		if r.Method != "GET" {
			break
		}
		after := 0
		if q := r.URL.Query().Get("after"); q != "" {
			var err error
			after, err = strconv.Atoi(q)
			if err != nil || after < 0 || after > len(rec.Events) {
				respond(w, 400, map[string]string{"error": "invalid cursor"})
				return
			}
		}
		respond(w, 200, rec.Events[after:])
		return
	case "result":
		if r.Method != "GET" {
			break
		}
		if rec.Result == nil {
			respond(w, 409, map[string]string{"error": "execution active"})
			return
		}
		respond(w, 200, rec.Result)
		return
	case "permissions":
		if r.Method != "POST" {
			break
		}
		s.answerPermission(w, r, rec)
		return
	case "cancel", "renew":
		if r.Method != "POST" {
			break
		}
		var owner struct {
			Token string `json:"owner_token"`
		}
		if err := decode(w, r, &owner, 4096); err != nil {
			respond(w, 400, map[string]string{"error": "invalid ownership"})
			return
		}
		hash := digest([]byte(owner.Token))
		if subtle.ConstantTimeCompare([]byte(hash), []byte(rec.OwnerHash)) != 1 {
			respond(w, 403, map[string]string{"error": "ownership mismatch"})
			return
		}
		if !terminal(rec.Status.State) {
			if parts[2] == "cancel" {
				rec.Status.Error = "cancelled by owner"
				if rec.cancel != nil {
					rec.cancel()
				}
			} else {
				if !time.Now().Before(rec.Status.LeaseExpiresAt) {
					respond(w, 409, map[string]string{"error": "lease expired"})
					return
				}
				rec.Status.LeaseExpiresAt = time.Now().UTC().Add(s.lease)
			}
			if err := s.persist(rec); err != nil {
				if rec.cancel != nil {
					rec.cancel()
				}
				respond(w, 500, map[string]string{"error": "state persistence failed"})
				return
			}
		}
		respond(w, 200, rec.Status)
		return
	}
	w.Header().Set("Allow", "GET, POST")
	respond(w, 405, map[string]string{"error": "method not allowed"})
}
func (s *Service) submit(w http.ResponseWriter, r *http.Request) {
	var req Request
	if err := decode(w, r, &req, 1<<20); err != nil {
		respond(w, 400, map[string]string{"error": "invalid execution request"})
		return
	}
	if !idPattern.MatchString(req.ExecutionID) || !specPattern.MatchString(req.SpecSHA) || len(req.OwnerToken) < 32 || len(req.OwnerToken) > 256 || strings.TrimSpace(req.Prompt) == "" || len(req.Prompt) > 512<<10 || (req.SessionID != "" && !idPattern.MatchString(req.SessionID)) {
		respond(w, 400, map[string]string{"error": "invalid execution request"})
		return
	}
	p, ok := s.profiles[req.Profile]
	if !ok {
		respond(w, 400, map[string]string{"error": "unknown execution profile"})
		return
	}
	if (req.Mode != "" && req.Mode != "selection") || ((req.Mode == "selection") != p.SelectionOnly) || (p.SelectionOnly && (req.Manifest != "" || req.Checkpoint != "")) {
		respond(w, 400, map[string]string{"error": "execution mode does not match profile"})
		return
	}
	if req.ProfileSHA != "" && req.ProfileSHA != digestJSON(p) {
		respond(w, 409, map[string]string{"error": "execution profile changed"})
		return
	}
	if (req.MaxCostUSD != nil && (*req.MaxCostUSD <= 0 || *req.MaxCostUSD > 100 || p.Harness != "claude-code")) || !validMetadata(req.Metadata) {
		respond(w, 400, map[string]string{"error": "invalid execution constraints"})
		return
	}
	if (p.TurnPolicy == nil && (req.PolicySHA != "" || len(req.Metadata) > 0)) || (p.TurnPolicy != nil && req.PolicySHA != policyDigest(p.TurnPolicy)) {
		respond(w, 400, map[string]string{"error": "execution policy does not match profile"})
		return
	}
	for _, ref := range []string{req.Manifest, req.Checkpoint} {
		if ref != "" {
			if _, err := s.manifest(ref); err != nil {
				respond(w, 400, map[string]string{"error": "manifest unavailable"})
				return
			}
		}
	}
	ownerHash := digest([]byte(req.OwnerToken))
	req.OwnerToken = ""
	fingerprint := digestJSON(req)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		respond(w, 503, map[string]string{"error": "service stopping"})
		return
	}
	if old := s.records[req.ExecutionID]; old != nil {
		if old.Fingerprint != fingerprint || old.OwnerHash != ownerHash {
			respond(w, 409, map[string]string{"error": "execution identity conflict"})
			return
		}
		respond(w, 200, old.Status)
		return
	}
	active := 0
	for _, rec := range s.records {
		if !terminal(rec.Status.State) {
			active++
		}
	}
	if active >= 16 {
		respond(w, 429, map[string]string{"error": "execution capacity reached"})
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(p.TimeoutSeconds)*time.Second)
	rec := &record{Status: Status{ExecutionID: req.ExecutionID, State: "queued", LeaseExpiresAt: time.Now().UTC().Add(s.lease)}, Request: req, OwnerHash: ownerHash, Fingerprint: fingerprint, ProfileSHA: digestJSON(p), StartedAt: time.Now().UTC(), Events: []Event{}, cancel: cancel}
	s.event(rec, "state", map[string]any{"status": "queued", "profile_sha": rec.ProfileSHA})
	if err := s.persist(rec); err != nil {
		cancel()
		respond(w, 500, map[string]string{"error": "state persistence failed"})
		return
	}
	s.records[req.ExecutionID] = rec
	s.wg.Add(1)
	go s.run(ctx, rec, p)
	respond(w, 202, rec.Status)
}
func (s *Service) Close() {
	s.mu.Lock()
	s.closed = true
	for _, r := range s.records {
		if r.cancel != nil {
			r.cancel()
		}
	}
	s.mu.Unlock()
	s.wg.Wait()
	if s.lock != nil {
		_ = s.lock.Close()
	}
}

func cleanupUnverified(r *record) bool {
	if r.Result == nil {
		return true
	}
	body, ok := r.Result.Result["sandbox"].(map[string]any)
	if !ok {
		return true
	}
	cleanup, ok := body["cleanup"].(map[string]any)
	if !ok {
		return true
	}
	return cleanup["verified"] != true
}
