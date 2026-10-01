package ontology

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

func predicateString(predicate Predicate) string {
	switch predicate.Kind {
	case KindCmp:
		return fmt.Sprintf("Cmp(%s %s %d)", predicate.Column, predicate.Op, predicate.Value)
	case KindEq:
		return fmt.Sprintf("Eq(%s,%s)", predicate.Column, predicate.Column2)
	case KindNe:
		return fmt.Sprintf("Ne(%s,%s)", predicate.Column, predicate.Column2)
	default:
		return fmt.Sprintf("Predicate(kind=%d)", predicate.Kind)
	}
}

func requireNoError(t *testing.T, err error, input string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: unexpected error: %v", input, err)
	}
}

func requireErrorIs(t *testing.T, err error, target error, input string) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("%s: error = %v, want %v", input, err, target)
	}
}

func TestCascadeExcludedPinsAtFive(t *testing.T) {
	engine := NewEngine()
	inputs := []Predicate{Cmp("x", OpGe, 3), Cmp("x", OpLe, 5), Cmp("x", OpNe, 3), Cmp("x", OpNe, 4)}
	for _, predicate := range inputs {
		requireNoError(t, engine.Add(predicate), predicateString(predicate))
	}

	lo, hi, err := engine.Range("x")
	requireNoError(t, err, "Range(x)")
	if lo != 5 || hi != 5 {
		t.Fatalf("Range(x) = [%d,%d], want [5,5]; rule: excluded endpoints 3 and 4 advance lo", lo, hi)
	}
	value, pinned, err := engine.Pinned("x")
	requireNoError(t, err, "Pinned(x)")
	if !pinned || value != 5 {
		t.Fatalf("Pinned(x) = (%d,%v), want (5,true); rule: lo==hi", value, pinned)
	}
	excluded, err := engine.Excluded("x")
	requireNoError(t, err, "Excluded(x)")
	if len(excluded) != 0 {
		t.Fatalf("Excluded(x) = %v, want []; rule: values outside [lo,hi] are discarded", excluded)
	}
	requireErrorIs(t, engine.Add(Cmp("x", OpNe, 5)), ErrContradiction, "x != 5")
	lo, hi, err = engine.Range("x")
	requireNoError(t, err, "Range(x) after rejected predicate")
	if lo != 5 || hi != 5 {
		t.Fatalf("Range(x) after rejection = [%d,%d], want [5,5]", lo, hi)
	}
	t.Logf("inputs=%v; output=Range[%d,%d], Pinned=(%d,%v), Excluded=%v; basis=cascade endpoint exclusion",
		inputs, lo, hi, value, pinned, excluded)
}

func TestStrictInequalityInt64Overflow(t *testing.T) {
	for _, tc := range []struct {
		name      string
		predicate Predicate
	}{
		{"less than minimum", Cmp("x", OpLt, minInt64)},
		{"greater than maximum", Cmp("x", OpGt, maxInt64)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			engine := NewEngine()
			err := engine.Add(tc.predicate)
			requireErrorIs(t, err, ErrContradiction, predicateString(tc.predicate))
			if _, _, err := engine.Range("x"); !errors.Is(err, ErrColumnNotFound) {
				t.Fatalf("rejected predicate registered x: err=%v", err)
			}
			t.Logf("input=%s; output=%v; basis=c-1 or c+1 escapes int64", predicateString(tc.predicate), err)
		})
	}
}

