package cidpool

import (
	"errors"
	"fmt"
	"testing"
)

func cid(n byte) []byte { return []byte{0xC0 + n} }

func tok(n byte) []byte {
	t := make([]byte, tokenLen)
	t[tokenLen-1] = n
	return t
}

func mustPool(t *testing.T, limit int) *Pool {
	t.Helper()
	p, err := NewPool(limit, cid(0), tok(0))
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	return p
}

// TestSpecMainScenario walks the headline example from the spec:
// L=3, limit rejection, batched retirement via retire_prior_to, path
// migration, duplicate resend and conflict.
func TestSpecMainScenario(t *testing.T) {
	p := mustPool(t, 3)
	if err := p.NewPath(1); err != nil {
		t.Fatalf("NewPath(1): %v", err)
	}
	if s, err := p.PathSeq(1); err != nil || s != (PathSlot{Seq: 0}) {
		t.Fatalf("path 1 = %+v, %v; want seq0", s, err)
	}

	if err := p.OnNew(1, 0, cid(1), tok(1)); err != nil {
		t.Fatalf("OnNew(1): %v", err)
	}
	if err := p.OnNew(2, 0, cid(2), tok(2)); err != nil {
		t.Fatalf("OnNew(2): %v", err)
	}
	if got := p.ActiveCount(); got != 3 {
		t.Fatalf("ActiveCount = %d, want 3", got)
	}

	// Fourth active entry exceeds L=3: ErrLimit, nothing changes and no
	// tombstone for seq3 is left behind.
	err := p.OnNew(3, 0, cid(3), tok(3))
	if !errors.Is(err, ErrLimit) {
		t.Fatalf("OnNew(3,0) err = %v, want ErrLimit", err)
	}
	if p.ActiveCount() != 3 || p.R() != 0 {
		t.Fatalf("state changed after ErrLimit: active=%d R=%d", p.ActiveCount(), p.R())
	}
	if _, ok := p.EntryAt(3); ok {
		t.Fatal("seq3 tombstone left by rejected call")
	}

	// Same frame with rpt=2: seq0/seq1 retire, active set becomes {2,3}.
	if err := p.OnNew(3, 2, cid(3), tok(3)); err != nil {
		t.Fatalf("OnNew(3,rpt=2): %v", err)
	}
	if p.R() != 2 {
		t.Fatalf("R = %d, want 2", p.R())
	}
	if got := p.ActiveCount(); got != 2 {
		t.Fatalf("ActiveCount = %d, want 2", got)
	}
	if q := p.TakeRetires(); fmt.Sprint(q) != "[0 1]" {
		t.Fatalf("retire queue = %v, want [0 1]", q)
	}
	if s, _ := p.PathSeq(1); s != (PathSlot{Seq: 2}) {
		t.Fatalf("path 1 = %+v, want seq2", s)
	}
	for _, seq := range []uint64{0, 1} {
		if e, ok := p.EntryAt(seq); !ok || !e.Retired {
			t.Fatalf("seq %d not a retained retired tombstone", seq)
		}
	}

	// Exact resend of the seq1 frame is a duplicate: success, no change,
	// R is not touched and the queue stays empty.
	if err := p.OnNew(1, 0, cid(1), tok(1)); err != nil {
		t.Fatalf("duplicate resend: %v", err)
	}
	if p.R() != 2 || len(p.TakeRetires()) != 0 {
		t.Fatal("duplicate resend changed state")
	}

	// Known seq with different cid/token is a violation.
	if err := p.OnNew(1, 0, cid(9), tok(9)); !errors.Is(err, ErrViolation) {
		t.Fatalf("conflicting seq1 err = %v, want ErrViolation", err)
	}
}

// TestSpecLateArrival covers a frame whose seq is already below R: the
// entry retires immediately, never counts against the limit, and resending
// it is a duplicate that does not re-enqueue.
func TestSpecLateArrival(t *testing.T) {
	p := mustPool(t, 2)
	if err := p.OnNew(5, 4, cid(5), tok(5)); err != nil {
		t.Fatalf("OnNew(5,rpt=4): %v", err)
	}
	if q := p.TakeRetires(); fmt.Sprint(q) != "[0]" {
		t.Fatalf("queue = %v, want [0]", q)
	}

	if err := p.OnNew(3, 0, cid(3), tok(3)); err != nil {
		t.Fatalf("late OnNew(3): %v", err)
	}
	if p.ActiveCount() != 1 {
		t.Fatalf("ActiveCount = %d, late entry must not count", p.ActiveCount())
	}
	e, ok := p.EntryAt(3)
	if !ok || !e.Retired {
		t.Fatal("late entry missing or not retired")
	}
	if q := p.TakeRetires(); fmt.Sprint(q) != "[3]" {
		t.Fatalf("queue = %v, want [3]", q)
	}

	if err := p.OnNew(3, 0, cid(3), tok(3)); err != nil {
		t.Fatalf("late resend: %v", err)
	}
	if q := p.TakeRetires(); len(q) != 0 {
		t.Fatalf("late resend re-enqueued %v", q)
	}
}

