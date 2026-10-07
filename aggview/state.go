package aggview

// aggState 维护单个视图内每个起点实例的聚合状态。
//
// 对每个起点 s 维护一个"多重集合" terms[s]：
//   - 键   ：终点实例 ID（同一终点经由任意多条走法到达，只计一次）
//   - 值   ：该终点当前被汇总属性的取值
//
// 最大值为多重集合中的最大值；集合为空时聚合结果为明确的"不存在"
// （Result.Absent），绝不使用任何默认数值。
//
// 属性变小：先在 terms 内删除该终点的旧贡献再写入新贡献；只有被变小
// 的终点恰好是当前最大值来源时才需要重新确定最大值，重新确定只扫描
// terms[s] 内现存的终点（数量 == s 当前真实可达终点数），不扫描无关实例。
type aggState struct {
	// reachable[s]：s 当前沿路径可达的"不同终点实例"集合（存在性）。
	reachable map[string]map[string]struct{}
	// terms[s]：s 可达终点中、汇总属性存在的终点 t -> 当前属性值。
	terms map[string]map[string]float64
}

func newAggState() *aggState {
	return &aggState{
		reachable: map[string]map[string]struct{}{},
		terms:     map[string]map[string]float64{},
	}
}

// get 返回起点 s 的聚合结果。
func (a *aggState) get(s string) Result {
	rs, ok := a.reachable[s]
	if !ok || len(rs) == 0 {
		return Result{Absent: true}
	}
	max := 0.0
	first := true
	for _, v := range a.terms[s] {
		if first || v > max {
			max = v
			first = false
		}
	}
	return Result{Max: max}
}

// put 设置 s -> t 的贡献（t 已在 s 的可达终点集合内）。
// 返回该终点此前是否已是贡献者，以及旧值。
func (a *aggState) put(s, t string, v float64) (existed bool, old float64) {
	ms := a.terms[s]
	if ms == nil {
		ms = map[string]float64{}
		a.terms[s] = ms
	}
	old, existed = ms[t]
	ms[t] = v
	return existed, old
}

// remove 删除 s -> t 的贡献。
func (a *aggState) remove(s, t string) {
	if rs, ok := a.reachable[s]; ok {
		delete(rs, t)
	}
	if ms, ok := a.terms[s]; ok {
		delete(ms, t)
	}
}

// contains 报告 t 是否为 s 当前的贡献终点。
func (a *aggState) contains(s, t string) bool {
	if rs, ok := a.reachable[s]; ok {
		if _, yes := rs[t]; yes {
			return true
		}
	}
	return false
}

// termValue 返回 s 的可达终点 t 的属性贡献是否存在及其值。
func (a *aggState) termValue(s, t string) (float64, bool) {
	if ms, ok := a.terms[s]; ok {
		v, yes := ms[t]
		return v, yes
	}
	return 0, false
}

// reachableCount 返回 s 当前真实可达终点总数。
func (a *aggState) reachableCount(s string) int { return len(a.reachable[s]) }

// size 兼容旧命名：返回可达终点数。
func (a *aggState) size(s string) int { return len(a.reachable[s]) }

// reachableSet 返回 s 的可达集合（内部引用，只读使用）。
func (a *aggState) reachableSet(s string) map[string]struct{} {
	return a.reachable[s]
}

// rebuild 用给定可达终点集合重建 s 的可达关系；values 提供其中汇总属性
// 存在的终点取值（属性缺失的终点计入可达集合，但不参与最大值）。
func (a *aggState) rebuild(s string, set map[string]struct{}, values map[string]float64) {
	if len(set) == 0 {
		delete(a.reachable, s)
		delete(a.terms, s)
		return
	}
	rs := make(map[string]struct{}, len(set))
	for t := range set {
		rs[t] = struct{}{}
	}
	a.reachable[s] = rs
	ms := make(map[string]float64, len(values))
	for t, v := range values {
		ms[t] = v
	}
	a.terms[s] = ms
}

// setTerm 更新 s -> t 的属性贡献（t 必须已在可达集合内）。
func (a *aggState) setTerm(s, t string, v float64) {
	ms := a.terms[s]
	if ms == nil {
		ms = map[string]float64{}
		a.terms[s] = ms
	}
	ms[t] = v
}

// deleteTerm 删除 s -> t 的属性贡献（可达关系保留）。
func (a *aggState) deleteTerm(s, t string) {
	if ms, ok := a.terms[s]; ok {
		delete(ms, t)
	}
}

// maxOf 扫描 s 当前全部属性贡献求最大值；无贡献时 Absent。
// 扫描次数计入 n（用于开销证明，扫描范围 == 当前可达终点集合）。
func (a *aggState) maxOf(s string) (r Result, n int) {
	if rs := a.reachable[s]; len(rs) == 0 {
		return Result{Absent: true}, 0
	}
	first := true
	max := 0.0
	// 只遍历现存贡献者，数目不超过可达终点总数。
	for _, v := range a.terms[s] {
		n++
		if first || v > max {
			max, first = v, false
		}
	}
	if first {
		// 可达但没有任何终点具备该属性：明确的不存在状态。
		return Result{Absent: true}, n
	}
	return Result{Max: max}, n
}

// recomputeMaxForSource 重新扫描 s 当前多重集合确定最大值。
// 扫描条目数不超过 s 当前真实可达终点数。
func (a *aggState) recomputeMaxForSource(s string) Result {
	r, _ := a.maxOf(s)
	return r
}
