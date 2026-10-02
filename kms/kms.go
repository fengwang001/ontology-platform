// Package kms implements a KMS key lifecycle manager with lazy automatic
// rotation, enable/disable and scheduled-deletion windows.
//
// All operations are safe for concurrent use; the result is equivalent to
// some serial order. Replaying the same operation sequence yields identical
// version numbers, creation timestamps and errors.
package kms

import (
	"container/heap"
	"sync"
)

// MaxNow is the largest acceptable logical clock value (inclusive).
const MaxNow = int64(1_000_000_000_000_000)

// State is the lifecycle state of a key.
type State int

const (
	// Enabled: key can encrypt/decrypt and rotates automatically to new versions.
	Enabled State = iota
	// Disabled: key rejects encrypt/decrypt and does not rotate.
	Disabled
	// Pending: key is scheduled for deletion at DeleteAt.
	Pending
)

func (s State) String() string {
	switch s {
	case Enabled:
		return "Enabled"
	case Disabled:
		return "Disabled"
	case Pending:
		return "Pending"
	}
	return "Unknown"
}

// ErrCode identifies the rejection category of an operation.
type ErrCode int

const (
	// ErrInvalidParam: empty id, bad period/window/now/credential fields.
	ErrInvalidParam ErrCode = iota
	// ErrClockRegression: now is below the max now of accepted operations.
	ErrClockRegression
	// ErrNotFound: no live key with that id (or unknown credential id/gen).
	ErrNotFound
	// ErrKeyDeleted: credential generation is not the live generation.
	ErrKeyDeleted
	// ErrStateConflict: operation does not match the current key state.
	ErrStateConflict
	// ErrStateDenied: encrypt/decrypt on a Disabled or Pending key.
	ErrStateDenied
	// ErrVersionProblem: credential version does not exist or was retired.
	ErrVersionProblem
	// ErrLimitExceeded: Create would exceed the live-key limit.
	ErrLimitExceeded
)

func (c ErrCode) String() string {
	switch c {
	case ErrInvalidParam:
		return "invalid parameter"
	case ErrClockRegression:
		return "clock regression"
	case ErrNotFound:
		return "not found"
	case ErrKeyDeleted:
		return "key deleted"
	case ErrStateConflict:
		return "state conflict"
	case ErrStateDenied:
		return "state denied"
	case ErrVersionProblem:
		return "version problem"
	case ErrLimitExceeded:
		return "limit exceeded"
	}
	return "unknown"
}

// Error describes a rejected operation with locating information.
type Error struct {
	Code    ErrCode // rejection category
	ID      string  // key id involved, if any
	Gen     int64   // credential generation involved, if any
	Version int64   // credential version involved, if any
	State   State   // current key state, for state conflicts/denials
	Reason  string  // precise cause, e.g. "disabled", "pending deletion",
	// "version retired", "version does not exist"
	MaxNow int64 // accepted max now, for clock regressions
	Limit  int64 // configured bound, for param/limit rejections
}

func (e *Error) Error() string {
	return e.Code.String() + ": " + e.Reason
}

// Version is one retained key version.
type Version struct {
	Number    int64
	CreatedAt int64
}

// Credential identifies the key material produced by an Encrypt.
type Credential struct {
	ID      string
	Gen     int64
	Version int64
}

// KeyView is the read-only snapshot returned by Describe.
type KeyView struct {
	ID           string
	State        State
	Gen          int64
	Versions     []Version // retained versions, ascending
	NextRotation int64     // next automatic rotation time d
	DeleteAt     int64     // scheduled deletion time (Pending only)
}

// Manager is a KMS key lifecycle manager. The zero value is not usable;
// construct with NewManager.
type Manager struct {
	mu           sync.Mutex
	v            int64
	wmin         int64
	wmax         int64
	kmax         int64
	maxNow       int64
	keys         map[string]*key
	genMax       map[string]int64
	pending      delHeap
	materialized int64 // versions materialized by real catch-ups
	heapPops     int64 // heap pops performed by reclamation
}

