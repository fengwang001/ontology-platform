package dirtyflush

import "sort"

// CheckInvariants 校验题述全部“任何时刻”不变量，在单把锁内取一致快照，
// 可在并发场景下安全调用。返回发现的第一个问题，nil 表示全部成立。
func (m *Manager) CheckInvariants() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.checkLocked()
}

func (m *Manager) checkLocked() error {
	entries := m.order.entries()
	if len(entries) != m.dirtyN {
		return errf("dirty count %d != list size %d", m.dirtyN, len(entries))
	}
	if m.dirtyN > m.cap {
		return errf("dirty pages %d exceed capacity %d", m.dirtyN, m.cap)
	}
	for i := 1; i < len(entries); i++ {
		a, b := entries[i-1], entries[i]
		if !keyLess(a.Oldest, a.Page, b.Oldest, b.Page) {
			return errf("list not strictly ascending: %+v before %+v", a, b)
		}
	}
	for _, e := range entries {
		st := m.pages[e.Page]
		if !st.dirty {
			return errf("clean page %d in list", e.Page)
		}
		if st.oldest != e.Oldest {
			return errf("page %d oldest mismatch: state %d list %d", e.Page, st.oldest, e.Oldest)
		}
		if st.oldest > st.lsn {
			return errf("page %d oldest %d > lsn %d", e.Page, st.oldest, st.lsn)
		}
	}
	for p := range m.out {
		if !m.pages[p].dirty {
			return errf("clean page %d has out edges %v", p, m.out[p])
		}
		for b := range m.out[p] {
			if pre := m.in[b]; pre == nil {
				return errf("edge %d->%d missing reverse edge", p, b)
			} else if _, ok := pre[p]; !ok {
				return errf("edge %d->%d missing reverse entry", p, b)
			}
		}
	}
	if cp := m.checkpointLocked(); len(entries) > 0 && cp > entries[0].Oldest {
		return errf("checkpoint %d > first oldest %d", cp, entries[0].Oldest)
	}
	return m.cycleLocked()
}

func (m *Manager) checkpointLocked() int64 {
	if oldest, _, ok := m.order.first(); ok {
		return oldest
	}
	return m.maxModify + 1
}

func (m *Manager) cycleLocked() error {
	color := make(map[int]uint8)
	var dfs func(int) bool
	dfs = func(u int) bool {
		color[u] = 1
		outs := make([]int, 0, len(m.out[u]))
		for v := range m.out[u] {
			outs = append(outs, v)
		}
		sort.Ints(outs)
		for _, v := range outs {
			switch color[v] {
			case 1:
				return true
			case 0:
				if dfs(v) {
					return true
				}
			}
		}
		color[u] = 2
		return false
	}
	nodes := make([]int, 0, len(m.out))
	for u := range m.out {
		nodes = append(nodes, u)
	}
	sort.Ints(nodes)
	for _, u := range nodes {
		if color[u] == 0 && dfs(u) {
			return errf("dependency graph has a cycle")
		}
	}
	return nil
}
