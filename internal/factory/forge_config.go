package factory

import (
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mitkox/esf/internal/forge"
)

// ConnectorConfig declares one host-side forge adapter.
//
// The token is a reference to an operator-owned private file, never a value.
// Configuration validation checks the reference's shape; constructing the
// adapter reads the file through the private-file rules and fails closed when it
// is missing, writable by another user, a symlink, or otherwise unsafe.
type ConnectorConfig struct {
	Kind      string `toml:"kind"`
	BaseURL   string `toml:"base_url"`
	TokenFile string `toml:"token_file"`
}

// PipelineConfig is one operator-only work-item mapping.
//
// Rules are matched against the forge attributes in WorkItemFields. A rule
// supplies exactly one scope, repository, base branch, and verification
// profile; no payload field can select any of them.
type PipelineConfig struct {
	Connector string         `toml:"connector"`
	Rules     []PipelineRule `toml:"rules"`
}

// PipelineRule maps matching forge attributes to one target.
//
// Empty match fields are wildcards. A rule with more non-empty match fields is
// more specific. Two equally specific matching rules that resolve to different
// targets are ambiguous and fail resolution.
type PipelineRule struct {
	Project string `toml:"project"`
	Area    string `toml:"area"`
	Label   string `toml:"label"`
	Type    string `toml:"type"`
	State   string `toml:"state"`

	Scope               string `toml:"scope"`
	Repository          string `toml:"repository"`
	BaseBranch          string `toml:"base_branch"`
	VerificationProfile string `toml:"verification_profile"`
}

// ResolvedPipeline is the operator-owned target for one work item.
type ResolvedPipeline struct {
	Pipeline            string `json:"pipeline"`
	Connector           string `json:"connector"`
	Scope               string `json:"scope"`
	Repository          string `json:"repository"`
	BaseBranch          string `json:"base_branch"`
	VerificationProfile string `json:"verification_profile"`
}

// Connector resolves a connector's configuration and reads its credential.
//
// It is the only path that turns a token-file reference into a credential. A
// credential that is not a private regular file fails construction rather than
// being used.
func (c Config) Connector(name string) (forge.ConnectorSettings, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return forge.ConnectorSettings{}, fmt.Errorf("connector name is required")
	}
	connector, ok := c.Connectors[name]
	if !ok {
		return forge.ConnectorSettings{}, fmt.Errorf("connector %q is not configured", name)
	}
	if problems := connector.problems(); len(problems) > 0 {
		return forge.ConnectorSettings{}, fmt.Errorf("connector %q: %s", name, strings.Join(problems, "; "))
	}
	credential, err := forge.ReadCredential(strings.TrimSpace(connector.TokenFile))
	if err != nil {
		return forge.ConnectorSettings{}, fmt.Errorf("connector %q: token_file: %w", name, err)
	}
	return forge.ConnectorSettings{
		Name:       name,
		Kind:       connector.kind(),
		BaseURL:    strings.TrimSpace(connector.BaseURL),
		Credential: credential,
	}, nil
}

// ResolvePipeline maps one work item through an operator-configured pipeline.
//
// The mapping input is WorkItemFields, which contains only the forge attributes
// a rule may match. A payload cannot add a repository, revision, executable,
// credential, or host path to the resolution.
func (c Config) ResolvePipeline(name string, fields forge.WorkItemFields) (ResolvedPipeline, error) {
	pipeline, ok := c.Pipelines[name]
	if !ok {
		return ResolvedPipeline{}, fmt.Errorf("pipeline %q is not configured", name)
	}
	if problems := c.pipelineProblems(pipeline); len(problems) > 0 {
		return ResolvedPipeline{}, fmt.Errorf("pipeline %q: %s", name, strings.Join(problems, "; "))
	}
	return pipeline.resolve(name, fields)
}

// validateForge accumulates connector and pipeline problems for Config.Validate.
func (c Config) validateForge() []string {
	var problems []string
	for _, name := range sortedForgeNames(c.Connectors) {
		if strings.TrimSpace(name) == "" {
			problems = append(problems, "connector names must not be empty")
			continue
		}
		if connectorProblems := c.Connectors[name].problems(); len(connectorProblems) > 0 {
			problems = append(problems, fmt.Sprintf("connector %q: %s", name, strings.Join(connectorProblems, "; ")))
		}
	}
	for _, name := range sortedForgeNames(c.Pipelines) {
		if strings.TrimSpace(name) == "" {
			problems = append(problems, "pipeline names must not be empty")
			continue
		}
		if pipelineProblems := c.pipelineProblems(c.Pipelines[name]); len(pipelineProblems) > 0 {
			problems = append(problems, fmt.Sprintf("pipeline %q: %s", name, strings.Join(pipelineProblems, "; ")))
		}
	}
	return problems
}

