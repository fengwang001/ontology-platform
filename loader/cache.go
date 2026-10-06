package loader

import "sort"

// cacheKey identifies a preload cache entry by source and address.
type cacheKey struct {
	origin Origin
	url    string
}

// cacheEntry is a completed, not yet consumed preload.
type cacheEntry struct {
	as          ResourceType
	credentials string
	integrity   string
	completedAt int64
}

// matches reports whether a non-preload request may be served by e.
// All four conditions are required: same address (guaranteed by the key),
// matching type, equal credentials mode and equal integrity digest
// (both empty counts as equal).
func (e *cacheEntry) matches(in RequestInput) bool {
	return in.Type == e.as &&
		in.Credentials == e.credentials &&
		in.Integrity == e.integrity
}

// preloadCache is a hash map keyed by (origin, url); hit lookup is a
// single map probe plus three scalar comparisons, independent of the
// number of cached entries.
type preloadCache struct {
	ttl     int64
	entries map[cacheKey]*cacheEntry
	waste   []WasteEntry
}

func newPreloadCache(ttl int64) *preloadCache {
	return &preloadCache{ttl: ttl, entries: make(map[cacheKey]*cacheEntry)}
}

func (c *preloadCache) hit(in RequestInput) bool {
	e, ok := c.entries[cacheKey{in.Origin, in.URL}]
	return ok && e.matches(in)
}

// take removes an entry upon use.
func (c *preloadCache) take(in RequestInput) {
	delete(c.entries, cacheKey{in.Origin, in.URL})
}

func (c *preloadCache) has(key cacheKey) bool {
	_, ok := c.entries[key]
	return ok
}

// put stores a completed preload; an existing entry is never overwritten.
func (c *preloadCache) put(key cacheKey, e *cacheEntry) {
	if _, ok := c.entries[key]; ok {
		return
	}
	c.entries[key] = e
}

// sweep moves entries unused for strictly longer than ttl into the waste
// report and removes them. Expiry order is deterministic.
func (c *preloadCache) sweep(now int64) {
	var expired []cacheKey
	for k, e := range c.entries {
		if now-e.completedAt > c.ttl {
			expired = append(expired, k)
		}
	}
	if len(expired) == 0 {
		return
	}
	sort.Slice(expired, func(i, j int) bool {
		a, b := c.entries[expired[i]], c.entries[expired[j]]
		if a.completedAt != b.completedAt {
			return a.completedAt < b.completedAt
		}
		if expired[i].url != expired[j].url {
			return expired[i].url < expired[j].url
		}
		ai, bi := expired[i].origin, expired[j].origin
		if ai.Scheme != bi.Scheme {
			return ai.Scheme < bi.Scheme
		}
		if ai.Host != bi.Host {
			return ai.Host < bi.Host
		}
		return ai.Port < bi.Port
	})
	for _, k := range expired {
		e := c.entries[k]
		c.waste = append(c.waste, WasteEntry{
			Origin:      k.origin,
			URL:         k.url,
			CompletedAt: e.completedAt,
			WastedAt:    now,
		})
		delete(c.entries, k)
	}
}
