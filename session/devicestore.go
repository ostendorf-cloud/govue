// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package session

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"sync"
	"time"
)

// ErrDeviceConflict is what a [DeviceStore] returns when a conditional write
// finds another state than the one the caller read, or when a new record would
// reuse an id or a hash the store already holds.
var ErrDeviceConflict = errors.New("session: device session changed concurrently")

// ErrDeviceStoreFull is what a [DeviceStore] returns when it holds as many
// device sessions as it may and refuses to issue another one.
var ErrDeviceStoreFull = errors.New("session: device store is full")

// ErrStaleAuthentication is a device session issue whose credential check is
// older than [MaxDeviceIssueDelay], or not later than the last time the
// account's device sessions were all ended. A password change that lands
// between the check of the old password and the issue must not leave a device
// behind.
var ErrStaleAuthentication = errors.New("session: authentication predates the end of the account's sessions")

// The bounds of the two retired lists of a device session. Reuse detection
// holds while a stolen token is among them. After that it is a plain unknown
// token.
const (
	// MaxRetiredCurrent is how many retired current hashes a device session
	// keeps: the tokens its holders had and rotated away from.
	MaxRetiredCurrent = 4
	// MaxRetiredPending is how many retired pending hashes a device session
	// keeps: pending tokens a later rotation replaced. They are a list of
	// their own, so rotations cannot push a retired current hash out.
	MaxRetiredPending = 2
)

// MaxDeviceIssueDelay is the longest time between the credential check and
// [Manager.IssueDevice]. A [DeviceStore] remembers the time of a
// [DeviceStore.DeleteSubject] for at least this long.
const MaxDeviceIssueDelay = 5 * time.Minute

// RetiredKind says which list a retired hash belongs to.
type RetiredKind int

// The two kinds of retired hashes.
const (
	// RetiredCurrent is a current hash that a confirmed rotation replaced.
	RetiredCurrent RetiredKind = iota + 1
	// RetiredPending is a pending hash that a later rotation replaced.
	RetiredPending
)

// maxRetired is the bound of one retired list, or zero for an unknown kind.
func maxRetired(kind RetiredKind) int {
	switch kind {
	case RetiredCurrent:
		return MaxRetiredCurrent
	case RetiredPending:
		return MaxRetiredPending
	}
	return 0
}

// TokenHash is the SHA-256 of a device token, the only form of it a store
// keeps. A token carries 256 random bits, so a plain hash needs no salt, no
// slow key derivation and no server key: a store dump yields nothing a caller
// can present.
//
// Lookup by hash needs no constant time comparison either. An attacker who
// learns how long a lookup takes learns about a hash, and a hash is useless
// without a token that produces it.
type TokenHash [32]byte

// IsZero reports whether the hash is unset. The zero hash marks an empty
// slot and never matches a lookup.
func (h TokenHash) IsZero() bool { return h == TokenHash{} }

// RetiredHash is a hash that no longer authenticates on its own. Until
// GraceEnd, exclusive, it still authenticates a request. From GraceEnd on,
// presenting it revokes the device session, because only a second holder
// still has it. Kind says which of the two retired lists it is in.
type RetiredHash struct {
	Hash     TokenHash
	GraceEnd time.Time
	Kind     RetiredKind
}

// DeviceSlot says which hash of a record a lookup matched.
type DeviceSlot int

// The slots a lookup can match.
const (
	// SlotCurrent is the hash the device signs in with.
	SlotCurrent DeviceSlot = iota + 1
	// SlotPending is the hash of a rotated token not used yet.
	SlotPending
	// SlotRetired is one of the retired hashes, of either kind.
	SlotRetired
)

// DeviceState is the part of a device session that rotation changes. A
// [DeviceStore] swaps it as a whole and on a condition, see
// [DeviceStore.Swap].
type DeviceState struct {
	// CurrentHash is the hash of the token the device signs in with.
	CurrentHash TokenHash
	// PendingHash is the hash of a rotated token that has not been used yet,
	// or zero. A device session with one is in the state Pending, one
	// without is Stable.
	PendingHash TokenHash
	// PendingNonce is the client nonce of the rotate call that issued the
	// pending token.
	PendingNonce string
	// PendingAt is when the pending token for PendingNonce was first issued.
	// A retry with the same nonce does not move it.
	PendingAt time.Time
	// RotatedAt is when the current token became current: the issue or the
	// last confirmed rotation. The minimum rotate interval counts from it.
	RotatedAt time.Time
}

