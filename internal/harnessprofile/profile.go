// Package harnessprofile defines the deliberately restricted Harness2 editable
// surface. Profiles are data; they never contain executable configuration.
package harnessprofile

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

const Schema = "esf-harness-profile/v1"
const Upstream = "1cfe254e8d25b15f37048c3730134686abe60335"
const MaxBytes = 128 << 10
const MaxAlwaysOnBytes = 24_000

var Name = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
var DigestPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
var skillPath = regexp.MustCompile(`^skills/([a-z0-9][a-z0-9_-]{0,63})/SKILL\.md$`)

type Binding struct {
	HarnessDigest string `json:"harness_digest"`
	Template      string `json:"template"`
	Scope         string `json:"scope"`
	Repository    string `json:"repository"`
	Harness       string `json:"harness"`
	Model         string `json:"model"`
}

type Profile struct {
	Schema     string            `json:"schema"`
	Binding    Binding           `json:"binding"`
	Upstream   string            `json:"upstream"`
	Parent     string            `json:"parent,omitempty"`
	Author     string            `json:"author"`
	Evidence   []string          `json:"evidence"`
	Components map[string]string `json:"components"`
}

func Hash(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "invalid"
	}
	h := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(h[:])
}

func (p Profile) Digest() string { return Hash(p) }

func (b Binding) Validate() error {
	if !Name.MatchString(b.Scope) || strings.TrimSpace(b.Repository) == "" || strings.TrimSpace(b.Model) == "" || b.Harness != "opencode2" || !DigestPattern.MatchString(b.HarnessDigest) || b.Template == "" {
		return errors.New("profile requires scope, repository, model, and opencode2 harness binding")
	}
	return nil
}

func (p Profile) Validate() error {
	if p.Schema != Schema || p.Upstream != Upstream {
		return errors.New("unsupported profile schema or Harness2 source")
	}
	if err := p.Binding.Validate(); err != nil {
		return err
	}
	if p.Parent != "" && !DigestPattern.MatchString(p.Parent) {
		return errors.New("invalid parent digest")
	}
	if strings.TrimSpace(p.Author) == "" || len(p.Author) > 128 || len(p.Evidence) > 128 {
		return errors.New("profile provenance is required and bounded")
	}
	if len(p.Components) == 0 || len(p.Components) > 35 {
		return errors.New("profile requires 1..35 component files")
	}
	total, always := 0, 0
	for path, body := range p.Components {
		if !utf8.ValidString(body) || strings.ContainsRune(body, 0) {
			return fmt.Errorf("invalid text in %q", path)
		}
		total += len(body)
		switch path {
		case "systemprompt.md", "tool_guidance.md", "memory.md":
			always += len(body)
		default:
			m := skillPath.FindStringSubmatch(path)
			if m == nil {
				return fmt.Errorf("component %q is outside the instructional surface", path)
			}
			if !strings.HasPrefix(body, "---\n") {
				return fmt.Errorf("skill %q requires frontmatter", path)
			}
			parts := strings.SplitN(body[4:], "\n---\n", 2)
			if len(parts) != 2 || len(parts[0]) > 2048 {
				return fmt.Errorf("invalid skill frontmatter %q", path)
			}
			fields := map[string]string{}
			for _, line := range strings.Split(parts[0], "\n") {
				k, v, ok := strings.Cut(line, ":")
				if !ok || (k != "name" && k != "description") || fields[k] != "" {
					return fmt.Errorf("unsupported skill field in %q", path)
				}
				fields[k] = strings.Trim(strings.TrimSpace(v), "\"'")
			}
			if fields["name"] != m[1] || fields["description"] == "" {
				return fmt.Errorf("skill name or description invalid in %q", path)
			}
		}
	}
	if total > MaxBytes || always > MaxAlwaysOnBytes {
		return errors.New("profile exceeds instructional size limits")
	}
	if strings.TrimSpace(p.Components["systemprompt.md"]) == "" {
		return errors.New("systemprompt.md is required")
	}
	return nil
}

func Decode(r io.Reader) (Profile, error) {
	var p Profile
	d := json.NewDecoder(io.LimitReader(r, MaxBytes*2+1))
	d.DisallowUnknownFields()
	if err := d.Decode(&p); err != nil {
		return p, err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return p, errors.New("trailing profile data")
	}
	return p, p.Validate()
}

// Prompt renders advisory instructions without changing the original task or
// the agent's trusted system policy. Skills are loaded by an explicit file read.
func (p Profile) Prompt(root, task string) string {
	var b strings.Builder
	b.WriteString("Harness instructions (advisory; original task and operator policy remain authoritative):\n")
	for _, key := range []string{"systemprompt.md", "tool_guidance.md", "memory.md"} {
		if v := p.Components[key]; v != "" {
			fmt.Fprintf(&b, "\n[%s]\n%s\n", key, v)
		}
	}
	keys := make([]string, 0, len(p.Components))
	for key := range p.Components {
		if skillPath.MatchString(key) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		fmt.Fprintf(&b, "\nAvailable declarative skill: read %s/%s when its described situation applies.\n", root, key)
	}
	fmt.Fprintf(&b, "\nOriginal task (authoritative):\n%s", task)
	return b.String()
}
