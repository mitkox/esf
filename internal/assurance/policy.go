package assurance

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

type Policy struct {
	APIVersion string         `yaml:"apiVersion" json:"apiVersion"`
	Kind       string         `yaml:"kind" json:"kind"`
	Metadata   PolicyMetadata `yaml:"metadata" json:"metadata"`
	Spec       PolicySpec     `yaml:"spec" json:"spec"`
}
type PolicyMetadata struct {
	Name          string    `yaml:"name" json:"name"`
	Version       string    `yaml:"version" json:"version"`
	Description   string    `yaml:"description,omitempty" json:"description,omitempty"`
	EffectiveFrom time.Time `yaml:"effective_from,omitempty" json:"effective_from,omitempty"`
	Supersedes    string    `yaml:"supersedes,omitempty" json:"supersedes,omitempty"`
}
type PolicySpec struct {
	Paths          []string                `yaml:"paths,omitempty" json:"paths,omitempty"`
	Risk           RiskRules               `yaml:"risk" json:"risk"`
	Gates          []Control               `yaml:"gates" json:"gates"`
	Evidence       []string                `yaml:"evidence" json:"evidence"`
	Approvals      map[string]ApprovalRule `yaml:"approvals" json:"approvals"`
	Qualifications []Qualification         `yaml:"qualifications,omitempty" json:"qualifications,omitempty"`
	Exceptions     []ExceptionRule         `yaml:"exceptions,omitempty" json:"exceptions,omitempty"`
}
type RiskRules struct {
	Levels  map[string]int `yaml:"levels,omitempty" json:"levels"`
	Default string         `yaml:"default" json:"default"`
	Floor   string         `yaml:"floor,omitempty" json:"floor,omitempty"`
	Rules   []RiskRule     `yaml:"rules,omitempty" json:"rules,omitempty"`
}
type RiskRule struct {
	Level    string   `yaml:"level" json:"level"`
	Paths    []string `yaml:"paths" json:"paths"`
	AllPaths bool     `yaml:"all_paths,omitempty" json:"all_paths,omitempty"`
}
type Control struct {
	ID                 string            `yaml:"id" json:"id"`
	Type               string            `yaml:"type" json:"type"` // verification or independent-review
	Profile            string            `yaml:"profile,omitempty" json:"profile,omitempty"`
	Harness            string            `yaml:"harness,omitempty" json:"harness,omitempty"`
	Risks              []string          `yaml:"risks,omitempty" json:"risks,omitempty"`
	Optional           bool              `yaml:"optional,omitempty" json:"optional,omitempty"`
	Outputs            map[string]string `yaml:"outputs,omitempty" json:"outputs,omitempty"` // evidence kind -> relative file
	DifferentModel     bool              `yaml:"different_model,omitempty" json:"different_model,omitempty"`
	FilesystemReadOnly bool              `yaml:"filesystem_read_only,omitempty" json:"filesystem_read_only,omitempty"`
	ReportFile         string            `yaml:"report_file,omitempty" json:"report_file,omitempty"`
	ProviderKind       string            `yaml:"provider_kind,omitempty" json:"provider_kind,omitempty"`
	Reference          string            `yaml:"reference,omitempty" json:"reference,omitempty"`
	ProviderTimeout    string            `yaml:"provider_timeout,omitempty" json:"provider_timeout,omitempty"`
}
type ApprovalRule struct {
	Mode        string   `yaml:"mode" json:"mode"`
	Roles       []string `yaml:"roles,omitempty" json:"roles,omitempty"`
	Count       int      `yaml:"count,omitempty" json:"count,omitempty"`
	Independent bool     `yaml:"independent,omitempty" json:"independent,omitempty"`
	Timeout     string   `yaml:"timeout,omitempty" json:"timeout,omitempty"`
}
type Qualification struct {
	ID           string   `yaml:"id" json:"id"`
	Phase        string   `yaml:"phase" json:"phase"`
	Harness      string   `yaml:"harness,omitempty" json:"harness,omitempty"`
	Model        string   `yaml:"model,omitempty" json:"model,omitempty"`
	BinarySHA256 string   `yaml:"binary_sha256,omitempty" json:"binary_sha256,omitempty"`
	Executable   string   `yaml:"executable,omitempty" json:"executable,omitempty"`
	Template     string   `yaml:"template,omitempty" json:"template,omitempty"`
	Risks        []string `yaml:"risks,omitempty" json:"risks,omitempty"`
}
type ExceptionRule struct {
	Controls []string `yaml:"controls" json:"controls"`
	Roles    []string `yaml:"roles" json:"roles"`
}

var identifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

func ValidID(s string) bool { return identifier.MatchString(s) }

func ParsePolicy(b []byte) (Policy, error) {
	if len(b) > 1<<20 {
		return Policy{}, fmt.Errorf("policy exceeds 1 MiB")
	}
	var p Policy
	d := yaml.NewDecoder(bytes.NewReader(b))
	d.KnownFields(true)
	if err := d.Decode(&p); err != nil {
		return p, fmt.Errorf("policy YAML: %w", err)
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return p, fmt.Errorf("exactly one policy document is required")
	}
	if p.Spec.Risk.Levels == nil {
		p.Spec.Risk.Levels = map[string]int{"R0": 0, "R1": 1, "R2": 2, "R3": 3, "R4": 4}
	}
	return p, p.Validate()
}

func (p Policy) Validate() error {
	bad := func(msg string) error { return fmt.Errorf("policy %q: %s", p.Metadata.Name, msg) }
	if p.APIVersion != "esf.io/v1" || p.Kind != "QualityPolicy" {
		return bad("expected esf.io/v1 QualityPolicy")
	}
	if !ValidID(p.Metadata.Name) || !ValidID(p.Metadata.Version) {
		return bad("name and version must be identifiers")
	}
	r := p.Spec.Risk
	if len(r.Levels) == 0 {
		return bad("risk levels are required")
	}
	seenRanks := map[int]bool{}
	for k, v := range r.Levels {
		if !ValidID(k) || v < 0 || seenRanks[v] {
			return bad("risk levels need unique nonnegative ranks")
		}
		seenRanks[v] = true
	}
	if _, ok := r.Levels[r.Default]; !ok {
		return bad("unknown default risk")
	}
	if r.Floor != "" {
		if _, ok := r.Levels[r.Floor]; !ok {
			return bad("unknown risk floor")
		}
	}
	patterns := append([]string{}, p.Spec.Paths...)
	for _, rule := range r.Rules {
		if _, ok := r.Levels[rule.Level]; !ok || len(rule.Paths) == 0 {
			return bad("risk rules need known levels and paths")
		}
		patterns = append(patterns, rule.Paths...)
	}
	for _, pat := range patterns {
		if _, err := glob(pat); err != nil {
			return bad(err.Error())
		}
	}
	ids := map[string]bool{}
	checkRisks := func(levels []string) bool {
		for _, v := range levels {
			if _, ok := r.Levels[v]; !ok {
				return false
			}
		}
		return true
	}
	for _, g := range p.Spec.Gates {
		if !ValidID(g.ID) || ids[g.ID] {
			return bad("duplicate or invalid gate id")
		}
		ids[g.ID] = true
		if !checkRisks(g.Risks) {
			return bad("gate references unknown risk")
		}
		switch g.Type {
		case "provider-snapshot":
			if !Contains([]string{"requirement", "change"}, g.ProviderKind) || !ValidID(g.Reference) || g.Profile != "" || g.Harness != "" {
				return bad("provider snapshot requires a kind and immutable reference")
			}
		case "provider-ack":
			if !ValidID(g.Reference) || g.Profile != "" || g.Harness != "" {
				return bad("provider acknowledgement requires an immutable reference")
			}
			d, err := time.ParseDuration(g.ProviderTimeout)
			if err != nil || d <= 0 || d > 30*24*time.Hour {
				return bad("provider acknowledgment timeout must be positive and at most 30 days")
			}
		case "verification":
			if !ValidID(g.Profile) || g.Harness != "" {
				return bad("verification gate requires only a profile")
			}
		case "independent-review":
			if !ValidID(g.Harness) || g.Profile != "" {
				return bad("review gate requires only a harness")
			}
		default:
			return bad("unsupported gate type")
		}
		for kind, path := range g.Outputs {
			if Contains([]string{"baseline", "patch", "gate-result", "author-result", "review-report", "cleanup", "qualification"}, kind) {
				return bad("gate outputs cannot impersonate reserved evidence receipts")
			}
			if !ValidID(kind) || !safeRelative(path) {
				return bad("gate output requires an evidence kind and safe relative path")
			}
		}
		if g.ReportFile != "" && (g.Type != "independent-review" || !strings.HasPrefix(g.ReportFile, "/workspace/.factory/") || !safeRelative(strings.TrimPrefix(g.ReportFile, "/workspace/.factory/"))) {
			return bad("review report_file must be inside /workspace/.factory")
		}
	}
	for _, k := range p.Spec.Evidence {
		if !ValidID(k) {
			return bad("invalid evidence kind")
		}
	}
	for level, rule := range p.Spec.Approvals {
		if _, ok := r.Levels[level]; !ok {
			return bad("approval references unknown risk")
		}
		switch rule.Mode {
		case "automatic", "gates":
			if len(rule.Roles) > 0 || rule.Count > 0 {
				return bad("automatic approval cannot name human roles")
			}
		case "human":
			if len(rule.Roles) == 0 || rule.Count < 1 {
				return bad("human approval requires roles and positive count")
			}
			d, err := time.ParseDuration(rule.Timeout)
			if err != nil || d <= 0 || d > 30*24*time.Hour {
				return bad("human timeout must be positive and at most 30 days")
			}
		default:
			return bad("unknown approval mode")
		}
		for _, role := range rule.Roles {
			if !ValidID(role) {
				return bad("invalid role")
			}
		}
	}
	for level := range r.Levels {
		if _, ok := p.Spec.Approvals[level]; !ok {
			return bad("every risk level requires an explicit approval rule")
		}
	}
	qids := map[string]bool{}
	for _, q := range p.Spec.Qualifications {
		if !ValidID(q.ID) || qids[q.ID] || !checkRisks(q.Risks) {
			return bad("invalid qualification")
		}
		qids[q.ID] = true
		if q.Phase != "author" && q.Phase != "review" && q.Phase != "verification" {
			return bad("unknown qualification phase")
		}
		if q.Harness == "" && q.Model == "" && q.BinarySHA256 == "" && q.Template == "" {
			return bad("empty qualification")
		}
		if q.BinarySHA256 != "" && !digestPattern.MatchString("sha256:"+strings.TrimPrefix(strings.ToLower(q.BinarySHA256), "sha256:")) {
			return bad("qualification binary digest must be SHA-256")
		}
		if q.Executable != "" && (!strings.HasPrefix(q.Executable, "/") || strings.Contains(q.Executable, "..") || q.BinarySHA256 == "") {
			return bad("qualified executable requires absolute path and binary digest")
		}
	}
	for _, e := range p.Spec.Exceptions {
		if len(e.Controls) == 0 || len(e.Roles) == 0 {
			return bad("exception requires controls and roles")
		}
		for _, id := range e.Controls {
			if !ids[id] {
				return bad("exception can only waive a declared gate")
			}
		}
		for _, role := range e.Roles {
			if !ValidID(role) {
				return bad("invalid exception role")
			}
		}
	}
	return nil
}

