package ontology

import "sort"

// LeakReport 描述当前未归还快照的分布，用于发现泄漏。
type LeakReport struct {
	// Outstanding 是未归还快照总数。
	Outstanding int
	// ByVersion 按版本号列出未归还快照数，只含大于零的项。
	ByVersion map[uint64]int
}

// Leaks 报告当前有多少份快照未归还、分别指向哪些版本。
func (m *Manager) Leaks() LeakReport {
	m.mu.Lock()
	defer m.mu.Unlock()
	rep := LeakReport{Outstanding: m.outstanding, ByVersion: map[uint64]int{}}
	for num, n := range m.refs {
		if n > 0 {
			rep.ByVersion[num] = n
		}
	}
	return rep
}

// Collect 回收所有没有任何未归还快照指向的旧版本，
// 按升序返回实际回收的版本号。当前版本永远不回收；
// 有快照指向的版本一律不回收。版本号回收后也永不复用。
func (m *Manager) Collect() []uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	var gone []uint64
	for num := range m.versions {
		if num == m.current || m.refs[num] > 0 {
			continue
		}
		delete(m.versions, num)
		delete(m.refs, num)
		gone = append(gone, num)
	}
	sort.Slice(gone, func(i, j int) bool { return gone[i] < gone[j] })
	return gone
}
