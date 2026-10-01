package numa

import "sync"

// Policy 是准入策略。
type Policy string

const (
	PolicyNone           Policy = "none"
	PolicyBestEffort     Policy = "best-effort"
	PolicyRestricted     Policy = "restricted"
	PolicySingleNUMANode Policy = "single-numa-node"
)

// Hint 是一个 NUMA 亲和提示：n 位非空掩码与 preferred 标记。
type Hint struct {
	Mask      uint16
	Preferred bool
}

// Provider 是外部资源提供者：nil 表示无偏好，否则给出提示列表（可为空）。
type Provider *[]Hint

// Allocation 记录容器的合并结果与各节点分配量。
type Allocation struct {
	Mask uint16
	CPUs []int64
	Req  int64
}

// AdmitResult 是一次 Admit 的结果。
type AdmitResult struct {
	Admitted  bool
	Mask      uint16
	Preferred bool
	CPUs      []int64
	Req       int64
}

// Manager 是带策略与 CPU 记账的拓扑提示合并器。
type Manager struct {
	mu         sync.Mutex
	n          int
	full       uint16
	caps       []int64
	free       []int64
	policy     Policy
	containers map[string]*Allocation
}

// NewManager 构造管理器。
func NewManager(n int, caps []int64, policy Policy) (*Manager, error) {
	if n < 1 || n > 8 {
		return nil, reject(ReasonInvalidConfig, "numa node count out of range")
	}
	if len(caps) != n {
		return nil, reject(ReasonInvalidConfig, "cap count does not match n")
	}
	free := make([]int64, n)
	for i, c := range caps {
		if c < 1 || c > 1_000_000_000 {
			return nil, reject(ReasonInvalidConfig, "node capacity out of range")
		}
		free[i] = c
	}
	switch policy {
	case PolicyNone, PolicyBestEffort, PolicyRestricted, PolicySingleNUMANode:
	default:
		return nil, reject(ReasonInvalidConfig, "unknown policy")
	}
	return &Manager{
		n:          n,
		full:       uint16(1<<uint(n) - 1),
		caps:       append([]int64(nil), caps...),
		free:       free,
		policy:     policy,
		containers: make(map[string]*Allocation),
	}, nil
}

// Admit 按策略合并提示、准入并分配 CPU。
func (m *Manager) Admit(id string, req int64, providers []Provider) (*AdmitResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	full := m.full

	// 1. 参数非法。
	if id == "" {
		return nil, reject(ReasonInvalidArgument, "container id is empty")
	}
	if req < 1 || req > 1_000_000_000_000 {
		return nil, reject(ReasonInvalidArgument, "cpu request out of range")
	}
	for _, p := range providers {
		if p == nil {
			continue
		}
		for _, h := range *p {
			if h.Mask == 0 || h.Mask&^full != 0 {
				return nil, reject(ReasonInvalidArgument, "hint mask out of n-bit range or empty")
			}
		}
	}

	// 2. 容器已存在。
	if _, ok := m.containers[id]; ok {
		return nil, reject(ReasonContainerExists, "container already admitted")
	}

	// 3. 容量不足（none 同样检查）。
	var totalFree int64
	for _, f := range m.free {
		totalFree += f
	}
	if totalFree < req {
		return nil, reject(ReasonInsufficientCPU, "total free cpu below request")
	}

	// none：忽略全部提示，不做组合数检查，结果为（全掩码，true）。
	if m.policy == PolicyNone {
		cpus := allocate(m.free, full, req)
		m.commit(id, req, full, cpus)
		return &AdmitResult{Admitted: true, Mask: full, Preferred: true, CPUs: cpus, Req: req}, nil
	}

	// 归一：先构造内置提供者提示，再对每个提供者归一。
	builtin := m.builtinHints(req)
	lists := make([][]Hint, 0, len(providers)+1)
	lists = append(lists, normalize(builtin, full, m.policy == PolicySingleNUMANode))
	for _, p := range providers {
		switch {
		case p == nil:
			lists = append(lists, []Hint{{Mask: full, Preferred: true}})
		default:
			lists = append(lists, normalize(*p, full, m.policy == PolicySingleNUMANode))
		}
	}

	// 4. 组合数检查（归一后长度乘积，不去重）。
	combos := 1
	for _, l := range lists {
		combos *= len(l)
		if combos > 100_000 {
			return nil, reject(ReasonTooManyCombos, "hint combinations exceed 1e5")
		}
	}

	best := merge(lists, full)

	// 5. 提示不满足：best-effort 总是准入；restricted / single-numa-node 要求 preferred。
	if !best.Preferred && (m.policy == PolicyRestricted || m.policy == PolicySingleNUMANode) {
		return nil, reject(ReasonHintNotSatisfied, "best hint is not preferred")
	}

	cpus := allocate(m.free, best.Mask, req)
	m.commit(id, req, best.Mask, cpus)
	return &AdmitResult{Admitted: true, Mask: best.Mask, Preferred: best.Preferred, CPUs: cpus, Req: req}, nil
}

