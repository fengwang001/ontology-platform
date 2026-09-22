package zone

import "testing"

func TestThreeStatesDistinct(t *testing.T) {
	n := NullValue()
	z := IntValue(0)
	e := StringValue("")
	if n.Kind == z.Kind || n.Kind == e.Kind || z.Kind == e.Kind {
		t.Fatal("null / 0 / empty string are not distinguishable")
	}
	if !n.IsNull() || z.IsNull() || e.IsNull() {
		t.Fatal("IsNull wrong")
	}
	if n.Equal(z) || z.Equal(e) || !n.Equal(NullValue()) {
		t.Fatal("Equal wrong")
	}
}

func TestStatsExcludeNulls(t *testing.T) {
	s := Build([]Value{NullValue(), IntValue(5), NullValue(), IntValue(10)})
	if s.Nulls != 2 || !s.HasMin || s.Min.I != 5 || s.Max.I != 10 {
		t.Fatalf("stats wrong: %+v", s)
	}
	a := Build([]Value{NullValue(), NullValue()})
	if a.HasMin || !a.AllNull() {
		t.Fatal("all-null group must have no min/max")
	}
}

func TestNullSkipsValuePredicates(t *testing.T) {
	preds := []Predicate{
		Cmp{Op: OpEQ, RHS: IntValue(0)},
		Cmp{Op: OpLT, RHS: IntValue(1 << 62)},
		Cmp{Op: OpLE, RHS: IntValue(1 << 62)},
		Cmp{Op: OpGT, RHS: IntValue(-1 << 62)},
		Cmp{Op: OpGE, RHS: IntValue(-1 << 62)},
		In{Items: []Value{IntValue(0)}},
		And{Parts: []Predicate{Cmp{Op: OpGE, RHS: IntValue(-1 << 62)}, IsNotNull{}}},
	}
	for i, p := range preds {
		if p.Match(NullValue()) {
			t.Fatalf("predicate %d matched NULL", i)
		}
	}
	if !(IsNull{}).Match(NullValue()) || (IsNull{}).Match(IntValue(0)) {
		t.Fatal("IS NULL wrong")
	}
	if (IsNotNull{}).Match(NullValue()) || !(IsNotNull{}).Match(StringValue("")) {
		t.Fatal("IS NOT NULL wrong")
	}
}

func TestBoundaryPruning(t *testing.T) {
	s := Build([]Value{IntValue(10), IntValue(20)})
	cases := []struct {
		p    Predicate
		keep bool
	}{
		{Cmp{Op: OpEQ, RHS: IntValue(10)}, true},  // equals min
		{Cmp{Op: OpEQ, RHS: IntValue(20)}, true},  // equals max
		{Cmp{Op: OpEQ, RHS: IntValue(21)}, false}, // greater than max
		{Cmp{Op: OpEQ, RHS: IntValue(9)}, false},  // smaller than min
		{Cmp{Op: OpLT, RHS: IntValue(11)}, true},
		{Cmp{Op: OpLT, RHS: IntValue(10)}, false},
		{Cmp{Op: OpGT, RHS: IntValue(19)}, true},
		{Cmp{Op: OpGT, RHS: IntValue(20)}, false},
		{In{Items: []Value{IntValue(9), IntValue(20)}}, true},
		{In{Items: []Value{IntValue(9), IntValue(21)}}, false},
		{And{Parts: []Predicate{
			Cmp{Op: OpGE, RHS: IntValue(10)},
			Cmp{Op: OpLE, RHS: IntValue(20)},
		}}, true},
		{And{Parts: []Predicate{
			Cmp{Op: OpGT, RHS: IntValue(20)},
			Cmp{Op: OpLE, RHS: IntValue(30)},
		}}, false},
	}
	for i, c := range cases {
		if got := c.p.CouldHave(s); got != c.keep {
			t.Fatalf("case %d: keep=%v want %v", i, got, c.keep)
		}
	}
}
