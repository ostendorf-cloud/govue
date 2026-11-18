// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package account

import (
	"encoding/base32"
	"errors"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
)

// rfcKey is the published test key of RFC 4226 appendix D and RFC 6238
// appendix B for SHA-1, ASCII "12345678901234567890". It is not a secret.
const rfcKey = "12345678901234567890"

// rfcBase32 is rfcKey in the base32 form a store holds.
const rfcBase32 = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"

// fakeSecret is a well formed secret that belongs to nobody.
const fakeSecret = "FAKESECRETFAKESECRETFAKESECRETFA"

// TestHOTP_RFC4226 checks the HOTP values and the truncated intermediate
// values of RFC 4226 appendix D.
func TestHOTP_RFC4226(t *testing.T) {
	t.Parallel()

	tests := []struct {
		counter   uint64
		truncated uint32
		want      string
	}{
		{0, 1284755224, "755224"},
		{1, 1094287082, "287082"},
		{2, 137359152, "359152"},
		{3, 1726969429, "969429"},
		{4, 1640338314, "338314"},
		{5, 868254676, "254676"},
		{6, 1918287922, "287922"},
		{7, 82162583, "162583"},
		{8, 673399871, "399871"},
		{9, 645520489, "520489"},
	}

	for _, tt := range tests {
		if got := truncate(hmacSHA1([]byte(rfcKey), tt.counter)); got != tt.truncated {
			t.Errorf("truncate(counter %d) = %d, want %d", tt.counter, got, tt.truncated)
		}
		if got := hotp([]byte(rfcKey), tt.counter, 6); got != tt.want {
			t.Errorf("hotp(counter %d) = %s, want %s", tt.counter, got, tt.want)
		}
	}
}

// TestTruncate covers the dynamic truncation of RFC 4226 section 5.3 with a
// crafted MAC whose selected byte has its high bit set, which no published
// vector exercises.
func TestTruncate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		sum  []byte
		want uint32
	}{
		{
			name: "RFC 4226 section 5.4 example",
			sum:  []byte{0x1f, 0x86, 0x98, 0x69, 0x0e, 0x02, 0xca, 0x16, 0x61, 0x85, 0x50, 0xef, 0x7f, 0x19, 0xda, 0x8e, 0x94, 0x5b, 0x55, 0x5a},
			want: 0x50ef7f19,
		},
		{
			name: "high bit of the first selected byte is masked",
			sum:  []byte{0, 0, 0, 0xff, 0xfe, 0xfd, 0xfc, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0x03},
			want: 0x7ffefdfc,
		},
		{
			name: "largest offset reaches the last four bytes",
			sum:  []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0x81, 0x02, 0x03, 0x04, 0x0f},
			want: 0x01020304,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := truncate(tt.sum); got != tt.want {
				t.Fatalf("truncate = %#x, want %#x", got, tt.want)
			}
		})
	}
}

// TestTOTP_RFC6238 checks the SHA-1 vectors of RFC 6238 appendix B, eight
// digits through hotp and the six digits this package uses through TOTPCode.
func TestTOTP_RFC6238(t *testing.T) {
	t.Parallel()

	tests := []struct {
		unix    int64
		counter int64
		want    string
	}{
		{59, 1, "94287082"},
		{1111111109, 37037036, "07081804"},
		{1111111111, 37037037, "14050471"},
		{1234567890, 41152263, "89005924"},
		{2000000000, 66666666, "69279037"},
		{20000000000, 666666666, "65353130"},
	}

	for _, tt := range tests {
		at := time.Unix(tt.unix, 0).UTC()

		if got := counterAt(at); got != tt.counter {
			t.Errorf("counterAt(%d) = %d, want %d", tt.unix, got, tt.counter)
		}
		if got := hotp([]byte(rfcKey), uint64(tt.counter), 8); got != tt.want {
			t.Errorf("hotp 8 digits at %d = %s, want %s", tt.unix, got, tt.want)
		}

		code, err := TOTPCode(rfcBase32, at)
		if err != nil {
			t.Fatalf("TOTPCode: %v", err)
		}
		if want := tt.want[2:]; code != want {
			t.Errorf("TOTPCode at %d = %s, want %s", tt.unix, code, want)
		}

		counter, ok := VerifyTOTPCode(rfcBase32, tt.want[2:], at)
		if !ok || counter != tt.counter {
			t.Errorf("VerifyTOTPCode at %d = %d, %v, want %d, true", tt.unix, counter, ok, tt.counter)
		}
	}
}

