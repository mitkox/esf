// Package inferencebudget reserves worst-case inference cost durably before a
// trusted gateway forwards a request. Pending/unknown calls retain their full
// reservation across crashes; they are never silently refunded.
package inferencebudget

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"syscall"
	"time"
)

var ID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,190}$`)
var ErrDenied = errors.New("inference budget admission denied")

type Call struct {
	Reserved int64  `json:"reserved"`
	Charged  int64  `json:"charged"`
	State    string `json:"state"`
	Input    int64  `json:"input"`
	Output   int64  `json:"output"`
}
type Account struct {
	ID              string          `json:"id"`
	Scope           string          `json:"scope"`
	Model           string          `json:"model"`
	LimitMicros     int64           `json:"limit_micros"`
	Expires         time.Time       `json:"expires"`
	MaxInputTokens  int64           `json:"max_input_tokens"`
	MaxOutputTokens int64           `json:"max_output_tokens"`
	InputPrice      int64           `json:"input_micros_per_million"`
	OutputPrice     int64           `json:"output_micros_per_million"`
	RouteDigest     string          `json:"route_digest"`
	PricingEvidence string          `json:"pricing_evidence"`
	MaxRequests     int             `json:"max_requests"`
	Revoked         bool            `json:"revoked"`
	Calls           map[string]Call `json:"calls,omitempty"`
}

func (a Account) Validate() error {
	if !ID.MatchString(a.ID) || a.Scope == "" || a.Model == "" || a.LimitMicros <= 0 || a.LimitMicros > 1e12 || a.MaxInputTokens < 1 || a.MaxInputTokens > 1e6 || a.MaxOutputTokens < 1 || a.MaxOutputTokens > 1e6 || a.InputPrice <= 0 || a.OutputPrice <= 0 || a.InputPrice > 1e9 || a.OutputPrice > 1e9 || a.MaxRequests < 1 || a.MaxRequests > 256 || !regexp.MustCompile(`^sha256:[a-f0-9]{64}$`).MatchString(a.RouteDigest) || !regexp.MustCompile(`^sha256:[a-f0-9]{64}$`).MatchString(a.PricingEvidence) {
		return errors.New("invalid bounded inference account or unqualified pricing route")
	}
	return nil
}
func cost(input, output int64, a Account) int64 {
	return (input*a.InputPrice+999999)/1000000 + (output*a.OutputPrice+999999)/1000000
}
func (a Account) WorstCase() int64 { return cost(a.MaxInputTokens, a.MaxOutputTokens, a) }
func (a Account) Spent() int64 {
	var n int64
	for _, c := range a.Calls {
		if c.State == "settled" {
			n += c.Charged
		} else {
			n += c.Reserved
		}
	}
	return n
}
func Token(secret []byte, id string) string {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte("esf-inference-budget/v1/" + id))
	return id + "." + hex.EncodeToString(m.Sum(nil))
}
func TokenID(secret []byte, token string) (string, error) {
	if len(token) < 66 {
		return "", ErrDenied
	}
	id := token[:len(token)-65]
	if !ID.MatchString(id) || !hmac.Equal([]byte(Token(secret, id)), []byte(token)) {
		return "", ErrDenied
	}
	return id, nil
}

type Ledger struct{ root *os.Root }

func Open(dir string) (*Ledger, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	r, e := os.OpenRoot(dir)
	return &Ledger{root: r}, e
}
func (l *Ledger) Close() error { return l.root.Close() }
func (l *Ledger) update(id string, fn func(*Account) error) error {
	if !ID.MatchString(id) {
		return ErrDenied
	}
	lock, e := l.root.OpenFile(id+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return e
	}
	defer lock.Close()
	if e = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); e != nil {
		return e
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	var a Account
	b, e := l.root.ReadFile(id + ".json")
	if e != nil && !errors.Is(e, os.ErrNotExist) {
		return e
	}
	if e == nil {
		if e = json.Unmarshal(b, &a); e != nil {
			return e
		}
	}
	if e = fn(&a); e != nil {
		return e
	}
	b, e = json.Marshal(a)
	if e != nil {
		return e
	}
	f, e := l.root.OpenFile(id+".pending", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	_, e = f.Write(b)
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	if e != nil {
		return e
	}
	if e = l.root.Rename(id+".pending", id+".json"); e != nil {
		return e
	}
	d, e := l.root.Open(".")
	if e != nil {
		return e
	}
	defer d.Close()
	return d.Sync()
}
func (l *Ledger) Create(a Account) error {
	if e := a.Validate(); e != nil {
		return e
	}
	if len(a.Calls) != 0 || a.Revoked {
		return ErrDenied
	}
	return l.update(a.ID, func(old *Account) error {
		if old.ID != "" {
			copy := *old
			copy.Calls = nil
			copy.Revoked = false
			if string(mustJSON(copy)) != string(mustJSON(a)) {
				return fmt.Errorf("account identity conflict")
			}
			return nil
		}
		*old = a
		return nil
	})
}
func mustJSON(a any) []byte { b, _ := json.Marshal(a); return b }
func (l *Ledger) Get(id string) (Account, error) {
	var out Account
	e := l.update(id, func(a *Account) error {
		if a.ID == "" {
			return ErrDenied
		}
		out = *a
		return nil
	})
	return out, e
}
func (l *Ledger) Reserve(id, request string, now time.Time) (Account, error) {
	var out Account
	e := l.update(id, func(a *Account) error {
		if a.ID == "" || a.Revoked || !now.Before(a.Expires) || len(a.Calls) >= a.MaxRequests {
			return ErrDenied
		}
		if e := a.Validate(); e != nil {
			return e
		}
		if _, ok := a.Calls[request]; ok {
			return errors.New("duplicate gateway request identity")
		}
		if a.Spent()+a.WorstCase() > a.LimitMicros {
			return ErrDenied
		}
		if a.Calls == nil {
			a.Calls = map[string]Call{}
		}
		a.Calls[request] = Call{Reserved: a.WorstCase(), State: "pending"}
		out = *a
		return nil
	})
	return out, e
}
func (l *Ledger) Settle(id, request string, input, output int64, known bool) error {
	return l.update(id, func(a *Account) error {
		c, ok := a.Calls[request]
		if !ok || c.State != "pending" {
			return errors.New("reservation missing or already settled")
		}
		if !known || input < 0 || output < 0 || input > a.MaxInputTokens || output > a.MaxOutputTokens {
			c.State = "unknown"
			a.Revoked = true
		} else {
			c.State = "settled"
			c.Input = input
			c.Output = output
			c.Charged = cost(input, output, *a)
		}
		a.Calls[request] = c
		return nil
	})
}
func (l *Ledger) Revoke(id string) error {
	return l.update(id, func(a *Account) error {
		if a.ID == "" {
			return ErrDenied
		}
		a.Revoked = true
		return nil
	})
}
