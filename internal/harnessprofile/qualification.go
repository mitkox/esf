package harnessprofile

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"sort"
	"strings"
	"time"
)

const QualificationSchema = "esf-harness-quality/v1"

type Trial struct {
	Task           string   `json:"task"`
	Group          string   `json:"group"`
	Repeat         int      `json:"repeat"`
	Arm            string   `json:"arm"`
	Verified       bool     `json:"verified"`
	Accepted       bool     `json:"accepted"`
	SeriousDefects int      `json:"serious_defects"`
	CostMicros     *int64   `json:"cost_micros"`
	DurationMillis int64    `json:"duration_millis"`
	Evidence       []string `json:"evidence"`
}

type QualityInput struct {
	Split         string  `json:"split"`
	Profile       string  `json:"profile"`
	Binding       Binding `json:"binding"`
	Mode          string  `json:"mode"`
	SourceCommit  string  `json:"source_commit"`
	CorpusDigest  string  `json:"corpus_digest"`
	LibraryDigest string  `json:"library_digest"`
	RouteDigest   string  `json:"route_digest"`
	PolicyDigest  string  `json:"policy_digest,omitempty"`
	Trials        []Trial `json:"trials"`
}

type Comparison struct {
	Against string  `json:"against"`
	Gain    float64 `json:"gain"`
	Lower95 float64 `json:"lower_95"`
}

type QualityReport struct {
	Schema      string       `json:"schema"`
	Input       QualityInput `json:"input"`
	Comparisons []Comparison `json:"comparisons"`
	Passed      bool         `json:"passed"`
	Problems    []string     `json:"problems"`
}