func TestGenerateTOTPSecret(t *testing.T) {
	t.Parallel()

	alphabet := regexp.MustCompile(`^[A-Z2-7]{32}$`)
	seen := map[string]bool{}

	for range 32 {
		secret, err := GenerateTOTPSecret()
		if err != nil {
			t.Fatalf("GenerateTOTPSecret: %v", err)
		}
		if !alphabet.MatchString(secret) {
			t.Fatalf("secret %q is not 32 unpadded base32 characters", secret)
		}

		raw, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
		if err != nil || len(raw) != 20 {
			t.Fatalf("secret decodes to %d bytes, %v, want 20 bytes, 160 bits", len(raw), err)
		}
		if seen[secret] {
			t.Fatal("GenerateTOTPSecret repeated a secret")
		}
		seen[secret] = true

		code, err := TOTPCode(secret, time.Unix(1_800_000_000, 0))
		if err != nil {
			t.Fatalf("TOTPCode(generated secret): %v", err)
		}
		if _, ok := VerifyTOTPCode(secret, code, time.Unix(1_800_000_000, 0)); !ok {
			t.Fatal("a generated secret does not verify its own code")
		}
	}
}

// codeAt is the six digit code of fakeSecret for one counter.
func codeAt(t *testing.T, counter int64) string {
	t.Helper()

	key, err := decodeTOTPSecret(fakeSecret)
	if err != nil {
		t.Fatalf("decodeTOTPSecret: %v", err)
	}

	return hotp(key, uint64(counter), 6)
}

// TestVerifyTOTPCode_window accepts the step of now and one step either side,
// nothing further, and returns the counter of the step that matched.
func TestVerifyTOTPCode_window(t *testing.T) {
	t.Parallel()

	const now = int64(1_800_000_000) // inside step 60000000
	nowCounter := now / 30

	tests := []struct {
		name    string
		counter int64
		wantOK  bool
	}{
		{name: "two steps behind", counter: nowCounter - 2},
		{name: "one step behind", counter: nowCounter - 1, wantOK: true},
		{name: "current step", counter: nowCounter, wantOK: true},
		{name: "one step ahead", counter: nowCounter + 1, wantOK: true},
		{name: "two steps ahead", counter: nowCounter + 2},
		{name: "far in the past", counter: nowCounter - 1000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			code := codeAt(t, tt.counter)

			counter, ok := VerifyTOTPCode(fakeSecret, code, time.Unix(now, 0))
			if ok != tt.wantOK {
				t.Fatalf("VerifyTOTPCode ok = %v, want %v", ok, tt.wantOK)
			}

			want := int64(0)
			if tt.wantOK {
				want = tt.counter
			}
			if counter != want {
				t.Fatalf("VerifyTOTPCode counter = %d, want %d", counter, want)
			}
		})
	}
}

// TestVerifyTOTPCode_stepEdges checks the window at the first and the last
// second of a step.
func TestVerifyTOTPCode_stepEdges(t *testing.T) {
	t.Parallel()

	const step = int64(60_000_000)

	tests := []struct {
		name    string
		unix    int64
		counter int64
		wantOK  bool
	}{
		{name: "first second, previous step", unix: step * 30, counter: step - 1, wantOK: true},
		{name: "first second, two steps back", unix: step * 30, counter: step - 2},
		{name: "last second, next step", unix: step*30 + 29, counter: step + 1, wantOK: true},
		{name: "last second, two steps ahead", unix: step*30 + 29, counter: step + 2},
		{name: "one second later, two steps back", unix: step*30 + 30, counter: step - 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			counter, ok := VerifyTOTPCode(fakeSecret, codeAt(t, tt.counter), time.Unix(tt.unix, 0))
			if ok != tt.wantOK || (ok && counter != tt.counter) {
				t.Fatalf("VerifyTOTPCode = %d, %v, want %d, %v", counter, ok, tt.counter, tt.wantOK)
			}
		})
	}
}

