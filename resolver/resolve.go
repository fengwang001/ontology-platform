package resolver

// fetched 记录一次解析中新向上游取得的、待提交的条目。
type fetched struct {
	name Name
	ttl  int64
	e    *entry
}

// Resolve 沿别名链解析名字。
//
// 拒绝原因的判定顺序固定为：名字为空 → 链成环 → 链过长 → 上游失败。
// 任一拒绝发生时缓存维持调用前状态：新取得的记录一律不提交。
func (c *Cache) Resolve(name Name) (*Result, error) {
	if name == "" {
		c.logf("resolve input=%q -> reject: empty name (checked first)", name)
		return nil, ErrEmptyName
	}

	c.mu.Lock()
	if call := c.rootCalls[name]; call != nil {
		c.mu.Unlock()
		c.logf("resolve input=%q -> join in-flight resolution, waiting", name)
		<-call.done
		c.logf("resolve input=%q -> output from shared call: found=%v ttl=%d err=%v",
			name, resultFound(call.res), resultTTL(call.res), call.err)
		return call.res, call.err
	}
	call := &inflightCall{done: make(chan struct{})}
	c.rootCalls[name] = call
	c.mu.Unlock()

	res, err := c.resolveChain(name)
	call.res, call.err = res, err

	c.mu.Lock()
	delete(c.rootCalls, name)
	c.mu.Unlock()
	close(call.done)

	c.logf("resolve input=%q -> output: found=%v ttl=%d chain=%d err=%v",
		name, resultFound(res), resultTTL(res), resultChainLen(res), err)
	return res, err
}

// resolveChain 执行实际的链遍历；成功时原子提交全部新记录，失败时不触碰缓存。
func (c *Cache) resolveChain(root Name) (*Result, error) {
	start := c.now()
	seen := map[Name]bool{root: true}
	current := root
	var chain []AliasLink
	var pending []fetched
	minTTL := int64(-1)

	noteTTL := func(ttl int64, source string) {
		minTTL = minPositive(minTTL, ttl)
		c.logf("  hop %q ttl=%d from %s -> running min ttl=%d", current, ttl, source, minTTL)
	}
	reject := func(err error) (*Result, error) {
		c.logf("resolve %q rejected: %v (cache untouched, %d fetched records discarded)",
			root, err, len(pending))
		return nil, err
	}

	for {
		now := c.now()
		c.mu.Lock()
		hit, cached := c.lookup(current, now)
		c.mu.Unlock()
		if cached {
			c.logf("  hop %q cache HIT kind=%s remaining=%d", current, hit.kind, hit.remaining(now))
			noteTTL(hit.remaining(now), "cache remaining")
			if hit.kind == entryAlias {
				if seen[hit.target] {
					c.logf("  cycle detected via cache: target %q already on chain", hit.target)
					return reject(ErrCycle)
				}
				seen[hit.target] = true
				chain = append(chain, AliasLink{Name: current, Target: hit.target})
				current = hit.target
				continue
			}
			return c.commit(root, chain, hit, minTTL, pending)
		}
		c.logf("  hop %q cache MISS -> upstream query", current)

		ans, err := c.queryUpstream(current)
		if err != nil {
			c.logf("  hop %q upstream failure: %v", current, err)
			return reject(err)
		}
		e := newEntry(ans, start)
		ttl := e.remaining(start)

		if e.kind == entryAlias {
			noteTTL(ttl, "upstream fresh alias")
			// 成环判定先于链过长：目标在链上出现过即为环。
			if seen[e.target] {
				c.logf("  cycle detected via upstream: target %q already on chain", e.target)
				return reject(ErrCycle)
			}
			// 已有 8 条别名记录且本环仍是别名：链过长。
			if len(chain) >= MaxAliasRecords {
				c.logf("  chain too long: %d alias records already and %q adds another",
					len(chain), current)
				return reject(ErrChainTooLong)
			}
			seen[e.target] = true
			chain = append(chain, AliasLink{Name: current, Target: e.target})
			pending = appendPending(pending, current, ttl, e)
			current = e.target
			continue
		}

		noteTTL(ttl, "upstream fresh "+e.kind.String())
		pending = appendPending(pending, current, ttl, e)
		return c.commit(root, chain, e, minTTL, pending)
	}
}

// commit 原子提交本次新取得的全部记录并返回终点结果。
func (c *Cache) commit(root Name, chain []AliasLink, terminal *entry, ttl int64, pending []fetched) (*Result, error) {
	res := &Result{Name: root, Chain: append([]AliasLink(nil), chain...)}
	if terminal.kind == entryAddress {
		res.Found = true
		res.Addresses = append([]Address(nil), terminal.addresses...)
	} else {
		res.Found = false
	}

	c.mu.Lock()
	for _, f := range pending {
		c.storeEntry(f.name, f.ttl, f.e)
	}
	res.TTL = ttl
	c.mu.Unlock()
	c.logf("resolve %q committed %d entries -> found=%v ttl=%d chain=%d addrs=%v",
		root, len(pending), res.Found, ttl, len(res.Chain), res.Addresses)
	return res, nil
}

// newEntry 把上游应答转换为以 start 为存入时刻的缓存条目。
func newEntry(ans Answer, start int64) *entry {
	e := &entry{}
	switch ans.Kind {
	case AnswerAlias:
		e.kind = entryAlias
		e.target = ans.Target
		e.expireAt = start + clampTTL(ans.TTL)
	case AnswerAddress:
		e.kind = entryAddress
		e.addresses = append([]Address(nil), ans.Addresses...)
		e.expireAt = start + clampTTL(ans.TTL)
	case AnswerNXDOMAIN:
		e.kind = entryNegative
		ttl := ans.SOATTL
		if ans.MinimumTTL < ttl {
			ttl = ans.MinimumTTL
		}
		e.expireAt = start + clampTTL(ttl)
	}
	return e
}

func appendPending(pending []fetched, name Name, ttl int64, e *entry) []fetched {
	if ttl <= 0 {
		return pending // TTL 为 0 的记录不存入，但已参与本次解析。
	}
	return append(pending, fetched{name: name, ttl: ttl, e: e})
}

func (c *Cache) logf(format string, args ...any) {
	if c.log != nil {
		c.log.Logf(format, args...)
	}
}

func clampTTL(ttl int64) int64 {
	if ttl < 0 {
		return 0
	}
	return ttl
}

func minPositive(a, b int64) int64 {
	if a < 0 || b < a {
		return b
	}
	return a
}

func resultFound(res *Result) bool { return res != nil && res.Found }

func resultTTL(res *Result) int64 {
	if res == nil {
		return 0
	}
	return res.TTL
}

func resultChainLen(res *Result) int {
	if res == nil {
		return 0
	}
	return len(res.Chain)
}

func (k entryKind) String() string {
	switch k {
	case entryAlias:
		return "alias"
	case entryAddress:
		return "address"
	case entryNegative:
		return "negative"
	default:
		return "unknown"
	}
}