// NewManager builds a Manager. The whole configuration is rejected if any
// bound is invalid: 1 <= V <= 64, 1 <= Wmin <= Wmax <= 1e9,
// 1 <= Kmax <= 1e6.
func NewManager(v, wmin, wmax, kmax int64) (*Manager, error) {
	if v < 1 || v > 64 {
		return nil, &Error{Code: ErrInvalidParam, Reason: "retained-version bound V out of range [1,64]", Limit: v}
	}
	if wmin < 1 || wmax > 1_000_000_000 || wmin > wmax {
		return nil, &Error{Code: ErrInvalidParam, Reason: "deletion window out of range [1,1e9] or Wmin>Wmax", Limit: wmax}
	}
	if kmax < 1 || kmax > 1_000_000 {
		return nil, &Error{Code: ErrInvalidParam, Reason: "live-key bound Kmax out of range [1,1e6]", Limit: kmax}
	}
	return &Manager{
		v:      v,
		wmin:   wmin,
		wmax:   wmax,
		kmax:   kmax,
		keys:   make(map[string]*key),
		genMax: make(map[string]int64),
	}, nil
}

// Materialized reports how many key versions real catch-ups have
// materialized so far. Each single catch-up materializes at most V versions.
func (m *Manager) Materialized() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.materialized
}

// HeapPops reports how many heap pops lazy reclamation has performed so far.
func (m *Manager) HeapPops() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.heapPops
}

// Create makes a new Enabled key with version 1 created at now and
// d = now+P, returning its generation (1 for the first creation of id).
func (m *Manager) Create(id string, p, now int64) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id == "" {
		return 0, &Error{Code: ErrInvalidParam, Reason: "empty key id"}
	}
	if !validPeriod(p) {
		return 0, &Error{Code: ErrInvalidParam, ID: id, Reason: "rotation period not 0 and out of range [60,1e9]", Limit: p}
	}
	if err := checkNow(now); err != nil {
		return 0, err
	}
	if err := m.checkClock(now); err != nil {
		return 0, err
	}
	if k := m.getKey(id, now); k != nil {
		return 0, &Error{Code: ErrStateConflict, ID: id, State: k.state, Reason: "live key with same id already exists"}
	}
	if m.liveCount(now)+1 > m.kmax {
		return 0, &Error{Code: ErrLimitExceeded, ID: id, Reason: "live key count would exceed Kmax", Limit: m.kmax}
	}
	m.accept(now)
	gen := m.genMax[id] + 1
	m.genMax[id] = gen
	m.keys[id] = &key{
		state:    Enabled,
		gen:      gen,
		p:        p,
		versions: []Version{{Number: 1, CreatedAt: now}},
		d:        now + p,
	}
	return gen, nil
}

// Encrypt returns a credential bound to the current max version.
func (m *Manager) Encrypt(id string, now int64) (Credential, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id == "" {
		return Credential{}, &Error{Code: ErrInvalidParam, Reason: "empty key id"}
	}
	if err := checkNow(now); err != nil {
		return Credential{}, err
	}
	if err := m.checkClock(now); err != nil {
		return Credential{}, err
	}
	k := m.getKey(id, now)
	if k == nil {
		return Credential{}, &Error{Code: ErrNotFound, ID: id, Reason: "no live key with this id"}
	}
	if k.state != Enabled {
		return Credential{}, deniedErr(id, k.state)
	}
	m.accept(now)
	m.materialized += k.catchUp(now, m.v)
	return Credential{ID: id, Gen: k.gen, Version: k.maxVersion()}, nil
}

// Decrypt reports whether the credential version is the current max version.
func (m *Manager) Decrypt(cred Credential, now int64) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k, err := m.checkCredential(cred, now)
	if err != nil {
		return false, err
	}
	// Version checks run on a copy: any rejection discards the catch-up.
	cp := k.clone()
	cp.catchUp(now, m.v)
	if err := checkVersion(cred, cp.versions[0].Number, cp.versions[len(cp.versions)-1].Number); err != nil {
		return false, err
	}
	m.accept(now)
	m.materialized += k.catchUp(now, m.v)
	return cred.Version == k.maxVersion(), nil
}