func TestEqMergesIntersectionsAndExclusions(t *testing.T) {
	engine := NewEngine()
	requireNoError(t, engine.Add(Cmp("a", OpGe, 1)), "a >= 1")
	requireNoError(t, engine.Add(Cmp("a", OpLe, 5)), "a <= 5")
	requireNoError(t, engine.Add(Cmp("a", OpNe, 2)), "a != 2")
	requireNoError(t, engine.Add(Cmp("b", OpGe, 3)), "b >= 3")
	requireNoError(t, engine.Add(Cmp("b", OpLe, 7)), "b <= 7")
	requireNoError(t, engine.Add(Cmp("b", OpNe, 4)), "b != 4")
	requireNoError(t, engine.Add(Eq("a", "b")), "Eq(a,b)")

	same, err := engine.Same("a", "b")
	requireNoError(t, err, "Same(a,b)")
	if !same {
		t.Fatalf("Same(a,b)=false, want true after Eq")
	}
	lo, hi, err := engine.Range("a")
	requireNoError(t, err, "Range(a)")
	if lo != 3 || hi != 5 {
		t.Fatalf("Range(a)=[%d,%d], want [3,5]; basis=intersection", lo, hi)
	}
	excluded, err := engine.Excluded("a")
	requireNoError(t, err, "Excluded(a)")
	assertInt64s(t, excluded, []int64{4}, "union of exclusions; 2 is outside intersection and discarded")
	t.Logf("input=Eq(a,b) after independent constraints; output=[%d,%d], excluded=%v; basis=intersect then normalize", lo, hi, excluded)
}

func TestNePinnedSideExcludesOther(t *testing.T) {
	engine := NewEngine()
	requireNoError(t, engine.Add(Cmp("a", OpEq, 5)), "a = 5")
	requireNoError(t, engine.Add(Cmp("b", OpGe, 4)), "b >= 4")
	requireNoError(t, engine.Add(Cmp("b", OpLe, 6)), "b <= 6")
	requireNoError(t, engine.Add(Ne("a", "b")), "Ne(a,b)")

	lo, hi, err := engine.Range("b")
	requireNoError(t, err, "Range(b)")
	if lo != 4 || hi != 6 {
		t.Fatalf("Range(b)=[%d,%d], want [4,6]", lo, hi)
	}
	excluded, err := engine.Excluded("b")
	requireNoError(t, err, "Excluded(b)")
	assertInt64s(t, excluded, []int64{5}, "pinned side forces exclusion on the other class")
	same, err := engine.Same("a", "b")
	requireNoError(t, err, "Same(a,b)")
	if same {
		t.Fatalf("Same(a,b)=true after Ne")
	}
	implied, err := engine.Implies(Cmp("b", OpNe, 5))
	requireNoError(t, err, "Implies(b != 5)")
	if !implied {
		t.Fatalf("Implies(b != 5)=false, want true")
	}
	t.Logf("inputs=a=5,4<=b<=6,Ne(a,b); output=b excluded=%v; basis=one pinned Ne side", excluded)
}

func TestNeRejectsEqualPinnedValuesAndSelf(t *testing.T) {
	for _, tc := range []struct {
		name  string
		build func(*Engine)
		add   Predicate
	}{
		{
			name: "two pinned equal classes",
			build: func(engine *Engine) {
				requireNoError(t, engine.Add(Cmp("a", OpEq, 3)), "a=3")
				requireNoError(t, engine.Add(Cmp("b", OpEq, 3)), "b=3")
			},
			add: Ne("a", "b"),
		},
		{
			name:  "same column",
			build: func(engine *Engine) { requireNoError(t, engine.Add(Cmp("a", OpGe, 0)), "a>=0") },
			add:   Ne("a", "a"),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			engine := NewEngine()
			tc.build(engine)
			err := engine.Add(tc.add)
			requireErrorIs(t, err, ErrContradiction, predicateString(tc.add))
			t.Logf("input=%s; output=%v; basis=Ne endpoints are same or both pinned to one value", predicateString(tc.add), err)
		})
	}
}

