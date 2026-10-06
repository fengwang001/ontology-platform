package cookie

import "container/heap"

// 本文件负责索引维护与容量淘汰。淘汰只在写入新条目时触发：
// 先站点上限后全局上限；每级先清扫已过期条目（计过期淘汰），
// 仍超限再按 (最近访问, 创建时刻, 序号) 升序淘汰（计容量淘汰）。

func (s *Store) siteOf(name string) *siteIndex {
	si, ok := s.sites[name]
	if !ok {
		si = &siteIndex{name: name, items: map[key]*Entry{}}
		s.sites[name] = si
	}
	return si
}

// bumpSite 在站点条目数变化后调用，压入新的计数堆项。
func (s *Store) bumpSite(si *siteIndex) {
	si.gen++
	heap.Push(&s.siteOrd, siteItem{name: si.name, count: len(si.items), gen: si.gen})
}

func (s *Store) pushLRU(si *siteIndex, e *Entry) {
	heap.Push(&si.lru, lruItem{k: e.key(), lastSeen: e.LastAccessedAt, created: e.CreatedAt, seq: e.seq, gen: e.gen})
}

func (s *Store) pushExpiry(si *siteIndex, e *Entry) {
	if e.ExpiresAt == nil {
		return
	}
	it := expiryItem{k: e.key(), expires: *e.ExpiresAt, seq: e.seq, gen: e.gen}
	heap.Push(&si.expiry, it)
	heap.Push(&s.expiry, it)
}

// removeLocked 从全部索引中移除条目并计入给定计数器。
// 堆采用惰性失效，无需就地删除。
func (s *Store) removeLocked(k key, counter *uint64) {
	if _, ok := s.entries[k]; !ok {
		return
	}
	delete(s.entries, k)
	si := s.sites[k.site]
	delete(si.items, k)
	s.bumpSite(si)
	*counter++
}

func (s *Store) enforceCapacity(site string) {
	if s.cfg.SiteLimit > 0 {
		si := s.sites[site]
		for len(si.items) > s.cfg.SiteLimit {
			s.sweepExpired(&si.expiry, si.items)
			if len(si.items) <= s.cfg.SiteLimit {
				break
			}
			if !s.evictLRU(si) {
				break
			}
		}
	}
	if s.cfg.GlobalLimit > 0 {
		for len(s.entries) > s.cfg.GlobalLimit {
			s.sweepExpired(&s.expiry, s.entries)
			if len(s.entries) <= s.cfg.GlobalLimit {
				break
			}
			if !s.evictGlobal() {
				break
			}
		}
	}
}

// sweepExpired 弹出堆顶所有已过期且仍有效的堆项并移除对应条目。
// 过期堆按过期时刻升序，遇到未过期的有效堆项即可停止。
// 有效性同时校验 seq（条目化身的唯一序号）与 gen（代际号），
// 防止同键条目删除重建后，前一代遗留的堆项被误判为有效。
func (s *Store) sweepExpired(h *expiryHeap, m map[key]*Entry) {
	for len(*h) > 0 {
		top := (*h)[0]
		e, ok := m[top.k]
		if !ok || e.seq != top.seq || e.gen != top.gen || e.ExpiresAt == nil {
			heap.Pop(h)
			continue
		}
		if !e.expired(s.now) {
			return
		}
		heap.Pop(h)
		s.removeLocked(top.k, &s.stats.ExpiredEvictions)
	}
}

// evictLRU 淘汰站点内 (最近访问, 创建时刻, 序号) 最小的一条。
func (s *Store) evictLRU(si *siteIndex) bool {
	for len(si.lru) > 0 {
		s.evictPops++
		top := si.lru[0]
		e, ok := si.items[top.k]
		if !ok || e.seq != top.seq || e.gen != top.gen {
			heap.Pop(&si.lru)
			continue
		}
		heap.Pop(&si.lru)
		s.removeLocked(top.k, &s.stats.CapacityEvictions)
		return true
	}
	return false
}

// evictGlobal 从条目数最多的站点（并列按站点名字序）淘汰一条。
func (s *Store) evictGlobal() bool {
	for len(s.siteOrd) > 0 {
		s.sitePops++
		top := s.siteOrd[0]
		si, ok := s.sites[top.name]
		if !ok || si.gen != top.gen || len(si.items) == 0 {
			heap.Pop(&s.siteOrd)
			continue
		}
		return s.evictLRU(si)
	}
	return false
}
