package deadlock

import (
	"fmt"
	"io"
	"math/rand"
	"sort"
	"sync"
)

// Txn is a transaction known to the detector.
type Txn struct {
	ID      string
	StartTS int64
	State   TxnState
}

// Cluster is the distributed deadlock detector: a set of sites, the
// transactions, and the simulated network carrying probes between sites.
// All exported methods are safe for concurrent use; internally a single
// mutex serializes lock-table mutations and message delivery, which also
// makes replay of the same operation and delivery sequence deterministic.
type Cluster struct {
	mu sync.Mutex

	sites map[string]*Site
	txns  map[string]*Txn
	// startTS uniqueness index.
	tsIndex map[int64]string
	// waitingSite[txn] is the site where txn is currently blocked.
	waitingSite map[string]string

	// network state
	rng      *rand.Rand
	maxDelay int64
	seq      int64
	pending  msgHeap

	victims []string

	logMu sync.Mutex
	logW  io.Writer
}

// NewCluster creates a detector. seed drives the injected network delays so
// that replaying the same operations with the same seed yields the same
// delivery order and the same victim sequence. logw receives the audit log
// (inputs, outputs, and decision justifications); it may be nil.
func NewCluster(seed int64, logw io.Writer) *Cluster {
	if logw == nil {
		logw = io.Discard
	}
	return &Cluster{
		sites:       make(map[string]*Site),
		txns:        make(map[string]*Txn),
		tsIndex:     make(map[int64]string),
		waitingSite: make(map[string]string),
		rng:         rand.New(rand.NewSource(seed)),
		maxDelay:    16,
		logW:        logw,
	}
}

func (c *Cluster) logf(format string, args ...any) {
	c.logMu.Lock()
	defer c.logMu.Unlock()
	fmt.Fprintf(c.logW, format+"\n", args...)
}

// AddSite registers a site.
func (c *Cluster) AddSite(name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.sites[name]; ok {
		return fmt.Errorf("site %q: %w", name, ErrDuplicateTxn)
	}
	c.sites[name] = newSite(name)
	c.logf("[SITE] add site=%s", name)
	return nil
}

// AddLock registers a lock on a site.
func (c *Cluster) AddLock(site, lock string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.sites[site]
	if !ok {
		return fmt.Errorf("lock %s@%s: %w", lock, site, ErrUnknownSite)
	}
	if _, ok := s.locks[lock]; ok {
		return fmt.Errorf("lock %s@%s already exists", lock, site)
	}
	s.locks[lock] = newLockState()
	c.logf("[LOCK] add lock=%s@%s", lock, site)
	return nil
}

// Begin registers a transaction with a globally unique start timestamp.
func (c *Cluster) Begin(txn string, startTS int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.logf("[BEGIN] txn=%s startTS=%d", txn, startTS)
	if _, ok := c.txns[txn]; ok {
		c.logf("[REJECT] begin txn=%s: %v", txn, ErrDuplicateTxn)
		return fmt.Errorf("txn %q: %w", txn, ErrDuplicateTxn)
	}
	if other, ok := c.tsIndex[startTS]; ok {
		c.logf("[REJECT] begin txn=%s startTS=%d (held by %s): %v", txn, startTS, other, ErrDuplicateStartTS)
		return fmt.Errorf("txn %q startTS %d (held by %q): %w", txn, startTS, other, ErrDuplicateStartTS)
	}
	c.txns[txn] = &Txn{ID: txn, StartTS: startTS, State: StateActive}
	c.tsIndex[startTS] = txn
	return nil
}