// DeviceSession is one stored device session of a native app.
//
// ID is a random handle for listing and revoking and is never a credential.
// Name is a bounded display label and is never trusted. ExpiresAt is the
// absolute end and never moves.
//
// Retired holds both retired lists in one slice, oldest first, told apart by
// [RetiredHash.Kind]: at most [MaxRetiredCurrent] of kind [RetiredCurrent]
// and at most [MaxRetiredPending] of kind [RetiredPending]. A store keeps the
// kind as a column next to the hash and the grace end.
type DeviceSession struct {
	ID        string
	Subject   string
	Name      string
	CreatedAt time.Time
	LastSeen  time.Time
	ExpiresAt time.Time
	DeviceState
	Retired []RetiredHash
}

// hashes is every hash of the record that a lookup matches.
func (d DeviceSession) hashes() []TokenHash {
	out := make([]TokenHash, 0, 2+len(d.Retired))
	for _, h := range append([]TokenHash{d.CurrentHash, d.PendingHash}, retiredHashes(d.Retired)...) {
		if !h.IsZero() {
			out = append(out, h)
		}
	}
	return out
}

// slot is which hash of the record h is, or zero.
func (d DeviceSession) slot(h TokenHash) DeviceSlot {
	switch {
	case h.IsZero():
		return 0
	case h == d.CurrentHash:
		return SlotCurrent
	case h == d.PendingHash:
		return SlotPending
	case slices.Contains(retiredHashes(d.Retired), h):
		return SlotRetired
	}
	return 0
}

func retiredHashes(retired []RetiredHash) []TokenHash {
	out := make([]TokenHash, 0, len(retired))
	for _, r := range retired {
		out = append(out, r.Hash)
	}
	return out
}

// cutRetired keeps the newest entries of each kind up to its bound, in their
// order. One list never pushes the other's entries out.
func cutRetired(retired []RetiredHash) []RetiredHash {
	kept := map[RetiredKind]int{}
	out := make([]RetiredHash, 0, len(retired))
	for _, r := range slices.Backward(retired) {
		if kept[r.Kind] < maxRetired(r.Kind) {
			kept[r.Kind]++
			out = append(out, r)
		}
	}
	slices.Reverse(out)
	return out
}

func (d DeviceSession) clone() DeviceSession {
	copied := d
	copied.Retired = slices.Clone(d.Retired)
	return copied
}

