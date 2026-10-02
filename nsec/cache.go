package nsec

import (
	"container/heap"
	"errors"
	"sync"
)

// DNS type codes referenced by the cache semantics.
const (
	TypeCNAME uint16 = 5
	TypeNSEC  uint16 = 47
)

const (
	maxTTL uint32 = 86400
	maxNow uint64 = 1000000000000
	maxCap        = 4096
)

var (
	// ErrInvalidParam reports a malformed parameter (bad name, bad TTL,
	// out-of-range now/qtype, or a Types set without NSEC).
	ErrInvalidParam = errors.New("nsec: invalid parameter")
	// ErrClockRegression reports an accepted-operation clock going backwards.
	ErrClockRegression = errors.New("nsec: clock regression")
	// ErrNotValidated reports an Insert of a record that failed DNSSEC
	// validation.
	ErrNotValidated = errors.New("nsec: record not validated")
	// ErrOutOfZone reports a name outside the configured zone.
	ErrOutOfZone = errors.New("nsec: name out of zone")
)

// Record is a validated NSEC record as fed into and reported by the cache.
type Record struct {
	Owner     string
	Next      string
	Types     []uint16
	TTL       uint32
	Validated bool
}

// Kind is the outcome classification of a Lookup.
type Kind int

const (
	// Miss means the cache cannot prove a negative answer.
	Miss Kind = iota
	// NoData means the name is proven to exist without the queried type
	// (this includes empty non-terminals).
	NoData
	// NXDomain means the name is proven not to exist.
	NXDomain
)

func (k Kind) String() string {
	switch k {
	case Miss:
		return "Miss"
	case NoData:
		return "NoData"
	case NXDomain:
		return "NXDomain"
	}
	return "Unknown"
}

// Result is the outcome of a Lookup. Used lists the NSEC records the
// answer relies on, sorted by canonical owner order, duplicates removed.
// TTL is the minimum remaining lifetime (in seconds) of the Used records.
type Result struct {
	Kind Kind
	Used []Record
	TTL  uint32
}

// entry is a stored NSEC record.
type entry struct {
	owner   name
	next    name
	types   map[uint16]struct{}
	rec     Record // canonicalized copy handed out in Results
	expiry  uint64 // absolute expiration second; alive iff now < expiry
	isWrap  bool   // owner >= next in canonical order
	heapIdx int
}

// expiryHeap is a min-heap of entries ordered by expiration second so that
// purging expired records is amortized: every record is purged at most once.
type expiryHeap []*entry

func (h expiryHeap) Len() int { return len(h) }

func (h expiryHeap) Less(i, j int) bool {
	if h[i].expiry != h[j].expiry {
		return h[i].expiry < h[j].expiry
	}
	return compareLabels(h[i].owner.labels, h[j].owner.labels) < 0
}

func (h expiryHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].heapIdx = i
	h[j].heapIdx = j
}

func (h *expiryHeap) Push(x any) {
	e := x.(*entry)
	e.heapIdx = len(*h)
	*h = append(*h, e)
}

func (h *expiryHeap) Pop() any {
	old := *h
	n := len(old)
	e := old[n-1]
	old[n-1] = nil
	*h = old[:n-1]
	e.heapIdx = -1
	return e
}

// Cache is a concurrency-safe negative-answer cache for one zone. All
// methods may be called concurrently; the result is equivalent to some
// serial execution order, and replaying the same accepted operation
// sequence yields identical results.
type Cache struct {
	mu     sync.Mutex
	zone   name
	soaMin uint32
	cap    int

	entries map[string]*entry // by canonical owner string
	sorted  []*entry          // by canonical owner order
	wraps   []*entry          // entries with owner >= next (chain tail wrap)
	byExp   expiryHeap        // lazy; stale entries are skipped on purge

	lastNow uint64
	nameCmp uint64 // number of canonical name comparisons performed

	// overlap is set once the stored intervals can no longer be proven
	// non-overlapping; it only switches Lookup to a slower (still exact)
	// covering scan and never affects results.
	overlap bool
}

// New builds a cache for zone (a non-empty valid DNS name) with negative
// TTL ceiling soaMin (0..86400 seconds) and room for capacity (1..4096)
// live records.
func New(zone string, soaMin uint32, capacity int) (*Cache, error) {
	z, ok := parseName(zone)
	if !ok || soaMin > maxTTL || capacity < 1 || capacity > maxCap {
		return nil, ErrInvalidParam
	}
	return &Cache{
		zone:    z,
		soaMin:  soaMin,
		cap:     capacity,
		entries: make(map[string]*entry),
	}, nil
}

// cmp counts and performs one canonical name comparison.
func (c *Cache) cmp(a, b name) int {
	c.nameCmp++
	return compareLabels(a.labels, b.labels)
}