// Request asks for lock on site in the given mode. It returns nil whether
// the lock was granted immediately or the request was queued; a queued
// request puts the transaction into the waiting state and launches probes.
//
// Validation is total and happens before any mutation: unknown site/lock,
// unknown, finished or already-waiting transactions are rejected with
// distinguishable errors and leave the lock table untouched. Re-requesting
// a lock the transaction already holds is a no-op success.
func (c *Cluster) Request(txn, site, lock string, mode Mode) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.logf("[REQUEST] txn=%s lock=%s@%s mode=%s", txn, lock, site, mode)

	s, ok := c.sites[site]
	if !ok {
		return c.reject("request", txn, ErrUnknownSite, "site=%s", site)
	}
	l, ok := s.locks[lock]
	if !ok {
		return c.reject("request", txn, ErrUnknownLock, "lock=%s@%s", lock, site)
	}
	t, ok := c.txns[txn]
	if !ok {
		return c.reject("request", txn, ErrUnknownTxn, "")
	}
	if t.State == StateCommitted || t.State == StateAborted {
		return c.reject("request", txn, ErrTxnFinished, "state=%s", t.State)
	}
	if t.State == StateWaiting {
		return c.reject("request", txn, ErrTxnWaiting, "")
	}

	if _, held := l.holders[txn]; held {
		c.logf("[GRANT] txn=%s lock=%s@%s mode=%s (already held)", txn, lock, site, mode)
		return nil
	}

	if len(l.queue) == 0 && l.holdersCompatible(mode) {
		l.holders[txn] = mode
		c.logf("[GRANT] txn=%s lock=%s@%s mode=%s", txn, lock, site, mode)
		return nil
	}

	// Incompatible with holders or someone is already queued: enqueue and
	// record the local wait-for edges, then launch probes along them.
	waits := waitSetFor(l, txn, mode)
	l.queue = append(l.queue, waitEntry{txn: txn, mode: mode})
	t.State = StateWaiting
	s.waits[txn] = waits
	c.waitingSite[txn] = site
	c.logf("[QUEUE] txn=%s lock=%s@%s mode=%s waits=%v", txn, lock, site, mode, sortedKeys(waits))
	for _, w := range sortedKeys(waits) {
		c.sendLocked(Probe{Initiator: txn, Path: []string{txn, w}})
	}
	return nil
}

// Release releases a lock held by txn and grants eligible waiters in FIFO
// order.
func (c *Cluster) Release(txn, site, lock string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.logf("[RELEASE] txn=%s lock=%s@%s", txn, lock, site)

	s, ok := c.sites[site]
	if !ok {
		return c.reject("release", txn, ErrUnknownSite, "site=%s", site)
	}
	l, ok := s.locks[lock]
	if !ok {
		return c.reject("release", txn, ErrUnknownLock, "lock=%s@%s", lock, site)
	}
	if _, ok := c.txns[txn]; !ok {
		return c.reject("release", txn, ErrUnknownTxn, "")
	}
	if _, held := l.holders[txn]; !held {
		return c.reject("release", txn, ErrLockNotHeld, "lock=%s@%s", lock, site)
	}

	delete(l.holders, txn)
	c.logf("[RELEASED] txn=%s lock=%s@%s", txn, lock, site)
	c.grantWaitersLocked(s, lock, l)
	return nil
}

// End commits an active transaction and releases all of its locks.
// Ending a waiting transaction is rejected: it must be aborted or granted
// first, so that a commit never silently discards a wait.
func (c *Cluster) End(txn string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.logf("[END] txn=%s", txn)

	t, ok := c.txns[txn]
	if !ok {
		return c.reject("end", txn, ErrUnknownTxn, "")
	}
	if t.State == StateCommitted || t.State == StateAborted {
		return c.reject("end", txn, ErrTxnFinished, "state=%s", t.State)
	}
	if t.State == StateWaiting {
		return c.reject("end", txn, ErrTxnWaiting, "")
	}
	c.releaseAllLocked(txn)
	t.State = StateCommitted
	c.logf("[COMMITTED] txn=%s", txn)
	return nil
}

// reject logs a rejected operation with its distinguishable reason.
func (c *Cluster) reject(op, txn string, err error, detail string, args ...any) error {
	if detail != "" {
		c.logf("[REJECT] op=%s txn=%s %s: %v", op, txn, fmt.Sprintf(detail, args...), err)
	} else {
		c.logf("[REJECT] op=%s txn=%s: %v", op, txn, err)
	}
	return fmt.Errorf("%s txn %q: %w", op, txn, err)
}

