// Package pagecache implements a shared page cache whose physical capacity is
// charged to exactly one owner cgroup per resident page.
//
// Every physical page that is still referenced is owned by exactly one of its
// holder cgroups: the holder that acquired its first reference earliest, with
// the cgroup name used as a tie breaker. Only the owner is charged for the
// page's physical size; when references come and go the ownership (and the
// charge) migrates atomically between cgroups.
package pagecache

import (
	"errors"
	"sync"
)

// ErrXxx are the sentinel errors returned by Cache.
var (
	ErrParam        = errors.New("pagecache: invalid parameter")
	ErrClock        = errors.New("pagecache: clock moved backwards")
	ErrNoLimit      = errors.New("pagecache: cgroup has no limit registered")
	ErrExists       = errors.New("pagecache: mapping already exists in cgroup")
	ErrSizeMismatch = errors.New("pagecache: page size conflicts with cache")
	ErrCapacity     = errors.New("pagecache: global physical capacity exceeded")
	ErrLimit        = errors.New("pagecache: cgroup limit exceeded")
	ErrNotFound     = errors.New("pagecache: mapping not found in cgroup")
)

const (
	maxCap    = 1_000_000_000_000_000
	maxPages  = 10_000
	maxPageSz = 1_000_000_000
)

// Page is one page occurrence inside a mapping.
type Page struct {
	ID   string
	Size int64
}

// Cache is a concurrently accessible shared page cache.
type Cache struct {
	mu sync.RWMutex

	cap int64
	// now is the largest accepted logical clock value.
	now int64
	// total is the sum of sizes of all resident pages; O(1) Total().
	total int64

	groups map[string]*group
	pages  map[string]*pinfo

	// rescans counts full-structure scans performed by query paths.
	// Queries are incremental; it must stay zero forever.
	rescans int
}

type group struct {
	limit int64
	// used is the sum of sizes of pages owned by this cgroup; O(1) Used().
	used int64
	maps map[string]mapping
}

type mapping struct {
	pages []Page
}

type pinfo struct {
	size  int64
	owner string
	hold  map[string]*holder
}

type holder struct {
	ref   int
	since int64
}

// New creates a Cache with the given global physical capacity.
// cap must satisfy 1 <= cap <= 10^15, otherwise ErrParam is returned.
func New(cap int64) (*Cache, error) {
	if cap < 1 || cap > maxCap {
		return nil, ErrParam
	}
	return &Cache{
		cap:    cap,
		groups: make(map[string]*group),
		pages:  make(map[string]*pinfo),
	}, nil
}

