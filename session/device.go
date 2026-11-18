// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package session

import (
	"cmp"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/ostendorf-cloud/govue/logging"
)

// Device sessions are the sign in of a native app, which cannot rely on the
// session cookie. ADR 0001 records the design. Here are the parts that need no
// HTTP: issue, read by token, rotation with reuse detection, listing and
// revocation. Nothing reads a bearer header yet.

// Defaults of a [DeviceConfig]. Each is the value a left zero field takes.
const (
	// DefaultDeviceIdle ends a device session unused for this long.
	DefaultDeviceIdle = 30 * 24 * time.Hour
	// DefaultDeviceLifetime ends a device session this long after the issue,
	// however active it is.
	DefaultDeviceLifetime = 180 * 24 * time.Hour
	// DefaultDeviceGrace is how long a retired current token still
	// authenticates, so requests in flight during a rotation do not sign the
	// app out.
	DefaultDeviceGrace = 5 * time.Minute
	// DefaultDeviceMinRotateInterval is the least time between two confirmed
	// rotations of one device. It keeps app launches from pushing a stolen
	// token out of the retired hashes.
	DefaultDeviceMinRotateInterval = time.Hour
	// DefaultDeviceRotateTimeout is how long after a pending token was first
	// issued a retry with the same nonce may replace it instead of retiring it.
	DefaultDeviceRotateTimeout = 30 * time.Second
	// DefaultDeviceTokenPrefix starts every device token, so a secret scanner
	// can find a leaked one. Like the cookie name, it does not name the
	// library behind it. A product sets its own.
	DefaultDeviceTokenPrefix = "device"
	// DefaultDevicesPerSubject is how many device sessions one account keeps.
	// Issuing past it ends the account's least recently seen one.
	DefaultDevicesPerSubject = 10
	// MaxDeviceNameBytes bounds the display label of a device.
	MaxDeviceNameBytes = 64
	// MaxDeviceNonceBytes bounds the client nonce of a rotation.
	MaxDeviceNonceBytes = 128
)

const (
	// tokenBodyLen is idBytes in unpadded base64url.
	tokenBodyLen = 43
	// tokenChecksumLen is a CRC-32 in unpadded base64url.
	tokenChecksumLen = 6
	// maxPrefixLen bounds TokenPrefix.
	maxPrefixLen = 16
	// deviceAttempts bounds how often a request re-reads a device session
	// that a concurrent confirm changed under it.
	deviceAttempts = 3
)

var (
	// ErrDevicesDisabled is what every device method returns when
	// [DeviceConfig.Store] is nil.
	ErrDevicesDisabled = errors.New("session: device sessions are not configured")

	// ErrDeviceToken is the one answer for every token that does not
	// authenticate: malformed, unknown, expired, revoked, and reused, which
	// revokes the device. The bearer step answers all of them with the same
	// 401, so a caller cannot learn which check failed. A store that cannot
	// answer is a different error.
	ErrDeviceToken = errors.New("session: device token not valid")

	// ErrRotateRefused is a rotation that issues no token while the presented
	// token stays valid: inside the minimum interval, a retired token inside
	// its grace, the first use of a pending token, or a concurrent rotation
	// that won. The app keeps the token it has.
	ErrRotateRefused = errors.New("session: device rotation refused")

	// ErrInvalidDeviceName is a device name that is too long, not UTF-8, or
	// carries a control or format character.
	ErrInvalidDeviceName = errors.New("session: device name not valid")

	// ErrInvalidNonce is a rotation nonce that is empty, too long or not
	// printable ASCII.
	ErrInvalidNonce = errors.New("session: rotation nonce not valid")
)

