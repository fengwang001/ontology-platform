package match

import (
	"sync"
	"testing"
)

func TestAddArmValidationOrder(t *testing.T) {
	checker := NewChecker()
	mustDefine(t, checker, "T", []Constructor{
		{Name: "A"},
		{Name: "B", FieldTypes: []string{"T"}},
	})

	_, err := checker.AddArm(999, OrPattern{}, false)
	assertErrorIs(t, err, ErrInvalidArgument)
	_, err = checker.AddArm(999, c("Unknown"), false)
	assertErrorIs(t, err, ErrSessionNotFound)

	session := mustSession(t, checker, "T")
	_, err = checker.AddArm(session, c("Unknown"), false)
	assertErrorIs(t, err, ErrUnknownConstructor)
	_, err = checker.AddArm(session, c("A", Wildcard{}), false)
	assertErrorIs(t, err, ErrConstructorArity)

	nestedWrong := c("B", c("A"))
	if _, err := checker.AddArm(session, nestedWrong, false); err != nil {
		t.Fatalf("valid nested pattern rejected: %v", err)
	}
	deep := Pattern(Wildcard{})
	for i := 0; i <= maxPatternDepth; i++ {
		deep = c("B", deep)
	}
	_, err = checker.AddArm(session, deep, false)
	assertErrorIs(t, err, ErrInvalidArgument)
}

func TestRejectedAddArmDoesNotChangeState(t *testing.T) {
	checker := NewChecker()
	mustDefine(t, checker, "T", []Constructor{{Name: "A"}, {Name: "B"}})
	session := mustSession(t, checker, "T")
	first := mustAdd(t, checker, session, c("A"), false)

	_, err := checker.AddArm(session, c("Unknown"), false)
	assertErrorIs(t, err, ErrUnknownConstructor)
	retry := mustAdd(t, checker, session, c("B"), false)
	if retry.Index != first.Index+1 {
		t.Fatalf("rejected arm consumed index %d, want %d", retry.Index, first.Index+1)
	}
	if len(checker.sessions[session].arms) != 2 {
		t.Fatalf("rejected arm changed arms: %d", len(checker.sessions[session].arms))
	}
}

func TestExpansionLimits(t *testing.T) {
	checker := NewChecker()
	mustDefine(t, checker, "Exp", []Constructor{
		{Name: "Leaf"},
		{Name: "Pair", FieldTypes: []string{"Exp", "Exp"}},
	})
	session := mustSession(t, checker, "Exp")

	allowed := exactExpansionPattern(maxPatternExpansion)
	if count := expansionCount(allowed); count != maxPatternExpansion {
		t.Fatalf("constructed count=%d, want %d", count, maxPatternExpansion)
	}
	if _, err := checker.AddArm(session, allowed, false); err != nil {
		t.Fatalf("exactly 4096 rows rejected: %v", err)
	}

	tooLarge := exactExpansionPattern(maxPatternExpansion + 1)
	_, err := checker.AddArm(session, tooLarge, false)
	assertErrorIs(t, err, ErrExpansionLimit)
	if count := checker.sessions[session].expansionCount; count != maxPatternExpansion {
		t.Fatalf("rejected expansion changed count to %d", count)
	}

	session2 := mustSession(t, checker, "Exp")
	for i := 0; i < 4; i++ {
		mustAdd(t, checker, session2, exactExpansionPattern(maxPatternExpansion), false)
	}
	_, err = checker.AddArm(session2, exactExpansionPattern(maxPatternExpansion), false)
	assertErrorIs(t, err, ErrExpansionLimit)
}

func TestConcurrentOperations(t *testing.T) {
	checker := NewChecker()
	mustDefine(t, checker, "T", []Constructor{{Name: "A"}, {Name: "B"}})
	var wait sync.WaitGroup
	for i := 0; i < 32; i++ {
		wait.Add(1)
		go func(i int) {
			defer wait.Done()
			session, err := checker.NewSession("T")
			if err != nil {
				t.Errorf("NewSession: %v", err)
				return
			}
			pattern := c("A")
			if i%2 == 0 {
				pattern = c("B")
			}
			if _, err := checker.AddArm(session, pattern, false); err != nil {
				t.Errorf("AddArm: %v", err)
			}
			if _, err := checker.Check(session); err != nil {
				t.Errorf("Check: %v", err)
			}
		}(i)
	}
	wait.Wait()
	if len(checker.sessions) != 32 {
		t.Fatalf("sessions=%d, want 32", len(checker.sessions))
	}
}

func exactExpansionPattern(target int) Pattern {
	if target == 1 {
		return c("Leaf")
	}
	if target == maxPatternExpansion {
		return c("Pair", sixtyFourRows(), sixtyFourRows())
	}
	return or(c("Leaf"), c("Pair", sixtyFourRows(), sixtyFourRows()))
}

func sixtyFourRows() Pattern {
	branches := make([]Pattern, 8)
	for i := range branches {
		eightLeaves := make([]Pattern, 8)
		for j := range eightLeaves {
			eightLeaves[j] = c("Leaf")
		}
		branches[i] = OrPattern{Branches: eightLeaves}
	}
	return OrPattern{Branches: branches}
}
