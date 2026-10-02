package match

import (
	"errors"
	"strings"
	"testing"
)

func TestDefineTypeValidationAndRejectionOrder(t *testing.T) {
	t.Run("names and limits", func(t *testing.T) {
		checker := NewChecker()
		assertErrorIs(t, checker.DefineType("", []Constructor{{Name: "A"}}), ErrInvalidArgument)
		assertErrorIs(t, checker.DefineType(strings.Repeat("x", maxNameBytes+1), []Constructor{{Name: "A"}}), ErrInvalidArgument)
		assertErrorIs(t, checker.DefineType("T", nil), ErrInvalidArgument)

		constructors := make([]Constructor, maxConstructors+1)
		for i := range constructors {
			constructors[i] = Constructor{Name: string(rune('A' + i))}
		}
		assertErrorIs(t, checker.DefineType("TooMany", constructors), ErrInvalidArgument)
		assertErrorIs(t, checker.DefineType("BadFields", []Constructor{{
			Name:       "A",
			FieldTypes: []string{"1", "2", "3", "4", "5"},
		}}), ErrInvalidArgument)
	})

	t.Run("unknown field before duplicate and finite checks", func(t *testing.T) {
		checker := NewChecker()
		err := checker.DefineType("Self", []Constructor{{Name: "Self", FieldTypes: []string{"Missing"}}})
		assertErrorIs(t, err, ErrInvalidArgument)
		if _, ok := checker.types["Self"]; ok {
			t.Fatal("rejected definition changed state")
		}
	})

	t.Run("duplicate type before constructor collision", func(t *testing.T) {
		checker := NewChecker()
		mustDefine(t, checker, "A", []Constructor{{Name: "C"}})
		assertErrorIs(t, checker.DefineType("A", []Constructor{{Name: "D"}}), ErrDuplicateName)
		assertErrorIs(t, checker.DefineType("B", []Constructor{{Name: "C"}}), ErrDuplicateName)
	})

	t.Run("non-finite argument error precedes duplicate name", func(t *testing.T) {
		checker := NewChecker()
		mustDefine(t, checker, "A", []Constructor{{Name: "C"}})
		err := checker.DefineType("A", []Constructor{{Name: "D", FieldTypes: []string{"A"}}})
		assertErrorIs(t, err, ErrInvalidArgument)
	})

	t.Run("all constructors contain self are rejected", func(t *testing.T) {
		checker := NewChecker()
		err := checker.DefineType("Loop", []Constructor{
			{Name: "A", FieldTypes: []string{"Loop"}},
			{Name: "B", FieldTypes: []string{"Loop", "Loop"}},
		})
		assertErrorIs(t, err, ErrInvalidArgument)
		if _, ok := checker.types["Loop"]; ok {
			t.Fatal("non-finite type was registered")
		}
	})

	t.Run("immutable after definition", func(t *testing.T) {
		checker := NewChecker()
		mustDefine(t, checker, "Unit", []Constructor{{Name: "Unit"}})
		assertErrorIs(t, checker.DefineType("Unit", []Constructor{{Name: "Other"}}), ErrDuplicateName)
		declaration := checker.types["Unit"].constructors[0]
		if declaration.Name != "Unit" {
			t.Fatalf("type changed after rejected redefinition: %q", declaration.Name)
		}
	})
}

func TestTypeLimit(t *testing.T) {
	checker := NewChecker()
	for i := 0; i < maxTypes; i++ {
		name := "T" + string(rune('A'+i/10)) + string(rune('A'+i%10))
		mustDefine(t, checker, name, []Constructor{{Name: "C" + string(rune('A'+i))}})
	}
	assertErrorIs(t, checker.DefineType("Overflow", []Constructor{{Name: "OverflowC"}}), ErrInvalidArgument)
}

func mustDefine(t *testing.T, checker *Checker, name string, constructors []Constructor) {
	t.Helper()
	if err := checker.DefineType(name, constructors); err != nil {
		t.Fatalf("DefineType(%q): %v", name, err)
	}
}

func assertErrorIs(t *testing.T, err, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("error=%v, want %v", err, target)
	}
}
