package session

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// MinKeyBytes is the shortest signing key a [Codec] accepts. The key is
// generated rather than typed, so a short one is a mistake in the deployment,
// not a length to stretch to.
const MinKeyBytes = 32

// MaxEncodedBytes is the longest value [Codec.Encode] produces. A browser is
// only required to keep about 4 KiB per cookie, and it drops what is over the
// limit without saying so, which looks exactly like a login that silently never
// completes. Erroring at the point the value is built puts the problem where it
// can be seen.
const MaxEncodedBytes = 4096

// ErrInvalid covers every reason a signed value is not accepted: a signature
// that does not verify, a payload that does not parse, an expiry that has
// passed. They are deliberately one error. A caller can do nothing but start
// over, and distinguishing them would mean telling whoever supplied the value
// which part of it was wrong.
var ErrInvalid = errors.New("invalid signed value")

// Codec signs a short lived value into a cookie, with no store behind it.
//
// It exists for the one thing a session store is wrong for: the state, nonce
// and PKCE verifier of a login in flight. Those have to survive the redirect to
// the identity provider and back, and at that point there is no session to put
// them in and no id to look one up by. Signing them into a cookie that expires
// in minutes is the whole mechanism.
//
// Everything that belongs to an account that is already logged in goes into the
// [Store] instead, so it can be ended. A Codec cannot take back what it signed:
// the value stays valid until its expiry, which is why the expiry is short and
// why nothing long lived belongs in one.
//
// A Codec is safe for concurrent use.
type Codec struct {
	key []byte
	now func() time.Time
}

// CodecConfig configures a [Codec].
type CodecConfig struct {
	// Key signs the values. At least [MinKeyBytes] long. Required.
	//
	// It is unrelated to the session cookie, which is protected by its id
	// being unguessable and by the store, not by a signature.
	Key []byte

	// Now is the clock. Zero is [time.Now]. Tests replace it.
	Now func() time.Time
}

// NewCodec validates the configuration.
func NewCodec(cfg CodecConfig) (*Codec, error) {
	if len(cfg.Key) < MinKeyBytes {
		return nil, fmt.Errorf("session: codec key is %d bytes, at least %d are required", len(cfg.Key), MinKeyBytes)
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	// The key is copied: a caller that reuses or zeroes its buffer afterwards
	// must not be able to change what this signs with.
	key := make([]byte, len(cfg.Key))
	copy(key, cfg.Key)
	return &Codec{key: key, now: cfg.Now}, nil
}

// signed is the wire format. The expiry is inside the signed body, so it cannot
// be edited and the value cannot be replayed a day later.
type signed struct {
	Payload json.RawMessage `json:"p"`
	Expires int64           `json:"e"`
}

// Encode signs value with an expiry lifetime from now. lifetime has to be
// positive: a value that is already expired would only be rejected on the way
// back in.
func (c *Codec) Encode(value any, lifetime time.Duration) (string, error) {
	if lifetime <= 0 {
		return "", fmt.Errorf("session: codec lifetime %s is not positive", lifetime)
	}

	payload, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("session: encode signed value: %w", err)
	}
	body, err := json.Marshal(signed{Payload: payload, Expires: c.now().Add(lifetime).Unix()})
	if err != nil {
		return "", fmt.Errorf("session: encode signed value: %w", err)
	}

	encoded := base64.RawURLEncoding.EncodeToString(body)
	out := encoded + "." + base64.RawURLEncoding.EncodeToString(c.mac(encoded))
	if len(out) > MaxEncodedBytes {
		return "", fmt.Errorf("session: signed value is %d bytes, over the %d a cookie holds", len(out), MaxEncodedBytes)
	}
	return out, nil
}

// Decode verifies the signature and the expiry and unmarshals the payload into
// into. Every failure is [ErrInvalid].
func (c *Codec) Decode(value string, into any) error {
	if len(value) > MaxEncodedBytes {
		// Nothing this Codec produced is longer, so there is nothing to gain
		// from hashing a megabyte somebody else sent.
		return ErrInvalid
	}

	encoded, signature, found := strings.Cut(value, ".")
	if !found || encoded == "" || signature == "" {
		return ErrInvalid
	}
	given, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil {
		return ErrInvalid
	}
	// The signature is verified before the payload is decoded, so nothing a
	// stranger wrote is ever parsed, and with a constant time comparison, so
	// the time taken does not leak how much of a guessed signature was right.
	if !hmac.Equal(c.mac(encoded), given) {
		return ErrInvalid
	}

	body, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return ErrInvalid
	}
	var wrapper signed
	if err := json.Unmarshal(body, &wrapper); err != nil {
		return ErrInvalid
	}
	// The expiry is exclusive, the same bound both session timeouts apply: a
	// value at exactly its expiry second is over, not good for one last use.
	if wrapper.Expires == 0 || !c.now().Before(time.Unix(wrapper.Expires, 0)) {
		return ErrInvalid
	}
	if err := json.Unmarshal(wrapper.Payload, into); err != nil {
		return ErrInvalid
	}
	return nil
}

func (c *Codec) mac(encoded string) []byte {
	mac := hmac.New(sha256.New, c.key)
	mac.Write([]byte(encoded))
	return mac.Sum(nil)
}
