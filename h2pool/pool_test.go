package h2pool

import (
	"errors"
	"reflect"
	"testing"
)

const bigMaxID = int64(1)<<31 - 1

func mustPool(t *testing.T, m0 int, idle, maxID int64, k int) *Pool {
	t.Helper()
	p, err := NewPool(m0, idle, maxID, k)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	return p
}

func mustAdd(t *testing.T, p *Pool, now int64, ids ...string) {
	t.Helper()
	for _, id := range ids {
		if err := p.AddConn(now, id); err != nil {
			t.Fatalf("AddConn(%q): %v", id, err)
		}
	}
}

func mustOpen(t *testing.T, p *Pool, now int64, req, wantConn string, wantStream int64) {
	t.Helper()
	id, stream, err := p.Open(now, req)
	if err != nil {
		t.Fatalf("Open(%q): %v", req, err)
	}
	if id != wantConn || stream != wantStream {
		t.Fatalf("Open(%q) = (%q,%d), want (%q,%d)", req, id, stream, wantConn, wantStream)
	}
}

func wantState(t *testing.T, p *Pool, id string, want State) {
	t.Helper()
	snap, err := p.Snapshot(id)
	if err != nil {
		t.Fatalf("Snapshot(%q): %v", id, err)
	}
	if snap.State != want {
		t.Fatalf("conn %q state = %v, want %v", id, snap.State, want)
	}
}

// The worked example from the specification: m0=2, conns A and B join at now=0.
func TestWorkedExample(t *testing.T) {
	p := mustPool(t, 2, 1_000_000, bigMaxID, 3)
	mustAdd(t, p, 0, "A", "B")

	mustOpen(t, p, 1, "r1", "A", 1) // tie at 0 active: smallest id wins
	mustOpen(t, p, 2, "r2", "B", 1) // A already has 1
	mustOpen(t, p, 3, "r3", "A", 3)
	mustOpen(t, p, 4, "r4", "B", 3)
	if _, _, err := p.Open(5, "r5"); !errors.Is(err, ErrNoCapacity) {
		t.Fatalf("Open(r5) err = %v, want ErrNoCapacity", err)
	}

	retry, err := p.GoAway(6, "A", 1)
	if err != nil {
		t.Fatalf("GoAway(A,1): %v", err)
	}
	if !reflect.DeepEqual(retry, []string{"r3"}) {
		t.Fatalf("GoAway(A,1) retry = %v, want [r3]", retry)
	}
	wantState(t, p, "A", Draining)
	if _, _, err := p.Open(7, "r3"); !errors.Is(err, ErrNoCapacity) {
		t.Fatalf("Open(r3) after GoAway err = %v, want ErrNoCapacity (A draining, B full)", err)
	}

	if err := p.SetMaxConcurrent(8, "B", 1); err != nil {
		t.Fatalf("SetMaxConcurrent(B,1): %v", err)
	}
	if _, err := p.CloseStream(9, "B", 1, Done); err != nil {
		t.Fatalf("CloseStream(B,1,Done): %v", err)
	}
	if snap, _ := p.Snapshot("B"); len(snap.Streams) != 1 {
		t.Fatalf("B active = %d, want 1 (not strictly < maxConc=1, so not allocatable)", len(snap.Streams))
	}
	if _, _, err := p.Open(10, "r3"); !errors.Is(err, ErrNoCapacity) {
		t.Fatalf("Open(r3) with B at 1/1 err = %v, want ErrNoCapacity", err)
	}
	if _, err := p.CloseStream(11, "B", 3, Done); err != nil {
		t.Fatalf("CloseStream(B,3,Done): %v", err)
	}
	mustOpen(t, p, 12, "r3", "B", 5)

	if _, err := p.GoAway(13, "A", 3); !errors.Is(err, ErrGoAwayUp) {
		t.Fatalf("GoAway(A,3) err = %v, want ErrGoAwayUp (goAwayLast=1)", err)
	}
	retry, err = p.GoAway(14, "A", 0)
	if err != nil {
		t.Fatalf("GoAway(A,0): %v", err)
	}
	if !reflect.DeepEqual(retry, []string{"r1"}) {
		t.Fatalf("GoAway(A,0) retry = %v, want [r1]", retry)
	}
	wantState(t, p, "A", Closed)
}

