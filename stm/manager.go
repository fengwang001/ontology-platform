// Package stm implements a contention manager for software transactional
// memory. It arbitrates conflicts between transactions opening shared
// objects using a score (kp), per-object retry counters (att) and a
// privilege level derived from the number of times a transaction has been
// aborted by others (ab).
//
// All methods are safe for concurrent use; the result of concurrent calls
// is equivalent to some serial order. The manager is fully deterministic:
// replaying the same call sequence yields identical results, scores and
// delays.
package stm

import (
	"errors"
	"sync"
)

// Mode is the way a transaction holds an object.
type Mode int

const (
	ModeNone  Mode = iota // not held
	ModeRead              // read-held
	ModeWrite             // write-held
)

// Config holds the manager construction parameters. Every field is range
// checked by NewManager; an out-of-range field rejects the whole
// configuration.
type Config struct {
	Objects        int // M: number of objects, 1..64
	PrivThreshold  int // L: aborts needed to become privileged, 1..16
	BaseDelay      int // D: base backoff delay, 1..1000
	ExpCap         int // E: exponent cap for backoff, 0..20
	ScoreCap       int // P: upper bound of the score kp, 1..1000
	ForceThreshold int // Q: retry count forcing a win over non-privileged, 1..16
}

// Rejection reasons. For each rejected call only the first applicable
// reason is reported, in the order: unknown transaction, bad state,
// object out of range (Open only).
var (
	ErrInvalidConfig = errors.New("stm: invalid configuration")
	ErrNoSuchTxn     = errors.New("stm: no such transaction")
	ErrBadState      = errors.New("stm: transaction state does not allow this call")
	ErrNoSuchObject  = errors.New("stm: object index out of range")
)

// OpenResult is the outcome of a successful Open call.
type OpenResult struct {
	// Aborted lists the ids of the transactions aborted by this call, in
	// ascending order. Empty when the caller acquired the object without
	// aborting anyone or when it has to wait.
	Aborted []int
	// Wait reports that the caller lost the arbitration and must back off;
	// no adversary was touched.
	Wait bool
	// Delay is the backoff delay D * 2^min(k, E), where k is the retry
	// counter att[o] before this call incremented it. Only set when Wait
	// is true.
	Delay int64
}

type txnState int

const (
	stateActive txnState = iota
	stateAborted
	stateCommitted
)

type txn struct {
	id    int
	state txnState
	kp    int
	ab    int
	holds map[int]Mode
	att   map[int]int
}

type object struct {
	writer  int // transaction id, 0 means none
	readers map[int]bool
}

// Manager arbitrates conflicts between concurrent transactions. The zero
// value is not usable; construct one with NewManager.
type Manager struct {
	mu   sync.Mutex
	cfg  Config
	txns []*txn // txns[id-1]
	objs []object
}

// NewManager validates cfg and returns a ready-to-use Manager. Any
// out-of-range field rejects the whole configuration.
func NewManager(cfg Config) (*Manager, error) {
	if cfg.Objects < 1 || cfg.Objects > 64 ||
		cfg.PrivThreshold < 1 || cfg.PrivThreshold > 16 ||
		cfg.BaseDelay < 1 || cfg.BaseDelay > 1000 ||
		cfg.ExpCap < 0 || cfg.ExpCap > 20 ||
		cfg.ScoreCap < 1 || cfg.ScoreCap > 1000 ||
		cfg.ForceThreshold < 1 || cfg.ForceThreshold > 16 {
		return nil, ErrInvalidConfig
	}
	m := &Manager{cfg: cfg, objs: make([]object, cfg.Objects)}
	for i := range m.objs {
		m.objs[i].readers = map[int]bool{}
	}
	return m, nil
}

// Begin starts a new transaction and returns its id. Ids are handed out
// in increasing order starting at 1.
func (m *Manager) Begin() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := len(m.txns) + 1
	m.txns = append(m.txns, &txn{
		id:    id,
		state: stateActive,
		holds: map[int]Mode{},
		att:   map[int]int{},
	})
	return id
}

// txnByID returns the transaction or ErrNoSuchTxn.
func (m *Manager) txnByID(id int) (*txn, error) {
	if id < 1 || id > len(m.txns) {
		return nil, ErrNoSuchTxn
	}
	return m.txns[id-1], nil
}

// activeTxn returns the transaction if it may perform Open/Commit/Abort.
func (m *Manager) activeTxn(id int) (*txn, error) {
	t, err := m.txnByID(id)
	if err != nil {
		return nil, err
	}
	if t.state != stateActive {
		return nil, ErrBadState
	}
	return t, nil
}

// backoff computes D * 2^min(k, E).
func (m *Manager) backoff(k int) int64 {
	if k > m.cfg.ExpCap {
		k = m.cfg.ExpCap
	}
	return int64(m.cfg.BaseDelay) << uint(k)
}

// privileged reports whether the transaction has been aborted by others
// at least L times.
func (m *Manager) privileged(t *txn) bool {
	return t.ab >= m.cfg.PrivThreshold
}

