package evidence

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
)

// Verify checks a bundle offline with only the public key and returns the
// statement it carries.
//
// The signature is checked before the payload is parsed, so unauthenticated
// bytes never reach the decoder. Unknown predicate fields are ignored; a
// different statement type or predicate major version fails closed.
func Verify(envelope *Envelope, publicKey ed25519.PublicKey) (*Statement, error) {
	if envelope == nil {
		return nil, errors.New("evidence: an envelope is required")
	}
	if envelope.PayloadType != PayloadType {
		return nil, fmt.Errorf("evidence: unsupported payload type %q, want %q", envelope.PayloadType, PayloadType)
	}
	if len(publicKey) != ed25519.PublicKeySize {
		return nil, errors.New("evidence: an ed25519 public key is required")
	}
	payload, err := base64.StdEncoding.DecodeString(envelope.Payload)
	if err != nil {
		return nil, fmt.Errorf("evidence: decode payload: %w", err)
	}
	if len(envelope.Signatures) == 0 {
		return nil, errors.New("evidence: envelope carries no signatures")
	}
	pae := preAuthenticationEncoding(envelope.PayloadType, payload)
	verified := false
	for _, signature := range envelope.Signatures {
		raw, err := base64.StdEncoding.DecodeString(signature.Sig)
		if err != nil {
			continue
		}
		if ed25519.Verify(publicKey, pae, raw) {
			verified = true
			break
		}
	}
	if !verified {
		return nil, errors.New("evidence: envelope payload does not match any signature for the supplied public key")
	}
	var statement Statement
	if err := json.Unmarshal(payload, &statement); err != nil {
		return nil, fmt.Errorf("evidence: decode statement: %w", err)
	}
	if statement.Type != StatementType {
		return nil, fmt.Errorf("evidence: unsupported statement type %q, want %q", statement.Type, StatementType)
	}
	if statement.PredicateType != PredicateType {
		return nil, fmt.Errorf("evidence: unsupported predicate type %q, want %q", statement.PredicateType, PredicateType)
	}
	return &statement, nil
}
