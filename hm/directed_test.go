package hm

import (
	"errors"
	"testing"
)

func baseCtors() map[string]int {
	return map[string]int{"Int": 0, "Bool": 0, "List": 1, "Fn": 2, "Pair": 2}
}

func mustNew(t *testing.T, ctors map[string]int, v int) *Session {
	t.Helper()
	s, err := New(ctors, v)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func mustOK(t *testing.T, err error, what string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: unexpected error %v", what, err)
	}
}

func mustErrIs(t *testing.T, got, want error, what string) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("%s: want %v, got %v", what, want, got)
	}
}

func TestSpecExample(t *testing.T) {
	s := mustNew(t, baseCtors(), 100)
	mustOK(t, s.Enter(), "enter")
	v1, err := s.NewVar()
	mustOK(t, err, "newvar")
	mustOK(t, s.Enter(), "enter2")
	v2, err := s.NewVar()
	mustOK(t, err, "newvar2")
	if v1 != 1 || v2 != 2 {
		t.Fatalf("ids = %d,%d", v1, v2)
	}
	mustOK(t, s.Unify(Var{1}, Con{"List", []Type{Var{2}}}), "unify list")
	if l, _ := s.Level(2); l != 1 {
		t.Fatalf("level2 = %d", l)
	}
	mustOK(t, s.Leave(), "leave")
	mustOK(t, s.Bind("f", Con{"Fn", []Type{Var{2}, Var{1}}}, false), "bind f")
	mustOK(t, s.Leave(), "leave2")
	mustOK(t, s.Bind("g", Con{"Fn", []Type{Var{2}, Var{2}}}, false), "bind g")
	r, err := s.Lookup("g")
	mustOK(t, err, "lookup g")
	if got := formatType(r); got != "Fn(Var3,Var3)" {
		t.Fatalf("g instance = %s", got)
	}
	r, err = s.Lookup("g")
	mustOK(t, err, "lookup g2")
	if got := formatType(r); got != "Fn(Var4,Var4)" {
		t.Fatalf("g instance2 = %s", got)
	}
}

func TestAtomicRollbackExample(t *testing.T) {
	s := mustNew(t, baseCtors(), 100)
	s.NewVar()
	s.Enter()
	s.NewVar()
	err := s.Unify(
		Con{"Pair", []Type{Var{1}, Con{"Int", nil}}},
		Con{"Pair", []Type{Con{"List", []Type{Var{2}}}, Con{"Bool", nil}}},
	)
	mustErrIs(t, err, ErrConstructorMismatch, "pair unify")
	if l, _ := s.Level(1); l != 0 {
		t.Fatalf("level1 after rollback = %d", l)
	}
	if l, _ := s.Level(2); l != 1 {
		t.Fatalf("level2 after rollback = %d", l)
	}
	r1, _ := s.Resolve(Var{1})
	r2, _ := s.Resolve(Var{2})
	if formatType(r1) != "Var1" || formatType(r2) != "Var2" {
		t.Fatalf("bindings not rolled back: %s %s", formatType(r1), formatType(r2))
	}
}

func TestValueRestrictionExample(t *testing.T) {
	s := mustNew(t, baseCtors(), 100)
	s.Enter()
	s.NewVar()
	s.Leave()
	mustOK(t, s.Bind("r", Con{"List", []Type{Var{1}}}, true), "bind r expensive")
	if l, _ := s.Level(1); l != 0 {
		t.Fatalf("level1 = %d, want 0", l)
	}
	mustOK(t, s.Bind("h", Con{"Fn", []Type{Var{1}, Var{1}}}, false), "bind h")
	if len(s.env["h"].Quant) != 0 {
		t.Fatalf("h quantified %v, want none", s.env["h"].Quant)
	}
	s2 := mustNew(t, baseCtors(), 100)
	s2.Enter()
	s2.NewVar()
	s2.Leave()
	mustOK(t, s2.Bind("r", Con{"List", []Type{Var{1}}}, false), "bind r cheap")
	mustOK(t, s2.Bind("h", Con{"Fn", []Type{Var{1}, Var{1}}}, false), "bind h cheap")
	if len(s2.env["h"].Quant) != 1 {
		t.Fatalf("cheap h quant = %v", s2.env["h"].Quant)
	}
}