// Allocating the last usable stream id (maxID) flips the conn to Draining.
func TestStreamIDExhaustion(t *testing.T) {
	p := mustPool(t, 10, 1000, 3, 2)
	mustAdd(t, p, 0, "C")
	mustOpen(t, p, 1, "r1", "C", 1)
	mustOpen(t, p, 2, "r2", "C", 3) // stream 3 = maxID is still allocatable
	wantState(t, p, "C", Draining)  // nextID=5 > maxID=3
	if _, _, err := p.Open(3, "r3"); !errors.Is(err, ErrNoCapacity) {
		t.Fatalf("Open after exhaustion err = %v, want ErrNoCapacity", err)
	}
	if snap, _ := p.Snapshot("C"); len(snap.Streams) != 2 {
		t.Fatalf("C active = %d, want 2 (stream 3 must stay usable)", len(snap.Streams))
	}
}

// GoAway(lastID=maxID) removes nothing but drains the conn.
func TestGoAwayMaxIDDrainsWithoutRemoval(t *testing.T) {
	p := mustPool(t, 10, 1000, 7, 2)
	mustAdd(t, p, 0, "D")
	mustOpen(t, p, 1, "r1", "D", 1)
	mustOpen(t, p, 2, "r2", "D", 3)
	retry, err := p.GoAway(3, "D", 7) // maxID bypasses the beyond check
	if err != nil {
		t.Fatalf("GoAway(D,7): %v", err)
	}
	if len(retry) != 0 {
		t.Fatalf("GoAway(D,7) retry = %v, want []", retry)
	}
	wantState(t, p, "D", Draining)
	if snap, _ := p.Snapshot("D"); len(snap.Streams) != 2 {
		t.Fatalf("D active = %d, want 2", len(snap.Streams))
	}
	if _, err := p.CloseStream(4, "D", 1, Done); err != nil {
		t.Fatalf("CloseStream(D,1): %v", err)
	}
	wantState(t, p, "D", Draining)
	if _, err := p.CloseStream(5, "D", 3, Done); err != nil {
		t.Fatalf("CloseStream(D,3): %v", err)
	}
	wantState(t, p, "D", Closed) // drained to zero -> Closed
}

// K=2: a Done between two Refused resets the streak; Reset does not.
func TestRefuseThreshold(t *testing.T) {
	p := mustPool(t, 10, 1000, bigMaxID, 2)
	mustAdd(t, p, 0, "E")
	mustOpen(t, p, 1, "r1", "E", 1)
	mustOpen(t, p, 2, "r2", "E", 3)
	mustOpen(t, p, 3, "r3", "E", 5)

	ok, err := p.CloseStream(4, "E", 1, Refused)
	if err != nil || !ok {
		t.Fatalf("CloseStream(Refused) = (%v,%v), want retryable", ok, err)
	}
	wantState(t, p, "E", Active) // refuse=1 < K=2
	if _, err := p.CloseStream(5, "E", 3, Done); err != nil {
		t.Fatalf("CloseStream(Done): %v", err)
	}
	if snap, _ := p.Snapshot("E"); snap.Refuse != 0 {
		t.Fatalf("refuse = %d after Done, want 0", snap.Refuse)
	}
	if _, err := p.CloseStream(6, "E", 5, Refused); err != nil {
		t.Fatalf("CloseStream(Refused): %v", err)
	}
	wantState(t, p, "E", Active) // streak broken by Done: refuse=1 again

	mustOpen(t, p, 7, "r4", "E", 7)
	ok, err = p.CloseStream(8, "E", 7, Reset)
	if err != nil || ok {
		t.Fatalf("CloseStream(Reset) = (%v,%v), want not retryable", ok, err)
	}
	if snap, _ := p.Snapshot("E"); snap.Refuse != 1 {
		t.Fatalf("refuse = %d after Reset, want 1 (Reset must not clear)", snap.Refuse)
	}
	mustOpen(t, p, 9, "r5", "E", 9)
	mustOpen(t, p, 9, "r6", "E", 11) // keep one stream active so E drains instead of closing
	if _, err := p.CloseStream(10, "E", 9, Refused); err != nil {
		t.Fatalf("CloseStream(Refused): %v", err)
	}
	wantState(t, p, "E", Draining) // two consecutive Refused hit K=2
	// Draining with zero active streams closes immediately.
	if _, err := p.CloseStream(11, "E", 11, Refused); err != nil {
		t.Fatalf("CloseStream(Refused): %v", err)
	}
	wantState(t, p, "E", Closed)
}

