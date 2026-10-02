package match

import "testing"

func TestTopLevelOrBranchRedundancy(t *testing.T) {
	checker := NewChecker()
	mustDefine(t, checker, "T", []Constructor{{Name: "A"}, {Name: "B"}, {Name: "C"}})
	session := mustSession(t, checker, "T")

	result := mustAdd(t, checker, session, or(c("A"), c("B")), false)
	assertArm(t, result, false, false, false)

	result = mustAdd(t, checker, session, or(c("A"), c("B"), c("C")), false)
	assertArm(t, result, false, true, true, false)

	nested := mustAdd(t, checker, session, OrPattern{Branches: []Pattern{
		OrPattern{Branches: []Pattern{c("A"), c("C")}},
	}}, false)
	if len(nested.BranchRedundant) != 1 {
		t.Fatalf("nested Or reported %d top-level branches, want 1", len(nested.BranchRedundant))
	}
	if !nested.Redundant {
		t.Fatal("nested Or should be one redundant top-level branch")
	}
}

func TestGuardedArmsDoNotCoverLaterArms(t *testing.T) {
	checker := NewChecker()
	mustDefine(t, checker, "T", []Constructor{{Name: "A"}, {Name: "B"}})
	session := mustSession(t, checker, "T")

	guarded := mustAdd(t, checker, session, or(c("A"), c("B")), true)
	assertArm(t, guarded, false, false, false)

	again := mustAdd(t, checker, session, c("A"), false)
	assertArm(t, again, false, false)

	result := mustCheck(t, checker, session)
	if result.Text != "B" {
		t.Fatalf("guarded arm participated in exhaustiveness: %q", result.Text)
	}
}

func TestCacheInvalidation(t *testing.T) {
	checker := NewChecker()
	mustDefine(t, checker, "T", []Constructor{{Name: "A"}, {Name: "B"}})
	session := mustSession(t, checker, "T")

	mustCheck(t, checker, session)
	before := checker.MissingCalls()
	mustCheck(t, checker, session)
	if calls := checker.MissingCalls(); calls != before {
		t.Fatalf("recheck recomputed Missing: %d != %d", calls, before)
	}

	mustAdd(t, checker, session, c("A"), true)
	mustCheck(t, checker, session)
	if calls := checker.MissingCalls(); calls != before {
		t.Fatalf("guarded arm recomputed Missing: %d != %d", calls, before)
	}

	mustAdd(t, checker, session, c("A"), false)
	mustCheck(t, checker, session)
	if calls := checker.MissingCalls(); calls <= before {
		t.Fatalf("new coverage did not invalidate cache: calls=%d before=%d", calls, before)
	}
	before = checker.MissingCalls()

	mustAdd(t, checker, session, c("A"), false)
	mustCheck(t, checker, session)
	if calls := checker.MissingCalls(); calls != before {
		t.Fatalf("redundant unguarded arm invalidated cache: calls=%d before=%d", calls, before)
	}

	mustAdd(t, checker, session, c("B"), false)
	mustCheck(t, checker, session)
	if calls := checker.MissingCalls(); calls <= before {
		t.Fatalf("new coverage did not invalidate cache: calls=%d before=%d", calls, before)
	}
}
