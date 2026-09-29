package zone

import "testing"

func TestBuildAndNullSemantics(t *testing.T) {
	vals := []Val{NullVal(), IntVal(5), NullVal(), IntVal(-3), IntVal(5)}
	s := Build(vals)
	if s.Rows != 5 || s.NullCount != 2 || !s.HasMinMax || s.Min != -3 || s.Max != 5 {
		t.Fatalf("stat wrong: %+v", s)
	}
	allNull := Build([]Val{NullVal(), NullVal()})
	if allNull.HasMinMax || allNull.NullCount != 2 {
		t.Fatalf("all-null group must have no min/max: %+v", allNull)
	}

	// NULL matches only IS NULL; never any numeric term.
	nf := And(Gt(-1<<60), Le(1<<60), Eq(0), In([]int64{0, 1}), IsNotNull())
	if nf.Match(NullVal()) {
		t.Fatal("null must not match numeric terms or IS NOT NULL")
	}
	if !And(IsNull()).Match(NullVal()) {
		t.Fatal("null must match IS NULL")
	}
	if And(IsNull()).Match(IntVal(0)) {
		t.Fatal("int 0 must not match IS NULL")
	}
	// 0 and empty string are distinct from null and each other.
	if IntVal(0).Kind == NullVal().Kind || StrVal("").Kind == NullVal().Kind {
		t.Fatal("three states must be distinguishable")
	}
}

func TestPruningExhaustive(t *testing.T) {
	// Every non-empty multiset over a small domain defines a zone map; verify
	// CouldHit against brute-force "any member matches" for every predicate,
	// including boundary predicates at min and max.
	domain := []int64{-2, -1, 0, 1, 2}
	preds := []Predicate{
		Eq(-2), Eq(-1), Eq(0), Eq(1), Eq(2), Eq(3), Eq(-3),
		Lt(-2), Lt(-1), Lt(2), Lt(3), Le(-2), Le(2), Le(3),
		Gt(-3), Gt(-2), Gt(2), Gt(3), Ge(-2), Ge(2), Ge(3),
		In([]int64{-2, 2}), In([]int64{3, 4}), In([]int64{0}),
	}
	match := func(p Predicate, x int64) bool { return And(p).Match(IntVal(x)) }

	// Enumerate non-empty subsets of the domain as groups (min/max only matter).
	for mask := 1; mask < 1<<len(domain); mask++ {
		var members []Val
		var vs []int64
		for i, x := range domain {
			if mask&(1<<i) != 0 {
				members = append(members, IntVal(x))
				vs = append(vs, x)
			}
		}
		s := Build(members)
		for _, p := range preds {
			brute := false
			for _, x := range vs {
				if match(p, x) {
					brute = true
				}
			}
			got := p.CouldHit(s)
			// Safety: pruning must never say "no hit" when a hit exists.
			if brute && !got {
				t.Fatalf("FALSE EXCLUSION mask=%b pred=%+v stat=%+v", mask, p, s)
			}
		}
		// Boundary predicates must never exclude a present group.
		mn, mx := vs[0], vs[0]
		for _, x := range vs {
			if x < mn {
				mn = x
			}
			if x > mx {
				mx = x
			}
		}
		for _, p := range []Predicate{Eq(mn), Eq(mx), Ge(mn), Le(mx)} {
			if !p.CouldHit(s) {
				t.Fatalf("boundary predicate %+v excluded group %+v", p, s)
			}
		}
	}

	// All-null group: numeric predicates exclude, IS NULL keeps.
	s := Build([]Val{NullVal(), NullVal()})
	for _, p := range []Predicate{Eq(0), Lt(1), Ge(-1), In([]int64{0})} {
		if p.CouldHit(s) {
			t.Fatalf("numeric predicate must exclude all-null group: %+v", p)
		}
	}
	if !IsNull().CouldHit(s) || IsNotNull().CouldHit(s) {
		t.Fatal("null predicates wrong on all-null group")
	}
	// AND combination.
	f := And(Ge(0), Le(1))
	if f.CouldHit(Build([]Val{IntVal(-5), IntVal(-1)})) {
		t.Fatal("AND pruning failed to exclude")
	}
	if !f.CouldHit(Build([]Val{IntVal(0), IntVal(2)})) {
		t.Fatal("AND pruning falsely excluded overlapping group")
	}
}
