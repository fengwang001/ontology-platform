package cors

// cacheKey 是预检缓存条目的键：发起来源、目标地址、凭据模式。
type cacheKey struct {
	origin      string
	target      string
	credentials bool
}

// cacheEntry 是一条预检缓存条目。
type cacheEntry struct {
	methods    map[string]struct{}
	headers    map[string]struct{}
	anyMethod  bool
	anyHeader  bool
	expiry     int64
	lastHit    int64
	createdSeq uint64
}

// allows 报告条目是否允许给定方法与非安全头集合（不检查过期）。
func (e *cacheEntry) allows(method string, nonSafe []string) bool {
	if !e.anyMethod {
		if _, ok := e.methods[method]; !ok {
			return false
		}
	}
	if !e.anyHeader {
		for _, h := range nonSafe {
			if _, ok := e.headers[h]; !ok {
				return false
			}
		}
	}
	return true
}

// cache 是容量受限的预检结果缓存；命中判定为 O(1) 哈希查找。
type cache struct {
	capacity int
	entries  map[cacheKey]*cacheEntry
	seq      uint64
}

func newCache(capacity int) *cache {
	return &cache{capacity: capacity, entries: make(map[cacheKey]*cacheEntry)}
}

// put 插入新条目并按需淘汰：先清过期条目，再按最近命中时刻最早者、
// 并列按创建序号最早者淘汰。
func (c *cache) put(k cacheKey, ent *cacheEntry, now int64) {
	c.entries[k] = ent
	if len(c.entries) <= c.capacity {
		return
	}
	for key, e := range c.entries {
		if e.expiry <= now {
			delete(c.entries, key)
		}
	}
	for len(c.entries) > c.capacity {
		var victim cacheKey
		var victimEnt *cacheEntry
		first := true
		for key, e := range c.entries {
			if first ||
				e.lastHit < victimEnt.lastHit ||
				(e.lastHit == victimEnt.lastHit && e.createdSeq < victimEnt.createdSeq) {
				victim, victimEnt, first = key, e, false
			}
		}
		delete(c.entries, victim)
	}
}

func (c *cache) purgeByOrigin(originKey string) int {
	n := 0
	for k := range c.entries {
		if k.origin == originKey {
			delete(c.entries, k)
			n++
		}
	}
	return n
}

func (c *cache) purgeByTarget(target string) int {
	n := 0
	for k := range c.entries {
		if k.target == target {
			delete(c.entries, k)
			n++
		}
	}
	return n
}
