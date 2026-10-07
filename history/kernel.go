package history

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

// Config configures a Kernel.
type Config struct {
	CacheCapacity int           // max cached documents; 0 disables caching
	CacheTTL      time.Duration // max cache lifetime; age == TTL counts as expired
	Now           time.Time     // initial logical clock
}

// Option customizes a Kernel.
type Option func(*Kernel)

// WithLogger attaches a logger receiving inputs, outputs and the
// rationale behind every kernel decision.
func WithLogger(f func(format string, args ...any)) Option {
	return func(k *Kernel) { k.logf = f }
}

// Kernel coordinates the entry list, current position, traversal
// scheduling, cache eligibility and cache eviction. A single mutex
// serializes every operation, so concurrent callers observe some
// serial order. Traversal requests queue in the scheduler; any other
// operation drains them first, preserving submission order.
type Kernel struct {
	mu      sync.Mutex
	list    *entryList
	docs    map[uint64]*Document
	cache   *bfCache
	sched   scheduler
	now     time.Time
	nextDoc uint64
	logf    func(format string, args ...any)
}

func NewKernel(cfg Config, opts ...Option) (*Kernel, error) {
	if cfg.CacheCapacity < 0 {
		return nil, fmt.Errorf("%w: cache capacity %d", ErrInvalidArgument, cfg.CacheCapacity)
	}
	if cfg.CacheTTL < 0 {
		return nil, fmt.Errorf("%w: cache ttl %s", ErrInvalidArgument, cfg.CacheTTL)
	}
	k := &Kernel{
		list:    newEntryList(),
		docs:    make(map[uint64]*Document),
		cache:   newBFCache(cfg.CacheCapacity, cfg.CacheTTL),
		now:     cfg.Now,
		nextDoc: 1,
	}
	for _, o := range opts {
		o(k)
	}
	k.log("kernel created: capacity=%d ttl=%s now=%s", cfg.CacheCapacity, cfg.CacheTTL, cfg.Now)
	return k, nil
}

func (k *Kernel) log(format string, args ...any) {
	if k.logf != nil {
		k.logf(format, args...)
	}
}

// checkClock rejects operations dated before the logical clock. It does
// not commit: the caller advances the clock only once the operation can
// no longer be rejected, so rejected operations never move the clock.
func (k *Kernel) checkClock(at time.Time) error {
	if at.Before(k.now) {
		return fmt.Errorf("%w: op at %s is before now %s", ErrClockRegression, at, k.now)
	}
	return nil
}

func (k *Kernel) allocDoc() uint64 {
	id := k.nextDoc
	k.nextDoc++
	return id
}

// Navigate pushes a new entry after the current position, truncating
// the forward entries. A same-document navigation shares the current
// document (which stays active); a cross-document navigation makes the
// current document leave (cached when eligible, unloaded otherwise)
// and loads a fresh document.
func (k *Kernel) Navigate(url string, state any, sameDoc bool, at time.Time) (Entry, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if url == "" {
		return Entry{}, fmt.Errorf("%w: empty url", ErrInvalidArgument)
	}
	k.drainLocked()
	if err := k.checkClock(at); err != nil {
		return Entry{}, err
	}
	k.now = at
	cur, ok := k.list.current()
	if sameDoc && ok {
		e, truncated := k.list.push(url, cur.DocID, state)
		k.onTruncated(truncated)
		k.log("navigate url=%q same-document -> seq=%d doc=%d pos=%d truncated=%d; doc %d stays active",
			url, e.Seq, e.DocID, k.list.pos, len(truncated), cur.DocID)
		return e, nil
	}
	newID := k.allocDoc()
	e, truncated := k.list.push(url, newID, state)
	k.onTruncated(truncated)
	if ok {
		k.leaveDocument(cur.DocID)
	}
	k.docs[newID] = newDocument(newID)
	k.log("navigate url=%q cross-document -> seq=%d doc=%d pos=%d truncated=%d",
		url, e.Seq, newID, k.list.pos, len(truncated))
	return e, nil
}

// Replace changes only the current entry's URL and state object.
func (k *Kernel) Replace(url string, state any, at time.Time) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if url == "" {
		return fmt.Errorf("%w: empty url", ErrInvalidArgument)
	}
	k.drainLocked()
	if err := k.checkClock(at); err != nil {
		return err
	}
	if _, ok := k.list.current(); !ok {
		return fmt.Errorf("%w: no current entry to replace", ErrInvalidState)
	}
	k.now = at
	k.list.replace(url, state)
	k.log("replace url=%q -> pos=%d state=%v (seq and document unchanged)", url, k.list.pos, state)
	return nil
}