// DeviceConfig configures the device sessions of a [Manager]. Store is the
// switch: nil leaves device sessions off. Every other field is secure when
// left zero.
type DeviceConfig struct {
	// Store keeps the device sessions. Nil turns them off.
	Store DeviceStore

	// Idle ends a device session unused for this long. Zero is
	// [DefaultDeviceIdle]. It has to be longer than [Config.TouchEvery].
	Idle time.Duration

	// Lifetime ends a device session this long after the issue. Zero is
	// [DefaultDeviceLifetime]. Rotation never extends it.
	Lifetime time.Duration

	// Grace is how long a retired current token still authenticates. Zero is
	// [DefaultDeviceGrace]. A longer grace gives a thief longer before reuse
	// detection catches a stale token.
	Grace time.Duration

	// MinRotateInterval is the least time between two confirmed rotations of
	// one device. Zero is [DefaultDeviceMinRotateInterval].
	MinRotateInterval time.Duration

	// RotateTimeout is how long after a pending token was first issued a
	// retry with the same nonce may replace it. Zero is
	// [DefaultDeviceRotateTimeout]. The client's own timeout for a rotate
	// call has to be shorter.
	RotateTimeout time.Duration

	// TokenPrefix starts every token, followed by an underscore. One to
	// sixteen lower case letters and digits. Empty is
	// [DefaultDeviceTokenPrefix], which is neutral. A product sets its own,
	// so secret scanning can find a leaked token of exactly that product.
	TokenPrefix string

	// MaxPerSubject is how many device sessions one account keeps. Zero is
	// [DefaultDevicesPerSubject].
	MaxPerSubject int
}

// withDefaults fills in every zero field.
func (c DeviceConfig) withDefaults() DeviceConfig {
	c.Idle = cmp.Or(c.Idle, DefaultDeviceIdle)
	c.Lifetime = cmp.Or(c.Lifetime, DefaultDeviceLifetime)
	c.Grace = cmp.Or(c.Grace, DefaultDeviceGrace)
	c.MinRotateInterval = cmp.Or(c.MinRotateInterval, DefaultDeviceMinRotateInterval)
	c.RotateTimeout = cmp.Or(c.RotateTimeout, DefaultDeviceRotateTimeout)
	c.TokenPrefix = cmp.Or(c.TokenPrefix, DefaultDeviceTokenPrefix)
	c.MaxPerSubject = cmp.Or(c.MaxPerSubject, DefaultDevicesPerSubject)
	return c
}

// problems lists what is wrong with a configuration after withDefaults.
func (c DeviceConfig) problems(touchEvery time.Duration) []error {
	var problems []error
	for _, field := range []struct {
		name  string
		value time.Duration
	}{
		{"device idle timeout", c.Idle},
		{"device lifetime", c.Lifetime},
		{"device grace", c.Grace},
		{"device rotate interval", c.MinRotateInterval},
		{"device rotate timeout", c.RotateTimeout},
	} {
		if field.value < 0 {
			problems = append(problems, fmt.Errorf("%s %s is negative", field.name, field.value))
		}
	}
	if c.MaxPerSubject < 0 {
		problems = append(problems, fmt.Errorf("devices per subject %d is negative", c.MaxPerSubject))
	}
	if !validPrefix(c.TokenPrefix) {
		problems = append(problems, fmt.Errorf(
			"device token prefix %q is not one to %d lower case letters and digits", c.TokenPrefix, maxPrefixLen))
	}
	if touchEvery > 0 && c.Idle > 0 && touchEvery >= c.Idle {
		problems = append(problems, fmt.Errorf(
			"touch interval %s is not shorter than the device idle timeout %s", touchEvery, c.Idle))
	}
	return problems
}

