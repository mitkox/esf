package factory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mitkox/esf/internal/assurance"
	"github.com/mitkox/esf/internal/repository"
	"github.com/mitkox/esf/internal/verification"
)

type QualityConfig struct {
	PolicyFiles  []string                     `toml:"policy_files"`
	Socket       string                       `toml:"socket"`
	SocketGID    *int                         `toml:"socket_gid"`
	Repositories map[string]QualityRepository `toml:"repositories"`
	Roles        map[string][]string          `toml:"roles"`
	CAPARoles    []string                     `toml:"capa_roles"`
}
type QualityRepository struct {
	Aliases    []string `toml:"aliases" json:"aliases"`
	LocalPaths []string `toml:"local_paths" json:"local_paths"`
	Scopes     []string `toml:"scopes" json:"scopes"`
}

func (q QualityConfig) Enabled() bool { return len(q.PolicyFiles) > 0 }
func (c Config) qualitySocket() string {
	if c.Quality.Socket != "" {
		return c.Quality.Socket
	}
	p, _ := filepath.Abs(filepath.Join(c.Storage.DataDir, "quality.sock"))
	return p
}

// resolveQualityRepository never trusts a caller's scope as an assurance floor.
// Exact operator aliases intentionally avoid guessing whether arbitrary mirrors
// or local copies represent the same repository.
func (c Config) resolveQualityRepository(req RunRequest) (string, []string, error) {
	if !c.Quality.Enabled() {
		return "", nil, nil
	}
	if (req.Repository != "" && req.LocalPath != "") || (req.Repository == "" && req.LocalPath == "") {
		return "", nil, fmt.Errorf("exactly one repository source is required")
	}
	expected := repository.SourceRemote
	if req.LocalPath != "" {
		expected = repository.SourceLocal
	}
	if req.RepositoryKind != "" && req.RepositoryKind != string(expected) {
		return "", nil, fmt.Errorf("repository kind conflicts with source")
	}
	local := ""
	if req.LocalPath != "" {
		var err error
		local, err = filepath.EvalSymlinks(req.LocalPath)
		if err != nil {
			return "", nil, err
		}
		local, err = filepath.Abs(local)
		if err != nil {
			return "", nil, err
		}
	}
	id := ""
	var reg QualityRepository
	for name, r := range c.Quality.Repositories {
		match := local == "" && assurance.Contains(r.Aliases, req.Repository)
		if local != "" {
			for _, p := range r.LocalPaths {
				canonical, err := filepath.EvalSymlinks(p)
				if err != nil {
					return "", nil, err
				}
				canonical, _ = filepath.Abs(canonical)
				if local == canonical {
					match = true
				}
			}
		}
		if match {
			if id != "" {
				return "", nil, fmt.Errorf("ambiguous repository identity")
			}
			id = name
			reg = r
		}
	}
	if id == "" {
		return "", nil, fmt.Errorf("repository has no registered quality identity")
	}
	scope := req.Scope
	if scope == "" {
		scope = DefaultScope
	}
	if !assurance.Contains(reg.Scopes, scope) {
		return "", nil, fmt.Errorf("repository %s is not bound to scope %s", id, scope)
	}
	set := map[string]bool{}
	for _, s := range reg.Scopes {
		for _, p := range c.Scopes[s].QualityPolicies {
			set[p] = true
		}
	}
	var names []string
	for p := range set {
		names = append(names, p)
	}
	sort.Strings(names)
	return id, names, nil
}

