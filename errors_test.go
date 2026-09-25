package ontology

import (
	"errors"
	"testing"
)

func TestTypeErrorInSkippedSubtreeNotReported(t *testing.T) {
	ev := NewEvaluator(8)
	// And(False, <type error>): the right subtree is short-circuited,
	// so its type error must not surface.
	p := AndP(Eq("a", int64(2)), Eq("name", int64(42)))
	v, err, n := evalCase(t, ev, p, map[string]any{"a": int64(1), "name": "alice"})
	if err != nil || v != False || n != 1 {
		t.Fatalf("got (%s, %v, %d), want (False, nil, 1)", v, err, n)
	}
}

func TestTypeErrorInEvaluatedSubtreeIsReported(t *testing.T) {
	ev := NewEvaluator(8)
	// And(True, <type error>): the error must abort the whole
	// evaluation and must not be masked by any And result.
	p := AndP(Eq("a", int64(1)), Eq("name", int64(42)))
	v, err, n := evalCase(t, ev, p, map[string]any{"a": int64(1), "name": "alice"})
	if !IsTypeError(err) || v != False || n != 2 {
		t.Fatalf("got (%s, %v, %d), want (False, TypeError, 2)", v, err, n)
	}
}

func TestTypeErrorBeatsEarlierFalseInOr(t *testing.T) {
	ev := NewEvaluator(8)
	// Or(False, <type error>): False does not short-circuit Or, so the
	// error is evaluated and must be returned.
	p := OrP(Eq("a", int64(2)), Lt("flag", true))
	v, err, _ := evalCase(t, ev, p, map[string]any{"a": int64(1), "flag": true})
	if !IsTypeError(err) || v != False {
		t.Fatalf("got (%s, %v), want (False, TypeError)", v, err)
	}
}

func TestTypeErrorInsideNotPropagates(t *testing.T) {
	ev := NewEvaluator(8)
	p := NotP(Eq("name", int64(42)))
	v, err, _ := evalCase(t, ev, p, map[string]any{"name": "alice"})
	if !IsTypeError(err) || v != False {
		t.Fatalf("got (%s, %v), want (False, TypeError)", v, err)
	}
}

func TestDepthLimitExceeded(t *testing.T) {
	ev := NewEvaluator(3)
	// Build a chain of Not nodes of depth 5 around a leaf.
	p := Eq("a", int64(1))
	for i := 0; i < 4; i++ {
		p = NotP(p)
	}
	v, err, _ := evalCase(t, ev, p, map[string]any{"a": int64(1)})
	if !IsDepthError(err) || v != False {
		t.Fatalf("got (%s, %v), want (False, DepthError)", v, err)
	}
	var de *DepthError
	if !errors.As(err, &de) || de.MaxDepth != 3 {
		t.Fatalf("DepthError fields wrong: %v", err)
	}
}

func TestDepthLimitExactBoundary(t *testing.T) {
	ev := NewEvaluator(3)
	// Depth exactly 3: And(Not(leaf)) is allowed.
	p := AndP(NotP(Eq("a", int64(1))))
	v, err, n := evalCase(t, ev, p, map[string]any{"a": int64(1)})
	if err != nil || v != False || n != 1 {
		t.Fatalf("got (%s, %v, %d), want (False, nil, 1)", v, err, n)
	}
}

func TestDepthErrorInsideSkippedSubtreeNotReported(t *testing.T) {
	ev := NewEvaluator(2)
	// And(False, Not(Not(leaf))): the deep right subtree is skipped.
	p := AndP(Eq("a", int64(2)), NotP(NotP(Eq("b", int64(1)))))
	v, err, n := evalCase(t, ev, p, map[string]any{"a": int64(1), "b": int64(1)})
	if err != nil || v != False || n != 1 {
		t.Fatalf("got (%s, %v, %d), want (False, nil, 1)", v, err, n)
	}
}