func TestInvalidBeforeContradictionAndRejectedRegistration(t *testing.T) {
	engine := NewEngine()
	requireNoError(t, engine.Add(Cmp("x", OpEq, 5)), "x=5")

	err := engine.Add(Cmp("y", CmpOp("bogus"), minInt64))
	requireErrorIs(t, err, ErrInvalidPredicate, "invalid op with a new column")
	if _, _, err := engine.Range("y"); !errors.Is(err, ErrColumnNotFound) {
		t.Fatalf("invalid predicate registered y: %v", err)
	}

	err = engine.Add(Cmp("x", CmpOp("bogus"), 0))
	requireErrorIs(t, err, ErrInvalidPredicate, "invalid op before contradiction")
	requireErrorIs(t, engine.Add(Cmp("x", OpLt, minInt64)), ErrContradiction, "x < MinInt64")

	_, err = engine.Implies(Ne("x", "z"))
	requireErrorIs(t, err, ErrInvalidPredicate, "Implies(Ne(...)) before unknown column check")
	_, err = engine.Implies(Cmp("z", OpEq, 0))
	requireErrorIs(t, err, ErrColumnNotFound, "Implies unknown column shares Range error")
	_, _, err = engine.Range("z")
	requireErrorIs(t, err, ErrColumnNotFound, "Range unknown column")
	t.Logf("inputs=invalid insertion, contradictory insertion, Implies(Ne); output=errors invalid/contradiction/unknown; basis=required precedence")
}

func TestImpliesComparisonsAndEquality(t *testing.T) {
	engine := NewEngine()
	requireNoError(t, engine.Add(Cmp("a", OpGe, 2)), "a>=2")
	requireNoError(t, engine.Add(Cmp("a", OpLe, 4)), "a<=4")
	requireNoError(t, engine.Add(Cmp("a", OpNe, 3)), "a!=3")
	requireNoError(t, engine.Add(Cmp("b", OpEq, 4)), "b=4")

	for _, tc := range []struct {
		predicate Predicate
		want      bool
	}{
		{Cmp("a", OpEq, 3), false},
		{Cmp("b", OpEq, 4), true},
		{Cmp("a", OpNe, 1), true},
		{Cmp("a", OpNe, 3), true},
		{Cmp("a", OpNe, 4), false},
		{Cmp("a", OpLt, 5), true},
		{Cmp("a", OpLe, 4), true},
		{Cmp("a", OpGt, 1), true},
		{Cmp("a", OpGe, 2), true},
		{Eq("a", "b"), false},
	} {
		got, err := engine.Implies(tc.predicate)
		requireNoError(t, err, predicateString(tc.predicate))
		if got != tc.want {
			t.Fatalf("Implies(%s)=%v, want %v; basis=specified range/exclusion/pinned equality rule",
				predicateString(tc.predicate), got, tc.want)
		}
		t.Logf("input=Implies(%s); output=%v", predicateString(tc.predicate), got)
	}
}

func TestAcceptedSetOrderIndependentAndReplayDeterministic(t *testing.T) {
	predicates := []Predicate{
		Cmp("a", OpGe, 1),
		Cmp("a", OpLe, 5),
		Cmp("a", OpNe, 2),
		Cmp("b", OpGe, 3),
		Cmp("b", OpLe, 7),
		Eq("a", "b"),
		Ne("b", "c"),
		Cmp("c", OpEq, 5),
	}
	permutations := permutePredicates(predicates)
	want, err := snapshotAfter(predicates)
	requireNoError(t, err, "reference order")
	for index, order := range permutations {
		got, err := snapshotAfter(order)
		requireNoError(t, err, fmt.Sprintf("permutation %d", index))
		if got != want {
			t.Fatalf("permutation %d snapshot=%s, want %s", index, got, want)
		}
	}

	sequence := []Predicate{
		Cmp("a", OpGe, 1),
		Cmp("b", OpEq, 1),
		Ne("a", "b"),
		Cmp("a", OpNe, 1),
		Cmp("a", OpEq, 1),
		Cmp("c", CmpOp("?"), 0),
		Ne("a", "a"),
	}
	firstErrors := replayErrors(sequence)
	secondErrors := replayErrors(sequence)
	if fmt.Sprint(firstErrors) != fmt.Sprint(secondErrors) {
		t.Fatalf("non-deterministic replay errors: %v vs %v", firstErrors, secondErrors)
	}
	t.Logf("permutations=%d; snapshot=%s; replay errors=%v; basis=canonical state independent of accepted insertion order",
		len(permutations), want, firstErrors)
}

