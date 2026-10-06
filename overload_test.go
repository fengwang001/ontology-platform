package overload

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"
)

type testLog struct {
	t testing.TB
}

func (l testLog) Printf(format string, args ...any) {
	l.t.Helper()
	l.t.Logf(format, args...)
}

func newTestRegistry(t *testing.T, types ...TypeID) *Registry {
	t.Helper()
	r := NewRegistry(WithLogWriter(io.Discard))
	for _, typ := range types {
		if err := r.DefineType(typ); err != nil {
			t.Fatalf("DefineType(%q): %v", typ, err)
		}
	}
	return r
}

func mustAddDeclaration(t *testing.T, r *Registry, d Declaration) {
	t.Helper()
	if err := r.AddDeclaration(d); err != nil {
		t.Fatalf("AddDeclaration(%+v): %v", d, err)
	}
}

func mustAddPromotion(t *testing.T, r *Registry, from, to TypeID) {
	t.Helper()
	if err := r.AddPromotion(from, to); err != nil {
		t.Fatalf("AddPromotion(%q,%q): %v", from, to, err)
	}
}

func mustAddUser(t *testing.T, r *Registry, from, to TypeID) {
	t.Helper()
	if err := r.AddUserConversion(from, to); err != nil {
		t.Fatalf("AddUserConversion(%q,%q): %v", from, to, err)
	}
}

func decl(params ...Param) Declaration {
	return Declaration{Name: "f", Params: params}
}

func call(args ...TypeID) Call {
	return Call{Name: "f", Args: args}
}

func TestConversionRanksPositionWise(t *testing.T) {
	r := newTestRegistry(t, "x", "a", "p", "u", "a2", "p2", "u2")
	mustAddPromotion(t, r, "x", "p")
	mustAddUser(t, r, "x", "u")
	mustAddPromotion(t, r, "a", "a2")
	mustAddPromotion(t, r, "p", "p2")
	mustAddUser(t, r, "a", "u2")
	mustAddDeclaration(t, r, decl(Param{Type: "a"}, Param{Type: "p2"}))
	mustAddDeclaration(t, r, decl(Param{Type: "p"}, Param{Type: "u2"}))

	report := r.Resolve("f", call("x", "a"))
	if report.Selected == nil || report.Selected.Params[0].Type != "p" {
		t.Fatalf("expected identity/promotion winner, got %+v", report)
	}
	winnerIndex := 0
	if report.Candidates[1].Declaration.Params[0].Type == "p" {
		winnerIndex = 1
	}
	winner := report.Candidates[winnerIndex]
	if winner.Conversions[0].Rank != RankPromotion || winner.Conversions[1].Rank != RankUser {
		t.Fatalf("unexpected ranks: %+v", winner.Conversions)
	}
}

func TestCrossedRanksAreAmbiguous(t *testing.T) {
	r := newTestRegistry(t, "x", "y", "p", "q", "u", "v")
	mustAddPromotion(t, r, "x", "p")
	mustAddPromotion(t, r, "y", "q")
	mustAddUser(t, r, "x", "u")
	mustAddUser(t, r, "y", "v")
	mustAddDeclaration(t, r, decl(Param{Type: "p"}, Param{Type: "v"}))
	mustAddDeclaration(t, r, decl(Param{Type: "u"}, Param{Type: "q"}))

	report := r.Resolve("f", call("x", "y"))
	if !report.Ambiguous || len(report.AmbiguousCandidates) != 2 {
		t.Fatalf("expected two-way ambiguity, got %+v", report)
	}
}

func TestChainedUserConversionRejectsCandidate(t *testing.T) {
	r := newTestRegistry(t, "x", "m", "n", "p")
	mustAddUser(t, r, "x", "m")
	mustAddUser(t, r, "m", "n")
	mustAddPromotion(t, r, "n", "p")
	mustAddDeclaration(t, r, decl(Param{Type: "p"}))

	report := r.Resolve("f", call("x"))
	if !report.NoMatch || report.Candidates[0].Rejection.Reason != RejectChainedUserDef {
		t.Fatalf("expected chained-user rejection, got %+v", report)
	}
}

