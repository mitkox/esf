package inferencebudget

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/mitkox/esf/internal/httpsecurity"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Gateway supports text-only OpenAI Chat Completions for the first OpenCode2
// qualification. Other protocols and media fail admission. Responses are
// buffered until terminal authoritative token usage is validated.
type Gateway struct {
	Ledger          *Ledger
	AdminKey        string
	TokenKey        []byte
	Upstream        string
	UpstreamKey     string
	Client          *http.Client
	RouteDigest     string
	PricingEvidence string
	InputPrice      int64
	OutputPrice     int64
	Model           string
}

func RouteDigest(upstream, model string, inputPrice, outputPrice int64, pricingEvidence string) string {
	b, _ := json.Marshal(struct {
		Protocol, Upstream, Model string
		InputPrice, OutputPrice   int64
		PricingEvidence           string
	}{"esf-text-chat/v1", strings.TrimRight(upstream, "/"), model, inputPrice, outputPrice, pricingEvidence})
	h := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(h[:])
}

func (g *Gateway) Validate() error {
	u, e := url.Parse(g.Upstream)
	if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || len(g.AdminKey) < 32 || !httpsecurity.ValidBearerToken(g.AdminKey) || len(g.TokenKey) < 32 || g.AdminKey == string(g.TokenKey) || g.UpstreamKey == "" || g.RouteDigest == "" || g.PricingEvidence == "" || g.InputPrice <= 0 || g.OutputPrice <= 0 {
		return errors.New("gateway requires HTTPS upstream, separate control/token keys, and qualified pricing")
	}
	if g.Model == "" || g.RouteDigest != RouteDigest(g.Upstream, g.Model, g.InputPrice, g.OutputPrice, g.PricingEvidence) {
		return errors.New("gateway route digest does not bind the upstream, model and pricing")
	}
	return nil
}
func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if strings.HasPrefix(r.URL.Path, "/control/accounts/") {
		g.control(w, r)
		return
	}
	if r.Method != "POST" || r.URL.Path != "/v1/chat/completions" || r.URL.RawQuery != "" {
		http.Error(w, "unsupported route", 404)
		return
	}
	token, ok := httpsecurity.Bearer(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	id, e := TokenID(g.TokenKey, token)
	if e != nil {
		http.Error(w, "unauthorized", 401)
		return
	}
	a, e := g.Ledger.Get(id)
	if e != nil {
		http.Error(w, "budget unavailable", 403)
		return
	}
	body, e := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if e != nil || len(body) >= 1<<20 {
		http.Error(w, "invalid request size", 400)
		return
	}
	var payload map[string]json.RawMessage
	if e = json.Unmarshal(body, &payload); e != nil {
		http.Error(w, "invalid request", 400)
		return
	}
	var model string
	json.Unmarshal(payload["model"], &model)
	// Text tokenization is bounded conservatively by encoded request bytes plus
	// protocol framing. Images/audio/video and token-expanding custom fields are
	// unsupported. Only a qualified tokenizer/provider route may use this bound.
	allowed := map[string]bool{"model": true, "messages": true, "tools": true, "tool_choice": true, "parallel_tool_calls": true, "temperature": true, "top_p": true, "stream": true, "stream_options": true, "max_tokens": true, "max_completion_tokens": true, "stop": true, "presence_penalty": true, "frequency_penalty": true, "seed": true, "response_format": true, "reasoning_effort": true, "store": true}
	for k := range payload {
		if !allowed[k] {
			http.Error(w, "unsupported request field", 400)
			return
		}
	}
	if store, ok := payload["store"]; ok && !bytes.Equal(bytes.TrimSpace(store), []byte("false")) {
		http.Error(w, "provider response storage is not allowed", 400)
		return
	}
	payload["store"] = json.RawMessage(`false`)
	if model != a.Model || g.Model != "" && model != g.Model || int64(len(body))+4096 > a.MaxInputTokens || bytes.Contains(body, []byte(`"image_url"`)) || bytes.Contains(body, []byte(`"input_audio"`)) || bytes.Contains(body, []byte(`"video_url"`)) {
		http.Error(w, "model or input token bound rejected", 400)
		return
	}
	var messages []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(payload["messages"], &messages) != nil || len(messages) == 0 || len(messages) > 128 {
		http.Error(w, "invalid messages", 400)
		return
	}
	for _, m := range messages {
		var content any
		if len(m.Content) > 0 && json.Unmarshal(m.Content, &content) != nil {
			http.Error(w, "invalid content", 400)
			return
		}
		switch c := content.(type) {
		case string, nil:
		case []any:
			for _, part := range c {
				p, ok := part.(map[string]any)
				if !ok || p["type"] != "text" {
					http.Error(w, "only text content is qualified", 400)
					return
				}
				if _, ok := p["text"].(string); !ok || len(p) != 2 {
					http.Error(w, "unsupported text part fields", 400)
					return
				}
			}
		default:
			http.Error(w, "unsupported content", 400)
			return
		}
	}
	capBytes, _ := json.Marshal(a.MaxOutputTokens)
	if v, ok := payload["max_completion_tokens"]; ok {
		var n int64
		if json.Unmarshal(v, &n) != nil || n <= 0 || n > a.MaxOutputTokens {
			payload["max_completion_tokens"] = capBytes
		}
		delete(payload, "max_tokens")
	} else {
		var n int64
		if json.Unmarshal(payload["max_tokens"], &n) != nil || n <= 0 || n > a.MaxOutputTokens {
			payload["max_tokens"] = capBytes
		}
	}
	var stream bool
	if b := payload["stream"]; len(b) > 0 && json.Unmarshal(b, &stream) != nil {
		http.Error(w, "invalid stream", 400)
		return
	}
	if stream {
		payload["stream_options"] = json.RawMessage(`{"include_usage":true}`)
	}
	body, _ = json.Marshal(payload)
	var nonce [16]byte
	rand.Read(nonce[:])
	callID := hex.EncodeToString(nonce[:])
	if _, e = g.Ledger.Reserve(id, callID, time.Now()); e != nil {
		http.Error(w, "budget denied", 429)
		return
	}
	settled := false
	defer func() {
		if !settled {
			_ = g.Ledger.Settle(id, callID, 0, 0, false)
		}
	}()
	ctx, cancel := context.WithDeadline(r.Context(), a.Expires)
	defer cancel()
	up, e := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(g.Upstream, "/")+"/chat/completions", bytes.NewReader(body))
	if e != nil {
		http.Error(w, "upstream unavailable", 502)
		return
	}
	up.Header.Set("Authorization", "Bearer "+g.UpstreamKey)
	up.Header.Set("Content-Type", "application/json")
	c := g.Client
	if c == nil {
		c = &http.Client{Timeout: 10 * time.Minute}
	}
	copyClient := *c
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, e := copyClient.Do(up)
	if e != nil {
		http.Error(w, "upstream unavailable", 502)
		return
	}
	defer resp.Body.Close()
	result, e := io.ReadAll(io.LimitReader(resp.Body, 32<<20+1))
	if e != nil || len(result) > 32<<20 || resp.StatusCode != 200 {
		http.Error(w, "upstream request incomplete", 502)
		return
	}
	input, output, known := usage(result, stream)
	if e = g.Ledger.Settle(id, callID, input, output, known); e != nil {
		http.Error(w, "usage persistence failed", 502)
		return
	}
	settled = true
	if !known || input > a.MaxInputTokens || output > a.MaxOutputTokens {
		http.Error(w, "authoritative usage unavailable or out of bounds", 502)
		return
	}
	if stream {
		w.Header().Set("Content-Type", "text/event-stream")
	} else {
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(200)
	_, _ = w.Write(result)
}
func usage(body []byte, stream bool) (int64, int64, bool) {
	var input, output int64
	found, terminal := false, !stream
	chunks := [][]byte{body}
	if stream {
		chunks = nil
		for _, line := range bytes.Split(body, []byte("\n")) {
			if bytes.HasPrefix(line, []byte("data:")) {
				v := bytes.TrimSpace(line[5:])
				if terminal {
					return 0, 0, false
				}
				if bytes.Equal(v, []byte("[DONE]")) {
					terminal = true
					continue
				}
				chunks = append(chunks, v)
			}
		}
	}
	for _, b := range chunks {
		var e struct {
			Error json.RawMessage `json:"error"`
			Usage *struct {
				Input  *int64 `json:"prompt_tokens"`
				Output *int64 `json:"completion_tokens"`
			} `json:"usage"`
		}
		if json.Unmarshal(b, &e) != nil || len(e.Error) > 0 && !bytes.Equal(bytes.TrimSpace(e.Error), []byte("null")) {
			return 0, 0, false
		}
		if e.Usage != nil && e.Usage.Input != nil && e.Usage.Output != nil {
			if found {
				return 0, 0, false
			}
			input, output = *e.Usage.Input, *e.Usage.Output
			found = input > 0 && output >= 0
		}
	}
	return input, output, found && terminal
}
func (g *Gateway) control(w http.ResponseWriter, r *http.Request) {
	if !httpsecurity.Authenticated(r, g.AdminKey) {
		http.Error(w, "unauthorized", 401)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/control/accounts/")
	if !ID.MatchString(id) {
		http.Error(w, "invalid account", 400)
		return
	}
	switch r.Method {
	case "PUT":
		var a Account
		d := json.NewDecoder(io.LimitReader(r.Body, 16385))
		d.DisallowUnknownFields()
		if d.Decode(&a) != nil || a.ID != id || a.RouteDigest != g.RouteDigest || a.PricingEvidence != g.PricingEvidence || a.InputPrice != g.InputPrice || a.OutputPrice != g.OutputPrice || a.Model != g.Model {
			http.Error(w, "account route or pricing mismatch", 400)
			return
		}
		if e := g.Ledger.Create(a); e != nil {
			http.Error(w, "account rejected", 409)
			return
		}
		w.WriteHeader(204)
	case "GET":
		a, e := g.Ledger.Get(id)
		if e != nil {
			http.Error(w, "account missing", 404)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(a)
	case "DELETE":
		if e := g.Ledger.Revoke(id); e != nil {
			http.Error(w, "account unavailable", 404)
			return
		}
		w.WriteHeader(204)
	default:
		http.Error(w, "unsupported method", 405)
	}
}

// Client is used only by trusted factory activities. No control key is staged.
type Client struct {
	URL, Key string
	HTTP     *http.Client
}

func (c Client) Do(ctx context.Context, method, id string, a *Account) (Account, error) {
	var out Account
	if !ID.MatchString(id) {
		return out, ErrDenied
	}
	var body io.Reader
	if a != nil {
		b, _ := json.Marshal(a)
		body = bytes.NewReader(b)
	}
	r, e := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.URL, "/")+"/control/accounts/"+id, body)
	if e != nil {
		return out, e
	}
	r.Header.Set("Authorization", "Bearer "+c.Key)
	r.Header.Set("Content-Type", "application/json")
	h := c.HTTP
	if h == nil {
		h = &http.Client{Timeout: 15 * time.Second}
	}
	copyClient := *h
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, e := copyClient.Do(r)
	if e != nil {
		return out, e
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 && resp.StatusCode != 204 {
		return out, fmt.Errorf("gateway control returned %d", resp.StatusCode)
	}
	if method == "GET" {
		e = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out)
	}
	return out, e
}
