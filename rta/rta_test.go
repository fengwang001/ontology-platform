package rta

import (
	"fmt"
	"testing"
)

func mustAdd(t *testing.T, a *Analyzer, tk Task, wantReorder bool) {
	t.Helper()
	re, err := a.Add(tk)
	if err != nil {
		t.Fatalf("Add(%s) unexpected error: %v", tk.ID, err)
	}
	if re != wantReorder {
		t.Fatalf("Add(%s) reordered=%v want %v; order=%v", tk.ID, re, wantReorder, a.Order())
	}
}

func resp(t *testing.T, a *Analyzer, id string) int64 {
	t.Helper()
	r, err := a.Response(id)
	if err != nil {
		t.Fatalf("Response(%s): %v", id, err)
	}
	return r
}

func errReason(t *testing.T, err error, want RejectReason) *Error {
	t.Helper()
	e, ok := err.(*Error)
	if !ok || e.Reason != want {
		t.Fatalf("error = %v, want reason %d", err, want)
	}
	return e
}

// A then B gives [A,B], R_A=1, R_B=4.
func TestSpecBasicAB(t *testing.T) {
	a := NewAnalyzer()
	mustAdd(t, a, Task{ID: "A", C: 1, T: 12, D: 10}, false)
	mustAdd(t, a, Task{ID: "B", C: 3, T: 6, D: 6}, false)
	if got := fmt.Sprint(a.Order()); got != "[A B]" {
		t.Fatalf("order = %v", got)
	}
	if r := resp(t, a, "A"); r != 1 {
		t.Fatalf("R_A = %d want 1", r)
	}
	if r := resp(t, a, "B"); r != 4 {
		t.Fatalf("R_B = %d want 4", r)
	}
}

// E fits only at position 1: [A,E,B] with R = 1,2,5, never promoted to top.
func TestSpecInsertMiddle(t *testing.T) {
	a := NewAnalyzer()
	mustAdd(t, a, Task{ID: "A", C: 1, T: 12, D: 10}, false)
	mustAdd(t, a, Task{ID: "B", C: 3, T: 6, D: 6}, false)
	mustAdd(t, a, Task{ID: "E", C: 1, T: 6, D: 2}, false)
	if got := fmt.Sprint(a.Order()); got != "[A E B]" {
		t.Fatalf("order = %v", got)
	}
	if r := resp(t, a, "A"); r != 1 || resp(t, a, "E") != 2 || resp(t, a, "B") != 5 {
		t.Fatalf("responses = %d,%d,%d want 1,2,5", resp(t, a, "A"), resp(t, a, "E"), resp(t, a, "B"))
	}
}

// C(B=2) fails all insertion slots; Audsley returns [C,B,A], reordered=true.
func TestSpecAudsleyReorder(t *testing.T) {
	a := NewAnalyzer()
	mustAdd(t, a, Task{ID: "A", C: 1, T: 12, D: 10}, false)
	mustAdd(t, a, Task{ID: "B", C: 3, T: 6, D: 6}, false)
	mustAdd(t, a, Task{ID: "C", C: 3, T: 10, D: 9, B: 2}, true)
	if got := fmt.Sprint(a.Order()); got != "[C B A]" {
		t.Fatalf("order = %v", got)
	}
	if r := resp(t, a, "C"); r != 5 || resp(t, a, "B") != 6 || resp(t, a, "A") != 10 {
		t.Fatalf("responses = %d,%d,%d want 5,6,10", resp(t, a, "C"), resp(t, a, "B"), resp(t, a, "A"))
	}
}

// Jitter of higher-priority tasks appears inside w; own jitter applies to
// the deadline test R = w + J.
func TestSpecJitter(t *testing.T) {
	a := NewAnalyzer()
	mustAdd(t, a, Task{ID: "G", C: 2, T: 10, D: 10, J: 8}, false)
	mustAdd(t, a, Task{ID: "H", C: 3, T: 7, D: 7}, false)
	if r := resp(t, a, "H"); r != 7 {
		t.Fatalf("R_H = %d want 7", r)
	}

	a2 := NewAnalyzer()
	mustAdd(t, a2, Task{ID: "G", C: 2, T: 10, D: 10}, false)
	mustAdd(t, a2, Task{ID: "H", C: 3, T: 7, D: 7}, false)
	if r := resp(t, a2, "H"); r != 5 {
		t.Fatalf("R_H(no jitter) = %d want 5", r)
	}

	a3 := NewAnalyzer()
	mustAdd(t, a3, Task{ID: "G", C: 2, T: 10, D: 10, J: 8}, false)
	_, err := a3.Add(Task{ID: "H", C: 3, T: 7, D: 7, J: 1})
	e := errReason(t, err, ReasonUnschedulable)
	// [G,H]: H w=7, R=8>7 fails; [H,G]: G w=5, R=13>10 fails. Audsley's
	// lowest level then has no feasible candidate either -> pending 2.
	if e.Pending != 2 {
		t.Fatalf("pending = %d want 2", e.Pending)
	}
	if got := a3.Order(); len(got) != 1 || got[0] != "G" {
		t.Fatalf("rejected add mutated state: %v", got)
	}
}

