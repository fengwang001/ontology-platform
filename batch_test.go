package ontology

import (
	"errors"
	"testing"
)

func batchChecker() *Checker {
	return NewChecker(
		NormOptions{TrimSpace: true, CaseFold: true},
		Constraint{Name: "uniq_name", Columns: []string{"name"}},
	)
}

// 批内先删除某记录、再插入规范化后键相同的新记录，必须成功。
func TestBatchDeleteThenInsert(t *testing.T) {
	c := batchChecker()
	if err := c.Insert("old", map[string]Value{"name": String("Alice")}); err != nil {
		t.Fatal(err)
	}
	err := c.Apply([]Op{
		DeleteOp("old"),
		InsertOp("new", map[string]Value{"name": String("  ALICE ")}),
	})
	if err != nil {
		t.Fatalf("delete-then-insert in one batch must succeed: %v", err)
	}
	if _, ok := c.Get("old"); ok {
		t.Fatal("old record should be gone")
	}
	got, ok := c.Get("new")
	if !ok || got["name"].Raw() != "  ALICE " {
		t.Fatal("new record missing or value changed")
	}
	if err := c.CheckInvariants(); err != nil {
		t.Fatal(err)
	}
}

// 批内插入两条规范化后键相同的记录必须拒绝，并指出是第几条与第几条。
func TestBatchDoubleInsertRejected(t *testing.T) {
	c := batchChecker()
	err := c.Apply([]Op{
		InsertOp("a", map[string]Value{"name": String("Alice")}),
		InsertOp("b", map[string]Value{"name": String(" ALICE")}),
	})
	if err == nil {
		t.Fatal("expected batch rejection")
	}
	var ce *ConflictError
	if !errors.As(err, &ce) {
		t.Fatalf("error is %T, want *ConflictError", err)
	}
	if ce.IncomingOpIndex != 1 || ce.ExistingOpIndex != 0 {
		t.Fatalf("op indices=(%d,%d), want (1,0)",
			ce.IncomingOpIndex, ce.ExistingOpIndex)
	}
}

// 批内任意冲突导致整批不生效，已有记录不受影响。
func TestBatchAtomicity(t *testing.T) {
	c := batchChecker()
	if err := c.Insert("keep", map[string]Value{"name": String("Carol")}); err != nil {
		t.Fatal(err)
	}
	err := c.Apply([]Op{
		InsertOp("x", map[string]Value{"name": String("Dave")}),
		InsertOp("y", map[string]Value{"name": String("carol")}), // 与已有记录冲突
	})
	if err == nil {
		t.Fatal("expected batch rejection")
	}
	var ce *ConflictError
	if !errors.As(err, &ce) || ce.ExistingPK != "keep" {
		t.Fatalf("conflict should point at existing record: %v", err)
	}
	if _, ok := c.Get("x"); ok {
		t.Fatal("batch must be rolled back: x should not exist")
	}
	if _, ok := c.Get("y"); ok {
		t.Fatal("batch must be rolled back: y should not exist")
	}
	got, ok := c.Get("keep")
	if !ok || got["name"].Raw() != "Carol" {
		t.Fatal("pre-existing record was affected")
	}
	if err := c.CheckInvariants(); err != nil {
		t.Fatal(err)
	}
}