// DeviceStore keeps device sessions. An implementation is safe for concurrent
// use and returns [ErrNotFound], wrapped or not, for a record it does not
// hold.
//
// It is separate from [Store], so no implementation of that one breaks, and
// it is not a key value store: every method is atomic on one record, and none
// writes a whole record back. Rotation and reuse detection depend on that. A
// store that reads a record, changes it in memory and writes it back lets two
// concurrent rotations both win and can bring a revoked record back.
//
// An error a store returns never carries a token hash or a nonce. The manager
// passes store errors on to its caller, and from there they reach logs.
//
// A store for more than one replica implements these methods against
// something shared, for example with a conditional UPDATE on the columns
// [DeviceStore.Swap] compares, and runs the sessiontest suite against it. The
// times [DeviceStore.Create] and [DeviceStore.DeleteSubject] compare come from
// the replicas' clocks, which therefore have to be synchronised.
type DeviceStore interface {
	// Create inserts a new record and returns the ids of the records it
	// evicted for it.
	//
	// authenticatedAt is when the credential check behind this issue ran.
	// When it is not later than the last [DeviceStore.DeleteSubject] of the
	// subject the store remembers, Create returns [ErrStaleAuthentication]
	// and inserts nothing. The check and the insert are one atomic step with
	// respect to DeleteSubject.
	//
	// When the subject already holds maxPerSubject records, the one with the
	// earliest LastSeen is removed in the same atomic step. A record of
	// another subject is never removed. Ties go to the earliest CreatedAt,
	// then the lowest ID.
	//
	// Create returns [ErrDeviceConflict] for an ID or a hash the store
	// already holds, and may return [ErrDeviceStoreFull] when it refuses to
	// grow. Neither changes anything.
	Create(ctx context.Context, d DeviceSession, maxPerSubject int, authenticatedAt time.Time) ([]string, error)

	// Lookup returns the record that holds hash as its current, its pending
	// or one of its retired hashes of either kind, and which of them matched.
	// The zero hash never matches. A store may hide a record past its
	// ExpiresAt.
	Lookup(ctx context.Context, hash TokenHash) (DeviceSession, DeviceSlot, error)

	// Touch sets LastSeen to lastSeen when that is later than the stored
	// value, and changes nothing else. A missing record is [ErrNotFound] and
	// is never created.
	Touch(ctx context.Context, id string, lastSeen time.Time) error

	// Swap replaces the rotation state of one record, on a condition.
	//
	// It succeeds only while the stored CurrentHash, PendingHash and
	// PendingNonce equal those of expect, which is the state the caller read.
	// Otherwise it returns [ErrDeviceConflict] and changes nothing. That
	// compare and the write are one atomic step, so of two concurrent callers
	// that read the same state exactly one wins.
	//
	// On success the record's DeviceState becomes next and every entry of
	// retire is appended to Retired in order. Each kind is then cut to its
	// own newest entries: [MaxRetiredCurrent] of [RetiredCurrent] and
	// [MaxRetiredPending] of [RetiredPending]. No other field changes. A hash
	// that is no longer current, pending or retired stops matching
	// [DeviceStore.Lookup]. An entry of another kind is an error that changes
	// nothing. A missing record is [ErrNotFound] and is never created.
	Swap(ctx context.Context, id string, expect, next DeviceState, retire []RetiredHash) error

	// Revoke removes the record with this id only when it belongs to
	// subject, in one atomic step. An unknown id and an id of another subject
	// both return [ErrNotFound] and remove nothing, so a caller cannot tell
	// them apart.
	Revoke(ctx context.Context, subject, id string) error

	// DeleteSubject removes every record of subject, for a password change, a
	// disabled account or a log out everywhere. It wins over a concurrent
	// Touch or Swap of those records: once it returns, none of them matches a
	// lookup. A subject without records is not an error.
	//
	// It does not reach a record created after it. That is what at is for:
	// the store remembers it for at least [MaxDeviceIssueDelay], and
	// [DeviceStore.Create] refuses an issue whose credential check is not
	// later. A store that cannot keep every such time may keep one floor for
	// all subjects instead, which refuses more and never less.
	DeleteSubject(ctx context.Context, subject string, at time.Time) error

	// List returns every record of subject and no record of another one.
	List(ctx context.Context, subject string) ([]DeviceSession, error)
}

// memoryDeviceSweepInterval is how long a [MemoryDeviceStore] may go without
// dropping what has expired.
const memoryDeviceSweepInterval = time.Minute

// defaultMaxDevices is the bound a non-positive maxDevices takes.
const defaultMaxDevices = 10000

// MemoryDeviceStore keeps device sessions in the process. It is safe for
// concurrent use: one mutex covers every method, so each is atomic.
//
// It holds at most maxDevices records. A full store refuses to issue, for
// every subject, with [ErrDeviceStoreFull]. It never evicts a device session
// of one account to make room for another: that would let anyone who can sign
// in sign everyone else's apps out. Records past their absolute expiry are
// swept, and hidden from [MemoryDeviceStore.Lookup] before that.
//
// It remembers the time of at most maxDevices recent deletions by subject,
// each for twice [MaxDeviceIssueDelay]. Past that bound it keeps one floor
// for every subject, which refuses every issue whose credential check is not
// later.
//
// It is correct for exactly one replica, and a restart signs every app out.
// An app has no way to sign in again silently, so a product with apps needs
// its own shared [DeviceStore].
type MemoryDeviceStore struct {
	mu         sync.Mutex
	devices    map[string]DeviceSession
	byHash     map[TokenHash]string
	ended      map[string]time.Time
	endedFloor time.Time
	now        func() time.Time
	lastSweep  time.Time
	maxDevices int
}

