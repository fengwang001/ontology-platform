package ontology

import (
	"errors"
	"strings"
	"testing"
)

func seed(t *testing.T, e *Engine) {
	t.Helper()
	err := e.Register(&ActionType{
		Name: "Seed",
		Run: func(tx *Tx, _ map[string]any) error {
			if err := tx.CreateObject("keep", "T", map[string]any{"n": 0}); err != nil {
				return err
			}
			if err := tx.CreateObject("victim", "T", nil); err != nil {
				return err
			}
			return tx.Link("keep-victim", "R", "keep", "victim")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Execute("Seed", nil); err != nil {
		t.Fatal(err)
	}
}

func TestRollbackRestoresExactPriorState(t *testing.T) {
	e := NewEngine()
	seed(t, e)
	err := e.Register(&ActionType{
		Name: "Boom",
		Run: func(tx *Tx, _ map[string]any) error {
			must := func(err error) {
				if err != nil {
					t.Fatal(err)
				}
			}
			must(tx.CreateObject("tmp1", "T", nil))
			must(tx.CreateObject("tmp2", "T", nil))
			must(tx.SetProperty("keep", "n", 99))
			must(tx.Link("tmp-rel", "R", "tmp1", "tmp2"))
			must(tx.DeleteObject("victim")) // also drops keep-victim
			return errors.New("boom")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	beforeObjs, beforeRels := e.Store().ObjectCount(), e.Store().RelationCount()
	if _, err := e.Execute("Boom", nil); err == nil {
		t.Fatal("want failure")
	}
	if got := e.Store().ObjectCount(); got != beforeObjs {
		t.Fatalf("objects: before %d after %d", beforeObjs, got)
	}
	if got := e.Store().RelationCount(); got != beforeRels {
		t.Fatalf("relations: before %d after %d", beforeRels, got)
	}
	keep, ok := e.Store().GetObject("keep")
	if !ok || keep.Props["n"] != 0 {
		t.Fatalf("keep not restored: %+v", keep)
	}
	if _, ok := e.Store().GetObject("victim"); !ok {
		t.Fatal("deleted object not restored")
	}
	if rels := e.Store().RelationsOf("victim"); len(rels) != 1 || rels[0].ID != "keep-victim" {
		t.Fatalf("dangling relations after rollback: %v", rels)
	}
	if _, ok := e.Store().GetObject("tmp1"); ok {
		t.Fatal("half-created object survived rollback")
	}
}

func TestRollbackIsStrictlyReverse(t *testing.T) {
	e := NewEngine()
	seed(t, e)
	err := e.Register(&ActionType{
		Name: "Twice",
		Run: func(tx *Tx, _ map[string]any) error {
			must := func(err error) {
				if err != nil {
					t.Fatal(err)
				}
			}
			must(tx.SetProperty("keep", "n", 1)) // undo restores 0
			must(tx.SetProperty("keep", "n", 2)) // undo restores 1
			return errors.New("boom")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Execute("Twice", nil); err == nil {
		t.Fatal("want failure")
	}
	keep, _ := e.Store().GetObject("keep")
	// Forward rollback would leave 1; strict reverse order restores 0.
	if keep.Props["n"] != 0 {
		t.Fatalf("rollback not strictly reverse: n=%v", keep.Props["n"])
	}
}

func TestUndoFailureMarksStoreInconsistent(t *testing.T) {
	e := NewEngine()
	err := e.Register(&ActionType{
		Name: "Doomed",
		Run: func(tx *Tx, _ map[string]any) error {
			if err := tx.CreateObject("doomed", "T", nil); err != nil {
				return err
			}
			return errors.New("boom")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	e.Store().InjectUndoFault("doomed")
	if _, err := e.Execute("Doomed", nil); err == nil {
		t.Fatal("want failure")
	}
	if !e.Store().Inconsistent() {
		t.Fatal("store must be marked inconsistent after undo failure")
	}
	incons := e.Inconsistencies()
	if len(incons) != 1 {
		t.Fatalf("want 1 inconsistency, got %v", incons)
	}
	if !strings.Contains(incons[0].Step, "doomed") {
		t.Fatalf("inconsistency must name the failed step: %+v", incons[0])
	}
	// The un-undoable step leaves its residue behind.
	if _, ok := e.Store().GetObject("doomed"); !ok {
		t.Fatal("failed undo step should leave its object behind")
	}
	// Every later write is rejected.
	if _, err := e.Execute("Doomed", nil); !errors.Is(err, ErrInconsistent) {
		t.Fatalf("writes after inconsistency must be rejected, got %v", err)
	}
}