// search returns the index of the first stored entry whose owner is not
// canonically smaller than target (i.e. the insertion point for target).
func (c *Cache) search(target name) int {
	lo, hi := 0, len(c.sorted)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		if c.cmp(c.sorted[mid].owner, target) < 0 {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

// purge drops every record expired at now (expiry <= now). Records are
// popped from the expiry heap in expiration order, so each record is
// removed exactly once.
func (c *Cache) purge(now uint64) {
	for len(c.byExp) > 0 && c.byExp[0].expiry <= now {
		e := c.byExp[0]
		heap.Pop(&c.byExp)
		if cur, ok := c.entries[e.owner.String()]; ok && cur == e {
			c.removeIndexes(e)
		}
	}
}

// removeIndexes deletes e from the owner map, the sorted slice and the
// wrap list; the caller is responsible for the expiry heap.
func (c *Cache) removeIndexes(e *entry) {
	delete(c.entries, e.owner.String())
	idx := c.search(e.owner)
	if idx < len(c.sorted) && c.sorted[idx] == e {
		c.sorted = append(c.sorted[:idx], c.sorted[idx+1:]...)
	}
	for i, w := range c.wraps {
		if w == e {
			c.wraps = append(c.wraps[:i], c.wraps[i+1:]...)
			break
		}
	}
}

// remove deletes e from every structure including the expiry heap.
func (c *Cache) remove(e *entry) {
	c.removeIndexes(e)
	heap.Remove(&c.byExp, e.heapIdx)
}

// evict drops the live record with the smallest expiration second; ties
// are broken by the canonically smaller owner.
func (c *Cache) evict() {
	victim := c.sorted[0]
	for _, e := range c.sorted[1:] {
		if e.expiry < victim.expiry ||
			(e.expiry == victim.expiry &&
				compareLabels(e.owner.labels, victim.owner.labels) < 0) {
			victim = e
		}
	}
	c.remove(victim)
}

// insert adds a fresh entry to every structure.
func (c *Cache) insert(e *entry) {
	idx := c.search(e.owner)
	c.sorted = append(c.sorted, nil)
	copy(c.sorted[idx+1:], c.sorted[idx:])
	c.sorted[idx] = e
	c.entries[e.owner.String()] = e
	if e.isWrap {
		c.wraps = append(c.wraps, e)
	}
	heap.Push(&c.byExp, e)
}

// coversGeneral reports whether e's interval strictly covers x.
func (c *Cache) coversGeneral(e *entry, x name) bool {
	if e.isWrap {
		return c.cmp(x, e.owner) > 0 || c.cmp(x, e.next) < 0
	}
	return c.cmp(e.owner, x) < 0 && c.cmp(x, e.next) < 0
}

// coversKnownOwnerLess reports whether e covers x when e.owner < x is
// already established by the sorted order.
func (c *Cache) coversKnownOwnerLess(e *entry, x name) bool {
	if e.isWrap {
		return true // x > e.owner implies coverage for a wrap interval
	}
	return c.cmp(x, e.next) < 0
}

// covering returns the live record covering x with the canonically largest
// owner, or nil if no live record covers x. Callers guarantee x is not the
// owner of any stored record.
func (c *Cache) covering(x name) *entry {
	lo := c.search(x)
	var best *entry
	if c.overlap {
		// Overlapping intervals: scan all owners below x, largest first.
		for i := lo - 1; i >= 0; i-- {
			if c.coversKnownOwnerLess(c.sorted[i], x) {
				best = c.sorted[i]
				break
			}
		}
	} else if lo > 0 && c.coversKnownOwnerLess(c.sorted[lo-1], x) {
		// Non-overlapping intervals: only the immediate predecessor of x
		// among the owners can cover it from below.
		best = c.sorted[lo-1]
	}
	// Wrap records whose owner sits above x may still cover x from the
	// top of the circle.
	for _, w := range c.wraps {
		if c.cmp(w.owner, x) > 0 && c.cmp(x, w.next) < 0 {
			if best == nil || c.cmp(w.owner, best.owner) > 0 {
				best = w
			}
		}
	}
	return best
}

// detectOverlap records whether inserting e makes the stored intervals
// overlap. It is only called while the cache is known non-overlapping, so
// it suffices to test whether any existing record covers e.owner and
// whether e covers a neighboring owner.
func (c *Cache) detectOverlap(e *entry) {
	if c.overlap {
		return
	}
	idx := c.search(e.owner)
	if idx > 0 && c.coversGeneral(c.sorted[idx-1], e.owner) {
		c.overlap = true
		return
	}
	for _, w := range c.wraps {
		if w != e && c.coversGeneral(w, e.owner) {
			c.overlap = true
			return
		}
	}
	if idx+1 < len(c.sorted) && c.coversGeneral(e, c.sorted[idx+1].owner) {
		c.overlap = true
		return
	}
	if e.isWrap && len(c.sorted) > 1 && c.coversGeneral(e, c.sorted[0].owner) {
		c.overlap = true
	}
}

// Insert validates and stores rec, replacing any previous record with the
// same owner. The effective TTL is min(rec.TTL, soaMin); a zero effective
// TTL only removes the old record. When the cache is full and the owner is
// new, the live record with the smallest expiration second (ties: smallest
// canonical owner) is evicted first.
func (c *Cache) Insert(now uint64, rec Record) error {
	owner, okOwner := parseName(rec.Owner)
	next, okNext := parseName(rec.Next)
	if now > maxNow || !okOwner || !okNext || rec.TTL > maxTTL || !hasNSEC(rec.Types) {
		return ErrInvalidParam
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if now < c.lastNow {
		return ErrClockRegression
	}
	if !rec.Validated {
		return ErrNotValidated
	}
	if !owner.inZone(c.zone) || !next.inZone(c.zone) {
		return ErrOutOfZone
	}
	c.lastNow = now
	c.purge(now)
	if old, ok := c.entries[owner.String()]; ok {
		c.remove(old)
	}
	eff := rec.TTL
	if c.soaMin < eff {
		eff = c.soaMin
	}
	if eff == 0 {
		return nil
	}
	if len(c.sorted) >= c.cap {
		c.evict()
	}
	types := make(map[uint16]struct{}, len(rec.Types))
	for _, t := range rec.Types {
		types[t] = struct{}{}
	}
	e := &entry{
		owner:  owner,
		next:   next,
		types:  types,
		expiry: now + uint64(eff),
		isWrap: compareLabels(owner.labels, next.labels) >= 0,
		rec: Record{
			Owner:     owner.String(),
			Next:      next.String(),
			Types:     append([]uint16(nil), rec.Types...),
			TTL:       rec.TTL,
			Validated: rec.Validated,
		},
	}
	c.insert(e)
	c.detectOverlap(e)
	return nil
}

func hasNSEC(types []uint16) bool {
	for _, t := range types {
		if t == TypeNSEC {
			return true
		}
	}
	return false
}

// Lookup classifies (qname, qtype) against the live NSEC records without
// mutating any record content. It performs the same amortized expiration
// purge as every accepted operation.
func (c *Cache) Lookup(now uint64, qname string, qtype uint32) (Result, error) {
	qn, ok := parseName(qname)
	if now > maxNow || !ok || qtype < 1 || qtype > 65535 {
		return Result{}, ErrInvalidParam
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if now < c.lastNow {
		return Result{}, ErrClockRegression
	}
	if !qn.inZone(c.zone) {
		return Result{}, ErrOutOfZone
	}
	c.lastNow = now
	c.purge(now)
	qt := uint16(qtype)

	// (1) Exact owner match: the name exists, so a negative answer is
	// only provable as NODATA. A matching type, or a CNAME for any other
	// qtype, means the cache cannot answer.
	if e, hit := c.entries[qn.String()]; hit {
		if _, has := e.types[qt]; has {
			return Result{Kind: Miss}, nil
		}
		if qt != TypeCNAME {
			if _, has := e.types[TypeCNAME]; has {
				return Result{Kind: Miss}, nil
			}
		}
		return Result{Kind: NoData, Used: []Record{e.rec}, TTL: uint32(e.expiry - now)}, nil
	}

	// (2) Find the live record covering qname.
	r := c.covering(qn)
	if r == nil {
		return Result{Kind: Miss}, nil
	}

	// (3) Empty non-terminal: qname is an ancestor of the closer of the
	// interval's endpoints.
	k := commonSuffix(r.owner.labels, qn.labels)
	if k2 := commonSuffix(r.next.labels, qn.labels); k2 > k {
		k = k2
	}
	if k == len(qn.labels) {
		return Result{Kind: NoData, Used: []Record{r.rec}, TTL: uint32(r.expiry - now)}, nil
	}

	// (4) Wildcard synthesis: the closest encloser is the rightmost k
	// labels of qname; NXDOMAIN requires the wildcard below it to be
	// provably absent too.
	wild := name{labels: append([]string{"*"}, qn.labels[len(qn.labels)-k:]...)}
	if _, hit := c.entries[wild.String()]; hit {
		return Result{Kind: Miss}, nil
	}
	w := c.covering(wild)
	if w == nil {
		return Result{Kind: Miss}, nil
	}
	used := []*entry{r, w}
	if w == r {
		used = used[:1]
	} else if compareLabels(w.owner.labels, r.owner.labels) < 0 {
		used[0], used[1] = used[1], used[0]
	}
	recs := make([]Record, len(used))
	ttl := ^uint64(0)
	for i, e := range used {
		recs[i] = e.rec
		if rem := e.expiry - now; rem < ttl {
			ttl = rem
		}
	}
	return Result{Kind: NXDomain, Used: recs, TTL: uint32(ttl)}, nil
}
