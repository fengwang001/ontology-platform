package unwind

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"testing"
)

// naiveThrow is the deliberately simple step-by-step reference model:
// walk frames top-down, the top frame uses p=pc, deeper frames use
// p=pc-1, and the first entry in declaration order with start<=p<end
// and (type==0 or type==t) wins. It returns the hit frame index, the
// handler, and the resulting stack (an unchanged copy on a miss).
func naiveThrow(tables map[string][]Entry, stack []Frame, t int) (int, int, []Frame, bool) {
	for i := len(stack) - 1; i >= 0; i-- {
		p := stack[i].PC
		if i != len(stack)-1 {
			p--
		}
		for _, e := range tables[stack[i].Func] {
			if e.Start <= p && p < e.End && (e.Type == 0 || e.Type == t) {
				ns := make([]Frame, i+1)
				copy(ns, stack[:i+1])
				ns[i].PC = e.Handler
				return i, e.Handler, ns, true
			}
		}
	}
	ns := make([]Frame, len(stack))
	copy(ns, stack)
	return -1, -1, ns, false
}

func mustRegister(t *testing.T, u *Unwinder, name string, entries []Entry) {
	t.Helper()
	if err := u.Register(name, entries); err != nil {
		t.Fatalf("Register(%q) failed: %v", name, err)
	}
}

func mustPush(t *testing.T, u *Unwinder, fn string, pc int) {
	t.Helper()
	if err := u.Push(fn, pc); err != nil {
		t.Fatalf("Push(%q, %d) failed: %v", fn, pc, err)
	}
}

// A non-top frame's pc is a return address, so the lookup point is pc-1.
// Return address exactly equal to the range end means the call
// instruction sits at end-1, still inside [start, end): must hit.
func TestReturnAddressAtRangeEndHits(t *testing.T) {
	u := New()
	mustRegister(t, u, "outer", []Entry{{Start: 10, End: 20, Handler: 99, Type: 1}})
	mustRegister(t, u, "inner", nil)
	mustPush(t, u, "outer", 20) // return address: call instruction at 19
	mustPush(t, u, "inner", 5)  // top frame, no table entries

	before := u.Stack()
	idx, handler, err := u.Throw(1)
	t.Logf("input: stack=%v throw(1); call site p=pc-1=19 in [10,20)", before)
	if err != nil {
		t.Fatalf("Throw failed: %v", err)
	}
	if idx != 0 || handler != 99 {
		t.Fatalf("got (idx=%d, handler=%d), want (0, 99)", idx, handler)
	}
	want := []Frame{{Func: "outer", PC: 99}}
	if got := u.Stack(); !reflect.DeepEqual(got, want) {
		t.Fatalf("stack = %v, want %v", got, want)
	}
	t.Logf("output: idx=%d handler=%d stack=%v; hit as expected", idx, handler, u.Stack())
}

// Return address exactly equal to the range start means the call
// instruction sits at start-1, outside [start, end): must not hit.
func TestReturnAddressAtRangeStartMisses(t *testing.T) {
	u := New()
	mustRegister(t, u, "outer", []Entry{{Start: 10, End: 20, Handler: 99, Type: 1}})
	mustRegister(t, u, "inner", nil)
	mustPush(t, u, "outer", 10) // call instruction at 9, before the range
	mustPush(t, u, "inner", 5)

	before := u.Stack()
	_, _, err := u.Throw(1)
	t.Logf("input: stack=%v throw(1); call site p=pc-1=9 not in [10,20)", before)
	if !errors.Is(err, ErrUncaught) {
		t.Fatalf("err = %v, want ErrUncaught", err)
	}
	if got := u.Stack(); !reflect.DeepEqual(got, before) {
		t.Fatalf("stack changed on uncaught: %v -> %v", before, got)
	}
	t.Logf("output: uncaught, stack unchanged; miss as expected")
}