// SetLimit registers or updates the byte limit of a cgroup.
// The name must be non-empty and bytes must satisfy 0 <= bytes <= 10^15.
// Lowering a limit below the current usage is allowed; the cgroup simply
// becomes over-limit until it releases or loses ownership of pages.
func (c *Cache) SetLimit(cg string, bytes int64) error {
	if cg == "" || bytes < 0 || bytes > maxCap {
		return ErrParam
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	g := c.groups[cg]
	if g == nil {
		g = &group{maps: make(map[string]mapping)}
		c.groups[cg] = g
	}
	g.limit = bytes
	return nil
}

// Map stores a mapping into a cgroup.
//
// pages must be a non-empty sequence (at most 10^4 entries) with non-empty
// IDs and 1 <= Size <= 10^9; the same ID may occur multiple times, and each
// occurrence counts as one reference. The size of an ID must agree both
// across the request and with any size already resident in the cache.
//
// On success ownership of every involved page is recomputed as if the new
// references were present, and charges migrate atomically. The operation is
// rejected (without any state change) with ErrCapacity when it would bring
// brand-new resident pages above the global cap, or with ErrLimit when this
// cgroup gains ownership of pages that would push its usage above its limit.
// A Map that only references pages without gaining ownership (delta == 0) is
// always admitted even when the cgroup is over its limit.
func (c *Cache) Map(now int64, cg, name string, pages []Page) error {
	if name == "" || len(pages) == 0 || len(pages) > maxPages {
		return ErrParam
	}
	for _, p := range pages {
		if p.ID == "" || p.Size < 1 || p.Size > maxPageSz {
			return ErrParam
		}
	}
	if now < 0 {
		return ErrParam
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if now < c.now {
		return ErrClock
	}
	g := c.groups[cg]
	if g == nil {
		return ErrNoLimit
	}
	if _, ok := g.maps[name]; ok {
		return ErrExists
	}

	// distinct[id] is the page size determined by the first occurrence.
	distinct := make(map[string]int64, len(pages))
	counts := make(map[string]int, len(pages))
	for _, p := range pages {
		if sz, ok := distinct[p.ID]; ok {
			if sz != p.Size {
				return ErrSizeMismatch
			}
		} else {
			if pi := c.pages[p.ID]; pi != nil && pi.size != p.Size {
				return ErrSizeMismatch
			}
			distinct[p.ID] = p.Size
		}
		counts[p.ID]++
	}

	// fresh is the total size of pages not resident before the operation.
	var fresh, delta int64
	for id, sz := range distinct {
		pi := c.pages[id]
		if pi == nil {
			fresh += sz
		}
		after, wasOwner := c.ownerAfterMap(pi, cg, now)
		if after == cg && !wasOwner {
			delta += sz
		}
	}

	if c.total+fresh > c.cap {
		return ErrCapacity
	}
	if delta > 0 && g.used+delta > g.limit {
		return ErrLimit
	}

	// Commit: references first, then ownership, so every page keeps exactly
	// one owner that is also a holder at every observable instant.
	for id, sz := range distinct {
		pi := c.pages[id]
		if pi == nil {
			pi = &pinfo{size: sz, owner: cg, hold: map[string]*holder{
				cg: {ref: counts[id], since: now},
			}}
			c.pages[id] = pi
			c.total += sz
			g.used += sz
			continue
		}
		h := pi.hold[cg]
		if h == nil {
			pi.hold[cg] = &holder{ref: counts[id], since: now}
		} else {
			h.ref += counts[id]
		}
		newOwner := pickOwner(pi, cg, now)
		if newOwner != pi.owner {
			c.groups[pi.owner].used -= pi.size
			c.groups[newOwner].used += pi.size
			pi.owner = newOwner
		}
	}

	stored := make([]Page, len(pages))
	copy(stored, pages)
	g.maps[name] = mapping{pages: stored}
	c.now = now
	return nil
}

// Unmap removes a mapping from a cgroup.
// Unmap is never rejected for limit or capacity reasons. References are
// dropped page by page; a cgroup whose reference count falls to zero stops
// being a holder and, if it owned the page, ownership migrates to the remaining
// holder with the smallest (since, name); pages with no holder are reclaimed
// and the cache forgets their size.
func (c *Cache) Unmap(now int64, cg, name string) error {
	if cg == "" || name == "" || now < 0 {
		return ErrParam
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if now < c.now {
		return ErrClock
	}
	g := c.groups[cg]
	if g == nil {
		return ErrNoLimit
	}
	m, ok := g.maps[name]
	if !ok {
		return ErrNotFound
	}

	for i := range m.pages {
		id := m.pages[i].ID
		pi := c.pages[id]
		h := pi.hold[cg]
		h.ref--
		if h.ref > 0 {
			continue
		}
		delete(pi.hold, cg)
		if len(pi.hold) == 0 {
			// The sole owner leaves with the last reference: reclaim.
			if pi.owner == cg {
				g.used -= pi.size
			}
			c.total -= pi.size
			delete(c.pages, id)
			continue
		}
		if pi.owner != cg {
			continue
		}
		newOwner, _ := bestHolder(pi.hold, "", 0)
		c.groups[newOwner].used += pi.size
		g.used -= pi.size
		pi.owner = newOwner
	}

	delete(g.maps, name)
	c.now = now
	return nil
}

// Used returns the physical usage charged to a cgroup.
// It reads an incrementally maintained counter and never scans.
func (c *Cache) Used(cg string) int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if g := c.groups[cg]; g != nil {
		return g.used
	}
	return 0
}

// Logical returns the logical usage of a cgroup.
// That is the sum of sizes of distinct pages for which the cgroup holds at
// least one reference, regardless of ownership.
func (c *Cache) Logical(cg string) int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var n int64
	for _, pi := range c.pages {
		if pi.hold[cg] != nil {
			n += pi.size
		}
	}
	return n
}

// Over reports whether a cgroup is over its limit.
// A cgroup without a registered limit is never considered over-limit.
func (c *Cache) Over(cg string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	g := c.groups[cg]
	return g != nil && g.used > g.limit
}

// Total returns the total physical usage of the cache.
// It reads an incrementally maintained counter and never scans.
func (c *Cache) Total() int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.total
}

// ownerAfterMap reports the owner of pi as it would be after the Map and
// whether cg already owns it before the operation. A nil pi means a brand-new
// page, of which cg becomes the sole holder and owner.
func (c *Cache) ownerAfterMap(pi *pinfo, cg string, now int64) (after string, wasOwner bool) {
	if pi == nil {
		return cg, false
	}
	return pickOwner(pi, cg, now), pi.owner == cg
}

// pickOwner returns the owner of pi after cg's references are added, without
// mutating anything. cg's candidate since is its existing holder since, or now
// when cg is a new holder.
func pickOwner(pi *pinfo, cg string, now int64) string {
	csince := now
	if h := pi.hold[cg]; h != nil {
		csince = h.since
	}
	best, _ := bestHolder(pi.hold, cg, csince)
	return best
}

// bestHolder returns the name of the holder with the smallest (since, name),
// optionally considering an extra candidate (extra, extraSince).
func bestHolder(hold map[string]*holder, extra string, extraSince int64) (string, int64) {
	best := ""
	bestSince := int64(0)
	consider := func(name string, since int64) {
		if best == "" || since < bestSince || (since == bestSince && name < best) {
			best = name
			bestSince = since
		}
	}
	for name, h := range hold {
		consider(name, h.since)
	}
	if extra != "" {
		consider(extra, extraSince)
	}
	return best, bestSince
}