func (c ConnectorConfig) kind() forge.Kind {
	return forge.Kind(strings.ToLower(strings.TrimSpace(c.Kind)))
}

func (c ConnectorConfig) problems() []string {
	var problems []string
	if !c.kind().Valid() {
		problems = append(problems, fmt.Sprintf("kind %q is not supported", strings.TrimSpace(c.Kind)))
	}
	if err := validateForgeBaseURL(c.BaseURL); err != nil {
		problems = append(problems, err.Error())
	}
	tokenFile := strings.TrimSpace(c.TokenFile)
	if tokenFile == "" {
		problems = append(problems, "token_file is required")
	} else if !filepath.IsAbs(tokenFile) {
		problems = append(problems, "token_file must be an absolute path")
	}
	return problems
}

// pipelineProblems checks one pipeline against the rest of the configuration.
// It deliberately does not read the connector credential: submission must not
// need a forge secret.
func (c Config) pipelineProblems(p PipelineConfig) []string {
	var problems []string
	connectorName := strings.TrimSpace(p.Connector)
	if connectorName == "" {
		problems = append(problems, "connector is required")
	} else if connector, ok := c.Connectors[connectorName]; !ok {
		problems = append(problems, fmt.Sprintf("unknown connector %q", connectorName))
	} else if connectorProblems := connector.problems(); len(connectorProblems) > 0 {
		problems = append(problems, fmt.Sprintf("connector %q: %s", connectorName, strings.Join(connectorProblems, "; ")))
	}
	if len(p.Rules) == 0 {
		problems = append(problems, "at least one mapping rule is required")
		return problems
	}

	type seenRule struct {
		index  int
		target ResolvedPipeline
	}
	seen := map[string]seenRule{}
	for i, rule := range p.Rules {
		for _, problem := range c.ruleProblems(rule) {
			problems = append(problems, fmt.Sprintf("rule %d: %s", i+1, problem))
		}
		target := rule.target("", p.Connector)
		key := rule.selectorKey()
		if previous, ok := seen[key]; ok {
			if previous.target != target {
				problems = append(problems, fmt.Sprintf("rules %d and %d are ambiguous: identical match criteria map to different targets", previous.index+1, i+1))
			}
			continue
		}
		seen[key] = seenRule{index: i, target: target}
	}
	return problems
}

func (c Config) ruleProblems(rule PipelineRule) []string {
	var problems []string
	for _, field := range []struct {
		name  string
		value string
	}{
		{"project", rule.Project},
		{"area", rule.Area},
		{"label", rule.Label},
		{"type", rule.Type},
		{"state", rule.State},
	} {
		if strings.ContainsAny(field.value, "\x00\r\n\t") {
			problems = append(problems, field.name+" must not contain control characters")
		}
	}

	scope := strings.TrimSpace(rule.Scope)
	if scope == "" {
		problems = append(problems, "scope is required")
	} else if !c.scopeConfigured(scope) {
		problems = append(problems, fmt.Sprintf("unknown scope %q", scope))
	}
	if strings.TrimSpace(rule.Repository) == "" {
		problems = append(problems, "repository is required")
	} else if strings.ContainsAny(rule.Repository, "\x00\r\n") {
		problems = append(problems, "repository must not contain control characters")
	}
	if err := validatePipelineBaseBranch(rule.BaseBranch); err != nil {
		problems = append(problems, err.Error())
	}
	profile := strings.TrimSpace(rule.VerificationProfile)
	if profile == "" {
		problems = append(problems, "verification_profile is required")
	} else if _, ok := c.Verification[profile]; !ok {
		problems = append(problems, fmt.Sprintf("unknown verification profile %q", profile))
	}
	return problems
}

// scopeConfigured mirrors resource resolution: the default scope is always
// available even when it has no explicit [scopes.default] table.
func (c Config) scopeConfigured(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	if name == DefaultScope {
		return true
	}
	_, ok := c.Scopes[name]
	return ok
}

