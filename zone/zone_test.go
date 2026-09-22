package zone

import "testing"

func statsOf(vals ...int64) Stats {
	var s Stats
	for _, v := range vals {
		s.Add(v, false)
	}
	return s
}

func TestStatsExcludeNulls(t *testing.T) {
	var s Stats
	s.Add(5, false)
	s.Add(0, true)
	s.Add(-3, false)
	if s.Min != -3 || s.Max != 5 || s.Nulls != 1 || s.Rows != 3 {
		t.Fatalf("stats %+v", s)
	}
	all := Stats{}
	all.Add(0, true)
	if all.HasMinMax {
		t.Fatalf("all-null group must have no min/max: %+v", all)
	}
}

func TestMayMatchBoundaries(t *testing.T) {
	s := statsOf(10, 20) // min=10 max=20
	cases := []struct {
		p    Pred
		want bool
	}{
		{Equal(10), true}, {Equal(20), true}, {Equal(15), true},
		{Equal(9), false}, {Equal(21), false},
		{LessThan(10), false}, {LessThan(11), true},
		{LessEqual(10), true}, {LessEqual(9), false},
		{GreaterThan(20), false}, {GreaterThan(19), true},
		{GreaterEqual(20), true}, {GreaterEqual(21), false},
		{InSet(1, 2), false}, {InSet(1, 10), true},
		{Null(), false}, {NotNull(), true},
	}
	for _, c := range cases {
		if got := s.MayMatch(c.p); got != c.want {
			t.Fatalf("MayMatch(%+v)=%v, want %v", c.p, got, c.want)
		}
	}
}

func TestMayMatchAllNullGroup(t *testing.T) {
	var s Stats
	s.Add(0, true)
	s.Add(0, true)
	if s.MayMatch(Equal(0)) {
		t.Fatal("all-null group must not match a numeric predicate")
	}
	if !s.MayMatch(Null()) {
		t.Fatal("all-null group must match IS NULL")
	}
	if s.MayMatch(NotNull()) {
		t.Fatal("all-null group must not match IS NOT NULL")
	}
}

func TestMatchNullSemantics(t *testing.T) {
	numPreds := []Pred{Equal(0), LessThan(0), LessEqual(0),
		GreaterThan(0), GreaterEqual(0), InSet(0, 1), NotNull()}
	for _, p := range numPreds {
		if Match(0, true, p) {
			t.Fatalf("null matched %+v", p)
		}
	}
	if !Match(0, true, Null()) {
		t.Fatal("null must match IS NULL")
	}
	// A real zero is distinct from null.
	if !Match(0, false, Equal(0)) || !Match(0, false, NotNull()) {
		t.Fatal("zero must satisfy = 0 and IS NOT NULL")
	}
	if Match(0, false, Null()) {
		t.Fatal("zero must not satisfy IS NULL")
	}
}