// TestSpecParkedPaths covers ErrNoCID, parking when the occupied entry
// retires, and pathID-ordered reassignment once new IDs arrive.
func TestSpecParkedPaths(t *testing.T) {
	p := mustPool(t, 4)
	if err := p.NewPath(1); err != nil {
		t.Fatalf("NewPath(1): %v", err)
	}
	if err := p.NewPath(2); !errors.Is(err, ErrNoCID) {
		t.Fatalf("NewPath(2) err = %v, want ErrNoCID", err)
	}

	if err := p.Retire(0); err != nil {
		t.Fatalf("Retire(0): %v", err)
	}
	if s, _ := p.PathSeq(1); !s.Parked {
		t.Fatalf("path 1 = %+v, want parked", s)
	}
	if err := p.NewPath(2); !errors.Is(err, ErrNoCID) {
		t.Fatalf("NewPath(2) while parked: %v", err)
	}

	if err := p.OnNew(1, 0, cid(1), tok(1)); err != nil {
		t.Fatalf("OnNew(1): %v", err)
	}
	if s, _ := p.PathSeq(1); s != (PathSlot{Seq: 1}) {
		t.Fatalf("path 1 = %+v, want seq1", s)
	}

	// Three parked paths are served in ascending pathID order as fresh
	// active entries appear one at a time.
	p2 := mustPool(t, 4)
	if err := p2.OnNew(1, 0, cid(1), tok(1)); err != nil {
		t.Fatal(err)
	}
	if err := p2.OnNew(2, 0, cid(2), tok(2)); err != nil {
		t.Fatal(err)
	}
	for _, pid := range []uint64{1, 2, 3} {
		if err := p2.NewPath(pid); err != nil {
			t.Fatalf("NewPath(%d): %v", pid, err)
		}
	}
	if err := p2.OnNew(3, 3, cid(3), tok(3)); err != nil { // retires 0,1,2
		t.Fatal(err)
	}
	if s, _ := p2.PathSeq(1); s != (PathSlot{Seq: 3}) {
		t.Fatalf("path 1 = %+v, want seq3", s)
	}
	if s, _ := p2.PathSeq(2); !s.Parked {
		t.Fatalf("path 2 = %+v, want parked", s)
	}
	if err := p2.OnNew(4, 3, cid(4), tok(4)); err != nil {
		t.Fatal(err)
	}
	if s, _ := p2.PathSeq(2); s != (PathSlot{Seq: 4}) {
		t.Fatalf("path 2 = %+v, want seq4 (lowest parked pid first)", s)
	}
	if s, _ := p2.PathSeq(3); !s.Parked {
		t.Fatalf("path 3 = %+v, still parked", s)
	}
	if err := p2.OnNew(5, 3, cid(5), tok(5)); err != nil {
		t.Fatal(err)
	}
	if s, _ := p2.PathSeq(3); s != (PathSlot{Seq: 5}) {
		t.Fatalf("path 3 = %+v, want seq5", s)
	}
}

// TestSpecRPTBoundary checks rpt == seq (new entry survives) vs
// rpt == seq+1 (encoding error).
func TestSpecRPTBoundary(t *testing.T) {
	p := mustPool(t, 4)
	if err := p.OnNew(7, 7, cid(7), tok(7)); err != nil {
		t.Fatalf("rpt == seq: %v", err)
	}
	if p.R() != 7 {
		t.Fatalf("R = %d, want 7", p.R())
	}
	e, ok := p.EntryAt(7)
	if !ok || e.Retired {
		t.Fatal("seq7 should be active when rpt == seq")
	}
	if e0, _ := p.EntryAt(0); !e0.Retired {
		t.Fatal("seq0 should retire (0 < R=7)")
	}

	if err := p.OnNew(8, 9, cid(8), tok(8)); !errors.Is(err, ErrEncoding) {
		t.Fatalf("rpt > seq err = %v, want ErrEncoding", err)
	}
	if p.R() != 7 {
		t.Fatalf("R = %d after encoding error, want 7", p.R())
	}
}

// TestSpecRetiredCIDReuse verifies a cid stays forbidden forever once it
// belonged to any entry, including a retired tombstone.
func TestSpecRetiredCIDReuse(t *testing.T) {
	p := mustPool(t, 4)
	if err := p.OnNew(1, 1, cid(1), tok(1)); err != nil {
		t.Fatal(err)
	}
	if err := p.OnNew(2, 1, cid(0), tok(2)); !errors.Is(err, ErrViolation) {
		t.Fatalf("reusing retired cid0 err = %v, want ErrViolation", err)
	}
	if p.R() != 1 {
		t.Fatalf("R = %d, violation must not advance R", p.R())
	}
	if _, ok := p.EntryAt(2); ok {
		t.Fatal("rejected frame left a tombstone")
	}
}