// NewMemoryDeviceStore returns an empty store. now is the clock; nil is
// [time.Now]. maxDevices bounds the records across all subjects; a
// non-positive value is clamped to 10000.
func NewMemoryDeviceStore(now func() time.Time, maxDevices int) *MemoryDeviceStore {
	if now == nil {
		now = time.Now
	}
	if maxDevices < 1 {
		maxDevices = defaultMaxDevices
	}
	return &MemoryDeviceStore{
		devices:    map[string]DeviceSession{},
		byHash:     map[TokenHash]string{},
		ended:      map[string]time.Time{},
		now:        now,
		maxDevices: maxDevices,
	}
}

// Create implements [DeviceStore.Create].
func (s *MemoryDeviceStore) Create(_ context.Context, d DeviceSession, maxPerSubject int, authenticatedAt time.Time) ([]string, error) {
	if d.ID == "" || d.CurrentHash.IsZero() {
		return nil, errors.New("session: device record without an id or a current hash")
	}
	maxPerSubject = max(maxPerSubject, 1)

	s.mu.Lock()
	defer s.mu.Unlock()

	s.maybeSweep(s.now())
	if ended := s.endedAt(d.Subject); !authenticatedAt.After(ended) {
		return nil, ErrStaleAuthentication
	}
	if _, taken := s.devices[d.ID]; taken {
		return nil, ErrDeviceConflict
	}
	for _, h := range d.hashes() {
		if _, taken := s.byHash[h]; taken {
			return nil, ErrDeviceConflict
		}
	}

	var own []DeviceSession
	for _, existing := range s.devices {
		if existing.Subject == d.Subject {
			own = append(own, existing)
		}
	}
	slices.SortFunc(own, func(a, b DeviceSession) int {
		return cmp.Or(a.LastSeen.Compare(b.LastSeen), a.CreatedAt.Compare(b.CreatedAt), cmp.Compare(a.ID, b.ID))
	})
	evict := own[:max(0, len(own)-maxPerSubject+1)]

	if len(s.devices)-len(evict) >= s.maxDevices {
		return nil, ErrDeviceStoreFull
	}
	evicted := make([]string, 0, len(evict))
	for _, old := range evict {
		s.remove(old.ID)
		evicted = append(evicted, old.ID)
	}
	s.insert(d.clone())
	return evicted, nil
}

