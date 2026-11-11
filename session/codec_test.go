package session_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ostendorf-cloud/web-kit/session"
)

// key is a signing key of the required length. A real one is generated, not
// written down.
var key = []byte("0123456789abcdef0123456789abcdef")

// inFlight is the shape the oidc flow puts into a Codec: what has to survive
// the redirect to the identity provider before a session exists.
type inFlight struct {
	State    string `json:"st"`
	Nonce    string `json:"n"`
	Verifier string `json:"cv"`
	Return   string `json:"r"`
}

func newCodec(t *testing.T, now *time.Time) *session.Codec {
	t.Helper()

	codec, err := session.NewCodec(session.CodecConfig{Key: key, Now: func() time.Time { return *now }})
	if err != nil {
		t.Fatalf("NewCodec: %v", err)
	}
	return codec
}

func TestNewCodecRefusesAShortKey(t *testing.T) {
	for _, length := range []int{0, 1, session.MinKeyBytes - 1} {
		if _, err := session.NewCodec(session.CodecConfig{Key: make([]byte, length)}); err == nil {
			t.Errorf("a %d byte key was accepted", length)
		}
	}
	if _, err := session.NewCodec(session.CodecConfig{Key: make([]byte, session.MinKeyBytes)}); err != nil {
		t.Errorf("a key of the minimum length was refused: %v", err)
	}
}

func TestCodecRoundTrip(t *testing.T) {
	now := start
	codec := newCodec(t, &now)
	want := inFlight{State: "abc", Nonce: "def", Verifier: "xyz", Return: "/dashboard"}

	value, err := codec.Encode(want, 10*time.Minute)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	var got inFlight
	if err := codec.Decode(value, &got); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got != want {
		t.Errorf("value = %+v, want %+v", got, want)
	}
}

func TestCodecRefusesWhatItDidNotSign(t *testing.T) {
	now := start
	codec := newCodec(t, &now)

	value, err := codec.Encode(inFlight{State: "abc"}, time.Minute)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	payload, signature, _ := strings.Cut(value, ".")

	cases := map[string]string{
		"no signature":         payload,
		"empty signature":      payload + ".",
		"empty payload":        "." + signature,
		"changed payload":      payload[:len(payload)-2] + "AA." + signature,
		"changed signature":    payload + "." + signature[:len(signature)-2] + "AA",
		"signature not base64": payload + ".not base64",
		"nonsense":             "not-a-signed-value",
		"empty":                "",
		"separator only":       ".",
		"over the size limit":  strings.Repeat("a", session.MaxEncodedBytes+1),
	}
	for name, broken := range cases {
		t.Run(name, func(t *testing.T) {
			var got inFlight
			if err := codec.Decode(broken, &got); !errors.Is(err, session.ErrInvalid) {
				t.Errorf("Decode = %v, want ErrInvalid", err)
			}
			if got != (inFlight{}) {
				t.Errorf("a rejected value was still unmarshalled: %+v", got)
			}
		})
	}
}

func TestCodecRefusesAnotherKeysValue(t *testing.T) {
	now := start
	codec := newCodec(t, &now)

	value, err := codec.Encode(inFlight{State: "abc"}, time.Minute)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	other, err := session.NewCodec(session.CodecConfig{
		Key: []byte("fedcba9876543210fedcba9876543210"),
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewCodec: %v", err)
	}

	var got inFlight
	if err := other.Decode(value, &got); !errors.Is(err, session.ErrInvalid) {
		t.Errorf("another key accepted the value: %v", err)
	}
}

func TestCodecExpiry(t *testing.T) {
	now := start
	codec := newCodec(t, &now)

	value, err := codec.Encode(inFlight{State: "abc"}, 10*time.Minute)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	now = now.Add(9 * time.Minute)
	var got inFlight
	if err := codec.Decode(value, &got); err != nil {
		t.Fatalf("Decode before the expiry: %v", err)
	}

	// The expiry is inside the signed body, so it cannot be moved and the
	// value cannot be replayed later.
	now = now.Add(2 * time.Minute)
	if err := codec.Decode(value, &got); !errors.Is(err, session.ErrInvalid) {
		t.Errorf("an expired value was accepted: %v", err)
	}
}

func TestCodecRefusesANonPositiveLifetime(t *testing.T) {
	now := start
	codec := newCodec(t, &now)

	for _, lifetime := range []time.Duration{0, -time.Minute} {
		if _, err := codec.Encode(inFlight{State: "abc"}, lifetime); err == nil {
			t.Errorf("a lifetime of %s was accepted", lifetime)
		}
	}
}

func TestCodecRefusesAValueTooLargeForACookie(t *testing.T) {
	now := start
	codec := newCodec(t, &now)

	// A browser drops an over long cookie without a word, which would look
	// like a login that silently never completes.
	oversized := inFlight{State: strings.Repeat("x", session.MaxEncodedBytes)}
	if _, err := codec.Encode(oversized, time.Minute); err == nil {
		t.Error("a value too large for a cookie was accepted")
	}
}

func TestCodecRefusesWhatCannotBeEncoded(t *testing.T) {
	now := start
	codec := newCodec(t, &now)

	if _, err := codec.Encode(func() {}, time.Minute); err == nil {
		t.Error("a value JSON cannot represent was accepted")
	}
}

func TestCodecRefusesAPayloadOfTheWrongShape(t *testing.T) {
	now := start
	codec := newCodec(t, &now)

	value, err := codec.Encode([]string{"not", "an", "object"}, time.Minute)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	// Signed by this codec, so the signature verifies, and still not something
	// the caller can read back into its own type.
	var got inFlight
	if err := codec.Decode(value, &got); !errors.Is(err, session.ErrInvalid) {
		t.Errorf("Decode = %v, want ErrInvalid", err)
	}
}

func TestCodecCopiesItsKey(t *testing.T) {
	buf := make([]byte, session.MinKeyBytes)
	copy(buf, key)

	codec, err := session.NewCodec(session.CodecConfig{Key: buf, Now: func() time.Time { return start }})
	if err != nil {
		t.Fatalf("NewCodec: %v", err)
	}
	value, err := codec.Encode(inFlight{State: "abc"}, time.Minute)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	// A caller that zeroes or reuses its buffer must not change what the codec
	// signs with.
	for i := range buf {
		buf[i] = 0
	}

	var got inFlight
	if err := codec.Decode(value, &got); err != nil {
		t.Errorf("Decode after the caller's buffer changed: %v", err)
	}
}

func TestCodecUsesARealClockByDefault(t *testing.T) {
	codec, err := session.NewCodec(session.CodecConfig{Key: key})
	if err != nil {
		t.Fatalf("NewCodec: %v", err)
	}

	value, err := codec.Encode(inFlight{State: "abc"}, time.Minute)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	var got inFlight
	if err := codec.Decode(value, &got); err != nil {
		t.Errorf("Decode: %v", err)
	}
}