// Qualify bootstraps whole repository/task-family clusters, keeping repeated
// runs together. The frozen input, deterministic seed and raw outcomes are
// retained so a reviewer can reproduce the decision.
func Qualify(in QualityInput) QualityReport {
	r := QualityReport{Schema: QualificationSchema, Input: in, Problems: []string{}}
	bad := func(s string) { r.Problems = append(r.Problems, s) }
	if err := in.Binding.Validate(); err != nil {
		bad(err.Error())
	}
	if !DigestPattern.MatchString(in.Profile) || len(in.SourceCommit) != 40 || strings.Trim(in.SourceCommit, "0123456789abcdef") != "" {
		bad("profile digest and exact source commit required")
	}
	if in.Split != "qualification" {
		bad("only the untouched qualification partition can authorize promotion")
	}
	for _, d := range []string{in.CorpusDigest, in.LibraryDigest, in.RouteDigest} {
		if !DigestPattern.MatchString(d) {
			bad("corpus, library, and route digests required")
		}
	}
	if in.Mode != "static" && in.Mode != "adaptive" {
		bad("mode must be static or adaptive")
	}
	if in.Mode == "adaptive" && !DigestPattern.MatchString(in.PolicyDigest) {
		bad("adaptive qualification requires policy digest")
	}
	arms := []string{"baseline", "candidate"}
	if in.Mode == "adaptive" {
		arms = append(arms, "repeated-baseline")
	}
	by := map[string]map[string]map[int]Trial{}
	groups := map[string]string{}
	for _, t := range in.Trials {
		validArm := false
		for _, a := range arms {
			validArm = validArm || a == t.Arm
		}
		if !validArm || t.Task == "" || t.Group == "" || t.Repeat < 0 || t.Repeat > 2 {
			bad("invalid task, cluster, arm or repeat")
			continue
		}
		if t.CostMicros == nil || *t.CostMicros < 0 || t.DurationMillis <= 0 || len(t.Evidence) == 0 {
			bad("unknown usage, duration or evidence")
		}
		for _, d := range t.Evidence {
			if !DigestPattern.MatchString(d) {
				bad("invalid trial evidence digest")
			}
		}
		if t.Accepted && !t.Verified {
			bad("acceptance cannot override failed verification")
		}
		if t.SeriousDefects < 0 || (t.Arm == "candidate" && t.SeriousDefects > 0) {
			bad("candidate has serious defects")
		}
		if g, ok := groups[t.Task]; ok && g != t.Group {
			bad("task cluster changed")
		}
		groups[t.Task] = t.Group
		if by[t.Task] == nil {
			by[t.Task] = map[string]map[int]Trial{}
		}
		if by[t.Task][t.Arm] == nil {
			by[t.Task][t.Arm] = map[int]Trial{}
		}
		if _, ok := by[t.Task][t.Arm][t.Repeat]; ok {
			bad("duplicate trial")
		}
		by[t.Task][t.Arm][t.Repeat] = t
	}
	if len(by) < 100 {
		bad("at least 100 untouched qualification tasks required")
	}
	tasks := make([]string, 0, len(by))
	for task := range by {
		tasks = append(tasks, task)
	}
	sort.Strings(tasks)
	for _, task := range tasks {
		aa := by[task]
		for _, arm := range arms {
			if len(aa[arm]) != 3 {
				bad("each task needs three repeats for every arm")
			}
		}
	}
	if len(r.Problems) > 0 {
		return r
	}
	var costB, costC float64
	var timeB, timeC []int64
	for _, task := range tasks {
		aa := by[task]
		for i := 0; i < 3; i++ {
			b, c := aa["baseline"][i], aa["candidate"][i]
			costB += float64(*b.CostMicros)
			costC += float64(*c.CostMicros)
			timeB = append(timeB, b.DurationMillis)
			timeC = append(timeC, c.DurationMillis)
		}
	}
	sort.Slice(timeB, func(i, j int) bool { return timeB[i] < timeB[j] })
	sort.Slice(timeC, func(i, j int) bool { return timeC[i] < timeC[j] })
	mult := 1.2
	if in.Mode == "adaptive" {
		mult = 5
	}
	if costB <= 0 || costC > costB*mult || float64(timeC[(len(timeC)*95-1)/100]) > float64(timeB[(len(timeB)*95-1)/100])*mult {
		bad("cost or p95 duration ceiling exceeded")
	}
	for _, arm := range arms {
		if arm == "candidate" {
			continue
		}
		cluster := map[string][]float64{}
		for _, task := range tasks {
			aa := by[task]
			var delta float64
			for i := 0; i < 3; i++ {
				if aa["candidate"][i].Accepted && aa["candidate"][i].Verified {
					delta++
				}
				if aa[arm][i].Accepted && aa[arm][i].Verified {
					delta--
				}
			}
			cluster[groups[task]] = append(cluster[groups[task]], delta/3)
		}
		keys := make([]string, 0, len(cluster))
		for k := range cluster {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		if len(keys) < 10 {
			bad("at least ten independent task-family/repository clusters required")
			continue
		}
		var sum float64
		for _, key := range keys {
			vals := cluster[key]
			for _, v := range vals {
				sum += v
			}
		}
		gain := sum / float64(len(by))
		samples := make([]float64, 10000)
		rng := rand.New(rand.NewPCG(42, 17))
		for i := range samples {
			var s float64
			n := 0
			for range keys {
				vals := cluster[keys[rng.IntN(len(keys))]]
				for _, v := range vals {
					s += v
					n++
				}
			}
			samples[i] = s / float64(n)
		}
		sort.Float64s(samples)
		cmp := Comparison{Against: arm, Gain: gain, Lower95: samples[249]}
		r.Comparisons = append(r.Comparisons, cmp)
		if gain < 0.05-1e-12 || cmp.Lower95 <= 0 {
			bad("accepted-task improvement is below threshold or inconclusive against " + arm)
		}
	}
	r.Passed = len(r.Problems) == 0
	return r
}

// Approval is signed by an operator-trusted independent reviewer. It binds
// the complete reproducible report, profile and tenancy boundary.
type Approval struct {
	Schema        string    `json:"schema"`
	Reviewer      string    `json:"reviewer"`
	Profile       string    `json:"profile"`
	Qualification string    `json:"qualification"`
	Binding       Binding   `json:"binding"`
	Expires       time.Time `json:"expires"`
	Signature     string    `json:"signature"`
}

func (a Approval) SigningBytes() []byte { a.Signature = ""; b, _ := json.Marshal(a); return b }
func VerifyApproval(p Profile, r QualityReport, a Approval, keys map[string]string, now time.Time) error {
	if err := p.Validate(); err != nil {
		return err
	}
	computed := Qualify(r.Input)
	if r.Schema != QualificationSchema || Hash(computed) != Hash(r) || !r.Passed {
		return errors.New("qualification is incomplete, modified, or failed")
	}
	if a.Schema != "esf-harness-approval/v1" || a.Reviewer == p.Author || a.Reviewer == "" || a.Profile != p.Digest() || r.Input.Profile != p.Digest() || a.Qualification != Hash(r) || a.Binding != p.Binding || r.Input.Binding != p.Binding || !a.Expires.After(now) {
		return errors.New("approval identity, binding, digest or expiry rejected")
	}
	key, e := base64.StdEncoding.DecodeString(keys[a.Reviewer])
	sig, se := base64.StdEncoding.DecodeString(a.Signature)
	if e != nil || se != nil || len(key) != ed25519.PublicKeySize || !ed25519.Verify(key, a.SigningBytes(), sig) {
		return errors.New("independent reviewer signature rejected")
	}
	return nil
}

type Promotion struct {
	Profile  string        `json:"profile"`
	Report   QualityReport `json:"report"`
	Approval Approval      `json:"approval"`
}

func (s *Store) Promote(p Profile, r QualityReport, a Approval, keys map[string]string, now time.Time) error {
	if err := VerifyApproval(p, r, a, keys, now); err != nil {
		return err
	}
	d, err := s.Put(p)
	if err != nil {
		return err
	}
	b, _ := json.Marshal(Promotion{d, r, a})
	return s.WriteImmutable("promotions/"+d[7:]+"/"+Hash(json.RawMessage(b))[7:]+".json", b)
}
func (s *Store) Approved(d string, keys map[string]string, now time.Time) (Profile, error) {
	p, _, e := s.Authorized(d, keys, now, nil)
	return p, e
}

// Authorized supports independent, immutable qualification receipts for the
// same profile, including later approval renewal and static/live evaluation.
func (s *Store) Authorized(d string, keys map[string]string, now time.Time, matches func(QualityInput) bool) (Profile, Promotion, error) {
	p, e := s.Get(d)
	if e != nil {
		return p, Promotion{}, e
	}
	path := "promotions/" + d[7:]
	if e = s.noLinks(path); e != nil {
		return p, Promotion{}, e
	}
	dir, e := s.root.Open(path)
	if e != nil {
		return p, Promotion{}, e
	}
	defer dir.Close()
	entries, e := dir.ReadDir(-1)
	if e != nil || len(entries) > 256 {
		return p, Promotion{}, errors.New("invalid promotion inventory")
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		if entry.IsDir() || !DigestPattern.MatchString("sha256:"+strings.TrimSuffix(name, ".json")) {
			return p, Promotion{}, errors.New("invalid promotion artifact")
		}
		b, e := s.Read(path+"/"+name, MaxArtifactBytes)
		if e != nil {
			return p, Promotion{}, e
		}
		var receipt Promotion
		if e = json.Unmarshal(b, &receipt); e != nil {
			return p, Promotion{}, e
		}
		if Hash(json.RawMessage(b))[7:]+".json" != name || receipt.Profile != d {
			return p, Promotion{}, fmt.Errorf("promotion digest mismatch")
		}
		if matches != nil && !matches(receipt.Report.Input) {
			continue
		}
		if e = VerifyApproval(p, receipt.Report, receipt.Approval, keys, now); e == nil {
			return p, receipt, nil
		}
	}
	return p, Promotion{}, errors.New("no current independent approval matches this qualification")
}
