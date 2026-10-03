package quota

import "sync"

// MaxQuota bounds every tenant quota (10^15).
const MaxQuota = int64(1_000_000_000_000_000)

type account struct {
	quota   int64
	used    int64
	pending int64
}

// Ledger keeps per-tenant quota, committed usage and the sum of outstanding
// positive-delta reservations. All methods are safe for concurrent use; the
// caller (the store) holds a per-tenant lock for the reserve -> install
// sequence, but the ledger also serializes internally.
type Ledger struct {
	mu       sync.Mutex
	accounts map[string]*account
}

func NewLedger() *Ledger {
	return &Ledger{accounts: make(map[string]*account)}
}

func (l *Ledger) get(tenant string) *account {
	a := l.accounts[tenant]
	if a == nil {
		a = &account{}
		l.accounts[tenant] = a
	}
	return a
}

// SetQuota always succeeds; a quota below current usage moves the tenant into
// the over-quota state.
func (l *Ledger) SetQuota(tenant string, q int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.get(tenant).quota = q
}

func (l *Ledger) Quota(tenant string) int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.get(tenant).quota
}

// Used returns the committed usage plus outstanding positive reservations:
// this is what concurrent writers observe.
func (l *Ledger) Used(tenant string) int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	a := l.get(tenant)
	return a.used + a.pending
}

// CommittedUsed returns only installed usage, without pending reservations.
func (l *Ledger) CommittedUsed(tenant string) int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.get(tenant).used
}

// Reserve reserves a positive delta only; d <= 0 needs no reservation and the
// returned token carries the real delta to be committed later. It fails when
// used + pending + d > quota (equality passes).
func (l *Ledger) Reserve(tenant string, d int64) (*Token, bool) {
	t := &Token{ledger: l, tenant: tenant, delta: d}
	if d <= 0 {
		return t, true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	a := l.get(tenant)
	if a.used+a.pending+d > a.quota {
		return nil, false
	}
	a.pending += d
	t.held = true
	return t, true
}

// Commit turns the reservation into committed usage. Negative deltas shrink
// usage. Exactly one of Commit/Release takes effect.
func (t *Token) Commit() {
	l := t.ledger
	l.mu.Lock()
	defer l.mu.Unlock()
	if t.done {
		return
	}
	t.done = true
	a := l.get(t.tenant)
	if t.held {
		a.pending -= t.delta
	}
	a.used += t.delta
}

// Release gives a positive reservation back; the store calls it when the
// object install fails so usage, versions and mtime stay unchanged.
func (t *Token) Release() {
	if t == nil {
		return
	}
	l := t.ledger
	l.mu.Lock()
	defer l.mu.Unlock()
	if t.done || !t.held {
		return
	}
	t.done = true
	a := l.get(t.tenant)
	a.pending -= t.delta
}

// Adjust replaces the token delta under the ledger lock. The store calls it
// when the net delta of an overwrite must be recomputed after another writer
// committed in between: the reservation window spans that commit, which is
// exactly what the pending counter protects. It returns false when the new
// delta would exceed quota (equality passes); the reservation is then gone.
func (t *Token) Adjust(d int64) bool {
	l := t.ledger
	l.mu.Lock()
	defer l.mu.Unlock()
	if t.done {
		return false
	}
	a := l.get(t.tenant)
	held := int64(0)
	if t.held {
		held = t.delta
	}
	// Re-check as if reserving d now, on top of every other pending delta.
	if d > 0 && a.used+(a.pending-held)+d > a.quota {
		if t.held {
			a.pending -= t.delta
		}
		t.held = false
		t.delta = 0
		return false
	}
	if t.held {
		a.pending -= t.delta
	}
	t.delta = d
	t.held = d > 0
	if t.held {
		a.pending += d
	}
	return true
}

// Delta reports the currently reserved net delta.
func (t *Token) Delta() int64 {
	l := t.ledger
	l.mu.Lock()
	defer l.mu.Unlock()
	return t.delta
}

// Token is a quota reservation held between reserve and install.
type Token struct {
	ledger *Ledger
	tenant string
	delta  int64
	held   bool
	done   bool
}