func safeRelative(p string) bool {
	if p == "" || strings.HasPrefix(p, "/") || strings.ContainsAny(p, "\\\x00\n\r") {
		return false
	}
	for _, s := range strings.Split(p, "/") {
		if s == ".." || s == "." || s == "" {
			return false
		}
	}
	return true
}
func glob(p string) (*regexp.Regexp, error) {
	if p == "" || strings.HasPrefix(p, "/") || strings.Contains(p, "..") || strings.ContainsAny(p, "\\\x00\n\r[]") {
		return nil, fmt.Errorf("invalid path pattern %q", p)
	}
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(p); i++ {
		switch p[i] {
		case '*':
			if i+1 < len(p) && p[i+1] == '*' {
				i++
				if i+1 < len(p) && p[i+1] == '/' {
					i++
					b.WriteString("(?:.*/)?")
				} else {
					b.WriteString(".*")
				}
			} else {
				b.WriteString("[^/]*")
			}
		case '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(string(p[i])))
		}
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}
func matches(paths, patterns []string, all bool) bool {
	if len(paths) == 0 {
		return false
	}
	for _, path := range paths {
		hit := false
		for _, pat := range patterns {
			re, err := glob(pat)
			if err == nil && re.MatchString(path) {
				hit = true
				break
			}
		}
		if all && !hit {
			return false
		}
		if !all && hit {
			return true
		}
	}
	return all
}
func applies(risks []string, risk string) bool { return len(risks) == 0 || Contains(risks, risk) }
func Contains(values []string, v string) bool {
	for _, s := range values {
		if s == v {
			return true
		}
	}
	return false
}
func HasRole(a Actor, roles []string) bool {
	for _, r := range roles {
		if Contains(a.Roles, r) {
			return true
		}
	}
	return false
}

type DeterministicClassifier struct{}

func (DeterministicClassifier) Classify(_ context.Context, s Snapshot, c Candidate) (Plan, error) {
	return Evaluate(s, c.Paths)
}

// Evaluate unions obligations, never lets an automatic clause remove a human
// clause, and rejects conflicting definitions instead of relying on file order.
func Evaluate(s Snapshot, paths []string) (Plan, error) {
	p := Plan{Rank: -1}
	controls := map[string]Control{}
	evidence := map[string]bool{}
	qualifications := map[string]Qualification{}
	levels := map[string]int{}
	for _, policy := range s.Policies {
		if len(policy.Spec.Paths) > 0 && !matches(paths, policy.Spec.Paths, false) {
			continue
		}
		p.Policies = append(p.Policies, PolicyRef{policy.Metadata.Name, policy.Metadata.Version, Hash(policy)})
		r := policy.Spec.Risk
		for k, v := range r.Levels {
			if old, ok := levels[k]; ok && old != v {
				return p, fmt.Errorf("conflicting risk ranks")
			}
			levels[k] = v
		}
		level := r.Default
		found := false
		for _, rule := range r.Rules {
			if matches(paths, rule.Paths, rule.AllPaths) && (!found || r.Levels[rule.Level] > r.Levels[level]) {
				level = rule.Level
				found = true
			}
		}
		if r.Floor != "" && r.Levels[r.Floor] > r.Levels[level] {
			level = r.Floor
		}
		if r.Levels[level] > p.Rank {
			p.Rank = r.Levels[level]
			p.Risk = level
		}
	}
	if len(p.Policies) == 0 {
		return p, fmt.Errorf("no applicable quality policy")
	}
	for _, policy := range s.Policies {
		included := false
		for _, ref := range p.Policies {
			if ref.Name == policy.Metadata.Name {
				included = true
			}
		}
		if !included {
			continue
		}
		if _, ok := policy.Spec.Risk.Levels[p.Risk]; !ok {
			return p, fmt.Errorf("incompatible policy risk vocabulary")
		}
		for _, g := range policy.Spec.Gates {
			if !applies(g.Risks, p.Risk) {
				continue
			}
			if old, ok := controls[g.ID]; ok && Hash(old) != Hash(g) {
				return p, fmt.Errorf("conflicting gate %s", g.ID)
			}
			controls[g.ID] = g
		}
		for _, e := range policy.Spec.Evidence {
			evidence[e] = true
		}
		p.Approvals = append(p.Approvals, policy.Spec.Approvals[p.Risk])
		for _, q := range policy.Spec.Qualifications {
			if !applies(q.Risks, p.Risk) {
				continue
			}
			if old, ok := qualifications[q.ID]; ok && Hash(old) != Hash(q) {
				return p, fmt.Errorf("conflicting qualification %s", q.ID)
			}
			qualifications[q.ID] = q
		}
	}
	for _, g := range controls {
		p.Controls = append(p.Controls, g)
		var allowed map[string]bool
		for _, policy := range s.Policies {
			included := false
			for _, ref := range p.Policies {
				if ref.Name == policy.Metadata.Name {
					included = true
				}
			}
			if !included {
				continue
			}
			requires := false
			for _, c := range policy.Spec.Gates {
				if c.ID == g.ID && !c.Optional && applies(c.Risks, p.Risk) {
					requires = true
				}
			}
			if !requires {
				continue
			}
			roles := map[string]bool{}
			for _, e := range policy.Spec.Exceptions {
				if Contains(e.Controls, g.ID) {
					for _, role := range e.Roles {
						roles[role] = true
					}
				}
			}
			if allowed == nil {
				allowed = roles
			} else {
				for role := range allowed {
					if !roles[role] {
						delete(allowed, role)
					}
				}
			}
		}
		if len(allowed) > 0 {
			var roles []string
			for role := range allowed {
				roles = append(roles, role)
			}
			sort.Strings(roles)
			p.Exceptions = append(p.Exceptions, ExceptionRule{Controls: []string{g.ID}, Roles: roles})
		}
	}
	sort.Slice(p.Exceptions, func(i, j int) bool { return p.Exceptions[i].Controls[0] < p.Exceptions[j].Controls[0] })
	sort.Slice(p.Controls, func(i, j int) bool { return p.Controls[i].ID < p.Controls[j].ID })
	for e := range evidence {
		p.Evidence = append(p.Evidence, e)
	}
	sort.Strings(p.Evidence)
	for _, q := range qualifications {
		p.Qualifications = append(p.Qualifications, q)
	}
	sort.Slice(p.Qualifications, func(i, j int) bool { return p.Qualifications[i].ID < p.Qualifications[j].ID })
	sort.Slice(p.Policies, func(i, j int) bool { return p.Policies[i].Name < p.Policies[j].Name })
	p.Digest = Hash(p)
	return p, nil
}
