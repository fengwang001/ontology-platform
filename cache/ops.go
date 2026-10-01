package cache

import (
	"container/heap"
	"sort"
)

// validateManifest 按「清单为空、空路径、未严格递增、空摘要」的
// 顺序校验，只报告第一个问题。空路径检查整体先于递增性检查。
func validateManifest(manifest []Pair) error {
	if len(manifest) == 0 {
		return ErrEmptyManifest
	}
	for _, p := range manifest {
		if p.Path == "" {
			return ErrEmptyPath
		}
	}
	for i := 1; i < len(manifest); i++ {
		if manifest[i].Path <= manifest[i-1].Path {
			return ErrPathsNotOrdered
		}
	}
	for _, p := range manifest {
		if p.Digest == "" {
			return ErrEmptyDigest
		}
	}
	return nil
}

// Put 写入一条「清单 -> 结果」条目。若该键下已有逐对完全相同的
// 清单，只覆盖结果并刷新 last，条目数不变；否则新增条目，随后先
// 在键内淘汰 last 最小者直到该键条目数不超过 M，再在全局淘汰
// last 最小者直到总条目数不超过 Cap。被拒绝时不改变 tick 与任何
// 条目。
func (c *Cache) Put(key string, manifest []Pair, result string) error {
	if key == "" {
		return ErrEmptyKey
	}
	if err := validateManifest(manifest); err != nil {
		return err
	}
	if result == "" {
		return ErrEmptyResult
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	mk := manifestKey(manifest)
	if g, ok := c.keys[key]; ok {
		if e, ok := g.byMKey[mk]; ok {
			e.result = result
			c.tick++
			e.last = c.tick
			heap.Push(&c.gc, heapItem{last: e.last, e: e})
			return nil
		}
	}

	e := &entry{
		key:      key,
		manifest: append([]Pair(nil), manifest...),
		mkey:     mk,
		result:   result,
		alive:    true,
	}
	c.tick++
	e.last = c.tick

	g := c.keys[key]
	if g == nil {
		g = &keyGroup{byMKey: make(map[string]*entry)}
		c.keys[key] = g
	}
	g.entries = append(g.entries, e)
	g.byMKey[mk] = e
	c.total++
	heap.Push(&c.gc, heapItem{last: e.last, e: e})

	// 键内淘汰先于全局淘汰。
	for len(g.entries) > c.m {
		c.evict(g, minLastEntry(g.entries))
	}
	for c.total > c.capLimit {
		victim := c.popGlobalMin()
		c.evict(c.keys[victim.key], victim)
	}
	return nil
}

// Lookup 按现状核对命中：条目清单中每一对的路径都必须在现状中
// 存在且摘要逐字节相等。多个条目同时命中时取 last 最大者，返回其
// 结果并刷新 tick 与 last；未命中是正常结果，不改 tick。
func (c *Cache) Lookup(key string, current map[string]string) (string, bool, error) {
	if key == "" {
		return "", false, ErrEmptyKey
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.examined = 0
	g := c.keys[key]
	if g == nil {
		return "", false, nil
	}
	var best *entry
	for _, e := range g.entries {
		c.examined++
		if manifestHit(e.manifest, current) && (best == nil || e.last > best.last) {
			best = e
		}
	}
	if best == nil {
		return "", false, nil
	}
	c.tick++
	best.last = c.tick
	heap.Push(&c.gc, heapItem{last: best.last, e: best})
	return best.result, true, nil
}

// Dump 返回该键全部条目按 last 降序的视图；键为空或不存在时返回
// 空列表，不是错误。
func (c *Cache) Dump(key string) []Entry {
	c.mu.Lock()
	defer c.mu.Unlock()

	g := c.keys[key]
	if g == nil {
		return nil
	}
	out := make([]Entry, 0, len(g.entries))
	for _, e := range g.entries {
		out = append(out, Entry{
			Manifest: append([]Pair(nil), e.manifest...),
			Result:   e.result,
			Last:     e.last,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Last > out[j].Last })
	return out
}

// Len 返回全局条目总数。
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.total
}

// manifestHit 判定清单是否在现状下命中。
func manifestHit(manifest []Pair, current map[string]string) bool {
	for _, p := range manifest {
		digest, ok := current[p.Path]
		if !ok || digest != p.Digest {
			return false
		}
	}
	return true
}

// minLastEntry 返回条目中 last 最小者。last 全局互不相同，无并列。
func minLastEntry(entries []*entry) *entry {
	victim := entries[0]
	for _, e := range entries[1:] {
		if e.last < victim.last {
			victim = e
		}
	}
	return victim
}

// popGlobalMin 弹出全局 last 最小的存活条目，惰性丢弃失效堆项。
func (c *Cache) popGlobalMin() *entry {
	for c.gc.Len() > 0 {
		item := heap.Pop(&c.gc).(heapItem)
		if item.e.alive && item.e.last == item.last {
			return item.e
		}
	}
	return nil
}

// evict 从所属键组中移除条目并维护计数。
func (c *Cache) evict(g *keyGroup, victim *entry) {
	victim.alive = false
	delete(g.byMKey, victim.mkey)
	for i, e := range g.entries {
		if e == victim {
			g.entries = append(g.entries[:i], g.entries[i+1:]...)
			break
		}
	}
	if len(g.entries) == 0 {
		delete(c.keys, victim.key)
	}
	c.total--
}
