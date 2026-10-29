// Package securityassessment binds bounded assessment evidence to exact factory candidates.
package securityassessment

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
)

const ProfileSchema = "esf.security-profile/v1"

var digestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
var revisionPattern = regexp.MustCompile(`^[a-f0-9]{40}$`)
var identifierPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

func SHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

type Limits struct {
	MaxDuration      time.Duration `json:"max_duration_ns"`
	MaxRequests      int           `json:"max_requests"`
	MaxLogBytes      int64         `json:"max_log_bytes"`
	MaxEvidenceBytes int64         `json:"max_evidence_bytes"`
	AutomaticRepairs int           `json:"automatic_repairs"`
}

func DefaultLimits() Limits {
	return Limits{MaxDuration: 15 * time.Minute, MaxRequests: 100, MaxLogBytes: 16 << 20, MaxEvidenceBytes: 16 << 20}
}

func (l Limits) Validate() error {
	if l.MaxDuration <= 0 || l.MaxDuration > 15*time.Minute || l.MaxRequests <= 0 || l.MaxRequests > 100 ||
		l.MaxLogBytes <= 0 || l.MaxLogBytes > 16<<20 || l.MaxEvidenceBytes <= 0 || l.MaxEvidenceBytes > 16<<20 || l.AutomaticRepairs != 0 {
		return errors.New("assessment limits exceed the bounded manual profile")
	}
	return nil
}

type RuntimePin struct {
	SourceRevision    string            `json:"source_revision"`
	ImageDigest       string            `json:"image_digest"`
	CatalogDigest     string            `json:"catalog_digest"`
	TemplateID        string            `json:"template_id"`
	Tools             map[string]string `json:"tools"`
	Assets            map[string]string `json:"assets,omitempty"`
	DatabaseUpdatedAt time.Time         `json:"database_updated_at,omitempty"`
}

func (p RuntimePin) Validate() error {
	if !revisionPattern.MatchString(p.SourceRevision) || !digestPattern.MatchString(strings.TrimPrefix(p.ImageDigest, "sha256:")) ||
		!strings.HasPrefix(p.ImageDigest, "sha256:") || !digestPattern.MatchString(p.CatalogDigest) || !identifierPattern.MatchString(p.TemplateID) || len(p.Tools) == 0 {
		return errors.New("assessment runtime requires immutable source/image/catalog/template/tool identities")
	}
	for _, versions := range []map[string]string{p.Tools, p.Assets} {
		for name, version := range versions {
			if !identifierPattern.MatchString(name) || len(version) == 0 || len(version) > 256 || strings.ContainsAny(version, "\r\n\x00") {
				return errors.New("invalid runtime tool or asset identity")
			}
		}
	}
	return nil
}

type AIConfig struct {
	Enabled  bool   `json:"enabled"`
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
}

type Profile struct {
	Schema         string     `json:"schema"`
	ID             string     `json:"id"`
	TargetURL      string     `json:"target_url"`
	Runtime        RuntimePin `json:"runtime"`
	Limits         Limits     `json:"limits"`
	RequiredTools  []string   `json:"required_tools"`
	RequiredChecks []string   `json:"required_checks"`
	AI             AIConfig   `json:"ai"`
}

var permittedTools = map[string]bool{
	"oc_engagement_create": true, "oc_http_request": true, "oc_log_timeline": true,
	"oc_evidence_export": true, "oc_evidence_verify": true, "oc_audit_verify": true, "oc_file_triage": true,
}

func (p Profile) Validate() error {
	if p.Schema != ProfileSchema || !identifierPattern.MatchString(p.ID) {
		return errors.New("unsupported or invalid assessment profile")
	}
	target, err := url.Parse(p.TargetURL)
	if err != nil || target.Scheme != "https" || target.Hostname() == "" || target.User != nil || target.Fragment != "" || target.RawQuery != "" || target.RawPath != "" {
		return errors.New("assessment target must be an explicit HTTPS endpoint without credentials, query or fragment")
	}
	if err := p.Runtime.Validate(); err != nil {
		return err
	}
	if err := p.Limits.Validate(); err != nil {
		return err
	}
	if len(p.RequiredTools) == 0 || len(p.RequiredTools) > len(permittedTools) || len(p.RequiredChecks) == 0 {
		return errors.New("profile requires explicit tool and assertion coverage")
	}
	seen := map[string]bool{}
	for _, name := range p.RequiredTools {
		if !permittedTools[name] || seen[name] || p.Runtime.Tools[name] == "" {
			return errors.New("unsupported, duplicate or unpinned required assessment tool")
		}
		seen[name] = true
	}
	seen = map[string]bool{}
	for _, id := range p.RequiredChecks {
		if !identifierPattern.MatchString(id) || seen[id] {
			return errors.New("invalid or duplicate required assessment check")
		}
		seen[id] = true
	}
	if p.AI.Enabled && (p.AI.Provider == "" || p.AI.Model == "") {
		return errors.New("optional AI requires explicit provider and model pins")
	}
	return nil
}

func (p Profile) Digest() (string, error) {
	if err := p.Validate(); err != nil {
		return "", err
	}
	p.RequiredTools = append([]string(nil), p.RequiredTools...)
	p.RequiredChecks = append([]string(nil), p.RequiredChecks...)
	sort.Strings(p.RequiredTools)
	sort.Strings(p.RequiredChecks)
	data, err := json.Marshal(p)
	if err != nil {
		return "", fmt.Errorf("encode profile: %w", err)
	}
	return SHA256(data), nil
}