// builtinHints 按当前空闲量生成内置 cpu 提供者的提示。
func (m *Manager) builtinHints(req int64) []Hint {
	hints := make([]Hint, 0)
	bestBits := m.n + 1
	for mask := 1; mask <= int(m.full); mask++ {
		var sum int64
		for i := 0; i < m.n; i++ {
			if uint(mask)&(uint(1)<<uint(i)) != 0 {
				sum += m.free[i]
			}
		}
		if sum >= req {
			bits := popcount(uint16(mask))
			if bits < bestBits {
				bestBits = bits
			}
		}
	}
	if bestBits > m.n {
		return hints
	}
	for mask := 1; mask <= int(m.full); mask++ {
		var sum int64
		for i := 0; i < m.n; i++ {
			if uint(mask)&(uint(1)<<uint(i)) != 0 {
				sum += m.free[i]
			}
		}
		if sum >= req {
			// 每个可行掩码都产出提示；仅位数等于 Zc 者 preferred。
			hints = append(hints, Hint{Mask: uint16(mask), Preferred: popcount(uint16(mask)) == bestBits})
		}
	}
	return hints
}

// normalize 对单个提供者的提示列表归一。
func normalize(hints []Hint, full uint16, singleOnly bool) []Hint {
	if len(hints) == 0 {
		return []Hint{{Mask: full, Preferred: false}}
	}
	if singleOnly {
		filtered := hints[:0:0]
		for _, h := range hints {
			if popcount(h.Mask) == 1 {
				filtered = append(filtered, h)
			}
		}
		hints = filtered
		if len(hints) == 0 {
			return []Hint{{Mask: full, Preferred: false}}
		}
	}
	return hints
}

// merge 枚举全部组合并返回最优提示。
func merge(lists [][]Hint, full uint16) Hint {
	type cand struct {
		mask    uint16
		allPref bool
	}
	cands := make([]cand, 0)

	var walk func(idx int, mask uint16, allPref bool)
	walk = func(idx int, mask uint16, allPref bool) {
		if idx == len(lists) {
			if mask != 0 {
				cands = append(cands, cand{mask: mask, allPref: allPref})
			}
			return
		}
		for _, h := range lists[idx] {
			walk(idx+1, mask&h.Mask, allPref && h.Preferred)
		}
	}
	walk(0, full, true)

	if len(cands) == 0 {
		return Hint{Mask: full, Preferred: false}
	}

	minBits := 9
	found := false
	for _, c := range cands {
		if c.allPref {
			found = true
			if b := popcount(c.mask); b < minBits {
				minBits = b
			}
		}
	}
	for i := range cands {
		cands[i].allPref = cands[i].allPref && found && popcount(cands[i].mask) == minBits
	}

	best := cands[0]
	bestPref := best.allPref
	for _, c := range cands[1:] {
		switch {
		case c.allPref && !bestPref:
			best, bestPref = c, true
		case c.allPref == bestPref:
			cb, bb := popcount(c.mask), popcount(best.mask)
			if cb < bb || (cb == bb && c.mask < best.mask) {
				best = c
			}
		}
	}
	return Hint{Mask: best.mask, Preferred: best.allPref}
}

// allocate 按结果掩码分配：先掩码内升序，不足再掩码外升序。
func allocate(free []int64, mask uint16, req int64) []int64 {
	cpus := make([]int64, len(free))
	need := req
	for i := range free {
		if mask&(uint16(1)<<uint(i)) != 0 {
			take := free[i]
			if take > need {
				take = need
			}
			cpus[i] = take
			need -= take
			if need == 0 {
				return cpus
			}
		}
	}
	for i := range free {
		if mask&(uint16(1)<<uint(i)) == 0 {
			take := free[i]
			if take > need {
				take = need
			}
			cpus[i] += take
			need -= take
			if need == 0 {
				return cpus
			}
		}
	}
	return cpus
}

// commit 记录容器并扣减空闲量。
func (m *Manager) commit(id string, req int64, mask uint16, cpus []int64) {
	rec := &Allocation{Mask: mask, CPUs: append([]int64(nil), cpus...), Req: req}
	m.containers[id] = rec
	for i, c := range cpus {
		m.free[i] -= c
	}
}

func popcount(m uint16) int {
	b := 0
	for m != 0 {
		b += int(m & 1)
		m >>= 1
	}
	return b
}

// Release 删除容器并归还 CPU。
func (m *Manager) Release(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.containers[id]
	if !ok {
		return reject(ReasonContainerMissing, "container not found on release")
	}
	for i, c := range rec.CPUs {
		m.free[i] += c
	}
	delete(m.containers, id)
	return nil
}

// Query 返回容器掩码与各节点分配量。
func (m *Manager) Query(id string) (*Allocation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.containers[id]
	if !ok {
		return nil, reject(ReasonContainerMissing, "container not found on query")
	}
	cpus := append([]int64(nil), rec.CPUs...)
	return &Allocation{Mask: rec.Mask, CPUs: cpus, Req: rec.Req}, nil
}
