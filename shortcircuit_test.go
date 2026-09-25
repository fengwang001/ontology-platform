package ontology

import "testing"

// evalCase runs one evaluation and returns value, error and leaf count.
func evalCase(t *testing.T, ev *Evaluator, p *Predicate, props map[string]any) (Trilean, error, int) {
	t.Helper()
	r := ev.NewResult()
	v, err := ev.Eval(p, props, r)
	return v, err, r.LeafCount()
}

func TestAndFalseShortCircuits(t *testing.T) {
	ev := NewEvaluator(8)
	p := AndP(Eq("a", int64(2)), Eq("b", int64(1)), Eq("c", int64(1)))
	v, err, n := evalCase(t, ev, p, map[string]any{"a": int64(1), "b": int64(1), "c": int64(1)})
	if err != nil || v != False || n != 1 {
		t.Fatalf("got (%s, %v, %d), want (False, nil, 1)", v, err, n)
	}
}

func TestAndFalseShortCircuitsAtSecondChild(t *testing.T) {
	ev := NewEvaluator(8)
	p := AndP(Eq("a", int64(1)), Eq("b", int64(2)), Eq("c", int64(1)))
	v, err, n := evalCase(t, ev, p, map[string]any{"a": int64(1), "b": int64(1), "c": int64(1)})
	if err != nil || v != False || n != 2 {
		t.Fatalf("got (%s, %v, %d), want (False, nil, 2)", v, err, n)
	}
}

func TestAndUnknownDoesNotShortCircuit(t *testing.T) {
	ev := NewEvaluator(8)
	// And(Unknown, False): right side must be evaluated to get False.
	p := AndP(Eq("missing", int64(1)), Eq("b", int64(2)))
	v, err, n := evalCase(t, ev, p, map[string]any{"b": int64(1)})
	if err != nil || v != False || n != 2 {
		t.Fatalf("got (%s, %v, %d), want (False, nil, 2)", v, err, n)
	}
}

func TestAndUnknownThenTrueIsUnknown(t *testing.T) {
	ev := NewEvaluator(8)
	p := AndP(Eq("missing", int64(1)), Eq("b", int64(1)))
	v, err, n := evalCase(t, ev, p, map[string]any{"b": int64(1)})
	if err != nil || v != Unknown || n != 2 {
		t.Fatalf("got (%s, %v, %d), want (Unknown, nil, 2)", v, err, n)
	}
}

func TestOrTrueShortCircuits(t *testing.T) {
	ev := NewEvaluator(8)
	p := OrP(Eq("a", int64(1)), Eq("b", int64(1)), Eq("c", int64(1)))
	v, err, n := evalCase(t, ev, p, map[string]any{"a": int64(1), "b": int64(1), "c": int64(1)})
	if err != nil || v != True || n != 1 {
		t.Fatalf("got (%s, %v, %d), want (True, nil, 1)", v, err, n)
	}
}

func TestOrUnknownDoesNotShortCircuit(t *testing.T) {
	ev := NewEvaluator(8)
	// Or(Unknown, True): right side must be evaluated to get True.
	p := OrP(Eq("missing", int64(1)), Eq("b", int64(1)))
	v, err, n := evalCase(t, ev, p, map[string]any{"b": int64(1)})
	if err != nil || v != True || n != 2 {
		t.Fatalf("got (%s, %v, %d), want (True, nil, 2)", v, err, n)
	}
}

func TestOrUnknownThenFalseIsUnknown(t *testing.T) {
	ev := NewEvaluator(8)
	p := OrP(Eq("missing", int64(1)), Eq("b", int64(2)))
	v, err, n := evalCase(t, ev, p, map[string]any{"b": int64(1)})
	if err != nil || v != Unknown || n != 2 {
		t.Fatalf("got (%s, %v, %d), want (Unknown, nil, 2)", v, err, n)
	}
}

func TestShortCircuitSkipsWholeSubtree(t *testing.T) {
	ev := NewEvaluator(16)
	// And(False, And(leaf, leaf)): the nested subtree is never touched.
	p := AndP(Eq("a", int64(2)), AndP(Eq("b", int64(1)), Eq("c", int64(1))))
	v, err, n := evalCase(t, ev, p, map[string]any{"a": int64(1), "b": int64(1), "c": int64(1)})
	if err != nil || v != False || n != 1 {
		t.Fatalf("got (%s, %v, %d), want (False, nil, 1)", v, err, n)
	}
}

func TestNotEvaluatesChild(t *testing.T) {
	ev := NewEvaluator(8)
	p := NotP(Eq("a", int64(1)))
	v, err, n := evalCase(t, ev, p, map[string]any{"a": int64(1)})
	if err != nil || v != False || n != 1 {
		t.Fatalf("got (%s, %v, %d), want (False, nil, 1)", v, err, n)
	}
}

func TestIsNullCountsNoLeaves(t *testing.T) {
	ev := NewEvaluator(8)
	p := AndP(IsNull("missing"), Eq("a", int64(1)))
	v, err, n := evalCase(t, ev, p, map[string]any{"a": int64(1)})
	if err != nil || v != True || n != 1 {
		t.Fatalf("got (%s, %v, %d), want (True, nil, 1)", v, err, n)
	}
}

func TestMissingAttrIsUnknownNotFalse(t *testing.T) {
	ev := NewEvaluator(8)
	for _, p := range []*Predicate{
		Eq("nope", int64(1)),
		Lt("nope", int64(1)),
		Gt("nope", "x"),
	} {
		v, err, n := evalCase(t, ev, p, map[string]any{})
		if err != nil || v != Unknown || n != 1 {
			t.Fatalf("%s: got (%s, %v, %d), want (Unknown, nil, 1)", p.Op, v, err, n)
		}
	}
}

func TestIsNullResolvesUnknown(t *testing.T) {
	ev := NewEvaluator(8)
	v, err, _ := evalCase(t, ev, IsNull("missing"), map[string]any{})
	if err != nil || v != True {
		t.Fatalf("IsNull on missing attr: got (%s, %v), want (True, nil)", v, err)
	}
	v, err, _ = evalCase(t, ev, IsNull("present"), map[string]any{"present": int64(1)})
	if err != nil || v != False {
		t.Fatalf("IsNull on present attr: got (%s, %v), want (False, nil)", v, err)
	}
	// IsNull itself never yields Unknown, even under Not.
	v, err, _ = evalCase(t, ev, NotP(IsNull("missing")), map[string]any{})
	if err != nil || v != False {
		t.Fatalf("Not(IsNull missing): got (%s, %v), want (False, nil)", v, err)
	}
}

func TestEvalIsPureAndRepeatable(t *testing.T) {
	ev := NewEvaluator(8)
	p := AndP(Eq("a", int64(1)), OrP(Eq("b", int64(2)), Eq("c", int64(3))))
	props := map[string]any{"a": int64(1), "b": int64(2), "c": int64(9)}
	for i := 0; i < 3; i++ {
		v, err, n := evalCase(t, ev, p, props)
		if err != nil || v != True || n != 2 {
			t.Fatalf("iteration %d: got (%s, %v, %d), want (True, nil, 2)", i, v, err, n)
		}
	}
	if len(props) != 3 || props["a"] != int64(1) || props["b"] != int64(2) || props["c"] != int64(9) {
		t.Fatalf("props mutated: %v", props)
	}
	want := AndP(Eq("a", int64(1)), OrP(Eq("b", int64(2)), Eq("c", int64(3))))
	if p.Kind != want.Kind || len(p.Children) != 2 || p.Children[0].Attr != "a" {
		t.Fatalf("predicate tree mutated: %+v", p)
	}
}
