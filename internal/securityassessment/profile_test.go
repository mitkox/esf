package securityassessment

import (
	"strings"
	"testing"
	"time"
)

func testRuntimePin() RuntimePin {
	return RuntimePin{SourceRevision: strings.Repeat("a", 40), ImageDigest: "sha256:" + strings.Repeat("b", 64), CatalogDigest: strings.Repeat("c", 64), TemplateID: "tpl-profile", Tools: map[string]string{"oc_http_request": "native-v1"}}
}

func testProfile() Profile {
	return Profile{Schema: ProfileSchema, ID: "tenant-api-v1", TargetURL: "https://18080-target.cube.test", Runtime: testRuntimePin(), Limits: DefaultLimits(), RequiredTools: []string{"oc_http_request"}, RequiredChecks: []string{"tenant-control", "tenant-isolation"}}
}

func TestProfileBoundsAndIdentity(t *testing.T) {
	p := testProfile()
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	first, _ := p.Digest()
	p.Runtime.CatalogDigest = strings.Repeat("d", 64)
	second, _ := p.Digest()
	if first == second {
		t.Fatal("catalog change did not invalidate profile identity")
	}
	for _, mutate := range []func(*Profile){
		func(p *Profile) { p.Limits.MaxRequests = 101 },
		func(p *Profile) { p.Limits.MaxDuration = 16 * time.Minute },
		func(p *Profile) { p.Limits.MaxLogBytes = 16*1024*1024 + 1 },
		func(p *Profile) { p.Limits.AutomaticRepairs = 1 },
		func(p *Profile) { p.TargetURL = "https://user:secret@target.test" },
		func(p *Profile) { p.TargetURL = "http://target.test" },
		func(p *Profile) { p.RequiredTools = []string{"oc_shell"} },
	} {
		p := testProfile()
		mutate(&p)
		if p.Validate() == nil {
			t.Fatalf("invalid profile accepted: %+v", p)
		}
	}
}

func TestProfileRejectsUnknownSchemaAndMissingRuntimePins(t *testing.T) {
	p := testProfile()
	p.Schema = "future"
	if p.Validate() == nil {
		t.Fatal("unknown schema accepted")
	}
	p = testProfile()
	p.Runtime.ImageDigest = "mutable:tag"
	if p.Validate() == nil {
		t.Fatal("mutable image accepted")
	}
}
