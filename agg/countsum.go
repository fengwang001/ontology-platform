package agg

// countAgg: cardinality of the group. Insertion adds one, deletion subtracts
// one; no member value or identity is ever needed, so it never triggers
// recomputation.
type countAgg struct{ n int }

func (a *countAgg) Kind() Kind                    { return Count }
func (a *countAgg) Insert(Member)                 { a.n++ }
func (a *countAgg) NeedsMembersOnDelete() bool    { return false }
func (a *countAgg) DeleteIncremental(Member) bool { a.n--; return true }
func (a *countAgg) Reset()                        { a.n = 0 }
func (a *countAgg) Recompute(members []Member)    { a.n = len(members) }
func (a *countAgg) Value() (float64, bool) {
	if a.n == 0 {
		return 0, false
	}
	return float64(a.n), true
}

// sumAgg adds values incrementally. To guarantee bit-identical equality
// with a full recomputation over the surviving multiset, it maintains a
// balanced binary accumulation tree keyed by member key (see sumtree.go):
// tree addition is associative per construction and does not depend on the
// arrival order of changes.
type sumAgg struct {
	root *sumNode
}

func newSumAgg() *sumAgg { return &sumAgg{} }

func (a *sumAgg) Kind() Kind                 { return Sum }
func (a *sumAgg) NeedsMembersOnDelete() bool { return false }

func (a *sumAgg) Insert(m Member) { a.root = a.root.insert(m.Key, m.Value) }

func (a *sumAgg) DeleteIncremental(m Member) bool {
	a.root = a.root.erase(m.Key)
	return true
}

func (a *sumAgg) Reset() { a.root = nil }

func (a *sumAgg) Recompute(members []Member) {
	a.root = nil
	for _, m := range members {
		a.root = a.root.insert(m.Key, m.Value)
	}
}

func (a *sumAgg) Value() (float64, bool) {
	if a.root == nil {
		return 0, false
	}
	return a.root.total, true
}