// ReEncrypt checks like Decrypt and returns a credential at the current max
// version; changed is false when the credential was already current.
func (m *Manager) ReEncrypt(cred Credential, now int64) (Credential, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k, err := m.checkCredential(cred, now)
	if err != nil {
		return Credential{}, false, err
	}
	cp := k.clone()
	cp.catchUp(now, m.v)
	if err := checkVersion(cred, cp.versions[0].Number, cp.versions[len(cp.versions)-1].Number); err != nil {
		return Credential{}, false, err
	}
	m.accept(now)
	m.materialized += k.catchUp(now, m.v)
	fresh := Credential{ID: cred.ID, Gen: cred.Gen, Version: k.maxVersion()}
	if fresh.Version == cred.Version {
		return cred, false, nil
	}
	return fresh, true, nil
}

// Disable moves an Enabled key to Disabled (after catch-up).
func (m *Manager) Disable(id string, now int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k, err := m.checkID(id, now)
	if err != nil {
		return err
	}
	if k.state != Enabled {
		return &Error{Code: ErrStateConflict, ID: id, State: k.state, Reason: "disable requires an Enabled key"}
	}
	m.accept(now)
	m.materialized += k.catchUp(now, m.v)
	k.state = Disabled
	return nil
}

// Enable moves a Disabled key back to Enabled. Missed rotations during the
// Disabled period are not caught up: if d <= now then d becomes now+P.
func (m *Manager) Enable(id string, now int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k, err := m.checkID(id, now)
	if err != nil {
		return err
	}
	if k.state != Disabled {
		return &Error{Code: ErrStateConflict, ID: id, State: k.state, Reason: "enable requires a Disabled key"}
	}
	m.accept(now)
	k.state = Enabled
	if k.p > 0 && k.d <= now {
		k.d = now + k.p
	}
	return nil
}

// ScheduleDeletion moves an Enabled or Disabled key to Pending with
// deleteAt = now + w.
func (m *Manager) ScheduleDeletion(id string, w, now int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id == "" {
		return &Error{Code: ErrInvalidParam, Reason: "empty key id"}
	}
	if w < m.wmin || w > m.wmax {
		return &Error{Code: ErrInvalidParam, ID: id, Reason: "deletion window outside [Wmin,Wmax]", Limit: w}
	}
	if err := checkNow(now); err != nil {
		return err
	}
	if err := m.checkClock(now); err != nil {
		return err
	}
	k := m.getKey(id, now)
	if k == nil {
		return &Error{Code: ErrNotFound, ID: id, Reason: "no live key with this id"}
	}
	if k.state != Enabled && k.state != Disabled {
		return &Error{Code: ErrStateConflict, ID: id, State: k.state, Reason: "schedule-deletion requires Enabled or Disabled"}
	}
	m.accept(now)
	if k.state == Enabled {
		m.materialized += k.catchUp(now, m.v)
	}
	k.state = Pending
	k.deleteAt = now + w
	k.heapToken++
	heap.Push(&m.pending, delEntry{deleteAt: k.deleteAt, id: id, token: k.heapToken})
	return nil
}

// CancelDeletion moves a Pending key to Disabled (not Enabled); d is kept.
func (m *Manager) CancelDeletion(id string, now int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k, err := m.checkID(id, now)
	if err != nil {
		return err
	}
	if k.state != Pending {
		return &Error{Code: ErrStateConflict, ID: id, State: k.state, Reason: "cancel-deletion requires a Pending key"}
	}
	m.accept(now)
	k.state = Disabled
	k.heapToken++ // invalidates the live heap entry, which becomes stale
	return nil
}

// Describe returns a consistent view of a key at now, computing a virtual
// catch-up for Enabled keys without mutating any state or the clock.
func (m *Manager) Describe(id string, now int64) (KeyView, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id == "" {
		return KeyView{}, &Error{Code: ErrInvalidParam, Reason: "empty key id"}
	}
	if err := checkNow(now); err != nil {
		return KeyView{}, err
	}
	if err := m.checkClock(now); err != nil {
		return KeyView{}, err
	}
	k := m.getKey(id, now)
	if k == nil {
		return KeyView{}, &Error{Code: ErrNotFound, ID: id, Reason: "no live key with this id"}
	}
	cp := k.clone()
	if cp.state == Enabled {
		cp.catchUp(now, m.v) // virtual: no state, clock or counter changes
	}
	return KeyView{
		ID:           id,
		State:        cp.state,
		Gen:          cp.gen,
		Versions:     cp.versions,
		NextRotation: cp.d,
		DeleteAt:     cp.deleteAt,
	}, nil
}
