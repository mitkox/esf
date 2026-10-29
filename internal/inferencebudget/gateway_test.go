package inferencebudget

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func account() Account {
	return Account{ID: "run-base", Scope: "scope", Model: "test-model", LimitMicros: 22000, Expires: time.Now().Add(time.Hour), MaxInputTokens: 10000, MaxOutputTokens: 1000, InputPrice: 1000000, OutputPrice: 1000000, RouteDigest: "sha256:" + strings.Repeat("a", 64), PricingEvidence: "sha256:" + strings.Repeat("b", 64), MaxRequests: 8}
}
func TestConcurrentReservationsAndCrashRecovery(t *testing.T) {
	dir := t.TempDir()
	l, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	a := account()
	a.LimitMicros = a.WorstCase()
	if e = l.Create(a); e != nil {
		t.Fatal(e)
	}
	var accepted atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, e := l.Reserve(a.ID, fmt.Sprint(i), time.Now()); e == nil {
				accepted.Add(1)
			}
		}(i)
	}
	wg.Wait()
	if accepted.Load() != 1 {
		t.Fatalf("admitted %d concurrent reservations", accepted.Load())
	}
	l.Close()
	l, e = Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	old, e := l.Get(a.ID)
	if e != nil || old.Spent() != a.LimitMicros {
		t.Fatal("pending cost lost on restart", old, e)
	}
	if _, e = l.Reserve(a.ID, "restart", time.Now()); e == nil {
		t.Fatal("crash refunded uncertain request")
	}
	if e = l.Create(a); e != nil {
		t.Fatal(e)
	}
	old, _ = l.Get(a.ID)
	if old.Spent() != a.LimitMicros {
		t.Fatal("idempotent create reset spending")
	}
	a.LimitMicros++
	if l.Create(a) == nil {
		t.Fatal("conflicting account reopened")
	}
}
func TestGatewayTokenAndUsageFailures(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		stream     bool
		want       int
	}{
		{"complete", `{"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":5}}`, false, 200},
		{"stream", "data: {\"choices\":[]}\n\ndata: {\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":5}}\n\ndata: [DONE]\n\n", true, 200},
		{"missing usage", `{"choices":[]}`, false, 502},
		{"truncated stream", "data: {\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":5}}\n", true, 502},
		{"data after terminal", "data: [DONE]\n\ndata: {\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":5}}\n", true, 502},
		{"conflicting usage", "data: {\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":5}}\n\ndata: {\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":0}}\n\ndata: [DONE]\n", true, 502},
		{"over bound", `{"usage":{"prompt_tokens":10001,"completion_tokens":5}}`, false, 502},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l, e := Open(t.TempDir())
			if e != nil {
				t.Fatal(e)
			}
			defer l.Close()
			a := account()
			if e = l.Create(a); e != nil {
				t.Fatal(e)
			}
			up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer provider-secret" {
					t.Error("provider key missing")
				}
				var body map[string]any
				json.NewDecoder(r.Body).Decode(&body)
				if body["model"] != "test-model" {
					t.Error("model changed")
				}
				if tc.stream && body["stream_options"].(map[string]any)["include_usage"] != true {
					t.Error("usage not requested")
				}
				if body["max_tokens"] != float64(1000) {
					t.Error("output cap not enforced")
				}
				io.WriteString(w, tc.body)
			}))
			defer up.Close()
			g := &Gateway{Ledger: l, TokenKey: []byte(strings.Repeat("k", 32)), AdminKey: strings.Repeat("a", 32), Upstream: up.URL, UpstreamKey: "provider-secret", Client: up.Client()}
			srv := httptest.NewServer(g)
			defer srv.Close()
			r, e := http.NewRequestWithContext(context.Background(), "POST", srv.URL+"/v1/chat/completions", strings.NewReader(fmt.Sprintf(`{"model":"test-model","messages":[{"role":"user","content":"hello"}],"stream":%t,"max_tokens":999999}`, tc.stream)))
			if e != nil {
				t.Fatal(e)
			}
			r.Header.Set("Authorization", "Bearer "+Token(g.TokenKey, a.ID))
			resp, e := srv.Client().Do(r)
			if e != nil {
				t.Fatal(e)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tc.want {
				b, _ := io.ReadAll(resp.Body)
				t.Fatalf("status %d: %s", resp.StatusCode, b)
			}
			got, _ := l.Get(a.ID)
			if tc.want == 200 {
				if got.Spent() != 15 || got.Revoked {
					t.Fatal("valid usage charged incorrectly", got)
				}
			} else {
				if !got.Revoked || got.Spent() != a.WorstCase() {
					t.Fatal("unknown usage was refunded", got)
				}
			}
		})
	}
	secret := []byte(strings.Repeat("s", 32))
	tok := Token(secret, "tenant-a")
	if id, e := TokenID(secret, tok); e != nil || id != "tenant-a" {
		t.Fatal("valid token rejected")
	}
	if _, e := TokenID(secret, strings.Replace(tok, "tenant-a", "tenant-b", 1)); e == nil {
		t.Fatal("cross-tenant token accepted")
	}
}

