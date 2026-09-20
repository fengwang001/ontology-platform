package ontology

import (
	"errors"
	"strings"
	"testing"
)

// 冲突报告必须精确：约束名、已有记录主键、规范化键、双方原始值。
func TestConflictReport(t *testing.T) {
	opts := NormOptions{TrimSpace: true, CaseFold: true}
	con := Constraint{Name: "uniq_name", Columns: []string{"name"}}
	c := NewChecker(opts, con)
	if err := c.Insert("emp-1", map[string]Value{"name": String("ALICE")}); err != nil {
		t.Fatal(err)
	}
	err := c.Insert("emp-2", map[string]Value{"name": String("  Alice")})
	if err == nil {
		t.Fatal("expected conflict")
	}
	var ce *ConflictError
	if !errors.As(err, &ce) {
		t.Fatalf("error is %T, want *ConflictError", err)
	}
	if ce.Constraint != "uniq_name" {
		t.Errorf("Constraint=%q, want uniq_name", ce.Constraint)
	}
	if ce.ExistingPK != "emp-1" || ce.IncomingPK != "emp-2" {
		t.Errorf("PKs=(%q,%q), want (emp-1,emp-2)", ce.ExistingPK, ce.IncomingPK)
	}
	if !strings.Contains(ce.NormKey, "alice") {
		t.Errorf("NormKey=%q should contain normalized key %q", ce.NormKey, "alice")
	}
	if got := ce.IncomingValues[0].Raw(); got != "  Alice" {
		t.Errorf("IncomingValues[0]=%q, want original %q", got, "  Alice")
	}
	if got := ce.ExistingValues[0].Raw(); got != "ALICE" {
		t.Errorf("ExistingValues[0]=%q, want original %q", got, "ALICE")
	}
	if ce.IncomingOpIndex != -1 || ce.ExistingOpIndex != -1 {
		t.Errorf("op indices should be -1 outside batch, got %d/%d",
			ce.IncomingOpIndex, ce.ExistingOpIndex)
	}
	// Error() 文本应包含约束名与已有主键。
	msg := ce.Error()
	if !strings.Contains(msg, "uniq_name") || !strings.Contains(msg, "emp-1") {
		t.Errorf("Error() missing details: %s", msg)
	}
}

// 复合约束的冲突报告应包含各列的原始值。
func TestConflictReportComposite(t *testing.T) {
	con := Constraint{Name: "uniq_name_city", Columns: []string{"name", "city"}}
	c := NewChecker(NormOptions{CaseFold: true}, con)
	if err := c.Insert("r1", map[string]Value{
		"name": String("Alice"), "city": String("BJ"),
	}); err != nil {
		t.Fatal(err)
	}
	err := c.Insert("r2", map[string]Value{
		"name": String("ALICE"), "city": String("bj"),
	})
	var ce *ConflictError
	if !errors.As(err, &ce) {
		t.Fatalf("error is %T, want *ConflictError", err)
	}
	if len(ce.IncomingValues) != 2 || len(ce.ExistingValues) != 2 {
		t.Fatalf("want 2 values per side, got %d/%d",
			len(ce.IncomingValues), len(ce.ExistingValues))
	}
	if ce.ExistingValues[1].Raw() != "BJ" || ce.IncomingValues[1].Raw() != "bj" {
		t.Errorf("city originals wrong: %q vs %q",
			ce.ExistingValues[1].Raw(), ce.IncomingValues[1].Raw())
	}
}