// TestVerifyTOTPCode_counterBelowOne refuses any step whose counter is below
// one. The store treats counter zero as an unconfirmed secret, so a code of
// that step must never reach it.
func TestVerifyTOTPCode_counterBelowOne(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		unix    int64
		counter int64
		wantOK  bool
	}{
		{name: "counter zero at the epoch", unix: 0, counter: 0},
		{name: "counter zero in step one", unix: 45, counter: 0},
		{name: "counter one at the epoch", unix: 0, counter: 1, wantOK: true},
		{name: "counter zero before the epoch", unix: -10, counter: 0},
		{name: "counter one ten seconds before the epoch", unix: -10, counter: 1},
		{name: "counter one long before the epoch", unix: -3600, counter: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			counter, ok := VerifyTOTPCode(fakeSecret, codeAt(t, tt.counter), time.Unix(tt.unix, 0))
			if ok != tt.wantOK {
				t.Fatalf("VerifyTOTPCode ok = %v, want %v", ok, tt.wantOK)
			}
			if !ok && counter != 0 {
				t.Fatalf("a refused code returned counter %d", counter)
			}
			if ok && counter < 1 {
				t.Fatalf("an accepted code returned counter %d", counter)
			}
		})
	}

	// An hour before the epoch the window is counters -121 to -119. Their
	// codes, taken as the unsigned counter a careless conversion would
	// hash, never match.
	key, err := decodeTOTPSecret(fakeSecret)
	if err != nil {
		t.Fatalf("decodeTOTPSecret: %v", err)
	}
	for _, negative := range []int64{-121, -120, -119} {
		code := hotp(key, uint64(negative), 6) // The wrap is the point of the case.
		if c, ok := VerifyTOTPCode(fakeSecret, code, time.Unix(-3600, 0)); ok {
			t.Fatalf("the code of counter %d matched as %d", negative, c)
		}
	}
}

func TestVerifyTOTPCode_refusals(t *testing.T) {
	t.Parallel()

	at := time.Unix(1_800_000_000, 0)
	good := codeAt(t, counterAt(at))

	tests := []struct {
		name   string
		secret string
		code   string
		wantOK bool
	}{
		{name: "the right code", secret: fakeSecret, code: good, wantOK: true},
		{name: "lower case secret", secret: strings.ToLower(fakeSecret), code: good, wantOK: true},
		{name: "empty code", secret: fakeSecret, code: ""},
		{name: "five digits", secret: fakeSecret, code: good[:5]},
		{name: "seven digits", secret: fakeSecret, code: good + "0"},
		{name: "leading space", secret: fakeSecret, code: " " + good},
		{name: "trailing newline", secret: fakeSecret, code: good + "\n"},
		{name: "letters", secret: fakeSecret, code: "abcdef"},
		{name: "full width digits", secret: fakeSecret, code: "１２３４５６"},
		{name: "empty secret", secret: "", code: good},
		{name: "padded secret", secret: fakeSecret + "========", code: good},
		{name: "secret with a space", secret: fakeSecret[:16] + " " + fakeSecret[16:], code: good},
		{name: "secret with a newline", secret: fakeSecret[:16] + "\n" + fakeSecret[16:], code: good},
		{name: "invalid secret with the dummy key's code", secret: "", code: hotp(dummyTOTPKey[:], uint64(counterAt(at)), 6)},
		{name: "secret not base32", secret: "FAKE1SECRET0FAKE1SECRET0FAKE1SEC", code: good},
		{name: "secret below 128 bits", secret: fakeSecret[:24], code: codeAt(t, counterAt(at))},
		{name: "secret above 512 bits", secret: strings.Repeat("A", 104), code: good},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			counter, ok := VerifyTOTPCode(tt.secret, tt.code, at)
			if ok != tt.wantOK {
				t.Fatalf("VerifyTOTPCode ok = %v, want %v", ok, tt.wantOK)
			}
			if !ok && counter != 0 {
				t.Fatalf("a refused code returned counter %d", counter)
			}
		})
	}
}

