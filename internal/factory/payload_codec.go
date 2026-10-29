package factory

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/mitkox/esf/internal/secretfile"

	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
	"google.golang.org/protobuf/proto"
)

const encryptedPayloadEncoding = "binary/factory-aes256-gcm-v1"

// The keyring retains old keys for replay while active selects the write key.
// Keys never enter workflow arguments or Temporal history.
type payloadCodec struct {
	active string
	keys   map[string]cipher.AEAD
}

func loadPayloadCodec(path string) (*payloadCodec, error) {
	data, err := secretfile.Read(path, 1<<20)
	if err != nil {
		return nil, fmt.Errorf("read Temporal payload keyring: %w", err)
	}
	var ring struct {
		Active string            `json:"active"`
		Keys   map[string]string `json:"keys"`
	}
	if err := json.Unmarshal(data, &ring); err != nil {
		return nil, fmt.Errorf("invalid Temporal payload keyring JSON")
	}
	c := &payloadCodec{active: ring.Active, keys: map[string]cipher.AEAD{}}
	for id, encoded := range ring.Keys {
		key, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil || len(key) != 32 || id == "" {
			return nil, fmt.Errorf("payload key %q must be a base64-encoded 32-byte key with a nonempty ID", id)
		}
		block, err := aes.NewCipher(key)
		if err != nil {
			return nil, err
		}
		aead, err := cipher.NewGCMWithRandomNonce(block)
		if err != nil {
			return nil, err
		}
		c.keys[id] = aead
	}
	if c.keys[c.active] == nil {
		return nil, fmt.Errorf("active Temporal payload key is missing")
	}
	return c, nil
}

func (c *payloadCodec) Encode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	out := make([]*commonpb.Payload, len(payloads))
	for i, p := range payloads {
		data, err := proto.Marshal(p)
		if err != nil {
			return nil, err
		}
		out[i] = &commonpb.Payload{
			Metadata: map[string][]byte{converter.MetadataEncoding: []byte(encryptedPayloadEncoding), "key_id": []byte(c.active)},
			Data:     c.keys[c.active].Seal(nil, nil, data, []byte(encryptedPayloadEncoding+":"+c.active)),
		}
	}
	return out, nil
}

func (c *payloadCodec) Decode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	out := make([]*commonpb.Payload, len(payloads))
	for i, p := range payloads {
		if string(p.Metadata[converter.MetadataEncoding]) != encryptedPayloadEncoding {
			out[i] = p
			continue
		}
		id := string(p.Metadata["key_id"])
		aead := c.keys[id]
		if aead == nil {
			return nil, fmt.Errorf("Temporal payload key %q is unavailable", id)
		}
		data, err := aead.Open(nil, nil, p.Data, []byte(encryptedPayloadEncoding+":"+id))
		if err != nil {
			return nil, fmt.Errorf("Temporal payload authentication failed")
		}
		out[i] = &commonpb.Payload{}
		if err := proto.Unmarshal(data, out[i]); err != nil {
			return nil, fmt.Errorf("invalid decrypted Temporal payload")
		}
	}
	return out, nil
}