// A stream already removed by GOAWAY is unknown to CloseStream.
func TestCloseStreamAfterGoAwayRemoval(t *testing.T) {
	p := mustPool(t, 10, 1000, bigMaxID, 2)
	mustAdd(t, p, 0, "F")
	mustOpen(t, p, 1, "r1", "F", 1)
	mustOpen(t, p, 2, "r2", "F", 3)
	retry, err := p.GoAway(3, "F", 1)
	if err != nil || !reflect.DeepEqual(retry, []string{"r2"}) {
		t.Fatalf("GoAway(F,1) = (%v,%v), want [r2]", retry, err)
	}
	if _, err := p.CloseStream(4, "F", 3, Done); !errors.Is(err, ErrUnknownStream) {
		t.Fatalf("CloseStream of GOAWAY-removed stream err = %v, want ErrUnknownStream", err)
	}
	// r2 is no longer in flight, so it can be reopened on another conn.
	mustAdd(t, p, 5, "G")
	mustOpen(t, p, 6, "r2", "G", 1)
}

// Tick closes an idle Active conn exactly at idleTimeout.
func TestTickExactTimeout(t *testing.T) {
	p := mustPool(t, 2, 100, bigMaxID, 2)
	mustAdd(t, p, 0, "H")
	mustOpen(t, p, 10, "r1", "H", 1)
	closed, err := p.Tick(109)
	if err != nil || len(closed) != 0 {
		t.Fatalf("Tick(109) = (%v,%v), want no close while stream active", closed, err)
	}
	if _, err := p.CloseStream(110, "H", 1, Done); err != nil {
		t.Fatalf("CloseStream: %v", err)
	}
	closed, err = p.Tick(209) // 209-110 = 99 < 100
	if err != nil || len(closed) != 0 {
		t.Fatalf("Tick(209) = (%v,%v), want no close", closed, err)
	}
	closed, err = p.Tick(210) // exactly idleTimeout
	if err != nil {
		t.Fatalf("Tick(210): %v", err)
	}
	if !reflect.DeepEqual(closed, []string{"H"}) {
		t.Fatalf("Tick(210) closed = %v, want [H]", closed)
	}
	wantState(t, p, "H", Closed)
}

