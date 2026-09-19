package ontology

import (
	"errors"
	"testing"
)

func TestHooksRunInDeclarationOrder(t *testing.T) {
	e := NewEngine()
	var order []int
	mk := func(i int) HookFunc {
		return func(*HookCtx) error { order = append(order, i); return nil }
	}
	err := e.Register(&ActionType{
		Name:  "H",
		Hooks: []HookFunc{mk(0), mk(1), mk(2)},
		Run:   func(*Tx, map[string]any) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Execute("H", nil); err != nil {
		t.Fatal(err)
	}
	if len(order) != 3 || order[0] != 0 || order[1] != 1 || order[2] != 2 {
		t.Fatalf("hooks out of order: %v", order)
	}
}

func TestHookRejectionReportsIndexAndReason(t *testing.T) {
	e := NewEngine()
	ran := []bool{false, false, false}
	mk := func(i int) HookFunc {
		return func(*HookCtx) error { ran[i] = true; return nil }
	}
	reject := func(*HookCtx) error { return errors.New("quota exceeded") }
	err := e.Register(&ActionType{
		Name:  "Guarded",
		Hooks: []HookFunc{mk(0), reject, mk(2)},
		Run: func(*Tx, map[string]any) error {
			t.Error("Run must not execute after hook rejection")
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.Execute("Guarded", nil)
	var hre *HookRejectedError
	if !errors.As(err, &hre) {
		t.Fatalf("want *HookRejectedError, got %T: %v", err, err)
	}
	if hre.Index != 1 || hre.Reason != "quota exceeded" || hre.Action != "Guarded" {
		t.Fatalf("bad rejection: %+v", hre)
	}
	if !ran[0] || ran[2] {
		t.Fatalf("hooks must stop at first rejection: %v", ran)
	}
	if got := e.Store().ObjectCount(); got != 0 {
		t.Fatalf("rejected action left state behind: %d objects", got)
	}
}

func TestHookSeesTransactionalChanges(t *testing.T) {
	e := NewEngine()
	seen := false
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(e.Register(&ActionType{
		Name: "Inner",
		Hooks: []HookFunc{func(h *HookCtx) error {
			_, seen = h.GetObject("outer-1")
			return nil
		}},
		Run: func(*Tx, map[string]any) error { return nil },
	}))
	must(e.Register(&ActionType{
		Name: "Outer",
		Run: func(tx *Tx, _ map[string]any) error {
			if err := tx.CreateObject("outer-1", "T", nil); err != nil {
				return err
			}
			return tx.ExecuteAction("Inner", nil)
		},
	}))
	if _, err := e.Execute("Outer", nil); err != nil {
		t.Fatal(err)
	}
	if !seen {
		t.Fatal("hook did not see the in-transaction object")
	}
}

func TestHookWriteViolationDetectedAndReverted(t *testing.T) {
	e := NewEngine()
	bad := func(h *HookCtx) error {
		return h.RawStore().PutObject(Object{ID: "smuggled", Type: "T"})
	}
	err := e.Register(&ActionType{
		Name:  "Sneaky",
		Hooks: []HookFunc{bad},
		Run: func(*Tx, map[string]any) error {
			t.Error("Run must not execute after hook violation")
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.Execute("Sneaky", nil)
	var hve *HookViolationError
	if !errors.As(err, &hve) {
		t.Fatalf("want *HookViolationError, got %T: %v", err, err)
	}
	if hve.Index != 0 || hve.Action != "Sneaky" {
		t.Fatalf("bad violation: %+v", hve)
	}
	if _, ok := e.Store().GetObject("smuggled"); ok {
		t.Fatal("hook write must not take effect")
	}
	if e.Store().Inconsistent() {
		t.Fatal("store must stay consistent after a reverted hook write")
	}
}
