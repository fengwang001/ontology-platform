package sched

import (
	"errors"
	"fmt"
	"testing"
)

// op is one deterministic scripted operation.
type op struct {
	kind   string // submit/finish/ackcancel/cancel
	group  string
	cancel bool
	prot   bool
	idRef  int // 1-based index into the test's accepted-id table
	ok     bool
	state  State // expected target state after the op
	err    error // expected sentinel error, nil for success
	note   string
}

func errIs(got, want error) bool {
	if want == nil {
		return got == nil
	}
	return errors.Is(got, want)
}

func mustNew(t *testing.T, C, Q int) *Scheduler {
	t.Helper()
	s, err := New(C, Q)
	if err != nil {
		t.Fatalf("New(%d,%d): %v", C, Q, err)
	}
	return s
}

// runScript executes a deterministic op table and asserts error classes,
// sequential ids and target states.
func runScript(t *testing.T, C, Q int, ops []op) {
	t.Helper()
	s := mustNew(t, C, Q)
	ids := make([]int64, 0, len(ops))
	for i, o := range ops {
		label := fmt.Sprintf("step %d %s (%s)", i, o.kind, o.note)
		if o.kind == "submit" {
			id, gerr := s.Submit([]byte(o.group), o.cancel, o.prot)
			t.Logf("INPUT  Submit(group=%q,cancel=%v,protected=%v) -> OUTPUT id=%d err=%v | %s",
				o.group, o.cancel, o.prot, id, gerr, o.note)
			if !errIs(gerr, o.err) {
				t.Fatalf("%s: got err %v want %v", label, gerr, o.err)
			}
			if o.err == nil {
				if id != int64(len(ids)+1) {
					t.Fatalf("%s: id=%d want %d", label, id, len(ids)+1)
				}
				ids = append(ids, id)
				if got, _ := s.StateOf(id); got != o.state {
					t.Fatalf("%s: new id state=%s want %s", label, got, o.state)
				}
			}
			continue
		}
		if o.idRef < 1 || o.idRef > len(ids) {
			t.Fatalf("%s: bad idRef %d", label, o.idRef)
		}
		id := ids[o.idRef-1]
		var gerr error
		switch o.kind {
		case "finish":
			gerr = s.Finish(id, o.ok)
		case "ackcancel":
			gerr = s.AckCancel(id)
		case "cancel":
			gerr = s.Cancel(id)
		default:
			t.Fatalf("%s: unknown kind %q", label, o.kind)
		}
		t.Logf("INPUT  %s(r%d#%d,ok=%v) -> OUTPUT err=%v | %s",
			o.kind, o.idRef, id, o.ok, gerr, o.note)
		if !errIs(gerr, o.err) {
			t.Fatalf("%s: got err %v want %v", label, gerr, o.err)
		}
		if gerr == nil {
			if got, _ := s.StateOf(id); got != o.state {
				t.Fatalf("%s: state=%s want %s", label, got, o.state)
			}
		}
	}
}

func TestCanonicalExample(t *testing.T) {
	// C=1,Q=10, group g: the worked example from the specification.
	ops := []op{
		{kind: "submit", group: "g", state: Running, note: "free slot -> Running"},
		{kind: "submit", group: "g", state: Pending, note: "placeholder busy -> Pending"},
		{kind: "submit", group: "g", state: Pending, note: "r3 supersedes r2"},
		{kind: "submit", group: "g", cancel: true, state: Pending,
			note: "r3 superseded; r1 Running non-protected -> Cancelling"},
		{kind: "submit", group: "", state: Waiting, note: "no group; r1 still holds slot"},
		{kind: "ackcancel", idRef: 1, state: Cancelled,
			note: "r1 releases slot; r4 promoted behind r5; r5 Running"},
		{kind: "submit", group: "g", cancel: true, state: Waiting,
			note: "r4 Waiting -> Cancelled O(1); r6 enqueued"},
		{kind: "finish", idRef: 5, ok: true, state: Succeeded, note: "r5 done -> r6 Running"},
		{kind: "submit", group: "g", cancel: true, state: Pending,
			note: "r6 Running -> Cancelling keeps slot; r7 Pending"},
		{kind: "finish", idRef: 6, ok: true, state: Cancelled,
			note: "late Finish on Cancelling -> Cancelled; r7 promoted and Running"},
	}
	runScript(t, 1, 10, ops)

	s := mustNew(t, 1, 10)
	g := []byte("g")
	r1, _ := s.Submit(g, false, false)
	r2, _ := s.Submit(g, false, false)
	r3, _ := s.Submit(g, false, false)
	r4, _ := s.Submit(g, true, false)
	r5, _ := s.Submit(nil, false, false)
	if err := s.AckCancel(r1); err != nil {
		t.Fatal(err)
	}
	r6, _ := s.Submit(g, true, false)
	if err := s.Finish(r5, true); err != nil {
		t.Fatal(err)
	}
	r7, _ := s.Submit(g, true, false)
	if err := s.Finish(r6, true); err != nil {
		t.Fatal(err)
	}
	want := map[int64]State{
		r1: Cancelled, r2: Superseded, r3: Superseded, r4: Cancelled,
		r5: Succeeded, r6: Cancelled, r7: Running,
	}
	for id, st := range want {
		if got, _ := s.StateOf(id); got != st {
			t.Fatalf("id %d: %s want %s", id, got, st)
		}
	}
}

