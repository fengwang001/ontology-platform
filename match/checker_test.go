package match

import "testing"

func c(name string, children ...Pattern) ConstructorPattern {
	return ConstructorPattern{Name: name, Children: children}
}

func or(branches ...Pattern) OrPattern {
	return OrPattern{Branches: branches}
}

func TestListExample(t *testing.T) {
	checker := NewChecker()
	if err := checker.DefineType("Nat", []Constructor{
		{Name: "Z"},
		{Name: "S", FieldTypes: []string{"Nat"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := checker.DefineType("List", []Constructor{
		{Name: "Nil"},
		{Name: "Cons", FieldTypes: []string{"Nat", "List"}},
	}); err != nil {
		t.Fatal(err)
	}

	session, err := checker.NewSession("List")
	if err != nil {
		t.Fatal(err)
	}
	arm1, err := checker.AddArm(session, c("Cons", c("Z"), Wildcard{}), false)
	if err != nil {
		t.Fatal(err)
	}
	assertArm(t, arm1, false, false)
	assertCheck(t, checker, session, "Nil")

	arm2, err := checker.AddArm(session, c("Cons", c("S", Wildcard{}), c("Nil")), false)
	if err != nil {
		t.Fatal(err)
	}
	assertArm(t, arm2, false, false)
	assertCheck(t, checker, session, "Nil")

	arm3, err := checker.AddArm(session, c("Nil"), false)
	if err != nil {
		t.Fatal(err)
	}
	assertArm(t, arm3, false, false)
	assertCheck(t, checker, session, "Cons(S(_), Cons(_, _))")

	arm4, err := checker.AddArm(session, c("Cons", c("Z"), c("Nil")), false)
	if err != nil {
		t.Fatal(err)
	}
	assertArm(t, arm4, true, true)

	callsBefore := checker.MissingCalls()
	arm5, err := checker.AddArm(session, Wildcard{}, true)
	if err != nil {
		t.Fatal(err)
	}
	assertArm(t, arm5, false, false)
	assertCheck(t, checker, session, "Cons(S(_), Cons(_, _))")
	if calls := checker.MissingCalls(); calls != callsBefore {
		t.Fatalf("guarded arm invalidated cache: calls before=%d after=%d", callsBefore, calls)
	}

	arm6, err := checker.AddArm(session, or(
		c("Cons", c("S", Wildcard{}), c("Cons", Wildcard{}, Wildcard{})),
		c("Cons", c("S", c("Z")), c("Cons", c("Z"), Wildcard{})),
	), false)
	if err != nil {
		t.Fatal(err)
	}
	assertArm(t, arm6, false, false, true)
	result, err := checker.Check(session)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Exhaustive {
		t.Fatalf("expected exhaustive, got %q", result.Text)
	}
}

func assertArm(t *testing.T, result ArmResult, redundant bool, branches ...bool) {
	t.Helper()
	if result.Redundant != redundant {
		t.Fatalf("arm %d redundant=%v, want %v", result.Index, result.Redundant, redundant)
	}
	if len(result.BranchRedundant) != len(branches) {
		t.Fatalf("arm %d branches=%v, want %v", result.Index, result.BranchRedundant, branches)
	}
	for i := range branches {
		if result.BranchRedundant[i] != branches[i] {
			t.Fatalf("arm %d branch %d=%v, want %v", result.Index, i, result.BranchRedundant[i], branches[i])
		}
	}
}

func assertCheck(t *testing.T, checker *Checker, session int, want string) {
	t.Helper()
	result, err := checker.Check(session)
	if err != nil {
		t.Fatal(err)
	}
	if result.Exhaustive || result.Text != want {
		t.Fatalf("check exhaustive=%v text=%q, want counterexample %q", result.Exhaustive, result.Text, want)
	}
}
