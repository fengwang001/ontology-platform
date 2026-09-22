package zone

import "testing"

func TestPredicateMatchThreeValued(t *testing.T) {
	// 空值：不命中任何数值谓词，只由 IS NULL 命中。
	numPreds := []Predicate{Eq(0), Lt(1), Le(0), Gt(-1), Ge(0), In(0, 1), IsNotNull()}
	for _, p := range numPreds {
		if p.Match(0, true) {
			t.Fatalf("null matched numeric predicate %+v", p)
		}
	}
	if !IsNull().Match(0, true) {
		t.Fatal("IS NULL must match null")
	}
	// 空值与 0 是三态中的不同结果：0 命中 Eq(0)，空值不命中。
	if !Eq(0).Match(0, false) {
		t.Fatal("zero must match Eq(0)")
	}
	if Eq(0).Match(0, true) {
		t.Fatal("null must not match Eq(0)")
	}
}

func TestConjunctionMatch(t *testing.T) {
	c := And(Ge(10), Lt(20), IsNotNull())
	if !c.Match(15, false) {
		t.Fatal("15 should match")
	}
	if c.Match(25, false) {
		t.Fatal("25 should not match")
	}
	if c.Match(15, true) {
		t.Fatal("null should not match")
	}
	if !And().Match(0, true) {
		t.Fatal("empty conjunction is always true")
	}
}

func statsOf(vals ...int64) Stats {
	s := Stats{Rows: len(vals)}
	for _, v := range vals {
		if !s.HasValue {
			s.Min, s.Max, s.HasValue = v, v, true
		} else {
			if v < s.Min {
				s.Min = v
			}
			if v > s.Max {
				s.Max = v
			}
		}
	}
	return s
}

func TestMayMatchBoundary(t *testing.T) {
	s := statsOf(10, 20, 30) // min=10 max=30
	keep := []Predicate{Eq(10), Eq(30), Eq(20), Le(10), Ge(30), Lt(11), Gt(29), In(5, 10), IsNotNull()}
	for _, p := range keep {
		if !s.MayMatch(And(p)) {
			t.Fatalf("predicate %+v wrongly pruned", p)
		}
	}
	drop := []Predicate{Eq(9), Eq(31), Lt(10), Le(9), Gt(30), Ge(31), In(1, 2, 3), IsNull()}
	for _, p := range drop {
		if s.MayMatch(And(p)) {
			t.Fatalf("predicate %+v should prune", p)
		}
	}
}

func TestMayMatchAllNullGroup(t *testing.T) {
	s := Stats{Rows: 5, Nulls: 5, HasValue: false}
	if !s.MayMatch(And(IsNull())) {
		t.Fatal("all-null group must be kept for IS NULL")
	}
	for _, p := range []Predicate{Eq(0), Lt(100), Ge(-100), In(0), IsNotNull()} {
		if s.MayMatch(And(p)) {
			t.Fatalf("all-null group must be pruned for %+v", p)
		}
	}
}

func TestMayMatchConjunction(t *testing.T) {
	s := statsOf(10, 20, 30)
	if !s.MayMatch(And(Ge(10), Le(30))) {
		t.Fatal("overlapping range must be kept")
	}
	// 单个谓词可排除即可整体排除。
	if s.MayMatch(And(Ge(10), Gt(30))) {
		t.Fatal("one impossible predicate prunes the group")
	}
}