// Traverse queues a relative traversal and returns its token. The
// request takes effect at the next drain point; only the newest queued
// request is applied, earlier ones are reported as superseded.
func (k *Kernel) Traverse(delta int, at time.Time) uint64 {
	k.mu.Lock()
	defer k.mu.Unlock()
	token := k.sched.submit(delta, at)
	k.log("traverse submitted: token=%d delta=%d", token, delta)
	return token
}

// Drain applies the newest queued traversal (if any) and reports the
// fate of every queued request in submission order.
func (k *Kernel) Drain() []Outcome {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.drainLocked()
}

func (k *Kernel) drainLocked() []Outcome {
	outcomes := k.sched.drain(k.applyTraversal)
	for _, o := range outcomes {
		if o.Err != nil {
			k.log("traverse token=%d delta=%d -> rejected: %v", o.Token, o.Delta, o.Err)
		} else {
			k.log("traverse token=%d delta=%d -> pos=%d", o.Token, o.Delta, o.Pos)
		}
	}
	return outcomes
}

func (k *Kernel) applyTraversal(req traverseRequest) Outcome {
	fail := func(err error) Outcome {
		return Outcome{Token: req.token, Delta: req.delta, Pos: -1, Err: err}
	}
	target, ok := k.list.locate(req.delta)
	if !ok {
		return fail(fmt.Errorf("%w: delta %d out of bounds", ErrInvalidArgument, req.delta))
	}
	if err := k.checkClock(req.at); err != nil {
		return fail(err)
	}
	k.now = req.at
	cur, _ := k.list.current()
	tgt := k.list.at(target)
	switch {
	case req.delta == 0:
		newID := k.reload(cur.DocID, "reload of current entry")
		k.log("traverse delta=0 -> reload: doc %d replaced by doc %d at pos=%d", cur.DocID, newID, k.list.pos)
	case tgt.DocID == cur.DocID:
		k.list.moveTo(target)
		k.log("traverse delta=%d -> same-document move to pos=%d; doc %d stays active, nothing cached",
			req.delta, target, cur.DocID)
	default:
		k.leaveDocument(cur.DocID)
		if d := k.cache.remove(tgt.DocID); d != nil {
			d.status = DocActive
			k.list.moveTo(target)
			k.log("traverse delta=%d -> restored doc %d from cache at pos=%d; state=%v",
				req.delta, d.ID, target, k.list.at(target).State)
		} else {
			newID := k.reload(tgt.DocID, "target document not cached")
			k.list.moveTo(target)
			k.log("traverse delta=%d -> target doc %d reloaded as doc %d at pos=%d",
				req.delta, tgt.DocID, newID, target)
		}
	}
	return Outcome{Token: req.token, Delta: req.delta, Pos: k.list.pos}
}

// SetEligibility changes one eligibility condition of a live document.
// If the document is cached and loses eligibility, it is evicted at once.
func (k *Kernel) SetEligibility(docID uint64, flag Flag, value bool, at time.Time) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if !flag.valid() {
		return fmt.Errorf("%w: unknown flag %d", ErrInvalidArgument, int(flag))
	}
	k.drainLocked()
	if err := k.checkClock(at); err != nil {
		return err
	}
	d, ok := k.docs[docID]
	if !ok {
		return fmt.Errorf("%w: doc %d", ErrDocumentNotFound, docID)
	}
	if d.status == DocUnloaded {
		return fmt.Errorf("%w: doc %d is unloaded", ErrInvalidState, docID)
	}
	k.now = at
	d.flags[flag] = value
	k.log("doc %d flag %s=%v", docID, flag, value)
	if d.status == DocCached {
		if ok, reason := d.eligible(); !ok {
			k.cache.remove(docID)
			k.unload(d, "lost eligibility while cached: "+reason)
		}
	}
	return nil
}

// AdvanceClock moves the logical clock forward and evicts cached
// documents whose age reached the TTL.
func (k *Kernel) AdvanceClock(d time.Duration) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if d < 0 {
		return fmt.Errorf("%w: negative advance %s", ErrClockRegression, d)
	}
	k.drainLocked()
	k.now = k.now.Add(d)
	for _, doc := range k.cache.evictExpired(k.now) {
		k.unload(doc, "cache ttl expired")
	}
	k.log("clock advanced by %s -> now=%s", d, k.now)
	return nil
}

