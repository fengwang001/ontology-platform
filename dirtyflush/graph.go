package dirtyflush

import "sort"

// AddDep 登记 “a 必须先于 b 刷写”。
// a 干净时视为成功但不登记；重复登记幂等；若会形成环则拒绝。
func (m *Manager) AddDep(a, b int) error {
	if a < MinPage || a > MaxPage || b < MinPage || b > MaxPage || a == b {
		return opErr("AddDep", ReasonInvalidArg, "page out of range or a == b")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.pages[a].dirty {
		return nil
	}
	if edges := m.out[a]; edges != nil {
		if _, ok := edges[b]; ok {
			return nil // 幂等
		}
	}
	if m.reaches(b, a) {
		return opErr("AddDep", ReasonCycle, "edge would create a cycle")
	}
	addEdge(m.out, a, b)
	addEdge(m.in, b, a)
	return nil
}

func addEdge(adj map[int]map[int]struct{}, from, to int) {
	set := adj[from]
	if set == nil {
		set = make(map[int]struct{})
		adj[from] = set
	}
	set[to] = struct{}{}
}

// reaches 判断从 from 出发沿 out 边是否能到达 to。
func (m *Manager) reaches(from, to int) bool {
	stack := []int{from}
	seen := map[int]bool{from: true}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if n == to {
			return true
		}
		for next := range m.out[n] {
			if !seen[next] {
				seen[next] = true
				stack = append(stack, next)
			}
		}
	}
	return false
}

// removeOutEdges 删除从 p 发出的全部边（页变干净时调用）。
func (m *Manager) removeOutEdges(p int) {
	for b := range m.out[p] {
		if pre := m.in[b]; pre != nil {
			delete(pre, p)
			if len(pre) == 0 {
				delete(m.in, b)
			}
		}
	}
	delete(m.out, p)
}

// Checkpoint 返回链表首页的 oldest；链表为空时返回最大 Modify LSN + 1。
func (m *Manager) Checkpoint() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.checkpointLocked()
}

// Plan 生成刷写计划：
//   - 按链表次序遍历 oldest < target 且不在途的页；
//   - 对每个尚未入计划的页 p，先递归展开其“脏且不在途”的前置页，
//     前置页按页号升序检查，已入计划的跳过；
//   - 前置页即使 oldest >= target 也会被拉入。
func (m *Manager) Plan(target int64) ([]int, error) {
	if target < 1 {
		return nil, opErr("Plan", ReasonInvalidArg, "target must be >= 1")
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	var chain []int // 链表中按 oldest 升序的页
	chain = m.order.inorder(chain)

	scheduled := make(map[int]bool)
	var plan []int
	var emit func(p int)
	emit = func(p int) {
		if scheduled[p] {
			return
		}
		// 收集“脏、不在途”的前置页，按页号升序展开。
		pres := m.in[p]
		if len(pres) > 0 {
			qs := make([]int, 0, len(pres))
			for q := range pres {
				if st := &m.pages[q]; st.dirty && !st.inFlight {
					qs = append(qs, q)
				}
			}
			sort.Ints(qs)
			for _, q := range qs {
				if !scheduled[q] {
					emit(q)
				}
			}
		}
		scheduled[p] = true
		plan = append(plan, p)
	}

	for _, p := range chain {
		st := &m.pages[p]
		if st.inFlight || st.oldest >= target {
			continue
		}
		if !scheduled[p] {
			emit(p)
		}
	}
	return plan, nil
}

// Snapshot 返回某一页的可观测状态副本；页不存在（从未 Modify）时 ok=false。
func (m *Manager) Snapshot(p int) (Page, bool) {
	if p < MinPage || p > MaxPage {
		return Page{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.pages[p]
	if !st.dirty && st.lsn == 0 {
		return Page{}, false
	}
	return Page{
		ID:         p,
		LSN:        st.lsn,
		Oldest:     st.oldest,
		Dirty:      st.dirty,
		InFlight:   st.inFlight,
		FirstAfter: st.firstAfter,
	}, true
}

// Order 返回刷写链表页号的升序（按 oldest、其次页号）副本。
func (m *Manager) Order() []int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.order.inorder(nil)
}

// OrderEntries 返回带 oldest 的链表升序副本。
func (m *Manager) OrderEntries() []OrderEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.order.entries()
}

// DirtyCount 返回当前脏页数（含在途页）。
func (m *Manager) DirtyCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.dirtyN
}

// Flushed 返回当前日志已落盘水位。
func (m *Manager) Flushed() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.flushed
}

// MaxModify 返回已接受的最大 Modify LSN。
func (m *Manager) MaxModify() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.maxModify
}

// OutEdges 返回从 a 发出的依赖边目标页号的升序副本。
func (m *Manager) OutEdges(a int) []int {
	m.mu.Lock()
	defer m.mu.Unlock()
	edges := m.out[a]
	if len(edges) == 0 {
		return nil
	}
	out := make([]int, 0, len(edges))
	for b := range edges {
		out = append(out, b)
	}
	sort.Ints(out)
	return out
}
