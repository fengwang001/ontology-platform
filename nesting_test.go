package ontology

import (
	"errors"
	"reflect"
	"testing"
)

func mustRegister(t *testing.T, e *Engine, a *ActionType) {
	t.Helper()
	if err := e.Register(a); err != nil {
		t.Fatal(err)
	}
}

func TestInnerFailureRollsBackToSavepoint(t *testing.T) {
	e := NewEngine()
	mustRegister(t, e, &ActionType{
		Name: "Inner",
		Run: func(tx *Tx, _ map[string]any) error {
			if err := tx.CreateObject("inner-1", "T", nil); err != nil {
				return err
			}
			return errors.New("inner boom")
		},
	})
	mustRegister(t, e, &ActionType{
		Name: "Outer",
		Run: func(tx *Tx, _ map[string]any) error {
			if err := tx.CreateObject("outer-1", "T", nil); err != nil {
				return err
			}
			if err := tx.ExecuteAction("Inner", nil); err == nil {
				t.Error("inner must fail")
			}
			// Outer chooses to continue after the inner failure.
			return tx.CreateObject("outer-2", "T", nil)
		},
	})
	rec, err := e.Execute("Outer", nil)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Seq != 1 {
		t.Fatalf("want seq 1, got %d", rec.Seq)
	}
	for _, id := range []string{"outer-1", "outer-2"} {
		if _, ok := e.Store().GetObject(id); !ok {
			t.Fatalf("%s must survive", id)
		}
	}
	if _, ok := e.Store().GetObject("inner-1"); ok {
		t.Fatal("inner-1 must be rolled back to the savepoint")
	}
}

func TestOuterFailureRollsBackSuccessfulInner(t *testing.T) {
	e := NewEngine()
	mustRegister(t, e, &ActionType{
		Name: "Inner",
		Run: func(tx *Tx, _ map[string]any) error {
			return tx.CreateObject("inner-1", "T", nil)
		},
	})
	mustRegister(t, e, &ActionType{
		Name: "Outer",
		Run: func(tx *Tx, _ map[string]any) error {
			if err := tx.ExecuteAction("Inner", nil); err != nil {
				return err
			}
			return errors.New("outer boom")
		},
	})
	if _, err := e.Execute("Outer", nil); err == nil {
		t.Fatal("want failure")
	}
	if _, ok := e.Store().GetObject("inner-1"); ok {
		t.Fatal("inner changes must roll back with the outer failure")
	}
	if got := e.Store().ObjectCount(); got != 0 {
		t.Fatalf("store must be empty, got %d objects", got)
	}
}

func TestDirectRecursionRejected(t *testing.T) {
	e := NewEngine()
	mustRegister(t, e, &ActionType{
		Name: "Loop",
		Run: func(tx *Tx, _ map[string]any) error {
			return tx.ExecuteAction("Loop", nil)
		},
	})
	_, err := e.Execute("Loop", nil)
	var re *RecursionError
	if !errors.As(err, &re) {
		t.Fatalf("want *RecursionError, got %T: %v", err, err)
	}
	if !reflect.DeepEqual(re.Chain, []string{"Loop", "Loop"}) {
		t.Fatalf("bad chain: %v", re.Chain)
	}
}

func TestIndirectRecursionRejected(t *testing.T) {
	e := NewEngine()
	mustRegister(t, e, &ActionType{
		Name: "A",
		Run: func(tx *Tx, _ map[string]any) error { return tx.ExecuteAction("B", nil) },
	})
	mustRegister(t, e, &ActionType{
		Name: "B",
		Run: func(tx *Tx, _ map[string]any) error { return tx.ExecuteAction("A", nil) },
	})
	_, err := e.Execute("A", nil)
	var re *RecursionError
	if !errors.As(err, &re) {
		t.Fatalf("want *RecursionError, got %T: %v", err, err)
	}
	if !reflect.DeepEqual(re.Chain, []string{"A", "B", "A"}) {
		t.Fatalf("bad chain: %v", re.Chain)
	}
}

func TestDepthLimitRejected(t *testing.T) {
	e := NewEngine()
	e.MaxDepth = 3
	names := []string{"n0", "n1", "n2", "n3"}
	for i, n := range names {
		n := n
		next := ""
		if i+1 < len(names) {
			next = names[i+1]
		}
		mustRegister(t, e, &ActionType{
			Name: n,
			Run: func(tx *Tx, _ map[string]any) error {
				if next == "" {
					return nil
				}
				return tx.ExecuteAction(next, nil)
			},
		})
	}
	_, err := e.Execute("n0", nil)
	var de *DepthError
	if !errors.As(err, &de) {
		t.Fatalf("want *DepthError, got %T: %v", err, err)
	}
	if de.Limit != 3 {
		t.Fatalf("bad limit: %+v", de)
	}
	if !reflect.DeepEqual(de.Chain, []string{"n0", "n1", "n2", "n3"}) {
		t.Fatalf("bad chain: %v", de.Chain)
	}
}
