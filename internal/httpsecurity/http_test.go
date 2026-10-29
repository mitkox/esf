package httpsecurity

import (
	"net/http/httptest"
	"testing"
)

func TestValidateListen(t *testing.T) {
	for _, address := range []string{"127.0.0.1:0", "127.1.2.3:8080", "[::1]:8080", "localhost:8080"} {
		if err := ValidateListen(address, false); err != nil {
			t.Errorf("%s: %v", address, err)
		}
	}
	for _, address := range []string{":8080", "0.0.0.0:8080", "[::]:8080", "192.168.1.1:8080", "example.com:8080"} {
		if err := ValidateListen(address, false); err == nil {
			t.Errorf("plaintext accepted %s", address)
		}
		if err := ValidateListen(address, true); err != nil {
			t.Errorf("TLS refused %s: %v", address, err)
		}
	}
	for _, address := range []string{"", "localhost", "127.0.0.1", "::1"} {
		if err := ValidateListen(address, true); err == nil {
			t.Errorf("malformed address accepted: %s", address)
		}
	}
}

func TestBearerRejectsAmbiguousCredentials(t *testing.T) {
	for _, tc := range []struct {
		name    string
		headers []string
		want    bool
	}{
		{"missing", nil, false}, {"raw token", []string{"secret"}, false},
		{"basic", []string{"Basic secret"}, false}, {"empty", []string{"Bearer "}, false},
		{"valid", []string{"Bearer secret"}, true}, {"case insensitive", []string{"bearer secret"}, true},
		{"duplicate", []string{"Bearer secret", "Bearer other"}, false},
		{"comma joined", []string{"Bearer secret,Bearer other"}, false},
		{"extra field", []string{"Bearer secret extra"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/?access_token=secret", nil)
			for _, v := range tc.headers {
				r.Header.Add("Authorization", v)
			}
			if got := Authenticated(r, "secret"); got != tc.want {
				t.Fatalf("authenticated=%v want %v", got, tc.want)
			}
			if Authenticated(r, "") {
				t.Fatal("empty expected credential accepted")
			}
		})
	}
}

func TestBearerTokenGrammarMatchesAuthentication(t *testing.T) {
	for _, token := range []string{"Abc012-._~+/", "abc=="} {
		if !ValidBearerToken(token) {
			t.Fatalf("valid token rejected: %q", token)
		}
		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("Authorization", "Bearer "+token)
		if !Authenticated(r, token) {
			t.Fatalf("accepted token unusable: %q", token)
		}
	}
	for _, token := range []string{"", "=", "a=b", "a,b", "a b", "a\u00a0b", "a\x00b", "a\x7fb", "a:b"} {
		if ValidBearerToken(token) {
			t.Fatalf("invalid token accepted: %q", token)
		}
	}
}
