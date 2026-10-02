package hm

import (
	"errors"
	"fmt"
	"testing"
)

func TestGeneralizeOrderAndEquality(t *testing.T) {
	s := mustNew(t, baseCtors(), 100)
	s.Enter()
	s.NewVar()
	s.Enter()
	s.NewVar()
	s.NewVar()
	s.Leave()
	s.Leave()
	mustOK(t, s.Bind("p",
		Con{"Pair", []Type{Var{2}, Con{"Fn", []Type{Var{3}, Var{2}}}}},
		false), "bind p")
	p := s.env["p"]
	if got := fmt.Sprint(p.Quant); got != "[2 3]" {
		t.Fatalf("quant order = %s", got)
	}
	if got := formatPattern(p); got != "Pair(G0,Fn(G1,G0))" {
		t.Fatalf("pattern = %s", got)
	}
}

func TestLevelBoundaryGeneralization(t *testing.T) {
	s := mustNew(t, baseCtors(), 100)
	s.Enter()
	s.NewVar()
	mustOK(t, s.Bind("eq", Var{1}, false), "bind at level 1")
	if len(s.env["eq"].Quant) != 0 {
		t.Fatalf("equal-level generalized")
	}
	s2 := mustNew(t, baseCtors(), 100)
	s2.Enter()
	s2.NewVar()
	s2.Leave()
	mustOK(t, s2.Bind("gt", Var{1}, false), "bind at level 0")
	if len(s2.env["gt"].Quant) != 1 {
		t.Fatalf("L+1 not generalized")
	}
}

func TestPatternDetachedFromVariables(t *testing.T) {
	s := mustNew(t, baseCtors(), 100)
	s.Enter()
	s.NewVar()
	s.Leave()
	mustOK(t, s.Bind("g", Con{"List", []Type{Var{1}}}, false), "bind g")
	mustOK(t, s.Unify(Var{1}, Con{"Int", nil}), "unify original")
	r, err := s.Lookup("g")
	mustOK(t, err, "lookup after unify")
	if got := formatType(r); got != "List(Var2)" {
		t.Fatalf("pattern affected by later unify: %s", got)
	}
}

func TestLookupConsecutiveIDs(t *testing.T) {
	s := mustNew(t, baseCtors(), 5)
	s.Enter()
	s.NewVar()
	s.Leave()
	mustOK(t, s.Bind("g", Con{"Pair", []Type{Var{1}, Var{1}}}, false), "bind")
	r, _ := s.Lookup("g")
	if got := formatType(r); got != "Pair(Var2,Var2)" {
		t.Fatalf("first = %s", got)
	}
	r, _ = s.Lookup("g")
	if got := formatType(r); got != "Pair(Var3,Var3)" {
		t.Fatalf("second = %s", got)
	}
}

func TestLimitsExact(t *testing.T) {
	s := mustNew(t, baseCtors(), 2)
	_, err := s.NewVar()
	mustOK(t, err, "v1")
	_, err = s.NewVar()
	mustOK(t, err, "v2")
	_, err = s.NewVar()
	mustErrIs(t, err, ErrVariableLimit, "v3")

	s2 := mustNew(t, baseCtors(), 10)
	for i := 0; i < maxLevel; i++ {
		mustOK(t, s2.Enter(), fmt.Sprintf("enter %d", i))
	}
	mustErrIs(t, s2.Enter(), ErrLevelLimit, "enter 1001")
	mustOK(t, s2.Leave(), "leave")

	s3 := mustNew(t, baseCtors(), 10)
	for i := 0; i < maxEnv; i++ {
		name := fmt.Sprintf("n%d", i)
		mustOK(t, s3.Bind(name, Con{"Int", nil}, false), name)
	}
	mustErrIs(t, s3.Bind("overflow", Con{"Int", nil}, false), ErrEnvironmentFull, "env overflow")
	mustErrIs(t, s3.Bind("n0", Con{"Int", nil}, false), ErrNameExists, "dup wins over full")

	// Lookup missing name beats the variable-limit error.
	s4 := mustNew(t, baseCtors(), 1)
	_, err = s4.Lookup("nope")
	mustErrIs(t, err, ErrNameNotFound, "missing lookup")
}

func TestLeaveAtZeroAndBoundLevel(t *testing.T) {
	s := mustNew(t, baseCtors(), 10)
	s.NewVar()
	mustErrIs(t, s.Leave(), ErrLevelUnderflow, "leave zero")
	mustOK(t, s.Unify(Var{1}, Con{"Int", nil}), "bind to con")
	_, err := s.Level(1)
	mustErrIs(t, err, ErrVariableBound, "level of bound var")
}

func TestInvalidArgumentsAndNoStateChange(t *testing.T) {
	s, err := New(map[string]int{"": 0}, 10)
	mustErrIs(t, err, ErrInvalidArgument, "empty ctor name")
	s, err = New(map[string]int{"x": 5}, 10)
	mustErrIs(t, err, ErrInvalidArgument, "arity 5")
	_, err = New(map[string]int{"x": 0}, 0)
	mustErrIs(t, err, ErrInvalidArgument, "v=0")

	s = mustNew(t, baseCtors(), 10)
	s.NewVar()
	before := dumpSession(s)
	err = s.Unify(Var{9}, Con{"Int", nil})
	mustErrIs(t, err, ErrInvalidArgument, "unknown var")
	err = s.Unify(Con{"Nope", nil}, Var{1})
	mustErrIs(t, err, ErrInvalidArgument, "unknown ctor (left first)")
	// Each side matches its own constructor: differing names -> mismatch.
	err = s.Unify(Con{"List", []Type{Var{1}}}, Con{"Pair", []Type{Var{1}, Var{1}}})
	mustErrIs(t, err, ErrConstructorMismatch, "different constructors")
	// Args not equal to the constructor's declared arity -> invalid.
	err = s.Unify(Con{"List", []Type{}}, Con{"Int", nil})
	mustErrIs(t, err, ErrInvalidArgument, "arity mismatch left")
	err = s.Unify(Con{"Int", []Type{Var{1}}}, Con{"Int", nil})
	mustErrIs(t, err, ErrInvalidArgument, "arity left invalid")
	// Unknown constructor on the right is found after the (valid) left.
	err = s.Unify(Con{"Int", nil}, Con{"Pair", []Type{Var{1}}})
	mustErrIs(t, err, ErrInvalidArgument, "right arity invalid")
	// Both constructors valid and same arity: structural mismatch, not invalid.
	err = s.Unify(Con{"List", []Type{Con{"Int", nil}}}, Con{"List", []Type{Con{"Bool", nil}}})
	mustErrIs(t, err, ErrConstructorMismatch, "inner mismatch")
	err = s.Bind("", Var{1}, false)
	mustErrIs(t, err, ErrInvalidArgument, "empty bind name")
	err = s.Bind("x", Con{"List", nil}, false)
	mustErrIs(t, err, ErrInvalidArgument, "bind bad type")
	after := dumpSession(s)
	if before != after {
		t.Fatalf("state changed after rejected ops:\nbefore %s\nafter  %s", before, after)
	}
}

func TestConstructorValidation(t *testing.T) {
	long := ""
	for i := 0; i < 33; i++ {
		long += "a"
	}
	if _, err := New(map[string]int{long: 0}, 1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("33-byte name: %v", err)
	}
	if _, err := New(map[string]int{}, 1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("empty ctor table: %v", err)
	}
	many := map[string]int{}
	for i := 0; i < 17; i++ {
		many[fmt.Sprintf("c%02d", i)] = 0
	}
	if _, err := New(many, 1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("17 ctors: %v", err)
	}
}
