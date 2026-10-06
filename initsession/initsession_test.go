package initsession

import (
	"reflect"
	"testing"
)

func mustSession(t *testing.T, pre ...string) *Session {
	t.Helper()
	s, err := New(pre...)
	if err != nil {
		t.Fatalf("New(%v): %v", pre, err)
	}
	return s
}

func addUnit(t *testing.T, lg *testLogger, s *Session, vars []string, refs []string) int {
	t.Helper()
	lg.inputf("AddVariableUnit vars=%v refs=%v", vars, refs)
	idx, err := s.AddVariableUnit(vars, refs)
	if err != nil {
		lg.outputf("-> error: %v", err)
		t.Fatalf("AddVariableUnit(%v,%v): %v", vars, refs, err)
	}
	lg.outputf("-> unit index %d", idx)
	return idx
}

func addFunc(t *testing.T, lg *testLogger, s *Session, name string, refs []string) {
	t.Helper()
	lg.inputf("AddFunction name=%s refs=%v", name, refs)
	if err := s.AddFunction(name, refs); err != nil {
		lg.outputf("-> error: %v", err)
		t.Fatalf("AddFunction(%s,%v): %v", name, refs, err)
	}
	lg.outputf("-> accepted function %s", name)
}

func solveOK(t *testing.T, lg *testLogger, s *Session) (*Result, Stats) {
	t.Helper()
	res, st, err := s.Solve()
	if err != nil {
		lg.outputf("Solve -> error: %v", err)
		t.Fatalf("Solve: %v", err)
	}
	lg.outputf("Solve -> order of units %v", res.Order)
	for _, d := range res.Dependencies {
		lg.reasonf("unit %d vars=%v transitive-deps=%v", d.UnitIndex, d.Variables, d.Deps)
	}
	lg.reasonf("stats: %+v", st)
	return res, st
}

func solveErr(t *testing.T, lg *testLogger, s *Session, kind ErrorKind) *Error {
	t.Helper()
	_, st, err := s.Solve()
	if err == nil {
		t.Fatalf("Solve: want error kind %d, got success", kind)
	}
	e, ok := err.(*Error)
	if !ok || e.Kind != kind {
		t.Fatalf("Solve: want kind %d, got %v", kind, err)
	}
	lg.outputf("Solve -> expected error kind=%d: %v (names=%v stats=%+v)", kind, e, e.Names, st)
	return e
}

func TestIdentifierValidation(t *testing.T) {
	lg := newTestLogger(t)
	valid := []string{"a", "A", "_", "a1", "_x", "X_y9", "abc"}
	invalid := []string{"", "1", "1a", "a-b", "a.b", "中", "a b", "a/b"}
	for _, s := range valid {
		if !IsValidIdentifier(s) {
			t.Errorf("IsValidIdentifier(%q) = false, want true", s)
		}
		lg.reasonf("identifier %q valid=true blank=%v", s, IsBlank(s))
	}
	for _, s := range invalid {
		if IsValidIdentifier(s) {
			t.Errorf("IsValidIdentifier(%q) = true, want false", s)
		}
		lg.reasonf("identifier %q valid=false", s)
	}

	if _, err := New("_"); err == nil {
		t.Fatalf(`New("_"): want invalid argument error`)
	}
	if _, err := New("x", "1bad"); err == nil {
		t.Fatalf(`New with "1bad": want error`)
	}
}

func TestEmptySession(t *testing.T) {
	lg := newTestLogger(t)
	s := mustSession(t)
	res, st := solveOK(t, lg, s)
	if len(res.Order) != 0 || len(res.Dependencies) != 0 {
		t.Fatalf("empty solve: %+v", res)
	}
	if st.Units != 0 || st.Functions != 0 {
		t.Fatalf("empty solve stats: %+v", st)
	}
	lg.reasonf("no declarations: order empty, solve succeeds")
}