// Error precedence and the no-side-effect rule for rejected calls.
func TestErrorPrecedenceAndRejection(t *testing.T) {
	p := mustPool(t, 1, 100, bigMaxID, 2)
	mustAdd(t, p, 10, "I")
	mustOpen(t, p, 20, "r1", "I", 1) // fills the only conn (maxConc=1)

	// Parameter errors beat clock errors: duplicate req + backwards clock.
	if _, _, err := p.Open(5, "r1"); !errors.Is(err, ErrDuplicateReq) {
		t.Fatalf("Open(dup, backwards) err = %v, want ErrDuplicateReq", err)
	}
	// A rejected call must not advance the clock: now=20 is still the last accepted.
	if _, _, err := p.Open(30, "r2"); !errors.Is(err, ErrNoCapacity) {
		t.Fatalf("Open(r2) err = %v, want ErrNoCapacity", err)
	}
	// now=25 < 30 would fail if the rejected Open(30) had moved the clock.
	if err := p.SetMaxConcurrent(25, "I", 2); err != nil {
		t.Fatalf("SetMaxConcurrent at now=25 after rejected now=30: %v (rejected calls must not advance the clock)", err)
	}
	mustOpen(t, p, 26, "r2", "I", 3)

	// State errors beat GOAWAY-specific errors.
	if _, err := p.CloseStream(27, "I", 1, Done); err != nil {
		t.Fatalf("CloseStream: %v", err)
	}
	if _, err := p.CloseStream(28, "I", 3, Done); err != nil {
		t.Fatalf("CloseStream: %v", err)
	}
	if _, err := p.GoAway(29, "I", 0); err != nil { // empties the conn -> Closed
		t.Fatalf("GoAway(I,0): %v", err)
	}
	wantState(t, p, "I", Closed)
	if _, err := p.GoAway(30, "I", 5); !errors.Is(err, ErrConnClosed) {
		t.Fatalf("GoAway on Closed err = %v, want ErrConnClosed (before ErrGoAwayUp)", err)
	}
	if err := p.SetMaxConcurrent(31, "I", 1); !errors.Is(err, ErrConnClosed) {
		t.Fatalf("SetMaxConcurrent on Closed err = %v, want ErrConnClosed", err)
	}
	// Ids stay taken even after Closed.
	if err := p.AddConn(32, "I"); !errors.Is(err, ErrDuplicateConn) {
		t.Fatalf("AddConn duplicate of Closed err = %v, want ErrDuplicateConn", err)
	}
	// GOAWAY order: ErrGoAwayUp is checked before ErrGoAwayBeyond.
	mustAdd(t, p, 33, "J")
	mustOpen(t, p, 34, "r9", "J", 1)
	if _, err := p.GoAway(35, "J", 1); err != nil {
		t.Fatalf("GoAway(J,1): %v", err)
	}
	if _, err := p.GoAway(36, "J", 5); !errors.Is(err, ErrGoAwayUp) {
		t.Fatalf("GoAway(J,5) err = %v, want ErrGoAwayUp (not ErrGoAwayBeyond)", err)
	}
	// lastID beyond the last assigned stream (and != maxID) is rejected.
	mustAdd(t, p, 37, "K")
	mustOpen(t, p, 38, "r10", "K", 1)
	if _, err := p.GoAway(39, "K", 3); !errors.Is(err, ErrGoAwayBeyond) {
		t.Fatalf("GoAway(K,3) err = %v, want ErrGoAwayBeyond (lastAssigned=1)", err)
	}
	// Invalid lastID (even, negative, > maxID) is a parameter error.
	for _, bad := range []int64{2, -1, bigMaxID + 1} {
		if _, err := p.GoAway(40, "K", bad); !errors.Is(err, ErrInvalidLastID) {
			t.Fatalf("GoAway(K,%d) err = %v, want ErrInvalidLastID", bad, err)
		}
	}
	// Constructor and m bounds.
	if _, err := NewPool(0, 1, 1, 1); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("NewPool(m0=0) err = %v, want ErrInvalidParam", err)
	}
	if _, err := NewPool(1, 1, 2, 1); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("NewPool(even maxID) err = %v, want ErrInvalidParam", err)
	}
	if err := p.SetMaxConcurrent(41, "K", 0); !errors.Is(err, ErrInvalidMaxConc) {
		t.Fatalf("SetMaxConcurrent(m=0) err = %v, want ErrInvalidMaxConc", err)
	}
	if err := p.SetMaxConcurrent(42, "nope", 1); !errors.Is(err, ErrUnknownConn) {
		t.Fatalf("SetMaxConcurrent unknown conn err = %v, want ErrUnknownConn", err)
	}
}
