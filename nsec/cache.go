package nsec

import (
	"container/heap"
	"errors"
	"sync"
)

var (
	// ErrInvalidParam reports malformed names, missing NSEC in Types,
	// out-of-range TTL/now/qtype, or bad constructor arguments.
	ErrInvalidParam = errors.New("nsec: invalid parameter")
	// ErrClockBackward reports an accepted-operation timestamp smaller
	// than the last accepted one.
	ErrClockBackward = errors.New("nsec: clock moved backward")
	// ErrNotValidated reports an Insert of an unvalidated record.
	ErrNotValidated = errors.New("nsec: record not validated")
	// ErrOutOfZone reports Owner/Next/qname outside the zone.
	ErrOutOfZone = errors.New("nsec: name out of zone")
)

// Kind is the outcome classification of a Lookup.
type Kind int

const (
	// Miss means the cache cannot answer negatively.
	Miss Kind = iota
	// NoData means the name exists but has no record of the query type
	// (also used for empty non-terminals).
	NoData
	// NXDomain means the name provably does not exist.
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

// Record is a validated NSEC record to insert. Owner and Next are domain
// names inside the zone; Types must contain 47 (NSEC); TTL is 0..86400.
type Record struct {
	Owner     string
	Next      string
	Types     []uint16
	TTL       uint64
	Validated bool
}

// Answer is the result of a Lookup. Used lists the owner names of the NSEC
// records proving the answer, sorted in canonical order; TTL is the minimum
// remaining lifetime (expiry-now) across Used, 0 when Used is empty.
type Answer struct {
	Kind Kind
	Used []string
	TTL  uint64
}

// nsecRecord is the stored form of an NSEC record.
type nsecRecord struct {
	owner  Name
	next   Name
	types  map[uint16]struct{}
	expiry uint64
	gen    uint64
}

func (r *nsecRecord) hasType(t uint16) bool {
	_, ok := r.types[t]
	return ok
}

// heapEntry orders records by (expiry, owner canonical order) for both
// amortized expiry purging and eviction.
type heapEntry struct {
	expiry uint64
	owner  Name
	gen    uint64
}

type expiryHeap []heapEntry

func (h expiryHeap) Len() int { return len(h) }

func (h expiryHeap) Less(i, j int) bool {
	if h[i].expiry != h[j].expiry {
		return h[i].expiry < h[j].expiry
	}
	return compareNames(h[i].owner, h[j].owner) < 0
}

func (h expiryHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *expiryHeap) Push(x any) { *h = append(*h, x.(heapEntry)) }

func (h *expiryHeap) Pop() any {
	old := *h
	n := len(old)
	e := old[n-1]
	*h = old[:n-1]
	return e
}

// Cache is a concurrency-safe negative-answer cache over NSEC intervals.
// All methods may be called concurrently; results are equivalent to some
// serial order, and replaying the same accepted operation sequence yields
// identical results.
type Cache struct {
	zone     Name
	soaMin   uint64
	capacity int

	mu      sync.Mutex
	owners  []Name // live owners in canonical order
	byOwner map[string]*nsecRecord
	exps    expiryHeap
	gen     uint64
	// overlap is set once live intervals may overlap; while false the
	// logarithmic covering search is exact.
	overlap bool

	hasNow  bool
	lastNow uint64

	// nameCmp counts canonical-order name comparisons (purges excluded).
	nameCmp int64
}

// NewCache builds a cache for zone (non-empty valid domain name), with
// soaMin in 0..86400 seconds and capacity in 1..4096 live records.
func NewCache(zone string, soaMin uint64, capacity int) (*Cache, error) {
	z, err := parseName(zone)
	if err != nil {
		return nil, err
	}
	if soaMin > maxTTL || capacity < 1 || capacity > 4096 {
		return nil, ErrInvalidParam
	}
	return &Cache{
		zone:     z,
		soaMin:   soaMin,
		capacity: capacity,
		byOwner:  make(map[string]*nsecRecord),
	}, nil
}

// cmp is the counting canonical-order comparison used on lookup paths.
func (c *Cache) cmp(a, b Name) int {
	c.nameCmp++
	return compareNames(a, b)
}

// Insert validates and stores rec. The effective TTL is min(rec.TTL,
// soaMin); the record lives while now < now_insert+eff. An existing record
// with the same Owner is always discarded first; eff == 0 stores nothing.
// When the cache is full and Owner is new, the live record with the
// smallest expiry (ties: canonically smaller owner) is evicted.
func (c *Cache) Insert(now uint64, rec Record) error {
	if now > maxNow || rec.TTL > maxTTL {
		return ErrInvalidParam
	}
	owner, err := parseName(rec.Owner)
	if err != nil {
		return err
	}
	next, err := parseName(rec.Next)
	if err != nil {
		return err
	}
	types := make(map[uint16]struct{}, len(rec.Types))
	for _, t := range rec.Types {
		types[t] = struct{}{}
	}
	if _, ok := types[typeNSEC]; !ok {
		return ErrInvalidParam
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.hasNow && now < c.lastNow {
		return ErrClockBackward
	}
	if !rec.Validated {
		return ErrNotValidated
	}
	if !owner.inZone(c.zone) || !next.inZone(c.zone) {
		return ErrOutOfZone
	}

	c.lastNow, c.hasNow = now, true
	c.purge(now)

	if old := c.byOwner[owner.key]; old != nil {
		c.remove(old)
	}
	eff := rec.TTL
	if c.soaMin < eff {
		eff = c.soaMin
	}
	if eff == 0 {
		return nil
	}
	if len(c.byOwner) >= c.capacity {
		c.evictOne()
	}
	if !c.overlap && c.detectOverlap(owner, next) {
		c.overlap = true
	}
	c.gen++
	nr := &nsecRecord{owner: owner, next: next, types: types, expiry: now + eff, gen: c.gen}
	c.byOwner[owner.key] = nr
	idx := c.upperBound(owner)
	c.owners = append(c.owners, Name{})
	copy(c.owners[idx+1:], c.owners[idx:])
	c.owners[idx] = owner
	heap.Push(&c.exps, heapEntry{expiry: nr.expiry, owner: owner, gen: nr.gen})
	return nil
}

// Lookup answers qname/qtype from cached NSEC intervals at time now.
func (c *Cache) Lookup(now uint64, qname string, qtype uint32) (Answer, error) {
	if now > maxNow || qtype < 1 || qtype > 65535 {
		return Answer{}, ErrInvalidParam
	}
	qn, err := parseName(qname)
	if err != nil {
		return Answer{}, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.hasNow && now < c.lastNow {
		return Answer{}, ErrClockBackward
	}
	if !qn.inZone(c.zone) {
		return Answer{}, ErrOutOfZone
	}

	c.lastNow, c.hasNow = now, true
	c.purge(now)

	// (1) exact owner match: type present (or CNAME for non-CNAME
	// queries) means the cache cannot deny; otherwise NODATA.
	if r := c.byOwner[qn.key]; r != nil {
		qt := uint16(qtype)
		if r.hasType(qt) || (qt != typeCNAME && r.hasType(typeCNAME)) {
			return Answer{Kind: Miss}, nil
		}
		return c.answer(NoData, now, r), nil
	}
	// (2) find the interval covering qname.
	cover := c.findCovering(qn)
	if cover == nil {
		return Answer{Kind: Miss}, nil
	}
	// (3) empty non-terminal: qname is an ancestor of owner or next.
	k := commonSuffix(cover.owner, qn)
	if k2 := commonSuffix(cover.next, qn); k2 > k {
		k = k2
	}
	if k == len(qn.labels) {
		return c.answer(NoData, now, cover), nil
	}
	// (4) wildcard proof at the closest encloser.
	wild := qn.wildcardBelow(k)
	if c.byOwner[wild.key] != nil {
		return Answer{Kind: Miss}, nil
	}
	wcov := c.findCovering(wild)
	if wcov == nil {
		return Answer{Kind: Miss}, nil
	}
	if wcov == cover {
		return c.answer(NXDomain, now, cover), nil
	}
	if c.cmp(cover.owner, wcov.owner) < 0 {
		return c.answer(NXDomain, now, cover, wcov), nil
	}
	return c.answer(NXDomain, now, wcov, cover), nil
}

// answer builds an Answer from the proving records in canonical order.
func (c *Cache) answer(kind Kind, now uint64, recs ...*nsecRecord) Answer {
	a := Answer{Kind: kind, Used: make([]string, 0, len(recs))}
	for i, r := range recs {
		a.Used = append(a.Used, r.owner.key)
		rem := r.expiry - now
		if i == 0 || rem < a.TTL {
			a.TTL = rem
		}
	}
	return a
}

// purge drops all records expired at now, amortized one heap pop per
// record. Heap comparisons are not charged to nameCmp.
func (c *Cache) purge(now uint64) {
	for len(c.exps) > 0 {
		top := c.exps[0]
		r := c.byOwner[top.owner.key]
		if r == nil || r.gen != top.gen {
			heap.Pop(&c.exps)
			continue
		}
		if top.expiry > now {
			return
		}
		heap.Pop(&c.exps)
		c.remove(r)
	}
}

// evictOne removes the live record with the smallest (expiry, owner).
func (c *Cache) evictOne() {
	for len(c.exps) > 0 {
		top := c.exps[0]
		r := c.byOwner[top.owner.key]
		if r == nil || r.gen != top.gen {
			heap.Pop(&c.exps)
			continue
		}
		heap.Pop(&c.exps)
		c.remove(r)
		return
	}
}

// remove deletes r from the owner index; its heap entry goes stale.
func (c *Cache) remove(r *nsecRecord) {
	delete(c.byOwner, r.owner.key)
	idx := c.upperBound(r.owner)
	if idx > 0 && c.owners[idx-1].key == r.owner.key {
		c.owners = append(c.owners[:idx-1], c.owners[idx:]...)
	}
}

// upperBound returns the first index whose owner is canonically > x.
func (c *Cache) upperBound(x Name) int {
	lo, hi := 0, len(c.owners)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		if c.cmp(c.owners[mid], x) <= 0 {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

// predecessor returns the record with the largest owner strictly < x.
func (c *Cache) predecessor(x Name) *nsecRecord {
	idx := c.upperBound(x)
	if idx == 0 {
		return nil
	}
	return c.byOwner[c.owners[idx-1].key]
}

// covers reports whether r's interval contains x.
func (c *Cache) covers(r *nsecRecord, x Name) bool {
	if c.cmp(r.owner, r.next) < 0 {
		return c.cmp(r.owner, x) < 0 && c.cmp(x, r.next) < 0
	}
	return c.cmp(x, r.owner) > 0 || c.cmp(x, r.next) < 0
}

// findCovering returns the live record covering x with the canonically
// largest owner, or nil.
func (c *Cache) findCovering(x Name) *nsecRecord {
	n := len(c.owners)
	if n == 0 {
		return nil
	}
	if c.overlap {
		var best *nsecRecord
		for _, o := range c.owners {
			r := c.byOwner[o.key]
			if c.covers(r, x) && (best == nil || c.cmp(best.owner, r.owner) < 0) {
				best = r
			}
		}
		return best
	}
	// Non-overlapping intervals: only the globally last record (the
	// unique wrap candidate) or the predecessor of x can cover x.
	if last := c.byOwner[c.owners[n-1].key]; c.covers(last, x) {
		return last
	}
	if p := c.predecessor(x); p != nil && c.covers(p, x) {
		return p
	}
	return nil
}

// detectOverlap reports whether inserting interval (owner,next) would
// overlap a live interval. Two circular arcs overlap iff either arc
// contains the other's owner.
func (c *Cache) detectOverlap(owner, next Name) bool {
	if c.findCovering(owner) != nil {
		return true
	}
	if idx := c.upperBound(owner); idx < len(c.owners) && inRegion(owner, next, c.owners[idx]) {
		return true
	}
	if compareNames(owner, next) >= 0 && len(c.owners) > 0 && inRegion(owner, next, c.owners[0]) {
		return true
	}
	return false
}

// inRegion reports whether x lies in the open interval (owner,next),
// wrapping when owner >= next.
func inRegion(owner, next, x Name) bool {
	if compareNames(owner, next) < 0 {
		return compareNames(owner, x) < 0 && compareNames(x, next) < 0
	}
	return compareNames(x, owner) > 0 || compareNames(x, next) < 0
}
