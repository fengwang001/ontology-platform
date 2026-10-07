package consteval

import (
	"fmt"
	"sync"
	"testing"
)

func TestRegisterBasics(t *testing.T) {
	reg := NewRegistry()
	// Untyped registration preserves the kind, even for integral rationals.
	if _, err := reg.Register("r", "", RatLit("4/2")); err != nil {
		t.Fatal(err)
	}
	v, ok := reg.Lookup("r")
	if !ok || v.KindOf() != RatKind || v.Type() != NoType {
		t.Fatalf("r = %v, want untyped rat", v)
	}
	if v.EffectiveType() != Float64 {
		t.Fatalf("default type of rat = %v, want float64", v.EffectiveType())
	}
	// Typed registration converts and fixes the concrete type.
	if _, err := reg.Register("x", "int8", IntLit("10")); err != nil {
		t.Fatal(err)
	}
	v, _ = reg.Lookup("x")
	if v.Type() != Int8 || v.Int().Int64() != 10 {
		t.Fatalf("x = %v, want int8 10", v)
	}
	// References resolve to the fixed value.
	v, err := Eval(Add(NameRef("x"), IntLit("5")), reg)
	if err != nil || v.Type() != Int8 || v.Int().Int64() != 15 {
		t.Fatalf("x+5 = %v, %v", v, err)
	}
}

func TestRegisterRejections(t *testing.T) {
	reg := NewRegistry()
	if _, err := reg.Register("ok", "", IntLit("1")); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, typ string
		expr      *Expr
		want      ErrKind
	}{
		{"", "", IntLit("1"), ErrInvalidArgument},          // empty name
		{"a", "int128", IntLit("1"), ErrInvalidArgument},   // unknown type name
		{"a", "", nil, ErrInvalidArgument},                 // nil tree
		{"ok", "", IntLit("2"), ErrDuplicateName},          // already registered
		{"b", "", NameRef("ghost"), ErrUnknownName},        // unknown reference
		{"c", "", Quo(IntLit("1"), IntLit("0")), ErrDivByZero},
		{"d", "int8", IntLit("200"), ErrOverflow}, // not representable
	}
	before := reg.Len()
	for _, c := range cases {
		if _, err := reg.Register(c.name, c.typ, c.expr); errKind(t, err) != c.want {
			t.Fatalf("Register(%q, %q): got %v, want kind %v", c.name, c.typ, err, c.want)
		}
	}
	// Rejected registrations change no state.
	if reg.Len() != before {
		t.Fatalf("registry grew from %d to %d after rejected registrations", before, reg.Len())
	}
	// A name whose registration failed is free to use again.
	if _, err := reg.Register("d", "int8", IntLit("20")); err != nil {
		t.Fatalf("re-register after rejection: %v", err)
	}
}

// Priority: invalid argument > duplicate name > unknown name > evaluation.
func TestRegisterPriority(t *testing.T) {
	reg := NewRegistry()
	if _, err := reg.Register("dup", "", IntLit("1")); err != nil {
		t.Fatal(err)
	}
	// Duplicate beats unknown name inside the expression.
	if _, err := reg.Register("dup", "", NameRef("ghost")); errKind(t, err) != ErrDuplicateName {
		t.Fatalf("got %v, want duplicate name", err)
	}
	// Invalid argument beats duplicate.
	if _, err := reg.Register("dup", "not-a-type", IntLit("1")); errKind(t, err) != ErrInvalidArgument {
		t.Fatalf("got %v, want invalid argument", err)
	}
	// Unknown name beats evaluation errors.
	if _, err := reg.Register("e", "", Quo(NameRef("ghost"), IntLit("0"))); errKind(t, err) != ErrUnknownName {
		t.Fatalf("got %v, want unknown name", err)
	}
}

// A registered constant's value is fixed at registration time; references
// never re-evaluate the defining expression.
func TestValueFixedAtRegistration(t *testing.T) {
	reg := NewRegistry()
	if _, err := reg.Register("c0", "", IntLit("1")); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Register("c1", "", Add(NameRef("c0"), IntLit("1"))); err != nil {
		t.Fatal(err)
	}
	v, err := Eval(NameRef("c1"), reg)
	if err != nil || v.Int().Int64() != 2 {
		t.Fatalf("c1 = %v, %v; want 2", v, err)
	}
}