// w exactly D-J is schedulable; w one larger is not.
func TestBoundaryExactDeadline(t *testing.T) {
	// hp C=1,T=3 over low C=4: w = 4 -> 6 fixed.
	a := NewAnalyzer()
	mustAdd(t, a, Task{ID: "p", C: 1, T: 3, D: 3}, false)
	mustAdd(t, a, Task{ID: "q", C: 4, T: 100, D: 8, J: 2}, false) // D-J=6 == w
	if r := resp(t, a, "q"); r != 8 {
		t.Fatalf("R_q = %d want 8", r)
	}
	b := NewAnalyzer()
	mustAdd(t, b, Task{ID: "p", C: 1, T: 3, D: 3}, false)
	_, err := b.Add(Task{ID: "q", C: 4, T: 100, D: 7, J: 2}) // D-J=5 < w
	errReason(t, err, ReasonUnschedulable)
	c := NewAnalyzer()
	_, err = c.Add(Task{ID: "q", C: 6, T: 100, D: 6}) // initial w == D exact
	if err != nil {
		t.Fatal(err)
	}
	if r := resp(t, c, "q"); r != 6 {
		t.Fatalf("R_q = %d want 6", r)
	}
	d := NewAnalyzer()
	_, err = d.Add(Task{ID: "q", C: 6, T: 100, D: 5}) // initial w=6 > D
	errReason(t, err, ReasonUnschedulable)
}

// B participates in the initial iterate w = C + B.
func TestBlockingInitialValue(t *testing.T) {
	a := NewAnalyzer()
	mustAdd(t, a, Task{ID: "h", C: 1, T: 4, D: 4}, false)
	mustAdd(t, a, Task{ID: "l", C: 2, T: 100, D: 6, B: 2}, false)
	// w0=4 -> 4 + ceil(4/4) = 5 -> 4 + ceil(5/4) = 6 fixed.
	if r := resp(t, a, "l"); r != 6 {
		t.Fatalf("R_l = %d want 6", r)
	}
	b := NewAnalyzer()
	// h must be high priority: D=2 requires C_h <= 2 with itself only.
	mustAdd(t, b, Task{ID: "h", C: 2, T: 4, D: 2}, false)
	_, err := b.Add(Task{ID: "l", C: 2, T: 100, D: 5, B: 2})
	errReason(t, err, ReasonUnschedulable)
	c := NewAnalyzer()
	_, err = c.Add(Task{ID: "z", C: 3, T: 10, D: 4, B: 2}) // initial w=5 > D
	errReason(t, err, ReasonUnschedulable)
}

// All insertion slots fail; Audsley reverses the old pair (b before a) and
// at the second level breaks a tie between b and c by choosing smaller ID.
//
// a:C2/T10/D10, b:C2/T10/D8, c:C5/T10/D8; insertion of c into [a,b]:
//
//	[a,b,c] c w=9>8 fail; [a,c,b] b w=9>8 fail; [c,a,b] b w=9>8 fail.
//
// Audsley lowest: a w=9<=10 (only feasible) -> a; then b(w=7) vs c(w=7),
// smaller ID b wins -> order [c,b,a].
func TestAudsleyReversalAndSmallestID(t *testing.T) {
	a := NewAnalyzer()
	mustAdd(t, a, Task{ID: "a", C: 2, T: 10, D: 10}, false)
	mustAdd(t, a, Task{ID: "b", C: 2, T: 10, D: 8}, false)
	mustAdd(t, a, Task{ID: "c", C: 5, T: 10, D: 8}, true)
	if got := fmt.Sprint(a.Order()); got != "[c b a]" {
		t.Fatalf("order = %v", got)
	}
	if r := resp(t, a, "c"); r != 5 || resp(t, a, "b") != 7 || resp(t, a, "a") != 9 {
		t.Fatalf("responses = %d,%d,%d want 5,7,9", resp(t, a, "c"), resp(t, a, "b"), resp(t, a, "a"))
	}
}