func validPrefix(prefix string) bool {
	if prefix == "" || len(prefix) > maxPrefixLen {
		return false
	}
	for _, r := range prefix {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

// Device is what a caller sees of a device session. It carries no hash and
// no rotation state, so nothing derived from a token reaches a handler, a log
// line or a response by accident.
type Device struct {
	ID        string
	Subject   string
	Name      string
	CreatedAt time.Time
	LastSeen  time.Time
	ExpiresAt time.Time
}

func (d DeviceSession) view() Device {
	return Device{
		ID: d.ID, Subject: d.Subject, Name: d.Name,
		CreatedAt: d.CreatedAt, LastSeen: d.LastSeen, ExpiresAt: d.ExpiresAt,
	}
}

// IssueDevice starts a device session for an account that has just
// authenticated and returns its token. The token is shown to the caller once
// and never stored: the store keeps its SHA-256.
//
// authenticatedAt is when the credential check behind this issue ran, read
// from the same clock as [Config.Now]. An issue whose check is
// [MaxDeviceIssueDelay] old or more, or not later than the last [Manager.EndAll]
// of the account, is refused with [ErrStaleAuthentication]. That keeps a
// password change that lands between the check of the old password and the
// issue from leaving a device behind.
//
// Issuing counts as the first rotation, so the first rotation follows one
// [DeviceConfig.MinRotateInterval] later. Past [DeviceConfig.MaxPerSubject],
// the account's least recently seen device session ends, and a log line names
// it. Whoever has the password can use that to sign the account's apps out;
// changing the password, which calls EndAll, is the remedy.
func (m *Manager) IssueDevice(ctx context.Context, subject, name string, authenticatedAt time.Time) (string, Device, error) {
	store := m.cfg.Devices.Store
	if store == nil {
		return "", Device{}, ErrDevicesDisabled
	}
	if subject == "" {
		return "", Device{}, errors.New("session: issue device: subject is empty")
	}
	if !validDeviceName(name) {
		return "", Device{}, ErrInvalidDeviceName
	}
	now := m.cfg.Now().UTC()
	if authenticatedAt.IsZero() || authenticatedAt.After(now) {
		return "", Device{}, errors.New("session: issue device: the credential check time is unset or in the future")
	}
	if now.Sub(authenticatedAt) >= MaxDeviceIssueDelay {
		return "", Device{}, ErrStaleAuthentication
	}

	token, err := m.newDeviceToken()
	if err != nil {
		return "", Device{}, err
	}
	id, err := NewID()
	if err != nil {
		return "", Device{}, err
	}

	d := DeviceSession{
		ID:        id,
		Subject:   subject,
		Name:      name,
		CreatedAt: now,
		LastSeen:  now,
		ExpiresAt: now.Add(m.cfg.Devices.Lifetime),
		DeviceState: DeviceState{
			CurrentHash: hashDeviceToken(token),
			RotatedAt:   now,
		},
	}
	evicted, err := store.Create(ctx, d, m.cfg.Devices.MaxPerSubject, authenticatedAt.UTC())
	if err != nil {
		return "", Device{}, fmt.Errorf("session: issue device: %w", err)
	}
	for _, id := range evicted {
		logging.FromContext(ctx).InfoContext(ctx, "session: device session ended by the per account cap",
			"device_id", id, "subject", subject)
	}
	return token, d.view(), nil
}

// AuthenticateDevice returns the device session a token belongs to.
//
// A current token authenticates. A pending token authenticates and is
// confirmed: it becomes current, and the previous current token is retired
// with [DeviceConfig.Grace]. A retired token authenticates until its grace
// ends. After that, presenting it means a second holder has it, and the whole
// device session is revoked.
//
// Every token that does not authenticate is [ErrDeviceToken]. Any other error
// means the store could not answer, and comes without a device. The last use
// is written back at most every [Config.TouchEvery], and a failed write back
// is not reported.
func (m *Manager) AuthenticateDevice(ctx context.Context, token string) (Device, error) {
	if m.cfg.Devices.Store == nil {
		return Device{}, ErrDevicesDisabled
	}
	for range deviceAttempts {
		device, err := m.authenticateDevice(ctx, token)
		// A concurrent confirm won the swap. Reading again finds the
		// token in its new slot, which is what a later request would see.
		if !errors.Is(err, ErrDeviceConflict) {
			return device, err
		}
	}
	return Device{}, fmt.Errorf("session: authenticate device: %w", ErrDeviceConflict)
}

func (m *Manager) authenticateDevice(ctx context.Context, token string) (Device, error) {
	d, slot, now, err := m.lookupDevice(ctx, token)
	if err != nil {
		return Device{}, err
	}
	if slot == SlotPending {
		if d, err = m.confirmDevice(ctx, d, now); err != nil {
			return Device{}, err
		}
	}
	return m.touchDevice(ctx, d, now)
}

// RotateDevice issues a pending token for the device session the token
// belongs to and returns it. nonce is the client's random value for this
// rotate attempt; a retry of the same attempt sends the same one.
//
// The pending token does not replace the current one until its first use.
// The rules follow the state table of ADR 0001:
//
//   - Stable, current token: a new pending token once
//     [DeviceConfig.MinRotateInterval] has passed since the last confirmed
//     rotation, otherwise [ErrRotateRefused].
//   - Pending, current token, same nonce, before [DeviceConfig.RotateTimeout]
//     has passed since that nonce's pending token was first issued: the
//     pending token is replaced and forgotten, not retired.
//   - Pending, current token, otherwise: the pending token is retired
//     without grace and a new one issued.
//   - Pending token: it is confirmed as on a request, and no new pending
//     token is issued. The confirmed device comes back together with
//     [ErrRotateRefused], so a caller sees that the pending token now
//     authenticates.
//   - Retired token inside its grace: [ErrRotateRefused].
//   - Retired token after its grace: the device session is revoked.
//
// Of two truly concurrent rotations, one wins and the other is
// [ErrRotateRefused]. A token that does not authenticate is [ErrDeviceToken].
func (m *Manager) RotateDevice(ctx context.Context, token, nonce string) (string, Device, error) {
	store := m.cfg.Devices.Store
	if store == nil {
		return "", Device{}, ErrDevicesDisabled
	}
	if !validNonce(nonce) {
		return "", Device{}, ErrInvalidNonce
	}

	d, slot, now, err := m.lookupDevice(ctx, token)
	if err != nil {
		return "", Device{}, err
	}

	switch slot {
	case SlotRetired:
		return "", Device{}, ErrRotateRefused
	case SlotPending:
		if d, err = m.confirmDevice(ctx, d, now); err != nil {
			return "", Device{}, refusedOnConflict(err)
		}
		device, err := m.touchDevice(ctx, d, now)
		if err != nil {
			return "", Device{}, err
		}
		// The confirm is a rotation, so the minimum interval starts now.
		return "", device, ErrRotateRefused
	}

	var retire []RetiredHash
	next := DeviceState{CurrentHash: d.CurrentHash, PendingNonce: nonce, PendingAt: now, RotatedAt: d.RotatedAt}
	switch {
	case d.PendingHash.IsZero():
		if now.Sub(d.RotatedAt) < m.cfg.Devices.MinRotateInterval {
			if _, err := m.touchDevice(ctx, d, now); err != nil {
				return "", Device{}, err
			}
			return "", Device{}, ErrRotateRefused
		}
	case sameNonce(nonce, d.PendingNonce) && now.Before(d.PendingAt.Add(m.cfg.Devices.RotateTimeout)):
		// A retry of the same attempt. The window stays where the first
		// issue put it, so a holder of the nonce cannot keep it open.
		next.PendingAt = d.PendingAt
	default:
		retire = []RetiredHash{{Hash: d.PendingHash, GraceEnd: now, Kind: RetiredPending}}
	}

	pending, err := m.newDeviceToken()
	if err != nil {
		return "", Device{}, err
	}
	next.PendingHash = hashDeviceToken(pending)
	if err := store.Swap(ctx, d.ID, d.DeviceState, next, retire); err != nil {
		return "", Device{}, refusedOnConflict(swapError(err))
	}
	device, err := m.touchDevice(ctx, d, now)
	if err != nil {
		return "", Device{}, err
	}
	return pending, device, nil
}

// ListDevices returns the live device sessions of one account, most recently
// seen first. It never returns another account's, even from a store that
// does.
func (m *Manager) ListDevices(ctx context.Context, subject string) ([]Device, error) {
	store := m.cfg.Devices.Store
	if store == nil {
		return nil, ErrDevicesDisabled
	}
	if subject == "" {
		return nil, errors.New("session: list devices: subject is empty")
	}

	records, err := store.List(ctx, subject)
	if err != nil {
		return nil, fmt.Errorf("session: list devices: %w", err)
	}
	now := m.cfg.Now().UTC()
	devices := make([]Device, 0, len(records))
	for _, d := range records {
		if d.Subject == subject && !m.deviceExpired(d, now) {
			devices = append(devices, d.view())
		}
	}
	slices.SortFunc(devices, func(a, b Device) int {
		return cmp.Or(b.LastSeen.Compare(a.LastSeen), cmp.Compare(a.ID, b.ID))
	})
	return devices, nil
}

// RevokeDevice ends one device session of an account, by its id. An id of
// another account is refused exactly like an unknown id, with an error
// wrapping [ErrNotFound], and ends nothing.
func (m *Manager) RevokeDevice(ctx context.Context, subject, id string) error {
	store := m.cfg.Devices.Store
	if store == nil {
		return ErrDevicesDisabled
	}
	if subject == "" || id == "" {
		return fmt.Errorf("session: revoke device: %w", ErrNotFound)
	}
	if err := store.Revoke(ctx, subject, id); err != nil {
		return fmt.Errorf("session: revoke device: %w", err)
	}
	return nil
}

// lookupDevice parses the token, finds its record and applies every rule
// that ends a device session: both timeouts and reuse after the grace. What it
// returns authenticates.
func (m *Manager) lookupDevice(ctx context.Context, token string) (DeviceSession, DeviceSlot, time.Time, error) {
	hash, ok := m.parseDeviceToken(token)
	if !ok {
		return DeviceSession{}, 0, time.Time{}, ErrDeviceToken
	}

	store := m.cfg.Devices.Store
	d, slot, err := store.Lookup(ctx, hash)
	if errors.Is(err, ErrNotFound) {
		return DeviceSession{}, 0, time.Time{}, ErrDeviceToken
	}
	if err != nil {
		return DeviceSession{}, 0, time.Time{}, fmt.Errorf("session: look up device: %w", err)
	}
	// The slot decides everything below, so it is taken from the record
	// rather than from what the store says about it.
	if slot == 0 || d.slot(hash) != slot {
		return DeviceSession{}, 0, time.Time{}, errors.New("session: look up device: the store returned a record that does not hold the hash")
	}
	// A record without an id could be neither touched nor revoked.
	if d.ID == "" {
		return DeviceSession{}, 0, time.Time{}, errors.New("session: look up device: the store returned a record without an id")
	}

	now := m.cfg.Now().UTC()
	if m.deviceExpired(d, now) {
		// Removing it is housekeeping. The check above already decided, and
		// it runs on every read whatever the store still holds.
		_ = store.Revoke(ctx, d.Subject, d.ID)
		return DeviceSession{}, 0, time.Time{}, ErrDeviceToken
	}
	if slot == SlotRetired && !now.Before(graceEnd(d.Retired, hash)) {
		return DeviceSession{}, 0, time.Time{}, m.revokeReused(ctx, d)
	}
	return d, slot, now, nil
}

// deviceExpired applies both timeouts. Both bounds are exclusive: a device
// session whose expiry is exactly now is over.
func (m *Manager) deviceExpired(d DeviceSession, now time.Time) bool {
	return !now.Before(d.ExpiresAt) || now.Sub(d.LastSeen) >= m.cfg.Devices.Idle
}

// graceEnd is the grace end of a retired hash. A hash the list does not hold
// has no grace left.
func graceEnd(retired []RetiredHash, hash TokenHash) time.Time {
	for _, r := range retired {
		if r.Hash == hash {
			return r.GraceEnd
		}
	}
	return time.Time{}
}

// revokeReused ends a device session whose retired token came back after its
// grace. The log line names the device and the account, never the token or
// its hash. A failed revocation is logged, and the answer is the same
// [ErrDeviceToken] as for every other token failure: the token does not
// authenticate either way.
func (m *Manager) revokeReused(ctx context.Context, d DeviceSession) error {
	logger := logging.FromContext(ctx)
	if err := m.cfg.Devices.Store.Revoke(ctx, d.Subject, d.ID); err != nil && !errors.Is(err, ErrNotFound) {
		logger.ErrorContext(ctx, "session: device token reused, revoking the device failed",
			"device_id", d.ID, "subject", d.Subject, "error", err)
		return ErrDeviceToken
	}
	logger.WarnContext(ctx, "session: device token reused, device revoked",
		"device_id", d.ID, "subject", d.Subject)
	return ErrDeviceToken
}

// confirmDevice makes the pending token current and retires the current one
// with the grace.
func (m *Manager) confirmDevice(ctx context.Context, d DeviceSession, now time.Time) (DeviceSession, error) {
	next := DeviceState{CurrentHash: d.PendingHash, RotatedAt: now}
	retire := []RetiredHash{{Hash: d.CurrentHash, GraceEnd: now.Add(m.cfg.Devices.Grace), Kind: RetiredCurrent}}
	if err := m.cfg.Devices.Store.Swap(ctx, d.ID, d.DeviceState, next, retire); err != nil {
		return DeviceSession{}, swapError(err)
	}
	d.DeviceState = next
	return d, nil
}

// touchDevice writes the last use back when it is due. A record that is gone
// by then was ended concurrently, and the end wins.
func (m *Manager) touchDevice(ctx context.Context, d DeviceSession, now time.Time) (Device, error) {
	if now.Sub(d.LastSeen) < m.cfg.TouchEvery {
		return d.view(), nil
	}
	err := m.cfg.Devices.Store.Touch(ctx, d.ID, now)
	if errors.Is(err, ErrNotFound) {
		return Device{}, ErrDeviceToken
	}
	if err != nil {
		// The device session is valid. Failing the request because its last
		// use could not be written would be worse than the idle timeout being
		// measured from the previous touch.
		return d.view(), nil
	}
	d.LastSeen = now
	return d.view(), nil
}

// swapError maps what a failed swap means for the token.
func swapError(err error) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return ErrDeviceToken
	case errors.Is(err, ErrDeviceConflict):
		return ErrDeviceConflict
	}
	return fmt.Errorf("session: swap device state: %w", err)
}

