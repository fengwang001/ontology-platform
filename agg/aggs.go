package agg

type countAgg struct{ n float64 }

func (a *countAgg) Kind() Kind                      { return Count }
func (a *countAgg) NeedsMembersOnDelete() bool      { return false }
func (a *countAgg) NeedsRecomputeOnDelete(float64) bool { return false }
func (a *countAgg) Insert(float64)                  { a.n++ }
func (a *countAgg) Delete(float64)                  { a.n-- }
func (a *countAgg) Value() float64                  { return a.n }
func (a *countAgg) Reset()                          { a.n = 0 }
func (a *countAgg) Build(m map[string]float64)       { a.n = float64(len(m)) }

type sumAgg struct{ s float64 }

func (a *sumAgg) Kind() Kind                      { return Sum }
func (a *sumAgg) NeedsMembersOnDelete() bool      { return false }
func (a *sumAgg) NeedsRecomputeOnDelete(float64) bool { return false }
func (a *sumAgg) Insert(v float64)                { a.s += v }
func (a *sumAgg) Delete(v float64)                { a.s -= v }
func (a *sumAgg) Value() float64                  { return a.s }
func (a *sumAgg) Reset()                          { a.s = 0 }
func (a *sumAgg) Build(m map[string]float64) {
	var s float64
	for _, v := range m {
		s += v
	}
	a.s = s
}

type minAgg struct {
	v     float64
	empty bool
}

func (a *minAgg) Kind() Kind                 { return Min }
func (a *minAgg) NeedsMembersOnDelete() bool { return true }
func (a *minAgg) NeedsRecomputeOnDelete(v float64) bool {
	return !a.empty && eq(v, a.v)
}
func (a *minAgg) Insert(v float64) {
	if a.empty || v < a.v {
		a.v, a.empty = v, false
	}
}
func (a *minAgg) Delete(float64) {} // 极值删除由 Build 重算；非极值无需动作。
func (a *minAgg) Value() float64 { return a.v }
func (a *minAgg) Reset()         { a.v, a.empty = 0, true }
func (a *minAgg) Build(m map[string]float64) {
	a.Reset()
	for _, v := range m {
		a.Insert(v)
	}
}

type maxAgg struct {
	v     float64
	empty bool
}

func (a *maxAgg) Kind() Kind                 { return Max }
func (a *maxAgg) NeedsMembersOnDelete() bool { return true }
func (a *maxAgg) NeedsRecomputeOnDelete(v float64) bool {
	return !a.empty && eq(v, a.v)
}
func (a *maxAgg) Insert(v float64) {
	if a.empty || v > a.v {
		a.v, a.empty = v, false
	}
}
func (a *maxAgg) Delete(float64) {}
func (a *maxAgg) Value() float64 { return a.v }
func (a *maxAgg) Reset()         { a.v, a.empty = 0, true }
func (a *maxAgg) Build(m map[string]float64) {
	a.Reset()
	for _, v := range m {
		a.Insert(v)
	}
}

type distinctAgg struct {
	seen map[float64]struct{}
}

func (a *distinctAgg) Kind() Kind                 { return DistinctCount }
func (a *distinctAgg) NeedsMembersOnDelete() bool { return true }
func (a *distinctAgg) NeedsRecomputeOnDelete(float64) bool { return true }
func (a *distinctAgg) Insert(v float64)           { a.seen[v] = struct{}{} }
func (a *distinctAgg) Delete(float64)             {}
func (a *distinctAgg) Value() float64             { return float64(len(a.seen)) }
func (a *distinctAgg) Reset() {
	a.seen = map[float64]struct{}{}
}
func (a *distinctAgg) Build(m map[string]float64) {
	a.Reset()
	for _, v := range m {
		a.seen[v] = struct{}{}
	}
}