// Type 0 matches any type. Declaration order decides which entry wins
// when several entries cover the same point.
func TestDeclarationOrderWildcardVsConcrete(t *testing.T) {
	// Wildcard declared first: it catches even a concrete type.
	u := New()
	mustRegister(t, u, "f", []Entry{
		{Start: 0, End: 100, Handler: 101, Type: 0},
		{Start: 0, End: 100, Handler: 102, Type: 7},
	})
	mustPush(t, u, "f", 50)
	idx, handler, err := u.Throw(7)
	if err != nil {
		t.Fatalf("Throw failed: %v", err)
	}
	t.Logf("input: entries [wildcard h=1, concrete h=2], p=50, t=7; first declared wins")
	if idx != 0 || handler != 101 {
		t.Fatalf("got (%d, %d), want (0, 101): wildcard first must win", idx, handler)
	}
	t.Logf("output: idx=%d handler=%d; wildcard entry won", idx, handler)

	// Concrete declared first: it wins for its type, wildcard takes the rest.
	u2 := New()
	mustRegister(t, u2, "f", []Entry{
		{Start: 0, End: 100, Handler: 102, Type: 7},
		{Start: 0, End: 100, Handler: 101, Type: 0},
	})
	mustPush(t, u2, "f", 50)
	if _, handler, err = u2.Throw(7); err != nil || handler != 102 {
		t.Fatalf("t=7: got handler=%d err=%v, want handler=102", handler, err)
	}
	mustPush(t, u2, "f", 50)
	if _, handler, err = u2.Throw(3); err != nil || handler != 101 {
		t.Fatalf("t=3: got handler=%d err=%v, want handler=101 (wildcard)", handler, err)
	}
	t.Logf("output: concrete-first wins for t=7, wildcard catches t=3; order respected")
}

// Nested ranges: the entry declared first wins regardless of nesting.
func TestDeclarationOrderInnerVsOuter(t *testing.T) {
	// Inner range declared first.
	u := New()
	mustRegister(t, u, "f", []Entry{
		{Start: 12, End: 15, Handler: 111, Type: 0},
		{Start: 10, End: 20, Handler: 222, Type: 0},
	})
	mustPush(t, u, "f", 13)
	_, handler, err := u.Throw(1)
	if err != nil {
		t.Fatalf("Throw failed: %v", err)
	}
	t.Logf("input: entries [inner [12,15) h=111, outer [10,20) h=222], p=13")
	if handler != 111 {
		t.Fatalf("handler = %d, want 111: inner declared first must win", handler)
	}
	t.Logf("output: handler=%d; inner-first order respected", handler)

	// Outer range declared first.
	u2 := New()
	mustRegister(t, u2, "f", []Entry{
		{Start: 10, End: 20, Handler: 222, Type: 0},
		{Start: 12, End: 15, Handler: 111, Type: 0},
	})
	mustPush(t, u2, "f", 13)
	if _, handler, err = u2.Throw(1); err != nil || handler != 222 {
		t.Fatalf("got handler=%d err=%v, want handler=222: outer declared first must win", handler, err)
	}
	t.Logf("output: handler=%d; outer-first order respected", handler)
}

// Unwinding past several frames truncates the stack to the catching
// frame and resumes it at the handler address.
func TestMultiLevelUnwindTruncates(t *testing.T) {
	u := New()
	mustRegister(t, u, "bottom", []Entry{{Start: 0, End: 50, Handler: 142, Type: 5}})
	mustRegister(t, u, "mid", []Entry{{Start: 0, End: 10, Handler: 17, Type: 9}}) // wrong type
	mustRegister(t, u, "top", []Entry{{Start: 60, End: 70, Handler: 8, Type: 5}}) // wrong range
	mustPush(t, u, "bottom", 30)                                                  // call site 29 in [0,50), type 5 matches
	mustPush(t, u, "mid", 5)                                                      // call site 4 in range but type 9 != 5
	mustPush(t, u, "top", 65)                                                     // top pc 65 in [60,70) but... see below

	// top's own table covers pc 65 with type 5: it must catch first.
	idx, handler, err := u.Throw(5)
	if err != nil {
		t.Fatalf("Throw failed: %v", err)
	}
	if idx != 2 || handler != 8 {
		t.Fatalf("got (%d, %d), want (2, 8): top frame should catch", idx, handler)
	}
	t.Logf("output: top caught at idx=%d handler=%d", idx, handler)

	// Rebuild and throw type 5 where only bottom matches.
	u2 := New()
	mustRegister(t, u2, "bottom", []Entry{{Start: 0, End: 50, Handler: 142, Type: 5}})
	mustRegister(t, u2, "mid", []Entry{{Start: 0, End: 10, Handler: 17, Type: 9}})
	mustRegister(t, u2, "top", nil)
	mustPush(t, u2, "bottom", 30)
	mustPush(t, u2, "mid", 5)
	mustPush(t, u2, "top", 65)

	idx, handler, err = u2.Throw(5)
	if err != nil {
		t.Fatalf("Throw failed: %v", err)
	}
	t.Logf("input: 3 frames, only bottom matches (p=29 in [0,50), type 5)")
	if idx != 0 || handler != 142 {
		t.Fatalf("got (%d, %d), want (0, 142)", idx, handler)
	}
	want := []Frame{{Func: "bottom", PC: 142}}
	if got := u2.Stack(); !reflect.DeepEqual(got, want) {
		t.Fatalf("stack = %v, want %v: frames above must be popped", got, want)
	}
	t.Logf("output: idx=%d handler=%d stack=%v; truncated to catching frame", idx, handler, u2.Stack())
}