// adversaries returns the ids of the transactions blocking t from
// acquiring o with the requested mode, in ascending order. It inspects
// only the object's writer and reader set, never the transaction table.
func (m *Manager) adversaries(t *txn, o int, write bool) []int {
	obj := &m.objs[o]
	var adv []int
	if obj.writer != 0 && obj.writer != t.id {
		adv = append(adv, obj.writer)
	}
	if write {
		for r := range obj.readers {
			if r != t.id {
				adv = append(adv, r)
			}
		}
	}
	for i := 1; i < len(adv); i++ { // insertion sort, adv is tiny
		for j := i; j > 0 && adv[j-1] > adv[j]; j-- {
			adv[j-1], adv[j] = adv[j], adv[j-1]
		}
	}
	return adv
}

// beats reports whether t prevails over adversary e given the current
// retry counter k of t on the contended object.
func (m *Manager) beats(t, e *txn, k int) bool {
	tPriv := m.privileged(t)
	ePriv := m.privileged(e)
	switch {
	case tPriv && ePriv:
		return t.id < e.id
	case tPriv:
		return true
	case ePriv:
		return false
	default:
		return k >= m.cfg.ForceThreshold || t.kp+k > e.kp
	}
}

// release drops t's hold on object o, updating the object's sets.
func (m *Manager) release(t *txn, o int) {
	obj := &m.objs[o]
	if obj.writer == t.id {
		obj.writer = 0
	}
	delete(obj.readers, t.id)
	delete(t.holds, o)
}

// releaseAll drops every hold of t and clears its retry counters.
func (m *Manager) releaseAll(t *txn) {
	for o := range t.holds {
		m.release(t, o)
	}
	t.att = map[int]int{}
}

// abortByOther marks e as aborted by another transaction: ab increments,
// kp halves rounding up, holds and retry counters are cleared.
func (m *Manager) abortByOther(e *txn) {
	m.releaseAll(e)
	e.state = stateAborted
	e.ab++
	e.kp = (e.kp + 1) / 2
}

// grant gives t the requested mode on o, scoring a point only when t did
// not already hold o during this attempt, and clearing att[o] either way.
func (m *Manager) grant(t *txn, o int, write bool) {
	if t.holds[o] == ModeNone {
		if t.kp < m.cfg.ScoreCap {
			t.kp++
		}
	}
	t.att[o] = 0
	obj := &m.objs[o]
	if write {
		delete(obj.readers, t.id)
		obj.writer = t.id
		t.holds[o] = ModeWrite
	} else {
		obj.readers[t.id] = true
		t.holds[o] = ModeRead
	}
}

// Open lets transaction id acquire object o in read mode (write=false) or
// write mode (write=true). See the package documentation for the
// arbitration rules.
func (m *Manager) Open(id, o int, write bool) (OpenResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, err := m.activeTxn(id)
	if err != nil {
		return OpenResult{}, err
	}
	if o < 0 || o >= m.cfg.Objects {
		return OpenResult{}, ErrNoSuchObject
	}
	need := ModeRead
	if write {
		need = ModeWrite
	}
	if t.holds[o] >= need {
		return OpenResult{}, nil // already held at least as strongly
	}
	adv := m.adversaries(t, o, write)
	if len(adv) == 0 {
		m.grant(t, o, write)
		return OpenResult{}, nil
	}
	k := t.att[o]
	for _, eid := range adv {
		if !m.beats(t, m.txns[eid-1], k) {
			t.att[o] = k + 1
			return OpenResult{Wait: true, Delay: m.backoff(k)}, nil
		}
	}
	aborted := make([]int, 0, len(adv))
	for _, eid := range adv {
		m.abortByOther(m.txns[eid-1])
		aborted = append(aborted, eid)
	}
	m.grant(t, o, write)
	return OpenResult{Aborted: aborted}, nil
}

// Restart reactivates an aborted transaction, clearing its holds and
// retry counters while keeping kp and ab. It returns the backoff delay
// D * 2^min(max(ab-1, 0), E).
func (m *Manager) Restart(id int) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, err := m.txnByID(id)
	if err != nil {
		return 0, err
	}
	if t.state != stateAborted {
		return 0, ErrBadState
	}
	t.state = stateActive
	t.holds = map[int]Mode{}
	t.att = map[int]int{}
	k := t.ab - 1
	if k < 0 {
		k = 0
	}
	return m.backoff(k), nil
}

// Commit releases all holds of an active transaction and marks it
// committed.
func (m *Manager) Commit(id int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, err := m.activeTxn(id)
	if err != nil {
		return err
	}
	m.releaseAll(t)
	t.state = stateCommitted
	return nil
}

// Abort voluntarily aborts an active transaction, releasing its holds
// without changing kp or ab.
func (m *Manager) Abort(id int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, err := m.activeTxn(id)
	if err != nil {
		return err
	}
	m.releaseAll(t)
	t.state = stateAborted
	return nil
}

// Stats returns the current score kp and abort count ab of a transaction.
func (m *Manager) Stats(id int) (kp, ab int, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, err := m.txnByID(id)
	if err != nil {
		return 0, 0, err
	}
	return t.kp, t.ab, nil
}

// Holds returns a snapshot of the objects currently held by a transaction.
func (m *Manager) Holds(id int) (map[int]Mode, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, err := m.txnByID(id)
	if err != nil {
		return nil, err
	}
	holds := make(map[int]Mode, len(t.holds))
	for o, mode := range t.holds {
		holds[o] = mode
	}
	return holds, nil
}
