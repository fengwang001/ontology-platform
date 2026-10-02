package ontology

import (
	"strconv"
	"sync"
)

const (
	stPending     = "pending"
	stValid       = "valid"
	stInvalid     = "invalid"
	stDeactivated = "deactivated"
	stExpired     = "expired"

	stActive = "active"
	stReady  = "ready"
)

type authz struct {
	id      int64
	account []byte
	ident   string
	status  string // stored status
	expires int64
	// inIndex marks whether the authz is currently linked from the
	// (account, ident) reuse index; removed entries are never revisited.
	inIndex bool
	// inPending marks membership in the account pendingList; entries that
	// leave pending out of order are skipped once and then never rescanned.
	inPending bool
}

type order struct {
	id      int64
	account []byte
	idents  []string
	authzs  []*authz
	expires int64
	status  string // stored status: active or valid
	certSN  int64
}

type accState struct {
	// pendingList supports the amortized per-account pending count. Entries
	// that are no longer effective-pending are dropped as the scan reaches
	// them; every authorization is scanned at most once (inPending guards
	// entries that left pending out of order).
	pendingList []*authz
}

type Machine struct {
	cfg Config

	mu sync.Mutex

	clock int64 // largest now seen by an accepted operation

	nonceSeq    int64              // last issued nonce value
	nonceSet    map[int64]struct{} // live pool contents
	nonceEvict  int64              // smallest candidate still to consider for eviction
	nonceInPool int64              // number of live nonces

	authzs map[int64]*authz
	orders map[int64]*order

	// accs is keyed by account (copied on insertion).
	accs map[string]*accState
	// reuse index: account|ident -> linked authzs; expired/non-valid entries
	// are unlinked lazily during reuse lookups.
	reuse map[string][]*authz
	// failures: account|ident -> in-window failure timestamps (sorted asc).
	failures map[string][]int64

	authzSeq int64
	orderSeq int64
	certSeq  int64

	// Non-exported amortized-work counters (for precise tests).
	probeCount     int64 // reuse-lookup examined authorizations
	clearedCount   int64 // authorizations dropped from a reuse index
	failDropCount  int64 // failure records discarded by window pruning
	pendCleanCount int64 // pending-list entries processed while cleaning
}

func newMachine(c Config) *Machine {
	return &Machine{
		cfg:      c,
		nonceSet: make(map[int64]struct{}),
		authzs:   make(map[int64]*authz),
		orders:   make(map[int64]*order),
		accs:     make(map[string]*accState),
		reuse:    make(map[string][]*authz),
		failures: make(map[string][]int64),
	}
}

func reuseKey(account []byte, ident string) string {
	return string(account) + "\x00" + ident
}

func (m *Machine) acc(account []byte) *accState {
	a := m.accs[string(account)]
	if a == nil {
		a = &accState{}
		m.accs[string(account)] = a
	}
	return a
}

// effStatus computes the effective authorization status at now:
// stored pending/valid becomes expired once now >= expires.
func effStatus(a *authz, now int64) string {
	if (a.status == stPending || a.status == stValid) && now >= a.expires {
		return stExpired
	}
	return a.status
}

// orderStatus derives the effective order status at now.
func orderStatus(o *order, now int64) string {
	if o.status == stValid {
		return stValid
	}
	if now >= o.expires {
		return stInvalid
	}
	allValid := true
	for _, a := range o.authzs {
		switch effStatus(a, now) {
		case stInvalid, stDeactivated, stExpired:
			return stInvalid
		case stValid:
		default:
			allValid = false
		}
	}
	if allValid {
		return stReady
	}
	return stPending
}

func parseRef(ref string, prefix string) (int64, bool) {
	if len(ref) <= len(prefix) || ref[:len(prefix)] != prefix {
		return 0, false
	}
	n, err := strconv.ParseInt(ref[len(prefix):], 10, 64)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

func (m *Machine) checkClock(now int64) *Error {
	if now < 0 || now > 1e15 {
		return errf(KindParam, "now must be in [0,1e15], got %d", now)
	}
	if now < m.clock {
		return errf(KindClock, "now %d regresses from %d", now, m.clock)
	}
	return nil
}

// probeReuse returns the best reusable authorization for the key: among
// effective-valid entries the one with the largest expires, ties broken by
// smallest id. Non-reusable entries are unlinked; each is cleared once.
func (m *Machine) probeReuse(key string, now int64) *authz {
	list := m.reuse[key]
	kept := list[:0]
	var best *authz
	for _, a := range list {
		m.probeCount++
		if effStatus(a, now) == stValid {
			kept = append(kept, a)
			if best == nil || a.expires > best.expires ||
				a.expires == best.expires && a.id < best.id {
				best = a
			}
		} else {
			a.inIndex = false
			m.clearedCount++
		}
	}
	m.reuse[key] = kept
	return best
}

// failureCount prunes records with t+H <= now (out of window) and returns the
// number still in window. Every pruned record is dropped at most once.
func (m *Machine) failureCount(key string, now int64) int {
	list := m.failures[key]
	drop := 0
	for drop < len(list) && list[drop]+m.cfg.H <= now {
		drop++
	}
	if drop > 0 {
		m.failDropCount += int64(drop)
		m.failures[key] = append(list[:0], list[drop:]...)
	}
	return len(m.failures[key])
}

// pendingCount returns the effective-pending authorization count of the
// account. The list is compacted in one amortized pass: an entry still
// effective-pending stays, everything else is dropped, and inPending=false
// entries (left pending out of order earlier) are merely skipped. Each
// authorization is therefore processed at most once across all calls.
func (m *Machine) pendingCount(a *accState, now int64) int {
	list := a.pendingList
	kept := list[:0]
	p := 0
	for _, az := range list {
		m.pendCleanCount++
		if !az.inPending {
			continue
		}
		if az.status == stPending && now < az.expires {
			kept = append(kept, az)
			p++
		} else {
			az.inPending = false
		}
	}
	a.pendingList = kept
	return p
}