// An uncaught exception leaves the stack exactly as it was.
func TestUncaughtLeavesStackUnchanged(t *testing.T) {
	u := New()
	mustRegister(t, u, "a", []Entry{{Start: 0, End: 10, Handler: 11, Type: 2}})
	mustRegister(t, u, "b", nil)
	mustPush(t, u, "a", 50) // outside [0,10)
	mustPush(t, u, "b", 3)

	before := u.Stack()
	_, _, err := u.Throw(2)
	if !errors.Is(err, ErrUncaught) {
		t.Fatalf("err = %v, want ErrUncaught", err)
	}
	if got := u.Stack(); !reflect.DeepEqual(got, before) {
		t.Fatalf("stack changed on uncaught: %v -> %v", before, got)
	}
	t.Logf("input: stack=%v throw(2); no entry matches", before)
	t.Logf("output: ErrUncaught, stack unchanged")
}

// Registration validation: name first, then entries in declaration
// order; within one entry: range, handler-in-range, negative type.
func TestRegisterValidationAndPriority(t *testing.T) {
	u := New()
	mustRegister(t, u, "f", []Entry{{Start: 0, End: 10, Handler: 20, Type: 1}})

	// Existing name beats invalid entries (name checked first).
	err := u.Register("f", []Entry{{Start: 5, End: 5}})
	if !errors.Is(err, ErrFunctionExists) {
		t.Fatalf("err = %v, want ErrFunctionExists", err)
	}
	t.Logf("input: re-register existing name with bad entry; output: %v (name checked first)", err)

	// Declaration order: entry 0 is fine, entry 1 has start >= end.
	err = u.Register("g", []Entry{
		{Start: 0, End: 10, Handler: 20, Type: 0},
		{Start: 7, End: 7, Handler: 30, Type: -1},
	})
	if !errors.Is(err, ErrInvalidRange) {
		t.Fatalf("err = %v, want ErrInvalidRange", err)
	}
	t.Logf("input: entry1 start==end (type also negative); output: %v (range checked first)", err)

	// Handler inside its own range beats negative type in the same entry.
	err = u.Register("h", []Entry{{Start: 0, End: 10, Handler: 5, Type: -1}})
	if !errors.Is(err, ErrHandlerInRange) {
		t.Fatalf("err = %v, want ErrHandlerInRange", err)
	}
	t.Logf("input: handler 5 in [0,10), type -1; output: %v (handler checked before type)", err)

	// Handler at the range boundary is fine; negative type reported.
	err = u.Register("i", []Entry{{Start: 0, End: 10, Handler: 10, Type: -2}})
	if !errors.Is(err, ErrNegativeType) {
		t.Fatalf("err = %v, want ErrNegativeType", err)
	}

	// Rejected registrations must not create table entries.
	for _, name := range []string{"g", "h", "i"} {
		if _, ok := u.Entries(name); ok {
			t.Fatalf("rejected registration left table for %q", name)
		}
	}
	// Handler exactly at the (exclusive) end boundary is accepted.
	mustRegister(t, u, "ok", []Entry{
		{Start: 0, End: 10, Handler: 10, Type: 0},
		{Start: 20, End: 30, Handler: 19, Type: 1},
	})
	t.Logf("output: rejected registrations left no tables behind")
}

// Push validation: unknown function, negative pc, and a non-bottom
// frame with pc 0 (a return address can never be 0). Rejected pushes
// leave the stack unchanged.
func TestPushValidation(t *testing.T) {
	u := New()
	mustRegister(t, u, "f", nil)

	if err := u.Push("ghost", 1); !errors.Is(err, ErrFunctionNotFound) {
		t.Fatalf("err = %v, want ErrFunctionNotFound", err)
	}
	if err := u.Push("f", -1); !errors.Is(err, ErrNegativePC) {
		t.Fatalf("err = %v, want ErrNegativePC", err)
	}
	mustPush(t, u, "f", 0) // bottom frame may have pc 0
	if err := u.Push("f", 0); !errors.Is(err, ErrZeroReturnPC) {
		t.Fatalf("err = %v, want ErrZeroReturnPC", err)
	}
	want := []Frame{{Func: "f", PC: 0}}
	if got := u.Stack(); !reflect.DeepEqual(got, want) {
		t.Fatalf("stack = %v, want %v: rejected pushes must not stick", got, want)
	}
	t.Logf("input: push ghost/negative/zero-return; output: rejected, stack=%v unchanged", u.Stack())
}

