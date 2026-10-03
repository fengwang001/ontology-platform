package engine

import "ontology/model"

// naive 是独立于引擎实现的逐步朴素模拟器：
// OrJoin 判定时在有效图上现做一遍可达搜索（不使用任何预计算闭包）。
type naive struct {
	n        int
	kinds    []model.NodeType
	out      [][]int
	in       [][]int
	act      []int
	arr      [][]int
	fires    []int
	endCount int
}

func newNaive(g *model.Graph, ch model.Choice) *naive {
	n := g.N
	out := g.EffectiveOut(ch)
	in := make([][]int, n+1)
	for u := 1; u <= n; u++ {
		for _, v := range out[u] {
			in[v] = append(in[v], u)
		}
	}
	m := &naive{
		n: n, kinds: g.Kinds, out: out, in: in,
		act:   make([]int, n+1),
		arr:   make([][]int, n+1),
		fires: make([]int, n+1),
	}
	for v := 1; v <= n; v++ {
		if g.Kinds[v] == model.AndJoin || g.Kinds[v] == model.OrJoin {
			m.arr[v] = make([]int, len(in[v]))
		}
	}
	start := 1
	for v := 1; v <= n; v++ {
		if g.Kinds[v] == model.Start {
			start = v
		}
	}
	m.edge(0, start, 1)
	m.settle()
	return m
}

// reachNow：在有效图上从 s 现算可达集合。
func (m *naive) reachNow(s int) map[int]bool {
	seen := map[int]bool{s: true}
	st := []int{s}
	for len(st) > 0 {
		v := st[len(st)-1]
		st = st[:len(st)-1]
		for _, w := range m.out[v] {
			if !seen[w] {
				seen[w] = true
				st = append(st, w)
			}
		}
	}
	return seen
}

func (m *naive) edge(u, v, c int) {
	if m.kinds[v] == model.AndJoin || m.kinds[v] == model.OrJoin {
		slot := 0
		for i, src := range m.in[v] {
			if src == u {
				slot = i
				break
			}
		}
		m.arr[v][slot] += c
		return
	}
	switch m.kinds[v] {
	case model.Start, model.AndSplit, model.XorSplit, model.OrSplit:
		for _, w := range m.out[v] {
			m.edge(v, w, c)
		}
	case model.Task:
		m.act[v] += c
	case model.End:
		m.endCount += c
	}
}

func (m *naive) complete(t int) bool {
	if m.kinds[t] != model.Task || m.act[t] <= 0 {
		return false
	}
	m.act[t]--
	m.edge(t, m.out[t][0], 1)
	m.settle()
	return true
}

func (m *naive) settle() {
	for {
		changed := false
		for v := 1; v <= m.n; v++ {
			if m.kinds[v] == model.AndJoin && m.andReady(v) {
				for i := range m.arr[v] {
					m.arr[v][i]--
				}
				m.fires[v]++
				m.edge(v, m.out[v][0], 1)
				changed = true
			} else if m.kinds[v] == model.OrJoin && m.orReady(v) {
				for i := range m.arr[v] {
					m.arr[v][i] = 0
				}
				m.fires[v]++
				m.edge(v, m.out[v][0], 1)
				changed = true
			}
		}
		if !changed {
			return
		}
	}
}

func (m *naive) andReady(v int) bool {
	for _, a := range m.arr[v] {
		if a < 1 {
			return false
		}
	}
	return true
}

func (m *naive) orReady(v int) bool {
	total := 0
	for _, a := range m.arr[v] {
		total += a
	}
	if total == 0 {
		return false
	}
	for p := 1; p <= m.n; p++ {
		if p == v {
			continue
		}
		has := false
		if m.kinds[p] == model.Task && m.act[p] > 0 {
			has = true
		} else if (m.kinds[p] == model.AndJoin || m.kinds[p] == model.OrJoin) && m.arrSum(p) > 0 {
			has = true
		}
		if has {
			if m.reachNow(p)[v] {
				return false
			}
		}
	}
	return true
}

func (m *naive) arrSum(v int) int {
	s := 0
	for _, a := range m.arr[v] {
		s += a
	}
	return s
}

func (m *naive) terminalKey() string {
	status := m.status()
	b := []byte(status)
	put := func(x int) { b = append(b, byte(x>>24), byte(x>>16), byte(x>>8), byte(x)) }
	put(m.endCount)
	for v := 1; v <= m.n; v++ {
		if m.kinds[v] == model.AndJoin || m.kinds[v] == model.OrJoin {
			put(v)
			put(m.fires[v])
		}
	}
	return string(b)
}

func (m *naive) status() Status {
	active := false
	for v := 1; v <= m.n; v++ {
		if m.kinds[v] == model.Task && m.act[v] > 0 {
			active = true
		}
	}
	if active {
		return Running
	}
	for v := 1; v <= m.n; v++ {
		if (m.kinds[v] == model.AndJoin || m.kinds[v] == model.OrJoin) && m.arrSum(v) > 0 {
			return Stuck
		}
	}
	return Completed
}