func TestPredeclaredReady(t *testing.T) {
	lg := newTestLogger(t)
	s := mustSession(t, "ready")
	addUnit(t, lg, s, []string{"a"}, []string{"ready"})
	addUnit(t, lg, s, []string{"b"}, []string{"a", "ready"})
	res, _ := solveOK(t, lg, s)
	if !reflect.DeepEqual(res.Order, []int{0, 1}) {
		t.Fatalf("order = %v, want [0 1]", res.Order)
	}
	if !reflect.DeepEqual(res.Dependencies[1].Deps, []string{"a"}) {
		t.Fatalf("deps of b = %v, want [a]", res.Dependencies[1].Deps)
	}

	if err := s.AddFunction("ready", nil); err == nil {
		t.Fatalf("function redeclaring predeclared name must be rejected")
	}
	if _, err := s.AddVariableUnit([]string{"ready"}, nil); err == nil {
		t.Fatalf("unit redeclaring predeclared name must be rejected")
	}
}

func TestMultiLayerFunctionIndirection(t *testing.T) {
	lg := newTestLogger(t)
	s := mustSession(t)
	addUnit(t, lg, s, []string{"a"}, nil)
	addUnit(t, lg, s, []string{"b"}, []string{"f2"})
	addFunc(t, lg, s, "f2", []string{"g", "h"})
	addFunc(t, lg, s, "g", []string{"a"})
	addFunc(t, lg, s, "h", nil)
	res, _ := solveOK(t, lg, s)
	if !reflect.DeepEqual(res.Order, []int{0, 1}) {
		t.Fatalf("order = %v, want [0 1]", res.Order)
	}
	if !reflect.DeepEqual(res.Dependencies[1].Deps, []string{"a"}) {
		t.Fatalf("b deps = %v, want [a]", res.Dependencies[1].Deps)
	}
	lg.reasonf("b transitively depends only on a through f2->{g,h}")
}

func TestSharedClosureDiamond(t *testing.T) {
	lg := newTestLogger(t)
	s := mustSession(t)
	// Diamond: left and right both reach base(); top reaches both.
	// Separate consumers reuse the same base closure; the result must
	// stay correct despite copy-on-write sharing.
	addUnit(t, lg, s, []string{"base"}, nil)
	addFunc(t, lg, s, "fbase", []string{"base"})
	addFunc(t, lg, s, "left", []string{"fbase"})
	addFunc(t, lg, s, "right", []string{"fbase"})
	addFunc(t, lg, s, "top", []string{"left", "right"})
	addUnit(t, lg, s, []string{"u1"}, []string{"left"})
	addUnit(t, lg, s, []string{"u2"}, []string{"right"})
	addUnit(t, lg, s, []string{"u3"}, []string{"top"})
	res, _ := solveOK(t, lg, s)
	for i := 1; i <= 3; i++ {
		if got := res.Dependencies[i].Deps; !reflect.DeepEqual(got, []string{"base"}) {
			t.Fatalf("unit %d deps = %v, want [base]", i, got)
		}
	}
	lg.reasonf("diamond closures shared safely; all consumers report [base]")
}

func TestMutuallyRecursiveFunctions(t *testing.T) {
	lg := newTestLogger(t)
	s := mustSession(t)
	addUnit(t, lg, s, []string{"x"}, []string{"p"})
	addUnit(t, lg, s, []string{"y"}, nil)
	addFunc(t, lg, s, "p", []string{"q"})
	addFunc(t, lg, s, "q", []string{"p", "y"})
	res, _ := solveOK(t, lg, s)
	if !reflect.DeepEqual(res.Order, []int{1, 0}) {
		t.Fatalf("order = %v, want [1 0]", res.Order)
	}
	if !reflect.DeepEqual(res.Dependencies[0].Deps, []string{"y"}) {
		t.Fatalf("x deps via p/q = %v, want [y]", res.Dependencies[0].Deps)
	}
	if len(res.Dependencies[1].Deps) != 0 {
		t.Fatalf("y deps = %v, want none", res.Dependencies[1].Deps)
	}
	lg.reasonf("p<->q recursion allowed; both reach y, no variable cycle")
}