func TestVariadicAndDefaultArgumentBoundaries(t *testing.T) {
	r := newTestRegistry(t, "a", "b")
	mustAddDeclaration(t, r, decl(Param{Type: "a"}, Param{Type: "b", Default: true}))
	mustAddDeclaration(t, r, decl(Param{Type: "a"}, Param{Type: "b", Variadic: true}))

	if report := r.Resolve("f", call()); !report.NoMatch || report.Candidates[0].Rejection.Reason != RejectTooFewArgs {
		t.Fatalf("expected too-few rejection, got %+v", report)
	}
	one := r.Resolve("f", call("a"))
	if one.Selected == nil || one.TieRule != TieFewerDefaults || len(one.AmbiguousCandidates) != 0 {
		t.Fatalf("expected fixed/default candidate at equality, got %+v", one)
	}
	two := r.Resolve("f", call("a", "b"))
	if two.Selected == nil || !two.Selected.Params[1].Default || two.TieRule != TieNoVariadic {
		t.Fatalf("expected fixed/default candidate for two args, got %+v", two)
	}
	three := r.Resolve("f", call("a", "b", "b"))
	if three.Selected == nil || !three.Selected.Params[1].Variadic || three.Candidates[0].Applicable {
		t.Fatalf("expected only variadic candidate for three args, got %+v", three)
	}
}

func TestDefaultOnVariadicDoesNotLowerRequiredFixedArgs(t *testing.T) {
	r := newTestRegistry(t, "a", "b")
	mustAddDeclaration(t, r, decl(Param{Type: "a"}, Param{Type: "b", Default: true, Variadic: true}))

	if report := r.Resolve("f", call()); !report.NoMatch || report.Candidates[0].Rejection.Reason != RejectTooFewArgs {
		t.Fatalf("variadic default must not remove fixed minimum, got %+v", report)
	}
	if report := r.Resolve("f", call("a")); report.Selected == nil {
		t.Fatalf("one fixed argument should accept defaulted variadic slice, got %+v", report)
	}
}

func TestNoMatchReasonOrder(t *testing.T) {
	r := newTestRegistry(t, "a", "b", "c")
	mustAddDeclaration(t, r, decl(Param{Type: "a"}, Param{Type: "b"}))
	mustAddDeclaration(t, r, decl(Param{Type: "a"}, Param{Type: "c"}, Param{Type: "b"}))

	report := r.Resolve("f", call("a", "b", "c"))
	if !report.NoMatch {
		t.Fatalf("expected no match, got %+v", report)
	}
	reasons := []RejectReason{report.Candidates[0].Rejection.Reason, report.Candidates[1].Rejection.Reason}
	want := []RejectReason{RejectTooManyArgs, RejectUnconvertible}
	for i := range want {
		if reasons[i] != want[i] || (reasons[i] == RejectUnconvertible && report.Candidates[1].Rejection.Position != 1) {
			t.Fatalf("reasons=%v report=%+v", reasons, report)
		}
	}
}

func TestFewerDefaultsTieBreak(t *testing.T) {
	r := newTestRegistry(t, "a", "b", "c")
	mustAddDeclaration(t, r, decl(Param{Type: "a"}, Param{Type: "b", Default: true}, Param{Type: "c", Default: true}))
	mustAddDeclaration(t, r, decl(Param{Type: "a"}, Param{Type: "c", Default: true}))

	report := r.Resolve("f", call("a"))
	if report.Selected == nil || report.TieRule != TieFewerDefaults || len(report.Selected.Params) != 2 {
		t.Fatalf("expected fewer-defaults winner, got %+v", report)
	}
}

func TestMoreSpecializedTieBreak(t *testing.T) {
	r := newTestRegistry(t, "x", "parent", "child")
	mustAddUser(t, r, "x", "parent")
	mustAddUser(t, r, "x", "child")
	mustAddPromotion(t, r, "child", "parent")
	mustAddDeclaration(t, r, decl(Param{Type: "parent"}))
	mustAddDeclaration(t, r, decl(Param{Type: "child"}))

	report := r.Resolve("f", call("x"))
	if report.Selected == nil || report.TieRule != TieMoreSpecialized || report.Selected.Params[0].Type != "child" {
		t.Fatalf("expected specialized winner, got %+v", report)
	}
}