// leaveDocument caches the current document when eligible, otherwise
// unloads it. Evictions caused by the insertion are unloaded too.
func (k *Kernel) leaveDocument(id uint64) {
	d := k.docs[id]
	if ok, reason := d.eligible(); !ok {
		k.unload(d, "not cacheable: "+reason)
		return
	}
	expired, overflow := k.cache.insert(d, k.now)
	for _, e := range expired {
		k.unload(e, "evicted: cache ttl expired")
	}
	for _, e := range overflow {
		k.unload(e, "evicted: oldest over capacity")
	}
	if d.status == DocCached {
		k.log("doc %d entered cache at %s", id, k.now)
	}
}

// reload discards oldID (unloading it if needed) and assigns a fresh
// document identifier; every entry carrying oldID is updated.
func (k *Kernel) reload(oldID uint64, reason string) uint64 {
	if d, ok := k.docs[oldID]; ok {
		if d.status == DocCached {
			k.cache.remove(oldID)
		}
		k.unload(d, reason)
		delete(k.docs, oldID)
	}
	newID := k.allocDoc()
	k.list.reassignDoc(oldID, newID)
	k.docs[newID] = newDocument(newID)
	return newID
}

// onTruncated evicts cached documents that lost their last reference
// to truncated entries.
func (k *Kernel) onTruncated(truncated []Entry) {
	seen := make(map[uint64]bool, len(truncated))
	for _, e := range truncated {
		if seen[e.DocID] {
			continue
		}
		seen[e.DocID] = true
		if !k.list.referenced(e.DocID) {
			if d := k.cache.remove(e.DocID); d != nil {
				k.unload(d, "truncated and no longer referenced")
			}
		}
	}
}

func (k *Kernel) unload(d *Document, reason string) {
	d.status = DocUnloaded
	k.log("doc %d unloaded (%s)", d.ID, reason)
}

// DocInfo describes a known document in a Snapshot.
type DocInfo struct {
	Status   DocStatus
	Flags    [4]bool
	CachedAt time.Time
}

// Snapshot is a consistent read-only view of the whole kernel state.
type Snapshot struct {
	Now     time.Time
	Pos     int
	Entries []Entry
	Cached  []uint64 // sorted cached document IDs
	Docs    map[uint64]DocInfo
}

func (k *Kernel) Snapshot() Snapshot {
	k.mu.Lock()
	defer k.mu.Unlock()
	snap := Snapshot{
		Now:     k.now,
		Pos:     k.list.pos,
		Entries: append([]Entry(nil), k.list.entries...),
		Docs:    make(map[uint64]DocInfo, len(k.docs)),
	}
	for id, d := range k.docs {
		snap.Docs[id] = DocInfo{Status: d.status, Flags: d.flags, CachedAt: d.cachedAt}
	}
	for id := range k.cache.byID {
		snap.Cached = append(snap.Cached, id)
	}
	sort.Slice(snap.Cached, func(i, j int) bool { return snap.Cached[i] < snap.Cached[j] })
	return snap
}

// CurrentEntry returns the entry at the current position.
func (k *Kernel) CurrentEntry() (Entry, bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.list.current()
}

// IsCached reports whether the document is currently in the cache.
func (k *Kernel) IsCached(docID uint64) bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.cache.contains(docID)
}

// CheckInvariants verifies the kernel invariants: cache within
// capacity, every cached document referenced by at least one entry and
// still eligible, the current document never cached, and every entry
// pointing at a known document.
func (k *Kernel) CheckInvariants() error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.cache.size() > k.cache.capacity {
		return fmt.Errorf("cache size %d exceeds capacity %d", k.cache.size(), k.cache.capacity)
	}
	cur, hasCur := k.list.current()
	for id := range k.cache.byID {
		if !k.list.referenced(id) {
			return fmt.Errorf("cached doc %d is not referenced by any entry", id)
		}
		if hasCur && cur.DocID == id {
			return fmt.Errorf("current document %d is cached", id)
		}
		if ok, reason := k.docs[id].eligible(); !ok {
			return fmt.Errorf("cached doc %d is ineligible: %s", id, reason)
		}
	}
	for _, e := range k.list.entries {
		if _, ok := k.docs[e.DocID]; !ok {
			return fmt.Errorf("entry seq=%d references unknown doc %d", e.Seq, e.DocID)
		}
	}
	return nil
}