// Reference resolution costs exactly one map lookup per reference node,
// independent of the number of registered constants and of the depth of
// the reference chain that produced the referenced constant. The
// LookupCount counter makes this externally verifiable.
func TestLookupCostIndependentOfSizeAndChainDepth(t *testing.T) {
	reg := NewRegistry()
	const depth = 1000
	if _, err := reg.Register("chain0", "", IntLit("0")); err != nil {
		t.Fatal(err)
	}
	for i := 1; i < depth; i++ {
		name := fmt.Sprintf("chain%d", i)
		prev := fmt.Sprintf("chain%d", i-1)
		if _, err := reg.Register(name, "", Add(NameRef(prev), IntLit("1"))); err != nil {
			t.Fatal(err)
		}
	}
	// Resolving the head of a 1000-deep chain: one lookup in the reference
	// pre-scan plus one during evaluation, no chain walking.
	before := reg.LookupCount()
	v, err := Eval(NameRef(fmt.Sprintf("chain%d", depth-1)), reg)
	if err != nil {
		t.Fatal(err)
	}
	if got := reg.LookupCount() - before; got != 2 {
		t.Fatalf("evaluating one reference took %d lookups, want 2", got)
	}
	if v.Int().Int64() != int64(depth-1) {
		t.Fatalf("chain head = %v, want %d", v, depth-1)
	}
	// A direct Lookup is exactly one map access regardless of registry size.
	before = reg.LookupCount()
	if _, ok := reg.Lookup("chain0"); !ok {
		t.Fatal("chain0 missing")
	}
	if got := reg.LookupCount() - before; got != 1 {
		t.Fatalf("Lookup took %d map accesses, want 1", got)
	}
}

// Concurrent registration and evaluation must behave as if executed in
// some serial order. Run with -race.
func TestConcurrentRegisterAndEval(t *testing.T) {
	reg := NewRegistry()
	if _, err := reg.Register("base", "", IntLit("1")); err != nil {
		t.Fatal(err)
	}
	const workers = 32
	var wg sync.WaitGroup
	errs := make(chan error, workers*3)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			name := fmt.Sprintf("c%d", w)
			if _, err := reg.Register(name, "", Add(NameRef("base"), IntLit(fmt.Sprint(w)))); err != nil {
				errs <- fmt.Errorf("register %s: %w", name, err)
				return
			}
			// Every registered constant must be visible to later readers
			// with its fixed value w+1.
			v, err := Eval(NameRef(name), reg)
			if err != nil {
				errs <- fmt.Errorf("eval %s: %w", name, err)
				return
			}
			if v.Int().Int64() != int64(w+1) {
				errs <- fmt.Errorf("%s = %d, want %d", name, v.Int().Int64(), w+1)
			}
		}(w)
	}
	// Concurrent duplicate registrations: exactly one succeeds.
	var once sync.WaitGroup
	for w := 0; w < workers; w++ {
		once.Add(1)
		go func() {
			defer once.Done()
			_, err := reg.Register("contended", "", IntLit("7"))
			errs <- err // nil for the single winner, ErrDuplicateName otherwise
		}()
	}
	wg.Wait()
	once.Wait()
	close(errs)
	successes := 0
	for err := range errs {
		if err == nil {
			successes++
			continue
		}
		var e *Error
		if !asError(err, &e) || e.Kind != ErrDuplicateName {
			t.Fatalf("unexpected concurrent error: %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("contended registration succeeded %d times, want exactly 1", successes)
	}
	if reg.Len() != workers+2 { // base + c0..c31 + contended
		t.Fatalf("registry size = %d, want %d", reg.Len(), workers+2)
	}
}

func asError(err error, target **Error) bool {
	for err != nil {
		if e, ok := err.(*Error); ok {
			*target = e
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
