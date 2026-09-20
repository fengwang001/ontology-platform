package ontology

import "testing"

func emailConstraint(nullsEqual bool) Constraint {
	return Constraint{Name: "uniq_email", Columns: []string{"email"}, NullsEqual: nullsEqual}
}

// 默认 SQL 语义：NULL 与任何值都不冲突，两个 NULL 之间也不冲突。
func TestNullDefaultSQLSemantics(t *testing.T) {
	c := NewChecker(NormOptions{}, emailConstraint(false))
	if err := c.Insert("r1", map[string]Value{"email": Null()}); err != nil {
		t.Fatal(err)
	}
	if err := c.Insert("r2", map[string]Value{"email": Null()}); err != nil {
		t.Fatalf("two NULLs must not conflict in SQL mode: %v", err)
	}
	if err := c.Insert("r3", map[string]Value{"email": String("a@b.c")}); err != nil {
		t.Fatalf("NULL and a value must not conflict: %v", err)
	}
	if err := c.Insert("r4", map[string]Value{"email": String("a@b.c")}); err == nil {
		t.Fatal("two equal non-NULL values must conflict")
	}
}

// NULL 视为相等模式：两条都为 NULL 的记录冲突。
func TestNullsEqualMode(t *testing.T) {
	c := NewChecker(NormOptions{}, emailConstraint(true))
	if err := c.Insert("r1", map[string]Value{"email": Null()}); err != nil {
		t.Fatal(err)
	}
	if err := c.Insert("r2", map[string]Value{"email": Null()}); err == nil {
		t.Fatal("two NULLs must conflict in nulls-equal mode")
	}
}

// 复合约束中只要有一列是 NULL，默认模式下整条约束不参与冲突判断。
func TestCompositeConstraintWithNullColumn(t *testing.T) {
	con := Constraint{Name: "uniq_name_city", Columns: []string{"name", "city"}}
	c := NewChecker(NormOptions{}, con)
	if err := c.Insert("r1", map[string]Value{"name": Null(), "city": String("bj")}); err != nil {
		t.Fatal(err)
	}
	// 同样 name=NULL, city=bj：因一列为 NULL，整条不参与，不冲突。
	if err := c.Insert("r2", map[string]Value{"name": Null(), "city": String("bj")}); err != nil {
		t.Fatalf("composite key with a NULL column must not participate: %v", err)
	}
	// 两列都非 NULL 且相同才冲突。
	if err := c.Insert("r3", map[string]Value{"name": String("a"), "city": String("bj")}); err != nil {
		t.Fatal(err)
	}
	if err := c.Insert("r4", map[string]Value{"name": String("a"), "city": String("bj")}); err == nil {
		t.Fatal("fully equal composite key must conflict")
	}
}

// 空字符串与 NULL 是不同的东西，即使在 nulls-equal 模式下也不冲突。
func TestEmptyStringIsNotNull(t *testing.T) {
	c := NewChecker(NormOptions{}, emailConstraint(true))
	if err := c.Insert("r1", map[string]Value{"email": Null()}); err != nil {
		t.Fatal(err)
	}
	if err := c.Insert("r2", map[string]Value{"email": String("")}); err != nil {
		t.Fatalf("empty string and NULL must not conflict: %v", err)
	}
	// 但两个空字符串彼此冲突。
	if err := c.Insert("r3", map[string]Value{"email": String("")}); err == nil {
		t.Fatal("two empty strings must conflict")
	}
	// 读回时值种类保持不变。
	got, _ := c.Get("r1")
	if !got["email"].IsNull() {
		t.Fatal("NULL read back as non-NULL")
	}
	got, _ = c.Get("r2")
	if got["email"].IsNull() || got["email"].Raw() != "" {
		t.Fatal("empty string read back as NULL or changed")
	}
}