// Throw validation: non-positive type and empty stack are rejected
// before any lookup happens.
func TestThrowValidation(t *testing.T) {
	u := New()
	mustRegister(t, u, "f", []Entry{{Start: 0, End: 10, Handler: 11, Type: 0}})

	if _, _, err := u.Throw(1); !errors.Is(err, ErrEmptyStack) {
		t.Fatalf("err = %v, want ErrEmptyStack", err)
	}
	mustPush(t, u, "f", 5)
	for _, bad := range []int{0, -3} {
		if _, _, err := u.Throw(bad); !errors.Is(err, ErrNonPositiveType) {
			t.Fatalf("Throw(%d): err = %v, want ErrNonPositiveType", bad, err)
		}
	}
	// Type validation happens even on an empty stack.
	u2 := New()
	if _, _, err := u2.Throw(0); !errors.Is(err, ErrNonPositiveType) {
		t.Fatalf("err = %v, want ErrNonPositiveType", err)
	}
	want := []Frame{{Func: "f", PC: 5}}
	if got := u.Stack(); !reflect.DeepEqual(got, want) {
		t.Fatalf("stack = %v, want %v: rejected throws must not unwind", got, want)
	}
	t.Logf("input: throw(0)/throw(-3)/throw on empty stack; output: rejected, stack unchanged")
}

// Concurrent registration of the same name must succeed exactly once.
func TestConcurrentSameNameRegistration(t *testing.T) {
	const goroutines = 32
	u := New()
	var wg sync.WaitGroup
	var mu sync.Mutex
	var successes, duplicates int
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			err := u.Register("f", []Entry{{Start: 0, End: 10, Handler: 20, Type: 0}})
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				successes++
			case errors.Is(err, ErrFunctionExists):
				duplicates++
			default:
				t.Errorf("unexpected error: %v", err)
			}
		}(g)
	}
	wg.Wait()
	t.Logf("input: %d concurrent Register(\"f\"); output: %d success, %d duplicate", goroutines, successes, duplicates)
	if successes != 1 || duplicates != goroutines-1 {
		t.Fatalf("successes=%d duplicates=%d, want exactly 1 success", successes, duplicates)
	}
}

// Concurrent mixed operations behave like some serial execution: the
// final state must be reproducible by a serial replay of the operations
// that actually took effect. Here we simply assert structural
// invariants under -race and that no goroutine sees a torn state.
func TestConcurrentMixedOperations(t *testing.T) {
	u := New()
	mustRegister(t, u, "f", []Entry{{Start: 0, End: 1000, Handler: 1500, Type: 0}})
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			name := fmt.Sprintf("fn-%d", g%4)
			_ = u.Register(name, []Entry{{Start: 0, End: 100, Handler: 200, Type: 1}})
			for i := 0; i < 50; i++ {
				_ = u.Push("f", i+1)
				_, _, _ = u.Throw(1)
				_ = u.Stack()
				_, _ = u.Entries("f")
			}
		}(g)
	}
	wg.Wait()
	// Every frame on the stack must reference a registered function.
	for _, fr := range u.Stack() {
		if _, ok := u.Entries(fr.Func); !ok {
			t.Fatalf("frame references unregistered function %q", fr.Func)
		}
	}
	t.Logf("output: mixed concurrent ops completed, stack depth=%d, invariants hold", len(u.Stack()))
}