func (p PipelineConfig) resolve(pipeline string, fields forge.WorkItemFields) (ResolvedPipeline, error) {
	bestScore := -1
	var matched []int
	for i, rule := range p.Rules {
		score, ok := rule.match(fields)
		if !ok {
			continue
		}
		switch {
		case score > bestScore:
			bestScore = score
			matched = []int{i}
		case score == bestScore:
			matched = append(matched, i)
		}
	}
	if bestScore < 0 {
		return ResolvedPipeline{}, fmt.Errorf(
			"pipeline %q: no mapping rule matches project %q, area %q, type %q, state %q",
			pipeline, strings.TrimSpace(fields.Project), strings.TrimSpace(fields.Area), strings.TrimSpace(fields.Type), strings.TrimSpace(fields.State),
		)
	}
	target := p.Rules[matched[0]].target(pipeline, p.Connector)
	for _, index := range matched[1:] {
		if p.Rules[index].target(pipeline, p.Connector) != target {
			return ResolvedPipeline{}, fmt.Errorf(
				"pipeline %q: rules %s are equally specific and map to different targets",
				pipeline, ruleNumbers(matched),
			)
		}
	}
	return target, nil
}

func (r PipelineRule) match(fields forge.WorkItemFields) (int, bool) {
	score := 0
	for _, pair := range []struct {
		rule  string
		value string
	}{
		{r.Project, fields.Project},
		{r.Area, fields.Area},
		{r.Type, fields.Type},
		{r.State, fields.State},
	} {
		expected := strings.TrimSpace(pair.rule)
		if expected == "" {
			continue
		}
		if expected != strings.TrimSpace(pair.value) {
			return 0, false
		}
		score++
	}
	if label := strings.TrimSpace(r.Label); label != "" {
		found := false
		for _, value := range fields.Labels {
			if strings.TrimSpace(value) == label {
				found = true
				break
			}
		}
		if !found {
			return 0, false
		}
		score++
	}
	return score, true
}

func (r PipelineRule) target(pipeline, connector string) ResolvedPipeline {
	return ResolvedPipeline{
		Pipeline:            strings.TrimSpace(pipeline),
		Connector:           strings.TrimSpace(connector),
		Scope:               strings.TrimSpace(r.Scope),
		Repository:          strings.TrimSpace(r.Repository),
		BaseBranch:          strings.TrimSpace(r.BaseBranch),
		VerificationProfile: strings.TrimSpace(r.VerificationProfile),
	}
}

func (r PipelineRule) selectorKey() string {
	return strings.Join([]string{
		strings.TrimSpace(r.Project),
		strings.TrimSpace(r.Area),
		strings.TrimSpace(r.Label),
		strings.TrimSpace(r.Type),
		strings.TrimSpace(r.State),
	}, "\x00")
}

func ruleNumbers(indexes []int) string {
	parts := make([]string, len(indexes))
	for i, index := range indexes {
		parts[i] = fmt.Sprintf("%d", index+1)
	}
	return strings.Join(parts, ", ")
}

func validateForgeBaseURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fmt.Errorf("base_url is required")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("base_url must be an absolute URL without userinfo, query, or fragment")
	}
	switch strings.ToLower(parsed.Scheme) {
	case "https":
		return nil
	case "http":
		host := parsed.Hostname()
		if strings.EqualFold(host, "localhost") {
			return nil
		}
		if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
			return nil
		}
		return fmt.Errorf("base_url must use HTTPS or HTTP on a loopback host")
	default:
		return fmt.Errorf("base_url must use HTTPS")
	}
}

func validatePipelineBaseBranch(branch string) error {
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return fmt.Errorf("base_branch is required")
	}
	if strings.HasPrefix(branch, "-") {
		return fmt.Errorf("base_branch must not begin with '-'")
	}
	if strings.ContainsAny(branch, " \t\r\n\x00") {
		return fmt.Errorf("base_branch must not contain whitespace or control characters")
	}
	if strings.Contains(branch, "..") {
		return fmt.Errorf("base_branch must not contain '..'")
	}
	if len(branch) > 255 {
		return fmt.Errorf("base_branch is unreasonably long")
	}
	return nil
}

func sortedForgeNames[V any](m map[string]V) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
