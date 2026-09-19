package ontology
import "testing"

func TestMissingPropertyIsUnknownNotFalse(t *testing.T) {
	ev := NewEvaluator(64)

	got, err := ev.Eval(map[string]any{}, &Compare{Property: "missing", Op: Eq, Literal: int64(1)})
	if err != nil {
		t.Fatalf("missing property returned error: %v", err)
	}
	if got.Value != Unknown {
		t.Fatalf("missing property = %s, want unknown", got.Value)
	}
	if got.Leaves != 1 {
		t.Fatalf("missing property leaves = %d, want 1", got.Leaves)
	}

	got, err = ev.Eval(map[string]any{}, &Not{Child: &Compare{Property: "missing", Op: Eq, Literal: int64(1)}})
	if err != nil || got.Value != Unknown {
		t.Fatalf("Not(missing) = (%s,%v), want unknown", got.Value, err)
	}
}

func TestIsNullTurnsUnknownIntoDefinite(t *testing.T) {
	ev := NewEvaluator(64)

	got, err := ev.Eval(map[string]any{}, &IsNull{Property: "missing"})
	if err != nil || got.Value != True {
		t.Fatalf("IsNull(missing) = (%s,%v), want true", got.Value, err)
	}

	got, err = ev.Eval(map[string]any{"present": int64(3)}, &IsNull{Property: "present"})
	if err != nil || got.Value != False {
		t.Fatalf("IsNull(present) = (%s,%v), want false", got.Value, err)
	}
}

func TestIsNullNeverUnknown(t *testing.T) {
	ev := NewEvaluator(64)

	// IsNull must collapse the unknown coming from its sibling into a
	// definite conjunction: False here, never Unknown.
	tree := &And{Children: []Predicate{
		&IsNull{Property: "present"},
		&Compare{Property: "missing", Op: Eq, Literal: int64(1)},
	}}
	got, err := ev.Eval(map[string]any{"present": 1}, tree)
	if err != nil || got.Value != False {
		t.Fatalf("And(IsNull(present), unknown) = (%s,%v), want false", got.Value, err)
	}
	if got.Leaves != 1 {
		t.Fatalf("leaves = %d, want 1 (IsNull is not a comparison leaf)", got.Leaves)
	}
}