func TestVarVarMinLevel(t *testing.T) {
	s := mustNew(t, baseCtors(), 100)
	s.NewVar()
	s.Enter()
	s.Enter()
	s.NewVar()
	mustOK(t, s.Unify(Var{1}, Var{2}), "v=v")
	if l, _ := s.Level(2); l != 0 {
		t.Fatalf("survivor level = %d", l)
	}
	r, _ := s.Resolve(Var{1})
	if formatType(r) != "Var2" {
		t.Fatalf("resolved = %s", formatType(r))
	}
	// Right-hand variable as the bound party; lower level is never raised.
	s2 := mustNew(t, baseCtors(), 100)
	s2.NewVar()
	s2.Enter()
	s2.NewVar()
	mustOK(t, s2.Unify(Con{"List", []Type{Var{1}}}, Var{2}), "con=v")
	if l, _ := s.Level(1); l != 0 {
		t.Fatalf("level1 raised to %d", l)
	}
	r, _ = s2.Resolve(Var{2})
	if formatType(r) != "List(Var1)" {
		t.Fatalf("resolved2 = %s", formatType(r))
	}
}

func TestSelfUnify(t *testing.T) {
	s := mustNew(t, baseCtors(), 10)
	s.NewVar()
	mustOK(t, s.Unify(Var{1}, Var{1}), "self")
}

func TestOccursThroughChain(t *testing.T) {
	s := mustNew(t, baseCtors(), 10)
	s.NewVar()
	s.NewVar()
	mustOK(t, s.Unify(Var{1}, Var{2}), "1->2")
	err := s.Unify(Var{2}, Con{"List", []Type{Var{1}}})
	mustErrIs(t, err, ErrOccursCheck, "occurs via chain")
	r, _ := s.Resolve(Var{1})
	if formatType(r) != "Var2" {
		t.Fatalf("chain changed: %s", formatType(r))
	}
}

func TestOccursRollsLevelsBack(t *testing.T) {
	s := mustNew(t, baseCtors(), 10)
	s.NewVar()
	s.Enter()
	s.NewVar()
	s.NewVar()
	mustOK(t, s.Unify(Var{1}, Con{"List", []Type{Var{2}}}), "1->List2")
	if l, _ := s.Level(2); l != 0 {
		t.Fatalf("level2 = %d", l)
	}
	err := s.Unify(Var{3}, Con{"Pair", []Type{Var{2}, Con{"List", []Type{Var{3}}}}})
	mustErrIs(t, err, ErrOccursCheck, "occurs")
	if l, _ := s.Level(2); l != 0 {
		t.Fatalf("level2 after rollback = %d", l)
	}
	if l, _ := s.Level(3); l != 1 {
		t.Fatalf("level3 after rollback = %d", l)
	}
	r, _ := s.Resolve(Var{3})
	if formatType(r) != "Var3" {
		t.Fatalf("var3 got bound: %s", formatType(r))
	}
}

func TestParamOrderFirstFailureWins(t *testing.T) {
	s := mustNew(t, baseCtors(), 10)
	s.NewVar()
	err := s.Unify(
		Con{"Fn", []Type{Con{"Int", nil}, Con{"Int", nil}}},
		Con{"Fn", []Type{Con{"Bool", nil}, Con{"Bool", nil}}},
	)
	mustErrIs(t, err, ErrConstructorMismatch, "first param mismatch")
	err = s.Unify(
		Con{"Fn", []Type{Var{1}, Con{"Int", nil}}},
		Con{"Fn", []Type{Con{"List", []Type{Var{1}}}, Con{"Bool", nil}}},
	)
	mustErrIs(t, err, ErrOccursCheck, "occurs before mismatch")
}
