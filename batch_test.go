package ontology

import (
	"errors"
	"testing"
)

func batchChecker() *Checker {
	return NewChecker(Normalizer{TrimSpace: true, CaseFold: true},
		Constraint{Name: "uniq_name", Props: []string{"name"}})
}

// 批内先删除、再插入规范化后键相同的新记录：必须成功。
func TestBatchDeleteThenInsertSameKey(t *testing.T) {
	c := batchChecker()
	mustAdd(t, c, "old", "Alice")
	err := c.ApplyBatch([]Op{
		Delete("old"),
		Insert(Record{PK: "new", Values: map[string]*string{"name": Str("  ALICE  ")}}),
	})
	if err != nil {
		t.Fatalf("delete-then-insert in one batch must succeed: %v", err)
	}
	if _, ok := c.Get("old"); ok {
		t.Fatal("old record must be gone")
	}
	rec, ok := c.Get("new")
	if !ok {
		t.Fatal("new record must exist")
	}
	if got := *rec.Values["name"]; got != "  ALICE  " {
		t.Fatalf("stored value = %q, want original bytes", got)
	}
	if err := c.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

// 批内插入两条规范化后键相同的记录：必须拒绝并指出是哪两条。
func TestBatchDoubleInsertRejected(t *testing.T) {
	c := batchChecker()
	mustAdd(t, c, "seed", "existing")
	err := c.ApplyBatch([]Op{
		Insert(Record{PK: "a", Values: map[string]*string{"name": Str("Bob")}}),
		Insert(Record{PK: "b", Values: map[string]*string{"name": Str(" bob ")}}),
	})
	var be *BatchConflictError
	if !errors.As(err, &be) {
		t.Fatalf("error type = %T (%v), want *BatchConflictError", err, err)
	}
	if be.First != 0 || be.Second != 1 {
		t.Errorf("First/Second = %d/%d, want 0/1", be.First, be.Second)
	}
	if be.Constraint != "uniq_name" {
		t.Errorf("Constraint = %q, want uniq_name", be.Constraint)
	}
	if be.FirstPK != "a" || be.SecondPK != "b" {
		t.Errorf("FirstPK/SecondPK = %q/%q, want a/b", be.FirstPK, be.SecondPK)
	}
	// 整批不生效：已有记录不受影响，新记录都不在。
	if c.Len() != 1 {
		t.Fatalf("Len = %d, want 1 (batch rolled back)", c.Len())
	}
	if _, ok := c.Get("a"); ok {
		t.Error("op #0 record must not be stored")
	}
	if _, ok := c.Get("b"); ok {
		t.Error("op #1 record must not be stored")
	}
	if _, ok := c.Get("seed"); !ok {
		t.Error("pre-existing record must be untouched")
	}
	if err := c.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

// 批内插入与已有记录冲突：整批回滚，错误指向已有记录与操作下标。
func TestBatchInsertConflictsWithExisting(t *testing.T) {
	c := batchChecker()
	mustAdd(t, c, "seed", "Carol")
	err := c.ApplyBatch([]Op{
		Insert(Record{PK: "x", Values: map[string]*string{"name": Str("dave")}}),
		Insert(Record{PK: "y", Values: map[string]*string{"name": Str(" CAROL")}}),
	})
	var ce *ConflictError
	if !errors.As(err, &ce) {
		t.Fatalf("error type = %T (%v), want *ConflictError", err, err)
	}
	if ce.ExistingPK != "seed" {
		t.Errorf("ExistingPK = %q, want seed", ce.ExistingPK)
	}
	if ce.OpIndex != 1 {
		t.Errorf("OpIndex = %d, want 1", ce.OpIndex)
	}
	if got := ce.Incoming["name"]; got == nil || *got != " CAROL" {
		t.Errorf("Incoming[name] = %v, want original bytes", got)
	}
	if c.Len() != 1 {
		t.Fatalf("Len = %d, want 1 (batch rolled back)", c.Len())
	}
}

// 批内删除不存在的主键是空操作；删除与插入同键不同记录也可共存。
func TestBatchDeleteMissingAndReinsert(t *testing.T) {
	c := batchChecker()
	mustAdd(t, c, "r1", "eve")
	err := c.ApplyBatch([]Op{
		Delete("missing"),
		Delete("r1"),
		Insert(Record{PK: "r2", Values: map[string]*string{"name": Str("EVE")}}),
	})
	if err != nil {
		t.Fatalf("batch must succeed: %v", err)
	}
	if c.Len() != 1 {
		t.Fatalf("Len = %d, want 1", c.Len())
	}
	if err := c.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}