// TestSpecMisc covers constructor validation, encoding variants, path
// argument errors, freeing, local retirement and reset-token scope.
func TestSpecMisc(t *testing.T) {
	if _, err := NewPool(1, cid(0), tok(0)); !errors.Is(err, ErrArg) {
		t.Fatalf("L=1 err = %v", err)
	}
	if _, err := NewPool(17, cid(0), tok(0)); !errors.Is(err, ErrArg) {
		t.Fatalf("L=17 err = %v", err)
	}
	if _, err := NewPool(3, []byte{}, tok(0)); !errors.Is(err, ErrArg) {
		t.Fatalf("empty cid err = %v", err)
	}
	if _, err := NewPool(3, make([]byte, 21), tok(0)); !errors.Is(err, ErrArg) {
		t.Fatalf("21-byte cid err = %v", err)
	}
	if _, err := NewPool(3, cid(0), make([]byte, 15)); !errors.Is(err, ErrArg) {
		t.Fatalf("15-byte token err = %v", err)
	}

	p := mustPool(t, 3)
	if err := p.OnNew(1, 0, cid(1), make([]byte, 15)); !errors.Is(err, ErrEncoding) {
		t.Fatalf("bad token len err = %v", err)
	}
	if err := p.OnNew(1, 0, []byte{}, tok(1)); !errors.Is(err, ErrEncoding) {
		t.Fatalf("empty cid err = %v", err)
	}
	if err := p.FreePath(9); !errors.Is(err, ErrArg) {
		t.Fatalf("FreePath missing err = %v", err)
	}
	if _, err := p.PathSeq(9); !errors.Is(err, ErrArg) {
		t.Fatalf("PathSeq missing err = %v", err)
	}
	if err := p.Retire(42); !errors.Is(err, ErrArg) {
		t.Fatalf("Retire unknown err = %v", err)
	}

	if err := p.OnNew(1, 0, cid(1), tok(1)); err != nil {
		t.Fatal(err)
	}
	if err := p.NewPath(7); err != nil {
		t.Fatal(err)
	}
	if s, _ := p.PathSeq(7); s != (PathSlot{Seq: 0}) {
		t.Fatalf("path 7 = %+v, want seq0", s)
	}
	if err := p.FreePath(7); err != nil {
		t.Fatal(err)
	}
	if _, err := p.PathSeq(7); !errors.Is(err, ErrArg) {
		t.Fatal("path 7 still exists after FreePath")
	}

	if err := p.Retire(0); err != nil {
		t.Fatal(err)
	}
	if err := p.Retire(0); !errors.Is(err, ErrArg) {
		t.Fatalf("double Retire err = %v", err)
	}
	if q := p.TakeRetires(); fmt.Sprint(q) != "[0]" {
		t.Fatalf("queue = %v, want [0]", q)
	}

	// Reset tokens only match active entries.
	if !p.IsReset(tok(1)) {
		t.Fatal("active token t1 should be a valid reset token")
	}
	if p.IsReset(tok(0)) {
		t.Fatal("retired token t0 must not reset")
	}
	if p.IsReset(make([]byte, 15)) {
		t.Fatal("15-byte token must not match")
	}

	// Freeing a path hands its active entry to parked paths.
	p3 := mustPool(t, 3)
	if err := p3.OnNew(1, 0, cid(1), tok(1)); err != nil {
		t.Fatal(err)
	}
	if err := p3.NewPath(1); err != nil {
		t.Fatal(err)
	}
	if err := p3.NewPath(2); err != nil {
		t.Fatal(err)
	}
	if err := p3.Retire(1); err != nil {
		t.Fatal(err)
	}
	// path 1 holds seq0, path 2 parks after seq1 retires.
	if err := p3.OnNew(2, 0, cid(2), tok(2)); err != nil {
		t.Fatal(err)
	}
	if s, _ := p3.PathSeq(2); s != (PathSlot{Seq: 2}) {
		t.Fatalf("path 2 = %+v, want seq2", s)
	}
	if err := p3.FreePath(1); err != nil {
		t.Fatal(err)
	}
	// seq0 is now free; no parked paths remain, so a new path takes it.
	if err := p3.NewPath(3); err != nil {
		t.Fatalf("NewPath(3): %v", err)
	}
	if s, _ := p3.PathSeq(3); s != (PathSlot{Seq: 0}) {
		t.Fatalf("path 3 = %+v, want freed seq0", s)
	}
}
