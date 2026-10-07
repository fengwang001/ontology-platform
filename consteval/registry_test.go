package consteval

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestRegisterAndLookup(t *testing.T) {
	r := NewRegistry()
	if _, err := r.Register("a", IntLit("1"), ""); err != nil {
		t.Fatalf("Register a: %v", err)
	}
	// b references the already-registered a.
	if _, err := r.Register("b", Bin(OpAdd, Name("a"), IntLit("1")), ""); err != nil {
		t.Fatalf("Register b: %v", err)
	}
	v, err := r.Lookup("b")
	if err != nil {
		t.Fatalf("Lookup b: %v", err)
	}
	wantUntypedInt(t, v, "2")

	// The value is fixed at registration: evaluating a reference later
	// returns the stored value, not a recomputation.
	v, err = r.Eval(Name("b"))
	if err != nil {
		t.Fatalf("Eval name(b): %v", err)
	}
	wantUntypedInt(t, v, "2")

	// Expressions may only reference registered names.
	if _, err := r.Eval(Bin(OpAdd, Name("b"), IntLit("10"))); err != nil {
		t.Fatalf("Eval b+10: %v", err)
	}
}

func TestRegisterErrorPriorities(t *testing.T) {
	r := NewRegistry()
	if _, err := r.Register("a", IntLit("1"), ""); err != nil {
		t.Fatalf("Register a: %v", err)
	}

	// Empty name: invalid argument.
	if _, err := r.Register("", IntLit("1"), ""); errKindOf(err) != ErrInvalidArgument {
		t.Fatalf("empty name: got %v, want invalid argument", err)
	}
	// Invalid argument outranks duplicate.
	if _, err := r.Register("", Name("missing"), ""); errKindOf(err) != ErrInvalidArgument {
		t.Fatalf("empty name + unknown ref: got %v, want invalid argument", err)
	}
	// Duplicate outranks unknown name.
	if _, err := r.Register("a", Name("missing"), ""); errKindOf(err) != ErrDuplicateName {
		t.Fatalf("duplicate + unknown ref: got %v, want duplicate", err)
	}
	// Unknown name outranks evaluation errors.
	if _, err := r.Register("c", Bin(OpAdd, Name("missing"), Bin(OpDiv, IntLit("1"), IntLit("0"))), ""); errKindOf(err) != ErrUnknownName {
		t.Fatalf("unknown + div0: got %v, want unknown name", err)
	}
	// Unknown type name is invalid argument.
	if _, err := r.Register("d", IntLit("1"), "int7"); errKindOf(err) != ErrInvalidArgument {
		t.Fatalf("unknown type: got %v, want invalid argument", err)
	}
	// Evaluation error surfaces when nothing higher-priority applies.
	if _, err := r.Register("e", Bin(OpDiv, IntLit("1"), IntLit("0")), ""); errKindOf(err) != ErrDivByZero {
		t.Fatalf("div0: got %v, want division by zero", err)
	}

	// Rejected registrations change no state: only "a" exists.
	if r.Len() != 1 {
		t.Fatalf("registry size = %d, want 1 (rejected registrations must not stick)", r.Len())
	}
	for _, n := range []string{"c", "d", "e"} {
		if _, err := r.Lookup(n); errKindOf(err) != ErrUnknownName {
			t.Fatalf("Lookup(%q): got %v, want unknown name", n, err)
		}
	}
}

func TestRegisterTypedAndDefaultType(t *testing.T) {
	r := NewRegistry()

	// Explicit type: value must be representable.
	if _, err := r.Register("ti", IntLit("100"), "int8"); err != nil {
		t.Fatalf("Register ti: %v", err)
	}
	v, _ := r.Lookup("ti")
	wantTypedInt(t, v, TypeInt8, 100)

	if _, err := r.Register("to", IntLit("200"), "int8"); errKindOf(err) != ErrOverflow {
		t.Fatalf("200 as int8: got %v, want overflow", err)
	}
	if _, err := r.Register("tt", RatLit("5/2"), "int8"); errKindOf(err) != ErrTruncation {
		t.Fatalf("5/2 as int8: got %v, want truncation", err)
	}

	// No type: the constant stays untyped but must be representable in
	// the default type of its kind.
	if _, err := r.Register("ok", IntLit("9223372036854775807"), ""); err != nil {
		t.Fatalf("Register ok: %v", err)
	}
	v, _ = r.Lookup("ok")
	if !v.Untyped() || v.Kind() != KindInt {
		t.Fatalf("untyped declaration must stay untyped, got %s", v)
	}
	if _, err := r.Register("big", IntLit("9223372036854775808"), ""); errKindOf(err) != ErrOverflow {
		t.Fatalf("2^63 untyped declaration: got %v, want overflow (default int64)", err)
	}
	// Rationals default to float64: 10^400 rounds to infinity.
	if _, err := r.Register("hr", RatLit("1e400"), ""); errKindOf(err) != ErrOverflow {
		t.Fatalf("1e400 untyped declaration: got %v, want overflow (default float64)", err)
	}
	if _, err := r.Register("s", StrLit("x"), ""); err != nil {
		t.Fatalf("Register s: %v", err)
	}

	// A typed expression result is stored typed even without typeName.
	if _, err := r.Register("tc", Convert(IntLit("5"), "uint16"), ""); err != nil {
		t.Fatalf("Register tc: %v", err)
	}
	v, _ = r.Lookup("tc")
	wantTypedUint(t, v, TypeUint16, 5)

	// References to typed constants behave as typed operands.
	v, err := r.Eval(Bin(OpAdd, Name("ti"), IntLit("1")))
	if err != nil {
		t.Fatalf("Eval ti+1: %v", err)
	}
	wantTypedInt(t, v, TypeInt8, 101)
	if _, err := r.Eval(Bin(OpAdd, Name("ti"), Convert(IntLit("1"), "int16"))); errKindOf(err) != ErrTypeMismatch {
		t.Fatalf("int8 + int16: got %v, want type mismatch", err)
	}
}

