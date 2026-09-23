package agg

// count：删除恒为 n-1，与被删值无关 → 可增量撤回。
type count int64

func (c count) Name() string                       { return "count" }
func (c count) IncrementalDelete() bool            { return true }
func (c *count) Add(float64)                       { *c++ }
func (c *count) Sub(float64)                       { *c-- }
func (c count) RemoveCausesRecompute(float64) bool { return false }
func (c *count) Recompute(m []float64)             { *c = count(len(m)) }
func (c count) Value() float64                     { return float64(c) }

// sum：被删值 v 已知，新和恒为 s-v → 可增量撤回。
type sum float64

func (s sum) Name() string                       { return "sum" }
func (s sum) IncrementalDelete() bool            { return true }
func (s *sum) Add(v float64)                     { *s += sum(v) }
func (s *sum) Sub(v float64)                     { *s -= sum(v) }
func (s sum) RemoveCausesRecompute(float64) bool { return false }
func (s *sum) Recompute(m []float64) {
	var t float64
	for _, v := range m {
		t += v
	}
	*s = sum(t)
}
func (s sum) Value() float64 { return float64(s) }

// extreme 是 min/max 的公共骨架：标量状态不含次极值，删除命中当前
// 极值时必须回到成员重算。better 报告 a 是否比 b 更优。
type extreme struct {
	name   string
	cur    float64
	set    bool
	better func(a, b float64) bool
}

func newMin() *extreme {
	return &extreme{name: "min", better: func(a, b float64) bool { return a < b }}
}

func newMax() *extreme {
	return &extreme{name: "max", better: func(a, b float64) bool { return a > b }}
}

func (e *extreme) Name() string            { return e.name }
func (e *extreme) IncrementalDelete() bool { return false }

func (e *extreme) Add(v float64) {
	if !e.set || e.better(v, e.cur) {
		e.cur, e.set = v, true
	}
}

func (e *extreme) Sub(float64) {} // 不会被调用

// RemoveCausesRecompute：仅当被删值不劣于当前极值时才需重算。
func (e *extreme) RemoveCausesRecompute(v float64) bool {
	return e.set && !e.better(e.cur, v)
}

func (e *extreme) Recompute(m []float64) {
	e.set = false
	for _, v := range m {
		e.Add(v)
	}
}

func (e *extreme) Value() float64 { return e.cur }

// distinct：标量计数不含引用计数信息 → 每次删除都需重算；重算同时
// 重建内部引用计数表，使插入保持增量。
type distinct struct {
	n    int64
	refs map[float64]int
}

func newDistinct() *distinct { return &distinct{refs: map[float64]int{}} }

func (d *distinct) Name() string            { return "distinct" }
func (d *distinct) IncrementalDelete() bool { return false }

func (d *distinct) Add(v float64) {
	d.refs[v]++
	if d.refs[v] == 1 {
		d.n++
	}
}

func (d *distinct) Sub(float64) {} // 不会被调用

func (d *distinct) RemoveCausesRecompute(float64) bool { return true }

func (d *distinct) Recompute(m []float64) {
	d.refs = make(map[float64]int, len(m))
	d.n = 0
	for _, v := range m {
		d.Add(v)
	}
}

func (d *distinct) Value() float64 { return float64(d.n) }
