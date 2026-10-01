package numa

import "sync"

// Manager 是带策略与 CPU 记账的拓扑提示合并器。
type Manager struct {
	mu      sync.Mutex
	n       int
	full    uint64
	caps    []int64
	free    []int64
	policy  Policy
	single  bool
	records map[string]*record
}

type record struct {
	req   int64
	mask  uint64
	pref  bool
	alloc []int64
}

// NewManager 创建一个管理器；配置非法时返回 ErrInvalidConfig。
func NewManager(n int, caps []int64, policy Policy) (*Manager, error) {
	if n < 1 || n > 8 {
		return nil, ErrInvalidConfig
	}
	if len(caps) != n {
		return nil, ErrInvalidConfig
	}
	for _, c := range caps {
		if c < 1 || c > 1_000_000_000 {
			return nil, ErrInvalidConfig
		}
	}
	switch policy {
	case PolicyNone, PolicyBestEffort, PolicyRestricted, PolicySingleNUMANode:
	default:
		return nil, ErrInvalidConfig
	}
	cpCaps := make([]int64, n)
	copy(cpCaps, caps)
	free := make([]int64, n)
	copy(free, caps)
	return &Manager{
		n:       n,
		full:    1<<uint(n) - 1,
		caps:    cpCaps,
		free:    free,
		policy:  policy,
		single:  policy == PolicySingleNUMANode,
		records: make(map[string]*record),
	}, nil
}

// Admit 尝试登记一个容器。
func (m *Manager) Admit(id string, req int64, providers []ProviderHints) (*Result, error) {
	if id == "" || req < 1 || req > 1_000_000_000_000 {
		return nil, ErrInvalidArgument
	}
	for _, p := range providers {
		if p.NoPreference {
			continue
		}
		for _, h := range p.Hints {
			if h.Mask == 0 || h.Mask&^m.full != 0 {
				return nil, ErrInvalidArgument
			}
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.records[id]; ok {
		return nil, ErrContainerExists
	}

	var totalFree int64
	for _, f := range m.free {
		totalFree += f
	}
	if totalFree < req {
		return nil, ErrInsufficientCapacity
	}

	if m.policy == PolicyNone {
		alloc := allocate(m.n, m.free, m.full, req)
		m.commitLocked(id, req, m.full, true, alloc)
		return &Result{Mask: m.full, Preferred: true, Allocation: alloc}, nil
	}

	lists := make([][]Hint, 0, len(providers)+1)
	lists = append(lists, normalizeProvider(ProviderHints{Hints: builtinHints(m.n, m.free, req)}, m.full, m.single))
	for _, p := range providers {
		lists = append(lists, normalizeProvider(p, m.full, m.single))
	}

	combos := 1
	for _, l := range lists {
		combos *= len(l)
		if combos > maxCombinations {
			return nil, ErrTooManyCombinations
		}
	}

	cands := mergeHints(lists)
	best := bestHint(cands, m.full)

	if (m.policy == PolicyRestricted || m.policy == PolicySingleNUMANode) && !best.Preferred {
		return nil, ErrHintNotSatisfied
	}

	alloc := allocate(m.n, m.free, best.Mask, req)
	m.commitLocked(id, req, best.Mask, best.Preferred, alloc)
	return &Result{Mask: best.Mask, Preferred: best.Preferred, Allocation: alloc}, nil
}

func (m *Manager) commitLocked(id string, req int64, mask uint64, pref bool, alloc []int64) {
	for i, a := range alloc {
		m.free[i] -= a
	}
	cp := make([]int64, len(alloc))
	copy(cp, alloc)
	m.records[id] = &record{req: req, mask: mask, pref: pref, alloc: cp}
}

// allocate 按结果掩码分配：先按节点号升序取掩码内节点，
// 仍不足再按节点号升序取掩码外节点。
func allocate(n int, free []int64, mask uint64, req int64) []int64 {
	alloc := make([]int64, n)
	remaining := req
	for i := 0; i < n && remaining > 0; i++ {
		if mask&(1<<uint(i)) != 0 {
			take := free[i]
			if take > remaining {
				take = remaining
			}
			alloc[i] += take
			remaining -= take
		}
	}
	for i := 0; i < n && remaining > 0; i++ {
		if mask&(1<<uint(i)) == 0 {
			take := free[i] - alloc[i]
			if take > remaining {
				take = remaining
			}
			alloc[i] += take
			remaining -= take
		}
	}
	return alloc
}

// Release 删除容器登记并归还其 CPU 分配。
func (m *Manager) Release(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.records[id]
	if !ok {
		return ErrReleaseNotFound
	}
	for i, a := range r.alloc {
		m.free[i] += a
	}
	delete(m.records, id)
	return nil
}

// Query 返回容器的结果掩码与各节点分配量。
func (m *Manager) Query(id string) (*Result, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.records[id]
	if !ok {
		return nil, ErrQueryNotFound
	}
	alloc := make([]int64, len(r.alloc))
	copy(alloc, r.alloc)
	return &Result{Mask: r.mask, Preferred: r.pref, Allocation: alloc}, nil
}
