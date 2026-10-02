package pagecache

import (
	"errors"
	"sync"
)

var (
	ErrParam        = errors.New("pagecache: invalid parameter")
	ErrClock        = errors.New("pagecache: clock moved backwards")
	ErrNoLimit      = errors.New("pagecache: cgroup has no limit")
	ErrExists       = errors.New("pagecache: mapping already exists")
	ErrSizeMismatch = errors.New("pagecache: page size mismatch")
	ErrCapacity     = errors.New("pagecache: global capacity exceeded")
	ErrLimit        = errors.New("pagecache: cgroup limit exceeded")
	ErrNotFound     = errors.New("pagecache: mapping not found")
)

// Page is one page occurrence inside a mapping.
type Page struct {
	ID   string
	Size int64
}

type holder struct {
	since int64
	ref   int64
}

type pageEntry struct {
	size    int64
	owner   string
	holders map[string]*holder
}

type mapping struct {
	pages []Page
}

type cgroupState struct {
	limit    int64
	used     int64
	logical  int64
	mappings map[string]*mapping
}

// Cache is a shared page cache with per-cgroup ownership accounting.
type Cache struct {
	mu      sync.RWMutex
	rescans int

	capacity int64
	now      int64
	total    int64
	cgroups  map[string]*cgroupState
	pages    map[string]*pageEntry
}

// New creates a cache with the given global physical capacity.
func New(capacity int64) (*Cache, error) {
	if capacity < 1 || capacity > 1e15 {
		return nil, ErrParam
	}
	return &Cache{
		capacity: capacity,
		cgroups:  make(map[string]*cgroupState),
		pages:    make(map[string]*pageEntry),
	}, nil
}