func TestConcurrentAddsQueriesAndRollback(t *testing.T) {
	engine := NewEngine()
	predicates := []Predicate{
		Cmp("x", OpGe, -3),
		Cmp("x", OpLe, 3),
		Cmp("y", OpEq, 0),
		Ne("x", "y"),
		Eq("z", "x"),
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			for _, predicate := range predicates {
				_ = engine.Add(predicate)
			}
		}(i)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_, _, _ = engine.Range("x")
				_, _, _ = engine.Pinned("y")
				_, _ = engine.Excluded("z")
				_, _ = engine.Same("x", "z")
				_, _ = engine.Implies(Cmp("x", OpNe, 0))
			}
		}()
	}
	wg.Wait()

	lo, hi, err := engine.Range("x")
	requireNoError(t, err, "Range(x)")
	if lo != -3 || hi != 3 {
		t.Fatalf("Range(x)=[%d,%d], want [-3,3] after concurrent duplicate inserts", lo, hi)
	}
	same, err := engine.Same("x", "z")
	requireNoError(t, err, "Same(x,z)")
	if !same {
		t.Fatalf("Same(x,z)=false, want true")
	}
	before, err := snapshotEngine(engine)
	requireNoError(t, err, "snapshot before rejected merge")
	mergeErr := engine.Add(Eq("x", "y"))
	requireErrorIs(t, mergeErr, ErrContradiction, "merging Ne classes pinned around same value")
	after, err := snapshotEngine(engine)
	requireNoError(t, err, "snapshot after rejected merge")
	if before != after {
		t.Fatalf("rejected Eq changed state: before=%s after=%s", before, after)
	}
	t.Logf("concurrent duplicate inserts; output=%s; rejected merge output=%v and state unchanged", after, mergeErr)
}

type engineSnapshot string

func snapshotAfter(predicates []Predicate) (engineSnapshot, error) {
	engine := NewEngine()
	for _, predicate := range predicates {
		if err := engine.Add(predicate); err != nil {
			return "", err
		}
	}
	return snapshotEngine(engine)
}

func snapshotEngine(engine *Engine) (engineSnapshot, error) {
	columns := []string{"a", "b", "c", "x", "y", "z"}
	result := ""
	for _, column := range columns {
		lo, hi, rangeErr := engine.Range(column)
		if errors.Is(rangeErr, ErrColumnNotFound) {
			continue
		}
		if rangeErr != nil {
			return "", rangeErr
		}
		value, pinned, err := engine.Pinned(column)
		if err != nil {
			return "", err
		}
		excluded, err := engine.Excluded(column)
		if err != nil {
			return "", err
		}
		result += fmt.Sprintf("%s[%d,%d]pinned(%d,%v)ex%v;", column, lo, hi, value, pinned, excluded)
	}
	for _, pair := range [][2]string{{"a", "b"}, {"b", "c"}, {"x", "y"}, {"x", "z"}} {
		same, err := engine.Same(pair[0], pair[1])
		if err == nil {
			result += fmt.Sprintf("same(%s,%s)=%v;", pair[0], pair[1], same)
		}
	}
	return engineSnapshot(result), nil
}

func permutePredicates(predicates []Predicate) [][]Predicate {
	var result [][]Predicate
	var walk func(int)
	order := append([]Predicate(nil), predicates...)
	walk = func(start int) {
		if start == len(order) {
			result = append(result, append([]Predicate(nil), order...))
			return
		}
		for i := start; i < len(order); i++ {
			order[start], order[i] = order[i], order[start]
			walk(start + 1)
			order[start], order[i] = order[i], order[start]
		}
	}
	walk(0)
	return result
}

func replayErrors(predicates []Predicate) []error {
	engine := NewEngine()
	errors := make([]error, len(predicates))
	for i, predicate := range predicates {
		errors[i] = engine.Add(predicate)
	}
	return errors
}

func assertInt64s(t *testing.T, got, want []int64, basis string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v; basis=%s", got, want, basis)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v; basis=%s", got, want, basis)
		}
	}
}
