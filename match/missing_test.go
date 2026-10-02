package match

import "testing"

func TestMissingBranchesAndDeclarationOrder(t *testing.T) {
	t.Run("empty sigma returns wildcard", func(t *testing.T) {
		checker := NewChecker()
		mustDefine(t, checker, "Color", []Constructor{
			{Name: "Red"},
			{Name: "Green"},
			{Name: "Blue"},
		})
		session := mustSession(t, checker, "Color")
		result := mustCheck(t, checker, session)
		if result.Text != "_" {
			t.Fatalf("text=%q, want _", result.Text)
		}
	})

	t.Run("incomplete sigma returns first declared constructor", func(t *testing.T) {
		checker := NewChecker()
		mustDefine(t, checker, "Order", []Constructor{
			{Name: "Zeta"},
			{Name: "Alpha"},
			{Name: "Mid"},
		})
		session := mustSession(t, checker, "Order")
		mustAdd(t, checker, session, c("Zeta"), false)
		result := mustCheck(t, checker, session)
		if result.Text != "Alpha" {
			t.Fatalf("text=%q, want Alpha (declaration order, not lexicographic)", result.Text)
		}
	})

	t.Run("complete sigma specializes in declaration order", func(t *testing.T) {
		checker := NewChecker()
		mustDefine(t, checker, "Tree", []Constructor{
			{Name: "Leaf"},
			{Name: "ZNode", FieldTypes: []string{"Tree"}},
			{Name: "ANode", FieldTypes: []string{"Tree"}},
		})
		session := mustSession(t, checker, "Tree")
		mustAdd(t, checker, session, c("Leaf"), false)
		mustAdd(t, checker, session, c("ZNode", c("Leaf")), false)
		mustAdd(t, checker, session, c("ANode", Wildcard{}), false)
		result := mustCheck(t, checker, session)
		if result.Text != "ZNode(ZNode(_))" {
			t.Fatalf("text=%q, want ZNode(ZNode(_))", result.Text)
		}
	})
}

func TestNestedOrExpansionOrder(t *testing.T) {
	checker := NewChecker()
	mustDefine(t, checker, "T", []Constructor{
		{Name: "A"},
		{Name: "B"},
		{Name: "C"},
	})

	session := mustSession(t, checker, "T")
	mustAdd(t, checker, session, OrPattern{Branches: []Pattern{
		c("A"),
		OrPattern{Branches: []Pattern{c("B"), c("C")}},
	}}, false)
	result := mustCheck(t, checker, session)
	if !result.Exhaustive {
		t.Fatalf("nested Or did not exhaustively cover: %q", result.Text)
	}

	session2 := mustSession(t, checker, "T")
	mustAdd(t, checker, session2, OrPattern{Branches: []Pattern{
		OrPattern{Branches: []Pattern{c("C"), c("B")}},
	}}, false)
	first, err := checker.Check(session2)
	if err != nil {
		t.Fatal(err)
	}
	if first.Text != "A" {
		t.Fatalf("first check text=%q, want A", first.Text)
	}
	current := checker.sessions[session2]
	if got := current.arms[0].rows[0][0]; got.(ConstructorPattern).Name != "C" {
		t.Fatalf("first nested expansion=%v, want C", got)
	}
	if got := current.arms[0].rows[1][0]; got.(ConstructorPattern).Name != "B" {
		t.Fatalf("second nested expansion=%v, want B", got)
	}
}

func mustSession(t *testing.T, checker *Checker, typeName string) int {
	t.Helper()
	session, err := checker.NewSession(typeName)
	if err != nil {
		t.Fatal(err)
	}
	return session
}

func mustAdd(t *testing.T, checker *Checker, session int, pattern Pattern, guarded bool) ArmResult {
	t.Helper()
	result, err := checker.AddArm(session, pattern, guarded)
	if err != nil {
		t.Fatalf("AddArm: %v", err)
	}
	return result
}

func mustCheck(t *testing.T, checker *Checker, session int) CheckResult {
	t.Helper()
	result, err := checker.Check(session)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
