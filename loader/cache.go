package loader

// cacheKey identifies a preload cache entry by source origin and address.
type cacheKey struct {
	origin Origin
	url    string
}

type cacheEntry struct {
	as          ResourceType
	cred        CredentialsMode
	integrity   string
	completedAt int64
}

// matches reports whether a non-preload request may consume the entry: the
// resource type, credentials mode and integrity digest must all be equal
// (two empty digests count as equal).
func (e *cacheEntry) matches(typ ResourceType, cred CredentialsMode, integrity string) bool {
	return e.as == typ && e.cred == cred && e.integrity == integrity
}

// WasteRecord describes a preload cache entry that expired unused.
type WasteRecord struct {
	Origin      Origin
	URL         string
	Type        ResourceType
	CompletedAt int64
	WastedAt    int64
}

// preloadCache stores completed preloads. Lookup is a single map access and
// therefore independent of the number of cached entries; expiry scans the
// completion-ordered front of the queue only.
type preloadCache struct {
	ttl     int64
	entries map[cacheKey]*cacheEntry
	order   []cacheKey
	wasted  []WasteRecord
}

func newPreloadCache(ttl int64) *preloadCache {
	return &preloadCache{ttl: ttl, entries: make(map[cacheKey]*cacheEntry)}
}

func (c *preloadCache) has(k cacheKey) bool {
	_, ok := c.entries[k]
	return ok
}

func (c *preloadCache) get(k cacheKey) *cacheEntry { return c.entries[k] }

func (c *preloadCache) put(k cacheKey, e *cacheEntry) {
	c.entries[k] = e
	c.order = append(c.order, k)
}

func (c *preloadCache) delete(k cacheKey) { delete(c.entries, k) }

func (c *preloadCache) size() int { return len(c.entries) }

// expire moves every entry whose age exceeds the TTL to the waste report and
// returns the corresponding events. Completion timestamps are monotonic, so
// the scan stops at the first entry that is still fresh.
func (c *preloadCache) expire(now int64) []Event {
	var evs []Event
	for len(c.order) > 0 {
		k := c.order[0]
		e := c.entries[k]
		if e == nil {
			c.order = c.order[1:]
			continue
		}
		if now-e.completedAt <= c.ttl {
			break
		}
		delete(c.entries, k)
		c.order = c.order[1:]
		c.wasted = append(c.wasted, WasteRecord{
			Origin:      k.origin,
			URL:         k.url,
			Type:        e.as,
			CompletedAt: e.completedAt,
			WastedAt:    now,
		})
		evs = append(evs, Event{Time: now, Kind: EventWasted, Origin: k.origin, URL: k.url})
	}
	return evs
}