// TestVerifyTOTPCode_128BitSecret accepts the shortest secret RFC 4226
// allows. Its 26 base32 characters are not a multiple of eight, so it only
// decodes without padding.
func TestVerifyTOTPCode_128BitSecret(t *testing.T) {
	t.Parallel()

	secret := fakeSecret[:26] // 16 bytes, 128 bits
	at := time.Unix(1_800_000_000, 0)

	code, err := TOTPCode(secret, at)
	if err != nil {
		t.Fatalf("TOTPCode(128 bit secret): %v", err)
	}
	if counter, ok := VerifyTOTPCode(secret, code, at); !ok || counter != counterAt(at) {
		t.Fatalf("VerifyTOTPCode(128 bit secret) = %d, %v, want %d, true", counter, ok, counterAt(at))
	}

	uri, err := TOTPProvisioningURI("Example", "alice", secret)
	if err != nil {
		t.Fatalf("TOTPProvisioningURI(128 bit secret): %v", err)
	}
	if !strings.Contains(uri, "secret="+secret+"&") {
		t.Fatalf("URI %q does not carry the unpadded secret", uri)
	}
}

// TestVerifyTOTPCode_shortSecretCode makes sure a secret below 128 bits is
// refused even with the code it really produces.
func TestVerifyTOTPCode_shortSecretCode(t *testing.T) {
	t.Parallel()

	short := fakeSecret[:24] // 15 bytes, 120 bits
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(short)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	at := time.Unix(1_800_000_000, 0)
	code := hotp(key, uint64(counterAt(at)), 6)

	if _, ok := VerifyTOTPCode(short, code, at); ok {
		t.Fatal("a 120 bit secret was accepted")
	}
	if _, err := TOTPCode(short, at); !errors.Is(err, ErrTOTPSecretInvalid) {
		t.Fatalf("TOTPCode(short) = %v, want ErrTOTPSecretInvalid", err)
	}
}

func TestTOTPProvisioningURI(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		issuer      string
		accountName string
		wantLabel   string
		wantIssuer  string
	}{
		{
			name:        "plain",
			issuer:      "Example",
			accountName: "alice@example.com",
			wantLabel:   "Example:alice@example.com",
			wantIssuer:  "Example",
		},
		{
			name:        "spaces",
			issuer:      "ACME Co",
			accountName: "Alice Example",
			wantLabel:   "ACME%20Co:Alice%20Example",
			wantIssuer:  "ACME%20Co",
		},
		{
			name:        "plus sign",
			issuer:      "A+B",
			accountName: "alice+tag@example.com",
			wantLabel:   "A%2BB:alice%2Btag@example.com",
			wantIssuer:  "A%2BB",
		},
		{
			name:        "query and fragment delimiters",
			issuer:      "a&b=c",
			accountName: "x?y#z/w%v",
			wantLabel:   "a%26b%3Dc:x%3Fy%23z%2Fw%25v",
			wantIssuer:  "a%26b%3Dc",
		},
		{
			name:        "at sign in the issuer",
			issuer:      "ops@example",
			accountName: "alice",
			wantLabel:   "ops@example:alice",
			wantIssuer:  "ops%40example",
		},
		{
			name:        "non ASCII",
			issuer:      "Bücher",
			accountName: "jörg@example.com",
			wantLabel:   "B%C3%BCcher:j%C3%B6rg@example.com",
			wantIssuer:  "B%C3%BCcher",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := TOTPProvisioningURI(tt.issuer, tt.accountName, fakeSecret)
			if err != nil {
				t.Fatalf("TOTPProvisioningURI: %v", err)
			}

			want := "otpauth://totp/" + tt.wantLabel +
				"?secret=" + fakeSecret +
				"&issuer=" + tt.wantIssuer +
				"&algorithm=SHA1&digits=6&period=30"
			if got != want {
				t.Fatalf("TOTPProvisioningURI =\n%s\nwant\n%s", got, want)
			}

			// A standard parser reads back exactly what went in.
			u, err := url.Parse(got)
			if err != nil {
				t.Fatalf("url.Parse: %v", err)
			}
			if u.Scheme != "otpauth" || u.Host != "totp" || u.Fragment != "" {
				t.Fatalf("parsed scheme %q host %q fragment %q", u.Scheme, u.Host, u.Fragment)
			}
			if u.Path != "/"+tt.issuer+":"+tt.accountName {
				t.Fatalf("parsed label %q, want %q", u.Path, "/"+tt.issuer+":"+tt.accountName)
			}

			q := u.Query()
			if q.Get("issuer") != tt.issuer || q.Get("secret") != fakeSecret || len(q) != 5 {
				t.Fatalf("parsed query %v", q)
			}
		})
	}
}