func TestLedgerProcessHelper(t *testing.T) {
	dir := os.Getenv("ESF_LEDGER_CHILD_DIR")
	if dir == "" {
		t.Skip("subprocess helper")
	}
	l, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	if _, e = l.Reserve("run-base", fmt.Sprint(os.Getpid()), time.Now()); e == nil {
		fmt.Println("RESERVATION_ACCEPTED")
	}
}

func TestBudgetReservationsAreSerializedAcrossProcesses(t *testing.T) {
	dir := t.TempDir()
	l, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	a := account()
	a.LimitMicros = a.WorstCase()
	if e = l.Create(a); e != nil {
		t.Fatal(e)
	}
	bin, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	var accepted atomic.Int32
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cmd := exec.Command(bin, "-test.run=^TestLedgerProcessHelper$")
			cmd.Env = append(os.Environ(), "ESF_LEDGER_CHILD_DIR="+dir)
			b, e := cmd.CombinedOutput()
			if e != nil {
				t.Errorf("ledger process: %v %s", e, b)
			}
			if strings.Contains(string(b), "RESERVATION_ACCEPTED") {
				accepted.Add(1)
			}
		}()
	}
	wg.Wait()
	if accepted.Load() != 1 {
		t.Fatalf("accepted %d processes against a one-call limit", accepted.Load())
	}
}
func TestGatewayRejectsUnqualifiedRequestBeforeForwarding(t *testing.T) {
	l, _ := Open(t.TempDir())
	defer l.Close()
	a := account()
	l.Create(a)
	g := &Gateway{Ledger: l, TokenKey: []byte(strings.Repeat("s", 32))}
	for _, body := range []string{`{"model":"different","messages":[]}`, `{"model":"test-model","messages":[{"content":[{"type":"image_url","image_url":"x"}]}]}`, `{"model":"test-model","messages":[],"unknown":true}`, `{"model":"test-model","messages":[{"role":"user","content":"hello"}],"store":true}`} {
		r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+Token(g.TokenKey, a.ID))
		w := httptest.NewRecorder()
		g.ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	got, _ := l.Get(a.ID)
	if len(got.Calls) != 0 {
		t.Fatal("invalid requests consumed reservations")
	}
}

func TestGatewayRejectsRawAndDuplicateCredentialsBeforeLedger(t *testing.T) {
	key := []byte(strings.Repeat("k", 32))
	token := Token(key, "run-base")
	gateway := &Gateway{TokenKey: key, AdminKey: strings.Repeat("a", 32)}
	for _, tc := range []struct {
		path    string
		headers []string
	}{
		{"/v1/chat/completions", []string{token}},
		{"/v1/chat/completions", []string{"Bearer " + token, "Bearer " + token}},
		{"/control/accounts/run-base", []string{"Bearer " + gateway.AdminKey, "Bearer " + gateway.AdminKey}},
	} {
		r := httptest.NewRequest("POST", tc.path, strings.NewReader("{}"))
		for _, v := range tc.headers {
			r.Header.Add("Authorization", v)
		}
		w := httptest.NewRecorder()
		gateway.ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("%s status=%d", tc.path, w.Code)
		}
	}
	empty := &Gateway{}
	r := httptest.NewRequest("GET", "/control/accounts/run-base", nil)
	r.Header.Set("Authorization", "Bearer ")
	w := httptest.NewRecorder()
	empty.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("empty control key status=%d", w.Code)
	}
}

func TestGatewayRejectsUnusableAdminTokens(t *testing.T) {
	g := Gateway{AdminKey: strings.Repeat("a", 32), TokenKey: []byte(strings.Repeat("k", 32)), Upstream: "https://provider.invalid/v1", UpstreamKey: "secret", Model: "test", InputPrice: 1, OutputPrice: 1, PricingEvidence: "evidence"}
	g.RouteDigest = RouteDigest(g.Upstream, g.Model, g.InputPrice, g.OutputPrice, g.PricingEvidence)
	if err := g.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{strings.Repeat("a", 32) + ",", strings.Repeat("a", 16) + "\u00a0" + strings.Repeat("b", 16)} {
		g.AdminKey = token
		if err := g.Validate(); err == nil {
			t.Fatal("unusable admin token accepted")
		}
	}
}