// grantWaitersLocked grants the FIFO prefix of the queue that is compatible
// with the current holders. Shared requests behind a granted shared request
// are granted together; the first incompatible waiter stops the scan.
func (c *Cluster) grantWaitersLocked(s *Site, lockName string, l *lockState) {
	for len(l.queue) > 0 {
		w := l.queue[0]
		if !l.holdersCompatible(w.mode) {
			break
		}
		l.queue = l.queue[1:]
		l.holders[w.txn] = w.mode
		if t, ok := c.txns[w.txn]; ok && t.State == StateWaiting {
			t.State = StateActive
		}
		delete(s.waits, w.txn)
		delete(c.waitingSite, w.txn)
		c.logf("[GRANT] txn=%s lock=%s@%s mode=%s (from queue)", w.txn, lockName, s.name, w.mode)
	}
}

// releaseAllLocked drops every lock held by txn, removes it from every
// queue, clears its wait-for edges, and re-grants affected waiters.
func (c *Cluster) releaseAllLocked(txn string) {
	for _, siteName := range c.sortedSiteNames() {
		s := c.sites[siteName]
		delete(s.waits, txn)
		for _, lockName := range sortedLockNames(s) {
			l := s.locks[lockName]
			if _, held := l.holders[txn]; held {
				delete(l.holders, txn)
				c.logf("[RELEASED] txn=%s lock=%s@%s", txn, lockName, s.name)
			}
			filtered := l.queue[:0]
			for _, e := range l.queue {
				if e.txn != txn {
					filtered = append(filtered, e)
				}
			}
			l.queue = filtered
			c.grantWaitersLocked(s, lockName, l)
		}
	}
	delete(c.waitingSite, txn)
}

func (c *Cluster) sortedSiteNames() []string {
	names := make([]string, 0, len(c.sites))
	for name := range c.sites {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func sortedLockNames(s *Site) []string {
	names := make([]string, 0, len(s.locks))
	for name := range s.locks {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// abortLocked aborts txn as a deadlock victim. It is idempotent: if the
// transaction is no longer waiting (already aborted, committed, or granted
// since the probe was sent) nothing happens, which guarantees that a cycle
// reported by multiple probes still aborts exactly one transaction.
func (c *Cluster) abortLocked(txn string) {
	t, ok := c.txns[txn]
	if !ok || t.State != StateWaiting {
		c.logf("[ABORT-SKIP] txn=%s no longer waiting", txn)
		return
	}
	c.releaseAllLocked(txn)
	t.State = StateAborted
	c.victims = append(c.victims, txn)
	c.logf("[ABORT] txn=%s state=aborted locks=released", txn)
}

// Victims returns the aborted transactions in abort order.
func (c *Cluster) Victims() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.victims...)
}

// TxnState returns the current state of a transaction.
func (c *Cluster) TxnState(txn string) (TxnState, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	t, ok := c.txns[txn]
	if !ok {
		return StateActive, fmt.Errorf("txn %q: %w", txn, ErrUnknownTxn)
	}
	return t.State, nil
}

// Holds reports whether txn currently holds lock on site.
func (c *Cluster) Holds(txn, site, lock string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.sites[site]
	if !ok {
		return false
	}
	l, ok := s.locks[lock]
	if !ok {
		return false
	}
	_, held := l.holders[txn]
	return held
}

// PendingMessages reports how many probes are still in flight.
func (c *Cluster) PendingMessages() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.pending)
}

// HasCycle reports whether the current wait-for graph contains a cycle.
// Used by tests to assert quiescence after all messages are delivered.
func (c *Cluster) HasCycle() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hasCycleLocked()
}

func (c *Cluster) hasCycleLocked() bool {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make(map[string]int)
	var visit func(t string) bool
	visit = func(t string) bool {
		color[t] = gray
		site, ok := c.waitingSite[t]
		if ok {
			for next := range c.sites[site].waits[t] {
				if color[next] == gray {
					return true
				}
				if color[next] == white && visit(next) {
					return true
				}
			}
		}
		color[t] = black
		return false
	}
	for txn := range c.waitingSite {
		if color[txn] == white && visit(txn) {
			return true
		}
	}
	return false
}

// edgeExistsLocked reports whether the wait-for edge from->to exists now.
func (c *Cluster) edgeExistsLocked(from, to string) bool {
	site, ok := c.waitingSite[from]
	if !ok {
		return false
	}
	return c.sites[site].waits[from][to]
}

func sortedKeys(set map[string]bool) []string {
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