func TestZeroQueueAndProtected(t *testing.T) {
	ops := []op{
		{kind: "submit", group: "g", state: Running, note: "free slot: not counted against Q"},
		{kind: "submit", group: "h", err: ErrQueueFull, note: "L1=1 > Q=0"},
		{kind: "submit", group: "g", state: Pending, note: "pending never rejected"},
		{kind: "finish", idRef: 1, ok: true, state: Succeeded, note: "r2 promoted and immediately Running"},
	}
	runScript(t, 1, 0, ops)

	s := mustNew(t, 1, 0)
	g := []byte("g")
	r1, _ := s.Submit(g, false, false) // placeholder Running
	r2, _ := s.Submit(g, true, true)   // new submitter asks cancel but is protected
	if st, _ := s.StateOf(r1); st != Running {
		t.Fatalf("protected shields auto-cancel: %s", st)
	}
	if st, _ := s.StateOf(r2); st != Pending {
		t.Fatalf("new submit: %s", st)
	}
	if err := s.Cancel(r1); err != nil { // explicit cancel bypasses protected
		t.Fatal(err)
	}
	if st, _ := s.StateOf(r1); st != Cancelling {
		t.Fatalf("explicit cancel: %s", st)
	}
}

func TestProtectedDoesNotShieldWaiting(t *testing.T) {
	s := mustNew(t, 2, 10)
	r1, _ := s.Submit([]byte("g"), false, false) // Running
	_, _ = s.Submit(nil, false, false)           // Running (slots full)
	r4, _ := s.Submit([]byte("k"), false, false) // Waiting
	if err := s.Cancel(r1); err != nil {         // r1 Cancelling, holds slot
		t.Fatal(err)
	}
	r5, _ := s.Submit([]byte("k"), true, true) // protected only, but r4 Waiting
	if st, _ := s.StateOf(r4); st != Cancelled {
		t.Fatalf("Waiting must be cancelled despite protected: %s", st)
	}
	if st, _ := s.StateOf(r5); st != Waiting {
		t.Fatalf("r5 placeholder Waiting: %s", st)
	}
}

func TestPendingChainAndNewestFlag(t *testing.T) {
	s := mustNew(t, 1, 10)
	g := []byte("g")
	r1, _ := s.Submit(g, false, false)
	var pend []int64
	for i := 0; i < 5; i++ {
		id, _ := s.Submit(g, false, false)
		pend = append(pend, id)
	}
	for _, id := range pend[:4] {
		if st, _ := s.StateOf(id); st != Superseded {
			t.Fatalf("chained pending %d: %s", id, st)
		}
	}
	if st, _ := s.StateOf(pend[4]); st != Pending {
		t.Fatalf("last pending: %s", st)
	}
	// The newest submitter's flag decides; its submit supersedes the old pending.
	rNew, _ := s.Submit(g, true, false)
	if st, _ := s.StateOf(pend[4]); st != Superseded {
		t.Fatalf("old pending: %s", st)
	}
	if st, _ := s.StateOf(r1); st != Cancelling {
		t.Fatalf("newest flag cancels placeholder: %s", st)
	}
	if st, _ := s.StateOf(rNew); st != Pending {
		t.Fatalf("new run Pending: %s", st)
	}
}

