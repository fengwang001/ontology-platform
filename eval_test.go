package ontology

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

// typeErrLeaf compares string property "s" with an int64 literal,
// which is a decidable TypeError when actually evaluated.
var typeErrLeaf = &Compare{Property: "s", Op: Eq, Literal: int64(1)}

func TestSkippedTypeErrorIsNotReported(t *testing.T) {
	ev := NewEvaluator(64)
	props := map[string]any{"s": "str", "f": int64(1), "t": int64(1)}

	// False short-circuits the right subtree: its type error is never
	// visited, so no error is returned.
	got, err := ev.Eval(props, &And{Children: []Predicate{leaf("f"), typeErrLeaf}})
	if err != nil {
		t.Fatalf("skipped error reported: %v", err)
	}
	if got.Value != False || got.Leaves != 1 {
		t.Fatalf("And(false, err) = (%s,%d), want false/1", got.Value, got.Leaves)
	}

	// True short-circuits the right subtree in an Or.
	got, err = ev.Eval(props, &Or{Children: []Predicate{leaf("t"), typeErrLeaf}})
	if err != nil || got.Value != True || got.Leaves != 1 {
		t.Fatalf("Or(true, err) = (%s,%d,%v), want true/1/nil", got.Value, got.Leaves, err)
	}
}

func TestVisitedTypeErrorAbortsTree(t *testing.T) {
	ev := NewEvaluator(64)
	props := map[string]any{"s": "str", "f": int64(1), "t": int64(1), "u2": int64(0)}

	visited := []Predicate{
		&And{Children: []Predicate{leaf("t"), typeErrLeaf}},
		&And{Children: []Predicate{leaf("u"), typeErrLeaf}}, // Unknown does not short-circuit
		&Or{Children: []Predicate{leaf("f"), typeErrLeaf}},
		&Or{Children: []Predicate{leaf("u"), typeErrLeaf}},
		&And{Children: []Predicate{typeErrLeaf, leaf("f")}}, // aborts even though False follows
	}
	for idx, tree := range visited {
		_, err := ev.Eval(props, tree)
		var te *TypeError
		if !errors.As(err, &te) {
			t.Fatalf("case %d: want TypeError, got %v", idx, err)
		}
	}
}

func TestDepthLimitIsDecidableError(t *testing.T) {
	ev := NewEvaluator(3)

	// Not(Not(leaf)) has depth 3 and must pass.
	shallow := &Not{Child: &Not{Child: leaf("t")}}
	if _, err := ev.Eval(triFixture, shallow); err != nil {
		t.Fatalf("depth-3 tree rejected: %v", err)
	}

	deep := &Not{Child: &Not{Child: &Not{Child: leaf("t")}}}
	_, err := ev.Eval(triFixture, deep)
	var de *DepthError
	if !errors.As(err, &de) || de.Limit != 3 {
		t.Fatalf("want *DepthError{Limit:3}, got %v", err)
	}
}

func TestEvalIsPureAndDeterministic(t *testing.T) {
	ev := NewEvaluator(64)
	tree := &And{Children: []Predicate{
		leaf("u"),
		&Or{Children: []Predicate{leaf("f"), leaf("t")}},
	}}
	props := map[string]any{"t": int64(1), "f": int64(1)}

	first, err := ev.Eval(props, tree)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		got, err := ev.Eval(props, tree)
		if err != nil || got != first {
			t.Fatalf("repeat %d = %+v (%v), want %+v", i, got, err, first)
		}
		if len(props) != 2 {
			t.Fatalf("props mutated: %v", props)
		}
	}
}

func TestConcurrentCountersDoNotBleed(t *testing.T) {
	ev := NewEvaluator(64)
	props := map[string]any{"t": int64(1), "f": int64(1), "s": "str"}

	cases := []struct {
		tree   Predicate
		value  Tri
		leaves int
	}{
		{&And{Children: []Predicate{leaf("f"), typeErrLeaf}}, False, 1},
		{&Or{Children: []Predicate{leaf("t"), typeErrLeaf}}, True, 1},
		{&And{Children: []Predicate{leaf("u"), leaf("f")}}, False, 2},
		{&Or{Children: []Predicate{leaf("u"), leaf("t")}}, True, 2},
	}

	const goroutines = 64
	var wg sync.WaitGroup
	errs := make(chan error, goroutines*len(cases))
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for idx, tc := range cases {
				got, err := ev.Eval(props, tc.tree)
				if err != nil {
					errs <- fmt.Errorf("case %d: %w", idx, err)
					return
				}
				if got.Value != tc.value || got.Leaves != tc.leaves {
					errs <- fmt.Errorf("case %d: got %+v", idx, got)
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}