// refusedOnConflict turns a lost race of a rotation into a refusal.
func refusedOnConflict(err error) error {
	if errors.Is(err, ErrDeviceConflict) {
		return ErrRotateRefused
	}
	return err
}

func sameNonce(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func validNonce(nonce string) bool {
	if nonce == "" || len(nonce) > MaxDeviceNonceBytes {
		return false
	}
	for i := range len(nonce) {
		if nonce[i] < '!' || nonce[i] > '~' {
			return false
		}
	}
	return true
}

func validDeviceName(name string) bool {
	if len(name) > MaxDeviceNameBytes || !utf8.ValidString(name) {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return false
		}
	}
	return true
}

// newDeviceToken is the prefix, 32 random bytes in unpadded base64url and a
// checksum, each joined by an underscore.
func (m *Manager) newDeviceToken() (string, error) {
	buf := make([]byte, idBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("session: read random bytes: %w", err)
	}
	signed := m.cfg.Devices.TokenPrefix + "_" + base64.RawURLEncoding.EncodeToString(buf)
	return signed + "_" + tokenChecksum(signed), nil
}

// parseDeviceToken checks the shape and the checksum and returns the hash to
// look up. A malformed token never reaches the store.
func (m *Manager) parseDeviceToken(token string) (TokenHash, bool) {
	prefix := m.cfg.Devices.TokenPrefix + "_"
	if len(token) != len(prefix)+tokenBodyLen+1+tokenChecksumLen || !strings.HasPrefix(token, prefix) {
		return TokenHash{}, false
	}
	signed := token[:len(prefix)+tokenBodyLen]
	if token[len(signed)] != '_' {
		return TokenHash{}, false
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(signed[len(prefix):])
	if err != nil || len(raw) != idBytes {
		return TokenHash{}, false
	}
	// The checksum is no secret. It only keeps a mistyped or truncated token
	// from costing a store lookup.
	if token[len(signed)+1:] != tokenChecksum(signed) {
		return TokenHash{}, false
	}
	return hashDeviceToken(token), true
}

func tokenChecksum(signed string) string {
	var sum [4]byte
	binary.BigEndian.PutUint32(sum[:], crc32.ChecksumIEEE([]byte(signed)))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func hashDeviceToken(token string) TokenHash {
	return sha256.Sum256([]byte(token))
}