func TestMultiVarUnitAndBlank(t *testing.T) {
	lg := newTestLogger(t)
	s := mustSession(t)
	addUnit(t, lg, s, []string{"a", "b"}, nil)
	addUnit(t, lg, s, []string{"_", "c"}, []string{"a", "b"})
	addUnit(t, lg, s, []string{"_"}, nil)
	res, _ := solveOK(t, lg, s)
	if !reflect.DeepEqual(res.Order, []int{0, 1, 2}) {
		t.Fatalf("order = %v, want [0 1 2]", res.Order)
	}
	if got := res.Dependencies[1].Deps; !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("c unit deps = %v, want [a b]", got)
	}
	_, err := s.AddVariableUnit([]string{"d"}, []string{"_"})
	if err == nil || err.(*Error).Kind != KindInvalidArgument {
		t.Fatalf("reference to blank: want invalid argument, got %v", err)
	}
	if _, err := s.AddVariableUnit([]string{"_", "_", "e"}, nil); err != nil {
		t.Fatalf("two blanks in one unit: %v", err)
	}
	lg.reasonf("blanks occupy no namespace and never appear in dep sets: %v", res.Dependencies)
}

func TestForwardReferences(t *testing.T) {
	lg := newTestLogger(t)
	s := mustSession(t)
	addUnit(t, lg, s, []string{"a"}, []string{"b", "lateFn"})
	addUnit(t, lg, s, []string{"b"}, nil)
	addFunc(t, lg, s, "lateFn", []string{"b"})
	res, _ := solveOK(t, lg, s)
	if !reflect.DeepEqual(res.Order, []int{1, 0}) {
		t.Fatalf("order = %v, want [1 0]", res.Order)
	}
	if !reflect.DeepEqual(res.Dependencies[0].Deps, []string{"b"}) {
		t.Fatalf("a deps = %v, want [b]", res.Dependencies[0].Deps)
	}
	lg.reasonf("forward refs to variable b and function lateFn resolved at solve time")
}

func TestSelfReferenceAndFunctionMediatedCycle(t *testing.T) {
	lg := newTestLogger(t)

	s := mustSession(t)
	addUnit(t, lg, s, []string{"a"}, []string{"a"})
	e := solveErr(t, lg, s, KindInitCycle)
	if !reflect.DeepEqual(e.Names, []string{"a"}) {
		t.Fatalf("cycle names = %v, want [a]", e.Names)
	}

	s = mustSession(t)
	addUnit(t, lg, s, []string{"a"}, []string{"f"})
	addUnit(t, lg, s, []string{"b"}, []string{"a"})
	addUnit(t, lg, s, []string{"c"}, nil)
	addFunc(t, lg, s, "f", []string{"b"})
	e = solveErr(t, lg, s, KindInitCycle)
	if !reflect.DeepEqual(e.Names, []string{"a", "b"}) {
		t.Fatalf("cycle names = %v, want [a b] in source order", e.Names)
	}

	s = mustSession(t)
	addUnit(t, lg, s, []string{"p", "_", "q"}, []string{"q"})
	e = solveErr(t, lg, s, KindInitCycle)
	if !reflect.DeepEqual(e.Names, []string{"p", "q"}) {
		t.Fatalf("cycle names = %v, want [p q]", e.Names)
	}
}

func TestUndeclaredReference(t *testing.T) {
	lg := newTestLogger(t)
	s := mustSession(t)
	addUnit(t, lg, s, []string{"a"}, nil)
	addFunc(t, lg, s, "f", []string{"missing2", "missing1"})
	addUnit(t, lg, s, []string{"b"}, []string{"ghost"})
	e := solveErr(t, lg, s, KindUndeclared)
	if e.Function != "f" || e.Name != "missing1" {
		t.Fatalf("undeclared = func %q name %q, want f/missing1", e.Function, e.Name)
	}

	s = mustSession(t)
	addFunc(t, lg, s, "unused", []string{"zzz"})
	addUnit(t, lg, s, []string{"a"}, []string{"aaa"})
	e = solveErr(t, lg, s, KindUndeclared)
	if e.Function != "unused" || e.Name != "zzz" {
		t.Fatalf("want unused/zzz, got %q/%q", e.Function, e.Name)
	}

	s = mustSession(t)
	addUnit(t, lg, s, []string{"cyc"}, []string{"cyc", "nope"})
	e = solveErr(t, lg, s, KindUndeclared)
	if e.Name != "nope" {
		t.Fatalf("undeclared must beat cycle, got %v", e)
	}
}