func (c Config) qualityPolicies() (map[string]assurance.Policy, error) {
	policies := map[string]assurance.Policy{}
	for _, file := range c.Quality.PolicyFiles {
		b, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		p, err := assurance.ParsePolicy(b)
		if err != nil {
			return nil, err
		}
		if _, ok := policies[p.Metadata.Name]; ok {
			return nil, fmt.Errorf("duplicate active policy name %s", p.Metadata.Name)
		}
		policies[p.Metadata.Name] = p
	}
	return policies, nil
}
func (c Config) validateQuality() error {
	if !c.Quality.Enabled() {
		for name, s := range c.Scopes {
			if len(s.QualityPolicies) > 0 {
				return fmt.Errorf("scope %s requires quality.policy_files", name)
			}
		}
		return nil
	}
	if len(c.Quality.Repositories) == 0 || len(c.Quality.Roles) == 0 {
		return fmt.Errorf("quality requires repository identities and UID role bindings")
	}
	if c.Quality.Socket != "" && !filepath.IsAbs(c.Quality.Socket) {
		return fmt.Errorf("quality.socket must be absolute")
	}
	for uid, roles := range c.Quality.Roles {
		if _, err := strconv.ParseUint(uid, 10, 32); err != nil {
			return fmt.Errorf("quality role binding key %q must be a UID", uid)
		}
		for _, role := range roles {
			if !assurance.ValidID(role) {
				return fmt.Errorf("invalid quality role")
			}
		}
	}
	policies, err := c.qualityPolicies()
	if err != nil {
		return err
	}
	aliases := map[string]string{}
	for id, r := range c.Quality.Repositories {
		if !assurance.ValidID(id) || len(r.Scopes) == 0 || len(r.Aliases)+len(r.LocalPaths) == 0 {
			return fmt.Errorf("invalid quality repository %s", id)
		}
		for _, a := range r.Aliases {
			if strings.TrimSpace(a) != a || a == "" {
				return fmt.Errorf("invalid repository alias")
			}
			if _, ok := aliases[a]; ok {
				return fmt.Errorf("duplicate repository alias")
			}
			aliases[a] = id
		}
		for _, p := range r.LocalPaths {
			if !filepath.IsAbs(p) {
				return fmt.Errorf("registered local paths must be absolute")
			}
			canonical, err := filepath.EvalSymlinks(p)
			if err != nil {
				return err
			}
			if _, ok := aliases["local:"+canonical]; ok {
				return fmt.Errorf("duplicate local repository alias")
			}
			aliases["local:"+canonical] = id
		}
		for _, scope := range r.Scopes {
			if _, ok := c.Scopes[scope]; !ok {
				return fmt.Errorf("unknown registered scope %s", scope)
			}
		}
	}
	for name, s := range c.Scopes {
		for _, p := range s.QualityPolicies {
			if _, ok := policies[p]; !ok {
				return fmt.Errorf("scope %s references unknown policy %s", name, p)
			}
		}
	}
	gateDefs := map[string]string{}
	qualificationDefs := map[string]string{}
	riskRanks := map[string]int{}
	for _, p := range policies {
		for _, q := range p.Spec.Qualifications {
			if previous, ok := qualificationDefs[q.ID]; ok && previous != assurance.Hash(q) {
				return fmt.Errorf("conflicting qualification %s", q.ID)
			}
			qualificationDefs[q.ID] = assurance.Hash(q)
			if q.Harness != "" {
				if _, ok := c.Harnesses[q.Harness]; !ok {
					return fmt.Errorf("unknown qualified harness %s", q.Harness)
				}
			}
		}
		for level, rank := range p.Spec.Risk.Levels {
			if prior, ok := riskRanks[level]; ok && prior != rank {
				return fmt.Errorf("incompatible risk ranks")
			}
			riskRanks[level] = rank
		}
		for _, g := range p.Spec.Gates {
			if prior, ok := gateDefs[g.ID]; ok && prior != assurance.Hash(g) {
				return fmt.Errorf("conflicting quality gate %s", g.ID)
			}
			gateDefs[g.ID] = assurance.Hash(g)
			switch g.Type {
			case "verification":
				if _, ok := c.Verification[g.Profile]; !ok {
					return fmt.Errorf("unknown quality profile %s", g.Profile)
				}
				mandatory := false
				for _, step := range c.Verification[g.Profile].Steps {
					if step.IsMandatory() {
						mandatory = true
					}
				}
				if !g.Optional && !mandatory {
					return fmt.Errorf("required quality gate %s has no mandatory verification step", g.ID)
				}
			case "independent-review":
				if _, ok := c.Harnesses[g.Harness]; !ok {
					return fmt.Errorf("unknown reviewer harness %s", g.Harness)
				}
				qualified := false
				for _, q := range p.Spec.Qualifications {
					if q.Phase == "review" && (q.Harness == g.Harness || q.Harness == "") {
						qualified = true
					}
				}
				if !qualified {
					return fmt.Errorf("review gate %s requires an explicit reviewer qualification", g.ID)
				}
			}
			if g.FilesystemReadOnly {
				return fmt.Errorf("enforced read-only guest filesystem is not a qualified Cube capability")
			}
		}
	}
	return nil
}

// QualityDependencies contains effective non-secret definitions, not live names.
// Credentials are resolved by reference at execution; their bytes never enter
// the workflow or quality database.
type QualityDependencies struct {
	Request        RunRequest                      `json:"request"`
	Resources      ResolvedResources               `json:"resources"`
	Harnesses      map[string]HarnessConfig        `json:"harnesses"`
	Profiles       map[string]verification.Profile `json:"profiles"`
	Sandbox        SandboxConfig                   `json:"sandbox"`
	Limits         LimitsConfig                    `json:"limits"`
	FactoryVersion string                          `json:"factory_version"`
	HostFiles      map[string]string               `json:"host_files,omitempty"`
	ProviderFacts  map[string]json.RawMessage      `json:"provider_facts,omitempty"`
}