// SetLimit registers or updates the limit of a cgroup.
func (c *Cache) SetLimit(cgroup string, bytes int64) error {
	if cgroup == "" || bytes < 0 || bytes > 1e15 {
		return ErrParam
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	cg := c.cgroups[cgroup]
	if cg == nil {
		cg = &cgroupState{mappings: make(map[string]*mapping)}
		c.cgroups[cgroup] = cg
	}
	cg.limit = bytes
	return nil
}

// Map stores a mapping in a cgroup.
func (c *Cache) Map(now int64, cgroup, name string, pages []Page) error {
	if cgroup == "" || name == "" || now < 0 ||
		len(pages) == 0 || len(pages) > 10000 {
		return ErrParam
	}
	occ := make(map[string]int64, len(pages))
	sizes := make(map[string]int64, len(pages))
	for _, p := range pages {
		if p.ID == "" || p.Size < 1 || p.Size > 1e9 {
			return ErrParam
		}
		if s, ok := sizes[p.ID]; ok {
			if s != p.Size {
				return ErrSizeMismatch
			}
		} else {
			sizes[p.ID] = p.Size
		}
		occ[p.ID]++
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if now < c.now {
		return ErrClock
	}
	cg := c.cgroups[cgroup]
	if cg == nil {
		return ErrNoLimit
	}
	if _, ok := cg.mappings[name]; ok {
		return ErrExists
	}

	type plan struct {
		size      int64
		fresh     bool
		newOwner  string
		oldOwner  string
		hadHolder bool
	}
	plans := make(map[string]*plan, len(sizes))
	var freshSum, deltaSum int64
	for id, size := range sizes {
		pe := c.pages[id]
		if pe != nil && pe.size != size {
			return ErrSizeMismatch
		}
		p := &plan{size: size}
		if pe == nil {
			p.fresh = true
			p.oldOwner = ""
			p.newOwner = cgroup
			freshSum += size
		} else {
			p.oldOwner = pe.owner
			since := now
			if h := pe.holders[cgroup]; h != nil {
				since = h.since
				p.hadHolder = true
			}
			p.newOwner = pickOwner(pe, cgroup, since)
		}
		if p.newOwner == cgroup && p.oldOwner != cgroup {
			deltaSum += size
		}
		plans[id] = p
	}

	if c.total+freshSum > c.capacity {
		return ErrCapacity
	}
	if deltaSum > 0 && cg.used+deltaSum > cg.limit {
		return ErrLimit
	}

	stored := make([]Page, len(pages))
	copy(stored, pages)
	cg.mappings[name] = &mapping{pages: stored}

	for id, p := range plans {
		if p.fresh {
			c.total += p.size
			cg.logical += p.size
			cg.used += p.size
			c.pages[id] = &pageEntry{
				size:  p.size,
				owner: cgroup,
				holders: map[string]*holder{
					cgroup: {since: now, ref: occ[id]},
				},
			}
			continue
		}
		pe := c.pages[id]
		h := pe.holders[cgroup]
		if h == nil {
			cg.logical += p.size
			pe.holders[cgroup] = &holder{since: now, ref: occ[id]}
		} else {
			h.ref += occ[id]
		}
		if p.newOwner != p.oldOwner {
			if p.oldOwner != "" {
				c.cgroups[p.oldOwner].used -= p.size
			}
			c.cgroups[p.newOwner].used += p.size
			pe.owner = p.newOwner
		}
	}

	c.now = now
	return nil
}

func pickOwner(pe *pageEntry, candidateName string, candidateSince int64) string {
	owner := ""
	var ownerSince int64
	for name, h := range pe.holders {
		since := h.since
		if name == candidateName {
			since = candidateSince
		}
		if owner == "" || since < ownerSince || (since == ownerSince && name < owner) {
			owner = name
			ownerSince = since
		}
	}
	if owner == "" || candidateSince < ownerSince ||
		(candidateSince == ownerSince && candidateName < owner) {
		owner = candidateName
	}
	return owner
}

// Unmap removes a mapping from a cgroup.
func (c *Cache) Unmap(now int64, cgroup, name string) error {
	if cgroup == "" || name == "" || now < 0 {
		return ErrParam
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if now < c.now {
		return ErrClock
	}
	cg := c.cgroups[cgroup]
	if cg == nil {
		return ErrNoLimit
	}
	m := cg.mappings[name]
	if m == nil {
		return ErrNotFound
	}

	refDec := make(map[string]int64)
	for _, p := range m.pages {
		refDec[p.ID]++
	}
	delete(cg.mappings, name)

	for id, dec := range refDec {
		pe := c.pages[id]
		if pe == nil {
			continue
		}
		h := pe.holders[cgroup]
		if h == nil {
			continue
		}
		h.ref -= dec
		if h.ref > 0 {
			continue
		}
		delete(pe.holders, cgroup)
		cg.logical -= pe.size
		if pe.owner != cgroup {
			continue
		}
		if len(pe.holders) == 0 {
			cg.used -= pe.size
			c.total -= pe.size
			delete(c.pages, id)
			continue
		}
		newOwner := ""
		var newSince int64
		for otherName, other := range pe.holders {
			if newOwner == "" || other.since < newSince ||
				(other.since == newSince && otherName < newOwner) {
				newOwner = otherName
				newSince = other.since
			}
		}
		cg.used -= pe.size
		c.cgroups[newOwner].used += pe.size
		pe.owner = newOwner
	}

	c.now = now
	return nil
}

// Used reports the physical bytes charged to a cgroup.
func (c *Cache) Used(cgroup string) int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if cg := c.cgroups[cgroup]; cg != nil {
		return cg.used
	}
	return 0
}

// Logical reports the logical bytes referenced by a cgroup.
func (c *Cache) Logical(cgroup string) int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if cg := c.cgroups[cgroup]; cg != nil {
		return cg.logical
	}
	return 0
}

// Over reports whether a cgroup is over its limit.
func (c *Cache) Over(cgroup string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if cg := c.cgroups[cgroup]; cg != nil {
		return cg.used > cg.limit
	}
	return false
}

// Total reports the physical bytes of all live pages.
func (c *Cache) Total() int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.total
}
