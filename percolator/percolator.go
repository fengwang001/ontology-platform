package percolator

// WriteKind is the kind of a pending mutation or committed write.
type WriteKind int

const (
	Put WriteKind = iota
	Delete
	Rollback
)

// Mutation is one key's pending write inside a Prewrite batch.
type Mutation struct {
	Key   string
	Kind  WriteKind
	Value string
}

// Version is one committed entry in a key's write list.
//
// Rollback entries use StartTS as both commit timestamp and start
// timestamp. Value is meaningful only for Put.
type Version struct {
	CommitTS int64
	StartTS  int64
	Kind     WriteKind
	Value    string
}

// Lock is at most one per-key pending primary/secondary lock.
type Lock struct {
	StartTS  int64
	Primary  string
	Deadline int64
	Kind     WriteKind
	Value    string
}

// TxnStatus is the resolution outcome of a primary lock.
type TxnStatus int

const (
	StatusLive TxnStatus = iota
	StatusCommitted
	StatusRolledBack
)

// TxnStatusResult is returned by CheckTxnStatus.
type TxnStatusResult struct {
	Status   TxnStatus
	CommitTS int64
}

// GetResult reports whether the key existed at the read timestamp.
type GetResult struct {
	Exists bool
	Value  string
}

// Sentinel errors. Rejected operations never mutate state.
var (
	ErrInvalidArg    = newError("percolator: invalid argument")
	ErrNoSuchTxn     = newError("percolator: transaction does not exist")
	ErrTxnState      = newError("percolator: illegal transaction state")
	ErrInvalidTime   = newError("percolator: invalid timestamp")
	ErrClockSkew     = newError("percolator: clock moved backwards")
	ErrKeyLocked     = newError("percolator: key is locked")
	ErrWriteConflict = newError("percolator: write conflict")
	ErrAlreadyAbort  = newError("percolator: transaction already rolled back")
	ErrLockLost      = newError("percolator: primary lock lost")
)

type lockError struct{ msg string }

func (e *lockError) Error() string { return e.msg }

func newError(msg string) error { return &lockError{msg: msg} }

// maxTime is the upper bound of accepted physical timestamps.
const maxTime int64 = 1_000_000_000_000_000

// Store is the in-memory Percolator-style transactional key-value store.
//
// All operations are safe for concurrent use and are linearizable: every
// concurrent execution is equivalent to some serial ordering.
type Store struct {
	mu   mutex
	data storeData
}

// NewStore creates an empty store. The timestamp oracle starts at 0 and
// every Begin/CommitPrimary allocation increments it by one.
func NewStore() *Store {
	return &Store{
		data: storeData{
			txns: map[int64]*txnInfo{},
			keys: map[string]*keyState{},
		},
	}
}

// Begin allocates and returns a fresh start timestamp.
func (s *Store) Begin() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.oracle++
	st := s.data.oracle
	s.data.txns[st] = &txnInfo{state: txnFresh}
	return st
}

// checkTimeRange validates a physical timestamp range.
// The caller must hold s.mu.
func (d *storeData) checkTimeRange(now int64) error {
	if now < 0 || now > maxTime {
		return ErrInvalidTime
	}
	return nil
}

// checkClockAndAdvance rejects timestamps below the water mark and, on
// success, advances the water mark. Call only once an operation is known
// to succeed so rejected operations leave the water mark untouched.
// The caller must hold s.mu.
func (d *storeData) checkClockAndAdvance(now int64) error {
	if err := d.checkTimeRange(now); err != nil {
		return err
	}
	if now < d.water {
		return ErrClockSkew
	}
	d.water = now
	return nil
}

// keyOf returns the per-key state, creating it on first use.
// The caller must hold the store mutex.
func (d *storeData) keyOf(key string) *keyState {
	k := d.keys[key]
	if k == nil {
		k = &keyState{}
		d.keys[key] = k
	}
	return k
}

// hasRollback reports whether the key carries a Rollback with startTS == st.
func (k *keyState) hasRollback(st int64) bool {
	for _, v := range k.versions {
		if v.Kind == Rollback && v.StartTS == st {
			return true
		}
	}
	return false
}

// committedAfter reports a committed Put/Delete with commitTS > st.
func (k *keyState) committedAfter(st int64) bool {
	for _, v := range k.versions {
		if v.Kind != Rollback && v.CommitTS > st {
			return true
		}
	}
	return false
}

// putVersion inserts a version keeping the list ordered by descending
// commitTS (ties by descending startTS), which makes replays byte-stable.
func (k *keyState) putVersion(v Version) {
	idx := len(k.versions)
	for i, cur := range k.versions {
		if v.CommitTS > cur.CommitTS ||
			(v.CommitTS == cur.CommitTS && v.StartTS > cur.StartTS) {
			idx = i
			break
		}
	}
	k.versions = append(k.versions, Version{})
	copy(k.versions[idx+1:], k.versions[idx:])
	k.versions[idx] = v
}