// Replaying the same serial operation sequence on a fresh Unwinder must
// reproduce identical unwind results and stack states.
func TestReplayDeterminism(t *testing.T) {
	run := func() ([]Frame, [][3]int) {
		u := New()
		mustRegister(t, u, "a", []Entry{{Start: 0, End: 100, Handler: 110, Type: 0}})
		mustRegister(t, u, "b", []Entry{{Start: 50, End: 60, Handler: 22, Type: 3}})
		var results [][3]int
		mustPush(t, u, "a", 70)
		mustPush(t, u, "b", 55)
		mustPush(t, u, "a", 90)
		for _, typ := range []int{3, 1, 2} {
			idx, h, err := u.Throw(typ)
			code := 0
			if err != nil {
				code = 1
			}
			results = append(results, [3]int{idx, h, code})
			mustPush(t, u, "a", 90)
		}
		return u.Stack(), results
	}
	stack1, res1 := run()
	stack2, res2 := run()
	if !reflect.DeepEqual(stack1, stack2) || !reflect.DeepEqual(res1, res2) {
		t.Fatalf("replay diverged: %v/%v vs %v/%v", stack1, res1, stack2, res2)
	}
	t.Logf("input: fixed op sequence replayed twice; output: identical results %v stack %v", res1, stack1)
}

// Randomized differential test: drive the Unwinder and the naive
// step-by-step model with the same operation sequence and require
// identical throw results and stack states. Seed is fixed so failures
// reproduce exactly.
func TestRandomizedMatchesNaiveModel(t *testing.T) {
	rng := rand.New(rand.NewSource(20261002))
	u := New()
	tables := map[string][]Entry{}
	var model []Frame
	names := []string{"f0", "f1", "f2", "f3"}

	for _, name := range names {
		var entries []Entry
		for j := 0; j < 1+rng.Intn(4); j++ {
			start := rng.Intn(40)
			end := start + 1 + rng.Intn(20)
			handler := rng.Intn(80)
			for handler >= start && handler < end {
				handler = rng.Intn(80)
			}
			entries = append(entries, Entry{Start: start, End: end, Handler: handler, Type: rng.Intn(4)})
		}
		mustRegister(t, u, name, entries)
		tables[name] = entries
	}
	t.Logf("input tables: %v", tables)

	for step := 0; step < 500; step++ {
		if len(model) < 6 && rng.Intn(2) == 0 {
			fr := Frame{Func: names[rng.Intn(len(names))], PC: 1 + rng.Intn(70)}
			if len(model) == 0 {
				fr.PC = rng.Intn(70) // bottom frame may be 0
			}
			if err := u.Push(fr.Func, fr.PC); err != nil {
				t.Fatalf("step %d: Push(%v) failed: %v", step, fr, err)
			}
			model = append(model, fr)
			continue
		}
		if len(model) == 0 {
			continue
		}
		typ := 1 + rng.Intn(4)
		wantIdx, wantHandler, wantStack, hit := naiveThrow(tables, model, typ)
		gotIdx, gotHandler, err := u.Throw(typ)
		if hit && err != nil {
			t.Fatalf("step %d: naive hit (%d,%d) but Throw failed: %v", step, wantIdx, wantHandler, err)
		}
		if !hit && !errors.Is(err, ErrUncaught) {
			t.Fatalf("step %d: naive missed but Throw gave %v", step, err)
		}
		if hit && (gotIdx != wantIdx || gotHandler != wantHandler) {
			t.Fatalf("step %d: got (%d,%d), naive wants (%d,%d)", step, gotIdx, gotHandler, wantIdx, wantHandler)
		}
		model = wantStack
		if got := u.Stack(); !reflect.DeepEqual(got, model) {
			t.Fatalf("step %d: stack %v, naive wants %v", step, got, model)
		}
		if step == 42 {
			t.Logf("step %d sample: throw(%d) -> idx=%d handler=%d stack=%v (matches naive model)", step, typ, gotIdx, gotHandler, model)
		}
	}
	t.Logf("output: 500 randomized steps identical to naive model, final stack=%v", u.Stack())
}

// The top frame's pc is used as-is; pc exactly equal to the range end is
// outside the half-open [start, end) interval: must not hit.
func TestTopPCAtRangeEndMisses(t *testing.T) {
	u := New()
	mustRegister(t, u, "f", []Entry{{Start: 10, End: 20, Handler: 99, Type: 1}})
	mustPush(t, u, "f", 20) // top frame: p = pc = 20, half-open end excluded

	before := u.Stack()
	_, _, err := u.Throw(1)
	t.Logf("input: stack=%v throw(1); top p=pc=20 not in [10,20)", before)
	if !errors.Is(err, ErrUncaught) {
		t.Fatalf("err = %v, want ErrUncaught", err)
	}
	if got := u.Stack(); !reflect.DeepEqual(got, before) {
		t.Fatalf("stack changed on uncaught: %v -> %v", before, got)
	}
	t.Logf("output: uncaught, stack unchanged; miss as expected")
}
