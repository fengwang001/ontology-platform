package agg

// extremumAgg maintains both the extremal value and how many surviving
// members currently hold it. Insertion is incremental. A deletion is
// incremental only when the removed value differs from the extremum (or
// when another surviving member still ties at the extremum). When the
// unique extremum is removed the new extremum cannot be derived from
// maintained state, so members are required and the view must recompute.
type extremumAgg struct {
	kind   Kind
	count  map[float64]int
	ext    float64
	have   bool
	better func(candidate, current float64) bool
}

func newExtremum(k Kind, better func(float64, float64) bool) *extremumAgg {
	return &extremumAgg{kind: k, count: map[float64]int{}, better: better}
}

func (a *extremumAgg) Kind() Kind { return a.kind }

// NeedsMembersOnDelete reports the conservative static strategy: deleting
// the unique extremum always needs members. The instance method
// DeleteIncremental exploits the common non-extremum fast path.
func (a *extremumAgg) NeedsMembersOnDelete() bool { return true }

func (a *extremumAgg) Insert(m Member) {
	a.count[m.Value]++
	if !a.have || a.better(m.Value, a.ext) {
		a.ext, a.have = m.Value, true
	}
}

func (a *extremumAgg) DeleteIncremental(m Member) (ok bool) {
	c := a.count[m.Value]
	if c <= 1 {
		if a.have && floatEqual(m.Value, a.ext) {
			return false
		}
		delete(a.count, m.Value)
		return true
	}
	a.count[m.Value] = c - 1
	return true
}

func (a *extremumAgg) Reset() {
	a.count = map[float64]int{}
	a.have = false
}

func (a *extremumAgg) Recompute(members []Member) {
	a.Reset()
	for _, m := range members {
		a.Insert(m)
	}
}

func (a *extremumAgg) Value() (float64, bool) { return a.ext, a.have }

// floatEqual treats +0 and -0 as equal, as required by the boundary
// semantics. NaN is rejected before reaching aggregators.
func floatEqual(x, y float64) bool {
	if x == 0 && y == 0 {
		return true
	}
	return x == y
}

func less(candidate, current float64) bool    { return candidate < current }
func greater(candidate, current float64) bool { return candidate > current }

type minAgg struct{ *extremumAgg }

func newMinAgg() *minAgg {
	return &minAgg{extremumAgg: newExtremum(Min, less)}
}

type maxAgg struct{ *extremumAgg }

func newMaxAgg() *maxAgg {
	return &maxAgg{extremumAgg: newExtremum(Max, greater)}
}

// distinctAgg counts distinct values while retaining a per-value
// occurrence count. A naive single counter cannot answer a deletion
// ("does another member still hold this value?") and would therefore need
// the surviving multiset; the occurrence table supplies exactly that
// knowledge, so deletions remain incremental. NeedsMembersOnDelete still
// declares the family's withdrawal requirement honestly: the derivation
// (DESIGN.md) treats a distinct count that keeps only the scalar as
// non-invertible.
type distinctAgg struct{ seen map[float64]int }

func (a *distinctAgg) Kind() Kind                 { return DistinctCount }
func (a *distinctAgg) NeedsMembersOnDelete() bool { return true }

func (a *distinctAgg) Insert(m Member) {
	if a.seen == nil {
		a.seen = map[float64]int{}
	}
	a.seen[m.Value]++
}

func (a *distinctAgg) DeleteIncremental(m Member) (ok bool) {
	if a.seen == nil {
		return false
	}
	switch a.seen[m.Value] {
	case 0:
		return false
	case 1:
		delete(a.seen, m.Value)
	default:
		a.seen[m.Value]--
	}
	return true
}
func (a *distinctAgg) Reset() { a.seen = nil }

func (a *distinctAgg) Recompute(members []Member) {
	a.Reset()
	for _, m := range members {
		a.Insert(m)
	}
}

func (a *distinctAgg) Value() (float64, bool) {
	if len(a.seen) == 0 {
		return 0, false
	}
	return float64(len(a.seen)), true
}
