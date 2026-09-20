package ontology

import "testing"

func recWith(pk string, vals map[string]*string) Record {
	return Record{PK: pk, Values: vals}
}

// 默认 SQL 语义：NULL 与任何值都不冲突，两个 NULL 之间也不冲突。
func TestNullDefaultSQLSemantics(t *testing.T) {
	c := newChecker(Normalizer{})
	if err := c.Add(recWith("pk1", map[string]*string{"name": nil})); err != nil {
		t.Fatalf("first NULL insert: %v", err)
	}
	if err := c.Add(recWith("pk2", map[string]*string{"name": nil})); err != nil {
		t.Fatalf("second NULL must not conflict with first NULL: %v", err)
	}
	if err := c.Add(recWith("pk3", map[string]*string{"name": Str("alice")})); err != nil {
		t.Fatalf("value must not conflict with NULL: %v", err)
	}
	if c.Len() != 3 {
		t.Fatalf("want 3 records, got %d", c.Len())
	}
}

// NULL 视为相等模式：两条都为 NULL 的记录冲突。
func TestNullsEqualMode(t *testing.T) {
	c := NewChecker(Normalizer{},
		Constraint{Name: "uniq_name", Props: []string{"name"}, NullsEqual: true})
	if err := c.Add(recWith("pk1", map[string]*string{"name": nil})); err != nil {
		t.Fatalf("first NULL insert: %v", err)
	}
	err := c.Add(recWith("pk2", map[string]*string{"name": nil}))
	ce, ok := err.(*ConflictError)
	if !ok {
		t.Fatalf("two NULLs must conflict in NullsEqual mode, got %v", err)
	}
	if ce.ExistingPK != "pk1" {
		t.Fatalf("conflict must point to pk1, got %q", ce.ExistingPK)
	}
	// NULL 与非 NULL 仍不冲突。
	if err := c.Add(recWith("pk3", map[string]*string{"name": Str("alice")})); err != nil {
		t.Fatalf("NULL vs value must not conflict: %v", err)
	}
}

// 复合约束：任一列为 NULL 时默认模式下整条约束不参与判断（单独断言）。
func TestCompositeConstraintWithNullSkipped(t *testing.T) {
	c := NewChecker(Normalizer{},
		Constraint{Name: "uniq_ab", Props: []string{"a", "b"}})
	add := func(pk string, a, b *string) error {
		return c.Add(recWith(pk, map[string]*string{"a": a, "b": b}))
	}
	if err := add("pk1", Str("x"), nil); err != nil {
		t.Fatalf("insert with NULL column: %v", err)
	}
	// 完全相同的 (x, NULL)：默认模式下不参与冲突判断。
	if err := add("pk2", Str("x"), nil); err != nil {
		t.Fatalf("composite key containing NULL must be skipped entirely: %v", err)
	}
	// 无 NULL 的相同组合必须冲突，证明约束本身仍在生效。
	if err := add("pk3", Str("x"), Str("y")); err != nil {
		t.Fatalf("baseline insert: %v", err)
	}
	if err := add("pk4", Str("x"), Str("y")); err == nil {
		t.Fatal("identical non-NULL composite key must conflict")
	}
}

// 复合约束 + NullsEqual：含 NULL 的相同组合冲突。
func TestCompositeConstraintNullsEqual(t *testing.T) {
	c := NewChecker(Normalizer{},
		Constraint{Name: "uniq_ab", Props: []string{"a", "b"}, NullsEqual: true})
	add := func(pk string, a, b *string) error {
		return c.Add(recWith(pk, map[string]*string{"a": a, "b": b}))
	}
	if err := add("pk1", Str("x"), nil); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := add("pk2", Str("x"), nil); err == nil {
		t.Fatal("NullsEqual: identical (x, NULL) must conflict")
	}
	if err := add("pk3", Str("x"), Str("")); err != nil {
		t.Fatalf("empty string differs from NULL: %v", err)
	}
}

// 空字符串与 NULL 是不同的东西。
func TestEmptyStringDistinctFromNull(t *testing.T) {
	for _, nullsEqual := range []bool{false, true} {
		c := NewChecker(Normalizer{},
			Constraint{Name: "uniq_name", Props: []string{"name"}, NullsEqual: nullsEqual})
		if err := c.Add(recWith("null1", map[string]*string{"name": nil})); err != nil {
			t.Fatalf("nullsEqual=%v insert NULL: %v", nullsEqual, err)
		}
		if err := c.Add(recWith("empty1", map[string]*string{"name": Str("")})); err != nil {
			t.Fatalf("nullsEqual=%v empty string must differ from NULL: %v", nullsEqual, err)
		}
		// 两个空字符串在两种模式下都冲突。
		if err := c.Add(recWith("empty2", map[string]*string{"name": Str("")})); err == nil {
			t.Fatalf("nullsEqual=%v two empty strings must conflict", nullsEqual)
		}
	}
}