func TestTOTPProvisioningURI_canonicalSecret(t *testing.T) {
	t.Parallel()

	got, err := TOTPProvisioningURI("Example", "alice", strings.ToLower(fakeSecret))
	if err != nil {
		t.Fatalf("TOTPProvisioningURI: %v", err)
	}
	if !strings.Contains(got, "secret="+fakeSecret+"&") {
		t.Fatalf("URI %q does not carry the upper case secret", got)
	}
}

func TestTOTPProvisioningURI_refusals(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		issuer      string
		accountName string
		secret      string
		wantErr     error
	}{
		{name: "empty issuer", issuer: "", accountName: "alice", secret: fakeSecret, wantErr: ErrTOTPLabelInvalid},
		{name: "empty account name", issuer: "Example", accountName: "", secret: fakeSecret, wantErr: ErrTOTPLabelInvalid},
		{name: "colon in the issuer", issuer: "Ex:ample", accountName: "alice", secret: fakeSecret, wantErr: ErrTOTPLabelInvalid},
		{name: "colon in the account name", issuer: "Example", accountName: "ali:ce", secret: fakeSecret, wantErr: ErrTOTPLabelInvalid},
		{name: "newline", issuer: "Example", accountName: "alice\n", secret: fakeSecret, wantErr: ErrTOTPLabelInvalid},
		{name: "delete character", issuer: "Exa\x7fmple", accountName: "alice", secret: fakeSecret, wantErr: ErrTOTPLabelInvalid},
		{name: "right to left override", issuer: "Example", accountName: "alice" + string(rune(0x202e)) + "moc.elpmaxe", secret: fakeSecret, wantErr: ErrTOTPLabelInvalid},
		{name: "invalid UTF-8", issuer: "Example", accountName: "alice\xff", secret: fakeSecret, wantErr: ErrTOTPLabelInvalid},
		{name: "issuer too long", issuer: strings.Repeat("e", 65), accountName: "alice", secret: fakeSecret, wantErr: ErrTOTPLabelInvalid},
		{name: "account name too long", issuer: "Example", accountName: strings.Repeat("a", 255), secret: fakeSecret, wantErr: ErrTOTPLabelInvalid},
		{name: "empty secret", issuer: "Example", accountName: "alice", secret: "", wantErr: ErrTOTPSecretInvalid},
		{name: "padded secret", issuer: "Example", accountName: "alice", secret: fakeSecret + "========", wantErr: ErrTOTPSecretInvalid},
		{name: "short secret", issuer: "Example", accountName: "alice", secret: fakeSecret[:24], wantErr: ErrTOTPSecretInvalid},
		{name: "secret with a query delimiter", issuer: "Example", accountName: "alice", secret: fakeSecret + "&issuer=Evil", wantErr: ErrTOTPSecretInvalid},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := TOTPProvisioningURI(tt.issuer, tt.accountName, tt.secret)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("TOTPProvisioningURI = %q, %v, want %v", got, err, tt.wantErr)
			}
			if got != "" {
				t.Fatalf("TOTPProvisioningURI returned %q with an error", got)
			}
		})
	}

	// The longest accepted values pass.
	if _, err := TOTPProvisioningURI(strings.Repeat("e", 64), strings.Repeat("a", 254), fakeSecret); err != nil {
		t.Fatalf("TOTPProvisioningURI at the limits: %v", err)
	}
}

func TestTOTPCode_invalidSecret(t *testing.T) {
	t.Parallel()

	for _, secret := range []string{"", "not base32!", fakeSecret + "="} {
		if _, err := TOTPCode(secret, time.Unix(1_800_000_000, 0)); !errors.Is(err, ErrTOTPSecretInvalid) {
			t.Fatalf("TOTPCode(%q) = %v, want ErrTOTPSecretInvalid", secret, err)
		}
	}

	// A step below counter one has no code to hand out.
	if _, err := TOTPCode(fakeSecret, time.Unix(10, 0)); !errors.Is(err, ErrTOTPCounterInvalid) {
		t.Fatalf("TOTPCode at the epoch = %v, want ErrTOTPCounterInvalid", err)
	}
}