// Concurrent registration, evaluation and lookup behave as if executed
// in some serial order.
func TestConcurrentRegisterEval(t *testing.T) {
	r := NewRegistry()
	if _, err := r.Register("base", IntLit("1"), ""); err != nil {
		t.Fatalf("Register base: %v", err)
	}

	const workers = 8
	const perWorker = 200
	var wg sync.WaitGroup
	errs := make(chan error, workers*perWorker)
	for g := 0; g < workers; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				name := fmt.Sprintf("g%d_c%d", g, i)
				expr := Bin(OpAdd, Name("base"), IntLit(fmt.Sprint(i)))
				if _, err := r.Register(name, expr, ""); err != nil {
					errs <- fmt.Errorf("Register(%s): %w", name, err)
					return
				}
				v, err := r.Eval(Bin(OpMul, Name(name), IntLit("2")))
				if err != nil {
					errs <- fmt.Errorf("Eval(2*%s): %w", name, err)
					return
				}
				if got := v.Int().String(); got != fmt.Sprint(2*(1+i)) {
					errs <- fmt.Errorf("Eval(2*%s) = %s, want %d", name, got, 2*(1+i))
					return
				}
			}
		}(g)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if want := 1 + workers*perWorker; r.Len() != want {
		t.Fatalf("registry size = %d, want %d", r.Len(), want)
	}
	// Every registered constant has its deterministic value.
	for g := 0; g < workers; g++ {
		for i := 0; i < perWorker; i++ {
			v, err := r.Lookup(fmt.Sprintf("g%d_c%d", g, i))
			if err != nil {
				t.Fatal(err)
			}
			if got := v.Int().String(); got != fmt.Sprint(1+i) {
				t.Fatalf("g%d_c%d = %s, want %d", g, i, got, 1+i)
			}
		}
	}
}

// Referencing a constant is a single map lookup: its cost does not grow
// with the number of registered constants or the reference-chain depth
// (values are fixed at registration, so no chain is ever walked).
func TestLookupFlatCost(t *testing.T) {
	r := NewRegistry()
	const n = 100000
	if _, err := r.Register("c0", IntLit("1"), ""); err != nil {
		t.Fatal(err)
	}
	// Build a reference chain 100k deep: c_i = c_{i-1} + 1.
	for i := 1; i < n; i++ {
		if _, err := r.Register(fmt.Sprintf("c%d", i),
			Bin(OpAdd, Name(fmt.Sprintf("c%d", i-1)), IntLit("1")), ""); err != nil {
			t.Fatal(err)
		}
	}
	// Evaluating a reference to the deepest constant must be O(1):
	// 100k evaluations of a 100k-deep chain would take ~10^10 steps if
	// chains were walked; a flat lookup finishes instantly.
	deepest := Name(fmt.Sprintf("c%d", n-1))
	start := time.Now()
	const iters = 100000
	for i := 0; i < iters; i++ {
		v, err := r.Eval(deepest)
		if err != nil {
			t.Fatal(err)
		}
		if got := v.Int().String(); got != "100000" {
			t.Fatalf("deepest = %s, want 100000", got)
		}
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("%d lookups of a %d-deep chain took %v; cost grows with chain depth", iters, n, elapsed)
	}
}

// BenchmarkLookupByRegistrySize demonstrates that lookup cost is
// independent of the number of registered constants.
func BenchmarkLookupByRegistrySize(b *testing.B) {
	for _, n := range []int{100, 10000, 1000000} {
		r := NewRegistry()
		for i := 0; i < n; i++ {
			if _, err := r.Register(fmt.Sprintf("c%d", i), IntLit(fmt.Sprint(i)), ""); err != nil {
				b.Fatal(err)
			}
		}
		expr := Name(fmt.Sprintf("c%d", n-1))
		b.Run(fmt.Sprintf("size=%d", n), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				if _, err := r.Eval(expr); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