// Full-set rejection after failed insertion reports the unassigned count at
// the failing Audsley level including the level being filled.
func TestUnschedulablePendingCount(t *testing.T) {
	a := NewAnalyzer()
	mustAdd(t, a, Task{ID: "a", C: 3, T: 10, D: 7}, false)
	mustAdd(t, a, Task{ID: "b", C: 3, T: 10, D: 7}, false)
	_, err := a.Add(Task{ID: "c", C: 3, T: 10, D: 7})
	// Two coexist (low w = 3 + ceil(3/10)*3 = 6 <= 7); with three tasks
	// every lowest-level candidate sees two others: w = 3+3+3 = 9 > 7,
	// so nobody can take the level and pending = 3.
	e := errReason(t, err, ReasonUnschedulable)
	if e.Pending != 3 {
		t.Fatalf("pending = %d want 3", e.Pending)
	}
	if got := fmt.Sprint(a.Order()); got != "[a b]" {
		t.Fatalf("state mutated by rejected add: %v", got)
	}

	// A two-task failure leaves pending 2 at the lowest level.
	b2 := NewAnalyzer()
	_, err = b2.Add(Task{ID: "x", C: 6, T: 10, D: 6})
	if err != nil {
		t.Fatal(err)
	}
	_, err = b2.Add(Task{ID: "y", C: 6, T: 10, D: 6})
	e = errReason(t, err, ReasonUnschedulable)
	if e.Pending != 2 {
		t.Fatalf("pending = %d want 2", e.Pending)
	}
}

func TestCapacityAndValidation(t *testing.T) {
	a := NewAnalyzer()
	for i := 0; i < MaxTasks; i++ {
		mustAdd(t, a, Task{ID: fmt.Sprintf("t%02d", i), C: 1, T: 100, D: 100}, false)
	}
	_, err := a.Add(Task{ID: "x", C: 1, T: 100, D: 100})
	errReason(t, err, ReasonCapacityFull)
	_, err = a.Add(Task{ID: "t00", C: 1, T: 100, D: 100}) // duplicate outranks capacity
	errReason(t, err, ReasonDuplicate)
	_, err = a.Add(Task{ID: "t00", C: 0, T: 100, D: 100}) // invalid outranks duplicate
	errReason(t, err, ReasonInvalidParam)
	_, err = a.Add(Task{ID: "", C: 1, T: 100, D: 100})
	errReason(t, err, ReasonInvalidParam)

	long := make([]byte, maxIDLen+1)
	for i := range long {
		long[i] = 'x'
	}
	_, err = a.Add(Task{ID: string(long), C: 1, T: 100, D: 100})
	errReason(t, err, ReasonInvalidParam)

	errReason(t, a.Remove(""), ReasonInvalidParam)
	_, err = a.Response("")
	errReason(t, err, ReasonInvalidParam)
	errReason(t, a.Remove("missing"), ReasonNotFound)
	_, err = a.Response("missing")
	errReason(t, err, ReasonNotFound)
}

func TestRemoveKeepsOrder(t *testing.T) {
	a := NewAnalyzer()
	mustAdd(t, a, Task{ID: "A", C: 1, T: 12, D: 10}, false)
	mustAdd(t, a, Task{ID: "B", C: 3, T: 6, D: 6}, false)
	mustAdd(t, a, Task{ID: "E", C: 1, T: 6, D: 2}, false)
	if got := fmt.Sprint(a.Order()); got != "[A E B]" {
		t.Fatalf("order = %v", got)
	}
	if err := a.Remove("E"); err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(a.Order()); got != "[A B]" {
		t.Fatalf("order after remove = %v", got)
	}
	if r := resp(t, a, "B"); r != 4 {
		t.Fatalf("R_B after remove = %d want 4", r)
	}
	if _, err := a.Response("E"); err == nil {
		t.Fatal("Response(E) after removal should fail")
	}
	mustAdd(t, a, Task{ID: "E", C: 1, T: 6, D: 2}, false)
	if got := fmt.Sprint(a.Order()); got != "[A E B]" {
		t.Fatalf("order after re-add = %v", got)
	}
}

// Response performs no fixed-point work; Order neither.
func TestResponseNoIteration(t *testing.T) {
	a := NewAnalyzer()
	mustAdd(t, a, Task{ID: "A", C: 1, T: 12, D: 10}, false)
	mustAdd(t, a, Task{ID: "B", C: 3, T: 6, D: 6}, false)
	before := a.SumTerms()
	for i := 0; i < 10; i++ {
		if _, err := a.Response("A"); err != nil {
			t.Fatal(err)
		}
		if _, err := a.Response("B"); err != nil {
			t.Fatal(err)
		}
		_ = a.Order()
	}
	if after := a.SumTerms(); after != before {
		t.Fatalf("counter delta = %d, want 0", after-before)
	}
}
