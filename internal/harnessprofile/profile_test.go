package harnessprofile

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testProfile() Profile {
	return Profile{Schema: Schema, Binding: Binding{Scope: "default", Repository: "https://example.com/repo", Harness: "opencode2", Model: "test-model", HarnessDigest: Hash("binary"), Template: "test-template"}, Upstream: Upstream, Author: "editor", Evidence: []string{}, Components: map[string]string{"systemprompt.md": "Inspect the source and complete the original task.", "skills/review/SKILL.md": "---\nname: review\ndescription: inspect a change\n---\nRead the diff."}}
}
func qualityInput(p Profile) QualityInput {
	in := QualityInput{Split: "qualification", Profile: p.Digest(), Binding: p.Binding, Mode: "static", SourceCommit: strings.Repeat("a", 40), CorpusDigest: Hash("frozen corpus"), LibraryDigest: Hash("library"), RouteDigest: Hash("route")}
	for task := 0; task < 100; task++ {
		for repeat := 0; repeat < 3; repeat++ {
			for _, arm := range []string{"baseline", "candidate"} {
				c := int64(1000)
				accepted := task%10 < 4
				if arm == "candidate" {
					accepted = task%10 < 6
				}
				in.Trials = append(in.Trials, Trial{Task: fmt.Sprint(task), Group: fmt.Sprint(task / 10), Repeat: repeat, Arm: arm, Verified: accepted, Accepted: accepted, CostMicros: &c, DurationMillis: 1000, Evidence: []string{Hash(fmt.Sprint(task, repeat, arm))}})
			}
		}
	}
	return in
}
func TestProfileSurfaceAndTransport(t *testing.T) {
	p := testProfile()
	if e := p.Validate(); e != nil {
		t.Fatal(e)
	}
	for _, path := range []string{"guardrails.md", "scripts/x.py", "hooks.json", "skills/review/scripts/x.py", "../memory.md", "/memory.md", "skills/review/../../x", "plugin/x.ts", "sub_agents/x/AGENT.md"} {
		t.Run(path, func(t *testing.T) {
			q := testProfile()
			q.Components[path] = "text"
			if q.Validate() == nil {
				t.Fatal("accepted unauthorized component")
			}
		})
	}
	q := testProfile()
	q.Components["systemprompt.md"] = strings.Repeat("x", MaxAlwaysOnBytes+1)
	if q.Validate() == nil {
		t.Fatal("accepted oversized instructions")
	}
	b, _ := json.Marshal(p)
	if _, e := Decode(bytes.NewReader(append(b, []byte("{}")...))); e == nil {
		t.Fatal("accepted trailing JSON")
	}
	prompt := p.Prompt("/workspace/.factory/profile", "original `$(not-a-command)` task")
	if !strings.Contains(prompt, "Original task (authoritative):\noriginal `$(not-a-command)` task") || !strings.Contains(prompt, "/workspace/.factory/profile/skills/review/SKILL.md") {
		t.Fatal("profile or original task missing")
	}
}
func TestImmutableProfileAndApproval(t *testing.T) {
	dir := t.TempDir()
	s, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	p := testProfile()
	d, e := s.Put(p)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Approved(d, nil, time.Now()); e == nil {
		t.Fatal("unreviewed candidate was active")
	}
	r := Qualify(qualityInput(p))
	if !r.Passed {
		t.Fatal(r.Problems)
	}
	for i := 0; i < 10; i++ {
		if Hash(Qualify(r.Input)) != Hash(r) {
			t.Fatal("qualification is nondeterministic")
		}
	}
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	keys := map[string]string{"reviewer": base64.StdEncoding.EncodeToString(pub)}
	a := Approval{Schema: "esf-harness-approval/v1", Reviewer: "reviewer", Profile: d, Qualification: Hash(r), Binding: p.Binding, Expires: time.Now().Add(time.Hour)}
	a.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(priv, a.SigningBytes()))
	if e = s.Promote(p, r, a, keys, time.Now()); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Approved(d, keys, time.Now()); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Approved(d, keys, time.Now().Add(2*time.Hour)); e == nil {
		t.Fatal("expired approval accepted")
	}
	// Renewal appends a separately signed immutable receipt.
	a.Expires = time.Now().Add(3 * time.Hour)
	a.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(priv, a.SigningBytes()))
	if e = s.Promote(p, r, a, keys, time.Now()); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Approved(d, keys, time.Now().Add(2*time.Hour)); e != nil {
		t.Fatal("approval renewal was not usable", e)
	}
	r.Passed = false
	if VerifyApproval(p, r, a, keys, time.Now()) == nil {
		t.Fatal("modified report accepted")
	}
	path := filepath.Join(dir, "objects", d[7:]+".json")
	if e = os.WriteFile(path, []byte(`{}`), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Get(d); e == nil {
		t.Fatal("corrupt object accepted")
	}
}
func TestQualityCannotHideFailuresOrMissingEvidence(t *testing.T) {
	mutations := map[string]func(*QualityInput){"unknown cost": func(in *QualityInput) { in.Trials[0].CostMicros = nil }, "duplicate": func(in *QualityInput) { in.Trials = append(in.Trials, in.Trials[0]) }, "small corpus": func(in *QualityInput) { in.Trials = in.Trials[:60] }, "incorrect acceptance": func(in *QualityInput) { in.Trials[0].Accepted = true; in.Trials[0].Verified = false }, "cost regression": func(in *QualityInput) {
		for i := range in.Trials {
			if in.Trials[i].Arm == "candidate" {
				n := int64(1300)
				in.Trials[i].CostMicros = &n
			}
		}
	}, "serious defect": func(in *QualityInput) { in.Trials[1].SeriousDefects = 1 }}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			in := qualityInput(testProfile())
			mutate(&in)
			if Qualify(in).Passed {
				t.Fatal("invalid quality input passed")
			}
		})
	}
	in := qualityInput(testProfile())
	in.Mode = "adaptive"
	in.PolicyDigest = Hash("policy")
	var repeated []Trial
	for _, x := range in.Trials {
		if x.Arm == "candidate" {
			x.Arm = "repeated-baseline"
			repeated = append(repeated, x)
		}
	}
	in.Trials = append(in.Trials, repeated...)
	if Qualify(in).Passed {
		t.Fatal("adaptation indistinguishable from repeated baseline was promoted")
	}
}
