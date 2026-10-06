package cors

// cacheKey 是预检缓存键：（发起来源、目标地址、凭据模式）。
type cacheKey struct {
	origin string
	target string
	creds  CredentialsMode
}

// entry 是一条预检缓存条目。
type entry struct {
	methods   map[string]bool // 允许的逐项方法
	headers   map[string]bool // 允许的逐项头（规范化小写）
	anyMethod bool            // 允许任意方法（凭据包含时恒为 false）
	anyHeader bool            // 允许任意头（凭据包含时恒为 false）
	expiresAt int64           // 过期时刻；等于当前时刻即视为已过期
	lastHit   int64           // 最近命中时刻（创建时刻视为一次命中）
	created   uint64          // 创建序号，用于淘汰并列打破
}

// covers 报告条目是否允许 method 与全部非安全头。
func (e *entry) covers(method string, nonSafe []string) bool {
	if !e.anyMethod && !e.methods[method] {
		return false
	}
	for _, h := range nonSafe {
		if !e.anyHeader && !e.headers[h] {
			return false
		}
	}
	return true
}

// parsed 是从预检响应解析出的结果。
type parsed struct {
	methods     map[string]bool
	headers     map[string]bool
	anyMethod   bool
	anyHeader   bool
	allowOrigin string
	allowCreds  bool
	expiresAt   int64
	cacheable   bool // 存活时长 <= 0 时为 false：结果只用于当前请求
}

// cache 是预检结果缓存。命中判定为哈希表查找，开销不随条目总数增长；
// 仅插入新键时的过期清除与淘汰扫描随条目数增长（这是被允许的）。
type cache struct {
	m          map[cacheKey]*entry
	maxEntries int
	seq        uint64
}

func newCache(maxEntries int) *cache {
	return &cache{m: make(map[cacheKey]*entry), maxEntries: maxEntries}
}

// get 返回存在且未过期的条目；过期条目（含恰等于当前时刻）视为不存在。
func (c *cache) get(key cacheKey, now int64) *entry {
	e := c.m[key]
	if e == nil || e.expiresAt <= now {
		return nil
	}
	return e
}

// store 把一次成功且可缓存的预检结果写入缓存：
// 旧条目存在且未过期时合并（允许集合取并集，过期时刻与通配标记取新值），
// 否则新建条目并在容量超限时先清过期条目、再按（最近命中时刻、创建时刻）
// 最早者淘汰。
func (c *cache) store(key cacheKey, p parsed, now int64) {
	e, ok := c.m[key]
	if ok && e.expiresAt > now {
		for m := range p.methods {
			e.methods[m] = true
		}
		for h := range p.headers {
			e.headers[h] = true
		}
		e.anyMethod = p.anyMethod
		e.anyHeader = p.anyHeader
		e.expiresAt = p.expiresAt
		e.lastHit = now
		return
	}
	if !ok {
		c.purgeExpired(now)
		for len(c.m) >= c.maxEntries {
			c.evictOne()
		}
	}
	c.seq++
	c.m[key] = &entry{
		methods:   p.methods,
		headers:   p.headers,
		anyMethod: p.anyMethod,
		anyHeader: p.anyHeader,
		expiresAt: p.expiresAt,
		lastHit:   now,
		created:   c.seq,
	}
}

func (c *cache) purgeExpired(now int64) {
	for k, e := range c.m {
		if e.expiresAt <= now {
			delete(c.m, k)
		}
	}
}

// evictOne 淘汰最近命中时刻最早者，并列按创建时刻最早者。
func (c *cache) evictOne() {
	var victim cacheKey
	var ve *entry
	for k, e := range c.m {
		if ve == nil || e.lastHit < ve.lastHit ||
			(e.lastHit == ve.lastHit && e.created < ve.created) {
			victim, ve = k, e
		}
	}
	if ve != nil {
		delete(c.m, victim)
	}
}

func (c *cache) purgeByOrigin(origin string) int {
	n := 0
	for k := range c.m {
		if k.origin == origin {
			delete(c.m, k)
			n++
		}
	}
	return n
}

func (c *cache) purgeByTarget(target string) int {
	n := 0
	for k := range c.m {
		if k.target == target {
			delete(c.m, k)
			n++
		}
	}
	return n
}

// live 返回未过期条目数（仅用于测试核对不变量）。
func (c *cache) live(now int64) int {
	n := 0
	for _, e := range c.m {
		if e.expiresAt > now {
			n++
		}
	}
	return n
}