func (c Config) QualityRequired(req RunRequest) (bool, error) {
	_, policies, err := c.resolveQualityRepository(req)
	return len(policies) > 0, err
}

func (r *Runtime) freezeQualityFiles(snapshot *assurance.Snapshot) error {
	var d QualityDependencies
	if err := json.Unmarshal(snapshot.Dependencies, &d); err != nil {
		return err
	}
	d.HostFiles = map[string]string{}
	d.ProviderFacts = map[string]json.RawMessage{}
	for _, policy := range snapshot.Policies {
		for _, g := range policy.Spec.Gates {
			if g.Type == "provider-snapshot" {
				body, err := r.Quality.Store.Object(context.Background(), g.ProviderKind, g.Reference)
				if err != nil {
					if g.Optional && errors.Is(err, assurance.ErrNotFound) {
						continue
					}
					return fmt.Errorf("required provider snapshot %s/%s: %w", g.ProviderKind, g.Reference, err)
				}
				d.ProviderFacts[g.ID] = body
			}
		}
	}
	for name, h := range d.Harnesses {
		resolved, err := r.Harnesses.Resolve(name)
		if err != nil {
			return err
		}
		h.Model = resolved.Model()
		if len(h.ProviderFiles) > 0 {
			return fmt.Errorf("controlled harness %s cannot load mutable provider files; use structured model configuration and credential references", name)
		}
		if h.Type == "opencode" && h.Binary == "" {
			return fmt.Errorf("controlled opencode harness %s requires an explicit binary", name)
		}
		for field, file := range map[string]string{"binary": h.Binary, "catalog": h.CatalogCache} {
			if file == "" {
				continue
			}
			info, err := os.Stat(file)
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() || info.Size() > assurance.MaxObjectBytes {
				return fmt.Errorf("unsupported harness file %s", name)
			}
			data, err := os.ReadFile(file)
			if err != nil {
				return err
			}
			digest, err := r.Quality.Content.Put(data)
			if err != nil {
				return err
			}
			if field == "binary" {
				if h.BinarySHA256 != "" && strings.ToLower(h.BinarySHA256) != strings.TrimPrefix(digest, "sha256:") {
					return fmt.Errorf("harness %s binary pin mismatch", name)
				}
				h.BinarySHA256 = strings.TrimPrefix(digest, "sha256:")
			}
			d.HostFiles[name+"/"+field] = digest
		}
		d.Harnesses[name] = h
	}
	body, err := json.Marshal(d)
	if err != nil {
		return err
	}
	if r.Redactor.Redact(string(body)) != string(body) || qualityJSONHasSecrets(r.Redactor, body) {
		return fmt.Errorf("execution definitions contain protected secret material")
	}
	snapshot.Dependencies = body
	snapshot.Digest = ""
	snapshot.Digest = assurance.Hash(*snapshot)
	return nil
}

func qualityJSONHasSecrets(redactor *Redactor, body []byte) bool {
	var value any
	if json.Unmarshal(body, &value) != nil {
		return false
	}
	var check func(any) bool
	check = func(v any) bool {
		switch x := v.(type) {
		case string:
			return redactor.Redact(x) != x
		case []any:
			for _, e := range x {
				if check(e) {
					return true
				}
			}
		case map[string]any:
			for k, e := range x {
				if redactor.Redact(k) != k || check(e) {
					return true
				}
			}
		}
		return false
	}
	return check(value)
}

func (c Config) qualitySnapshot(req RunRequest, policies map[string]assurance.Policy) (assurance.Snapshot, error) {
	id, names, err := c.resolveQualityRepository(req)
	if err != nil {
		return assurance.Snapshot{}, err
	}
	if len(names) == 0 {
		return assurance.Snapshot{}, nil
	}
	resources, err := c.ResolveResources(req)
	if err != nil {
		return assurance.Snapshot{}, err
	}
	deps := QualityDependencies{Request: req, Resources: resources, Harnesses: c.Harnesses, Profiles: c.VerificationProfiles().Profiles, Sandbox: c.Sandbox, Limits: c.Limits, FactoryVersion: c.effectiveVersion()}
	b, err := json.Marshal(deps)
	if err != nil {
		return assurance.Snapshot{}, err
	}
	s := assurance.Snapshot{Evaluator: assurance.EvaluatorVersion, RepositoryID: id, Dependencies: b}
	for _, name := range names {
		p, ok := policies[name]
		if !ok {
			return s, fmt.Errorf("policy %s unavailable", name)
		}
		if p.Metadata.EffectiveFrom.After(time.Now()) {
			return s, fmt.Errorf("policy %s is not effective", name)
		}
		s.Policies = append(s.Policies, p)
	}
	s.Digest = assurance.Hash(s)
	return s, nil
}