func TestTieBreakerPrecedence(t *testing.T) {
	r := newTestRegistry(t, "x", "a", "b", "generic", "specific")
	mustAddUser(t, r, "x", "a")
	mustAddUser(t, r, "x", "b")
	mustAddPromotion(t, r, "specific", "generic")
	mustAddDeclaration(t, r, decl(Param{Type: "a"}, Param{Type: "generic", Default: true}))
	mustAddDeclaration(t, r, decl(Param{Type: "b", Variadic: true}))

	report := r.Resolve("f", call("x"))
	if report.Selected == nil || report.TieRule != TieNoVariadic || report.Selected.Params[0].Type != "a" {
		t.Fatalf("variadic rule must precede later rules, got %+v", report)
	}
}

func TestRegistrationValidationOrderAndSideEffects(t *testing.T) {
	r := newTestRegistry(t, "a")
	illegal := Declaration{Name: "g", Params: []Param{{Type: "missing", Default: true}, {Type: "missing2"}}}
	err := r.AddDeclaration(illegal)
	var regErr *RegistrationError
	if !errors.As(err, &regErr) || regErr.Kind != ErrUndefinedType {
		t.Fatalf("expected undefined type, got %v", err)
	}
	badDefault := decl(Param{Type: "a", Default: true}, Param{Type: "a"})
	err = r.AddDeclaration(badDefault)
	if !errors.As(err, &regErr) || regErr.Kind != ErrIllegalSignature {
		t.Fatalf("expected illegal signature, got %v", err)
	}
	good := decl(Param{Type: "a"})
	mustAddDeclaration(t, r, good)
	err = r.AddDeclaration(good)
	if !errors.As(err, &regErr) || regErr.Kind != ErrDuplicate {
		t.Fatalf("expected duplicate, got %v", err)
	}
	if report := r.Resolve("f", call("a")); len(report.Candidates) != 1 {
		t.Fatalf("rejected declarations changed candidates: %+v", report)
	}
}

func TestPromotionCycle(t *testing.T) {
	r := newTestRegistry(t, "a", "b", "c")
	mustAddPromotion(t, r, "a", "b")
	mustAddPromotion(t, r, "b", "c")
	err := r.AddPromotion("c", "a")
	var regErr *RegistrationError
	if !errors.As(err, &regErr) || regErr.Kind != ErrPromotionCycle {
		t.Fatalf("expected cycle error, got %v", err)
	}
	mustAddUser(t, r, "c", "a")
}

func TestRegistrationOrderDeterminismAndConcurrency(t *testing.T) {
	types := []TypeID{"x", "p", "u"}
	first := func(r *Registry) {
		mustAddPromotion(t, r, "x", "p")
		mustAddUser(t, r, "x", "u")
		mustAddDeclaration(t, r, decl(Param{Type: "p"}))
		mustAddDeclaration(t, r, decl(Param{Type: "u"}))
	}
	second := func(r *Registry) {
		mustAddDeclaration(t, r, decl(Param{Type: "u"}))
		mustAddDeclaration(t, r, decl(Param{Type: "p"}))
		mustAddUser(t, r, "x", "u")
		mustAddPromotion(t, r, "x", "p")
	}
	r1 := newTestRegistry(t, types...)
	r2 := newTestRegistry(t, types...)
	first(r1)
	second(r2)
	got1 := r1.Resolve("f", call("x"))
	got2 := r2.Resolve("f", call("x"))
	if got1.Ambiguous != got2.Ambiguous || len(got1.AmbiguousCandidates) != len(got2.AmbiguousCandidates) {
		t.Fatalf("order changed resolution: %+v vs %+v", got1, got2)
	}

	r := newTestRegistry(t, types...)
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = r.Resolve("f", call("x"))
		}()
		go func() {
			defer wg.Done()
			_ = r.AddDeclaration(decl(Param{Type: TypeID(fmt.Sprint(0))}))
		}()
	}
	wg.Wait()
}

func TestLoggerReceivesInputAndBasis(t *testing.T) {
	var buffer bytes.Buffer
	r := NewRegistry(WithLogWriter(&buffer))
	if err := r.DefineType("a"); err != nil {
		t.Fatal(err)
	}
	mustAddDeclaration(t, r, decl(Param{Type: "a"}))
	r.Resolve("f", call("a"))
	log := buffer.String()
	if !bytes.Contains([]byte(log), []byte("DEFINE_TYPE")) || !bytes.Contains([]byte(log), []byte("RESOLVE")) || !bytes.Contains([]byte(log), []byte("basis=")) {
		t.Fatalf("log missing input or basis: %q", log)
	}
}
