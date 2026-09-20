package ontology

import (
	"errors"
	"strings"
	"testing"
)

// 冲突报告必须包含：约束名、已有记录主键、规范化键、双方原始值。
func TestConflictErrorReport(t *testing.T) {
	c := NewChecker(Normalizer{TrimSpace: true, CaseFold: true},
		Constraint{Name: "uniq_name", Props: []string{"name"}})
	if err := c.Add(Record{PK: "pk1", Values: map[string]*string{"name": Str("ALICE")}}); err != nil {
		t.Fatalf("seed insert: %v", err)
	}
	err := c.Add(Record{PK: "pk2", Values: map[string]*string{"name": Str("  Alice")}})
	if err == nil {
		t.Fatal("expected conflict")
	}
	var ce *ConflictError
	if !errors.As(err, &ce) {
		t.Fatalf("error type = %T, want *ConflictError", err)
	}
	if ce.Constraint != "uniq_name" {
		t.Errorf("Constraint = %q, want uniq_name", ce.Constraint)
	}
	if ce.ExistingPK != "pk1" {
		t.Errorf("ExistingPK = %q, want pk1", ce.ExistingPK)
	}
	if !strings.Contains(ce.Key, `"ALICE"`) {
		t.Errorf("Key = %q, want normalized key containing \"ALICE\"", ce.Key)
	}
	// 双方原始值保持写入时的字节，用于展示。
	if got := ce.Incoming["name"]; got == nil || *got != "  Alice" {
		t.Errorf("Incoming[name] = %v, want %q", got, "  Alice")
	}
	if got := ce.Existing["name"]; got == nil || *got != "ALICE" {
		t.Errorf("Existing[name] = %v, want %q", got, "ALICE")
	}
	if ce.OpIndex != -1 {
		t.Errorf("OpIndex = %d, want -1 for non-batch write", ce.OpIndex)
	}
	if !strings.Contains(ce.Error(), "uniq_name") || !strings.Contains(ce.Error(), "pk1") {
		t.Errorf("Error() = %q, want mention of constraint and existing pk", ce.Error())
	}
}

// 复合约束的冲突报告包含所有属性列。
func TestConflictErrorCompositeKey(t *testing.T) {
	c := NewChecker(Normalizer{CaseFold: true},
		Constraint{Name: "uniq_ab", Props: []string{"a", "b"}})
	seed := Record{PK: "pk1", Values: map[string]*string{"a": Str("X"), "b": Str("Y")}}
	if err := c.Add(seed); err != nil {
		t.Fatalf("seed insert: %v", err)
	}
	err := c.Add(Record{PK: "pk2", Values: map[string]*string{"a": Str("x"), "b": Str("y")}})
	var ce *ConflictError
	if !errors.As(err, &ce) {
		t.Fatalf("error type = %T, want *ConflictError", err)
	}
	if !strings.Contains(ce.Key, "a=") || !strings.Contains(ce.Key, "b=") {
		t.Errorf("Key = %q, want both properties", ce.Key)
	}
	if len(ce.Incoming) != 2 || len(ce.Existing) != 2 {
		t.Errorf("want 2 props on each side, got %d / %d", len(ce.Incoming), len(ce.Existing))
	}
}

// 失败的写入不得改变已有状态。
func TestFailedInsertLeavesStateUntouched(t *testing.T) {
	c := newChecker(Normalizer{CaseFold: true})
	mustAdd(t, c, "pk1", "alice")
	if err := addErr(c, "pk2", "ALICE"); err == nil {
		t.Fatal("expected conflict")
	}
	if c.Len() != 1 {
		t.Fatalf("Len = %d, want 1 after rejected insert", c.Len())
	}
	if _, ok := c.Get("pk2"); ok {
		t.Fatal("rejected record must not be stored")
	}
	if err := c.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck after rejected insert: %v", err)
	}
}

// 删除后同一规范化键可以再次写入。
func TestDeleteFreesKey(t *testing.T) {
	c := newChecker(Normalizer{CaseFold: true})
	mustAdd(t, c, "pk1", "alice")
	if !c.Remove("pk1") {
		t.Fatal("Remove(pk1) = false, want true")
	}
	if c.Remove("pk1") {
		t.Fatal("second Remove must return false")
	}
	mustAdd(t, c, "pk2", "ALICE")
	if err := c.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}
