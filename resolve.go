package ontology

// Query 执行一次解析。
//
// 顺序固定为：参数校验 → 缓存命中 → 并发合并/上游查询，因此参数非法永远先于
// 上游失败报告，且被拒绝的查询不会读取之外地改动任何状态。
//
// 并发合并以(规范化名字,类型,族,按源前缀清零后的地址,源前缀长度)为组键：
// 同组并发未命中只向上游发出一次。应答按其“有效范围”写入缓存；若上游声明的
// 原始范围未覆盖某个等待者，该等待者重新进入完整查询流程（此时通常直接命中
// 同源前缀条目；该条目不缓存时则产生其自己的一次上游查询）。
func (c *Cache) Query(q Query) (Result, error) {
	if err := validateQuery(q); err != nil {
		return Result{}, err
	}
	_, mb := familyWidth(q.Family)

	nk := nameKey{name: normalizeName(q.Name), rrtype: q.Rrtype}

	for {
		now := c.clock.Now()

		c.mu.Lock()
		if b := c.buckets[nk]; b != nil {
			b.purgeExpired(now)
			if _, e := b.lookup(q.Family, q.Client, now); e != nil {
				res := Result{Kind: e.kind, Records: append([]byte(nil), e.records...)}
				c.mu.Unlock()
				return res, nil
			}
		}

		srcMasked := maskPrefix(q.Client, q.SrcPrefix)
		fk := prefixKey{name: nk, family: q.Family, prefix: string(srcMasked), bits: q.SrcPrefix}
		if f := c.inFlight[fk]; f != nil {
			w := &flightWaiter{client: append(Addr(nil), q.Client...), notify: make(chan flightOutcome, 1)}
			f.waiters = append(f.waiters, w)
			c.mu.Unlock()

			out := <-w.notify
			if out.covered {
				return out.res, out.err
			}
			// 未被应答原始范围覆盖：重新进入完整流程（先看缓存，再决定是否上游）。
			continue
		}

		f := &flight{}
		c.inFlight[fk] = f
		c.mu.Unlock()

		ans, err := c.upstream.Resolve(q)

		now = c.clock.Now()
		c.mu.Lock()
		// 注意：inFlight 条目必须在缓存写入完成、且所有等待者已被唤醒之后才删除，
		// 否则未覆盖等待者的“重新查询”可能与本应答的缓存写入交错，错过合并与命中。
		if err != nil {
			waiters := f.waiters
			delete(c.inFlight, fk)
			c.mu.Unlock()
			out := flightOutcome{err: ErrUpstreamFailure, covered: true}
			for _, w := range waiters {
				w.notify <- out
			}
			return Result{}, ErrUpstreamFailure
		}
		if err := validateAnswer(ans, mb); err != nil {
			waiters := f.waiters
			delete(c.inFlight, fk)
			c.mu.Unlock()
			out := flightOutcome{err: ErrUpstreamFailure, covered: true}
			for _, w := range waiters {
				w.notify <- out
			}
			return Result{}, err
		}

		// 有效范围：上游范围 > 源前缀时按源前缀处理；上游范围 < 源前缀时按上游范围处理；
		// 范围为 0 时覆盖该族所有地址。
		effectiveBits := ans.ScopePrefix
		if effectiveBits > q.SrcPrefix {
			effectiveBits = q.SrcPrefix
		}
		var cachePrefix Addr
		if effectiveBits == 0 {
			cachePrefix = make(Addr, len(q.Client))
		} else {
			cachePrefix = maskPrefix(q.Client, effectiveBits)
		}
		cacheKey := prefixKey{name: nk, family: q.Family, prefix: string(cachePrefix), bits: effectiveBits}
		c.insertLocked(cacheKey, ans, now)

		successRes := Result{Kind: ans.Kind, Records: append([]byte(nil), ans.Records...)}
		waiters := f.waiters
		pending := make([]*flightWaiter, 0, len(waiters))
		// 覆盖判定以发起者的真实客户端地址为锚，按上游“原始”范围比较，
		// 因此即便合并键只保留源前缀位，也能正确区分源前缀内、范围外的等待者。
		scopeAnchor := maskPrefix(q.Client, ans.ScopePrefix)
		for _, w := range waiters {
			// 上游原始范围为 0 即覆盖同族所有地址。
			var covered bool
			if ans.ScopePrefix == 0 {
				covered = true
			} else {
				covered = prefixCovers(scopeAnchor, ans.ScopePrefix, w.client)
			}
			if covered {
				w.notify <- flightOutcome{res: successRes, covered: true}
			} else {
				pending = append(pending, w)
			}
		}
		delete(c.inFlight, fk)
		c.mu.Unlock()

		for _, w := range pending {
			w.notify <- flightOutcome{covered: false}
		}
		return successRes, nil
	}
}