func TestRejectionOrdering(t *testing.T) {
	s := mustNew(t, 1, 10)
	if _, err := s.Submit(make([]byte, 65), false, false); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("long group: %v", err)
	}
	if err := s.Finish(0, true); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("id 0: %v", err)
	}
	if err := s.Finish(999, true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing run: %v", err)
	}
	g := []byte("g")
	r1, _ := s.Submit(g, false, false) // Running
	r2, _ := s.Submit(g, false, false) // Pending
	if err := s.AckCancel(r1); !errors.Is(err, ErrConflict) {
		t.Fatalf("ack Running: %v", err)
	}
	if err := s.Finish(r2, true); !errors.Is(err, ErrConflict) {
		t.Fatalf("finish Pending: %v", err)
	}
	if err := s.Cancel(r1); err != nil {
		t.Fatal(err) // Running -> Cancelling
	}
	if err := s.Cancel(r1); !errors.Is(err, ErrConflict) {
		t.Fatalf("cancel Cancelling: %v", err)
	}
	if err := s.AckCancel(r1); err != nil {
		t.Fatal(err) // Cancelling -> Cancelled
	}
	terminalOps := []func() error{
		func() error { return s.Finish(r1, true) },
		func() error { return s.Cancel(r1) },
		func() error { return s.AckCancel(r1) },
	}
	for _, f := range terminalOps {
		if err := f(); !errors.Is(err, ErrConflict) {
			t.Fatalf("terminal op: %v", err)
		}
	}
	if err := s.Cancel(r2); err != nil { // Pending -> Cancelled
		t.Fatal(err)
	}
}

func TestQueueFullNetGrowth(t *testing.T) {
	// A rejected submit changes nothing, including a prior Pending.
	s := mustNew(t, 1, 1)
	g := []byte("g")
	r1, _ := s.Submit(g, false, false)           // Running
	r2, _ := s.Submit([]byte("h"), false, false) // Waiting (queue len 1 == Q)
	r3, _ := s.Submit(g, false, false)           // Pending (queue unaffected)
	if _, err := s.Submit([]byte("k"), false, false); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("new group must be rejected: %v", err)
	}
	if st, _ := s.StateOf(r2); st != Waiting {
		t.Fatalf("reject disturbed queue: %s", st)
	}
	if st, _ := s.StateOf(r3); st != Pending {
		t.Fatalf("reject must not supersede pending: %s", st)
	}
	// Existing-placeholder path accepted even with a full queue.
	r4, err := s.Submit(g, true, false)
	if err != nil {
		t.Fatalf("placeholder path: %v", err)
	}
	if st, _ := s.StateOf(r3); st != Superseded {
		t.Fatalf("accepted submit supersedes r3: %s", st)
	}
	if st, _ := s.StateOf(r1); st != Cancelling {
		t.Fatalf("r1: %s", st)
	}
	if st, _ := s.StateOf(r4); st != Pending {
		t.Fatalf("r4: %s", st)
	}

	// Cancelling a Waiting placeholder frees its queue slot; the net-zero
	// replacement submit is not rejected even with Q saturated.
	s2 := mustNew(t, 1, 1)
	w1, _ := s2.Submit([]byte("a"), false, false) // Running
	w2, _ := s2.Submit([]byte("b"), false, false) // Waiting, Q saturated
	if _, err := s2.Submit([]byte("c"), false, false); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("saturated: %v", err)
	}
	if err := s2.Cancel(w2); err != nil { // dequeue w2
		t.Fatal(err)
	}
	if st, _ := s2.StateOf(w2); st != Cancelled {
		t.Fatalf("w2: %s", st)
	}
	w3, err := s2.Submit([]byte("b"), false, false) // re-enter same group
	if err != nil {
		t.Fatalf("submit after dequeue: %v", err)
	}
	if st, _ := s2.StateOf(w3); st != Waiting {
		t.Fatalf("w3: %s", st)
	}
	if err := s2.Finish(w1, true); err != nil { // w3 promoted-allocated
		t.Fatal(err)
	}
	if st, _ := s2.StateOf(w3); st != Running {
		t.Fatalf("w3 after finish: %s", st)
	}
}