// Lookup implements [DeviceStore.Lookup].
func (s *MemoryDeviceStore) Lookup(_ context.Context, hash TokenHash) (DeviceSession, DeviceSlot, error) {
	if hash.IsZero() {
		return DeviceSession{}, 0, ErrNotFound
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	id, ok := s.byHash[hash]
	if !ok {
		return DeviceSession{}, 0, ErrNotFound
	}
	d := s.devices[id]
	if !s.now().Before(d.ExpiresAt) {
		return DeviceSession{}, 0, ErrNotFound
	}
	return d.clone(), d.slot(hash), nil
}

// Touch implements [DeviceStore.Touch].
func (s *MemoryDeviceStore) Touch(_ context.Context, id string, lastSeen time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	d, ok := s.devices[id]
	if !ok {
		return ErrNotFound
	}
	if lastSeen.After(d.LastSeen) {
		d.LastSeen = lastSeen
		s.devices[id] = d
	}
	return nil
}

// Swap implements [DeviceStore.Swap].
func (s *MemoryDeviceStore) Swap(_ context.Context, id string, expect, next DeviceState, retire []RetiredHash) error {
	if next.CurrentHash.IsZero() {
		return errors.New("session: device state without a current hash")
	}
	for _, r := range retire {
		if maxRetired(r.Kind) == 0 || r.Hash.IsZero() {
			return errors.New("session: retired hash without a known kind or a hash")
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	d, ok := s.devices[id]
	if !ok {
		return ErrNotFound
	}
	if d.CurrentHash != expect.CurrentHash || d.PendingHash != expect.PendingHash || d.PendingNonce != expect.PendingNonce {
		return ErrDeviceConflict
	}

	updated := d.clone()
	updated.DeviceState = next
	updated.Retired = cutRetired(append(updated.Retired, retire...))
	for _, h := range updated.hashes() {
		if owner, taken := s.byHash[h]; taken && owner != id {
			return ErrDeviceConflict
		}
	}

	s.remove(id)
	s.insert(updated)
	return nil
}

// Revoke implements [DeviceStore.Revoke].
func (s *MemoryDeviceStore) Revoke(_ context.Context, subject, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	d, ok := s.devices[id]
	if !ok || d.Subject != subject {
		return ErrNotFound
	}
	s.remove(id)
	return nil
}

// DeleteSubject implements [DeviceStore.DeleteSubject].
func (s *MemoryDeviceStore) DeleteSubject(_ context.Context, subject string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for id, d := range s.devices {
		if d.Subject == subject {
			s.remove(id)
		}
	}

	if at.Before(s.ended[subject]) {
		return nil
	}
	if _, known := s.ended[subject]; !known && len(s.ended) >= s.maxDevices {
		s.sweepEnded(s.now())
	}
	if _, known := s.ended[subject]; !known && len(s.ended) >= s.maxDevices {
		// No room to remember this subject on its own. The floor covers
		// it, and every other subject with it.
		if at.After(s.endedFloor) {
			s.endedFloor = at
		}
		return nil
	}
	s.ended[subject] = at
	return nil
}

// List implements [DeviceStore.List].
func (s *MemoryDeviceStore) List(_ context.Context, subject string) ([]DeviceSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var out []DeviceSession
	for _, d := range s.devices {
		if d.Subject == subject {
			out = append(out, d.clone())
		}
	}
	return out, nil
}

// Len is how many records the store holds, including ones the sweep has not
// reached yet. It is for a metric and for tests.
func (s *MemoryDeviceStore) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return len(s.devices)
}

// endedAt is the latest deletion time that applies to subject. The caller
// holds the lock.
func (s *MemoryDeviceStore) endedAt(subject string) time.Time {
	ended := s.ended[subject]
	if s.endedFloor.After(ended) {
		return s.endedFloor
	}
	return ended
}

// insert adds a record and indexes its hashes. The caller holds the lock.
func (s *MemoryDeviceStore) insert(d DeviceSession) {
	s.devices[d.ID] = d
	for _, h := range d.hashes() {
		s.byHash[h] = d.ID
	}
}

// remove drops a record and every hash of it. The caller holds the lock.
func (s *MemoryDeviceStore) remove(id string) {
	d, ok := s.devices[id]
	if !ok {
		return
	}
	for _, h := range d.hashes() {
		delete(s.byHash, h)
	}
	delete(s.devices, id)
}

// maybeSweep drops records past their absolute expiry and deletion times no
// issue can race any more, when the last sweep is long enough ago or the store
// is full. The idle timeout belongs to the [Manager], which drops an idle
// record on the read that finds it. The caller holds the lock.
func (s *MemoryDeviceStore) maybeSweep(now time.Time) {
	if now.Sub(s.lastSweep) < memoryDeviceSweepInterval && len(s.devices) < s.maxDevices {
		return
	}
	for id, d := range s.devices {
		if !now.Before(d.ExpiresAt) {
			s.remove(id)
		}
	}
	s.sweepEnded(now)
	s.lastSweep = now
}

// sweepEnded drops deletion times older than twice the issue delay. The
// manager refuses a credential check that old, so no issue can race them.
// The caller holds the lock.
func (s *MemoryDeviceStore) sweepEnded(now time.Time) {
	keep := 2 * MaxDeviceIssueDelay
	for subject, at := range s.ended {
		if now.Sub(at) > keep {
			delete(s.ended, subject)
		}
	}
	if now.Sub(s.endedFloor) > keep {
		s.endedFloor = time.Time{}
	}
}
