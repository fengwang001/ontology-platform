package h2pool

import (
	"reflect"
	"sync"
	"testing"
)

func mustPool(t *testing.T, m0 int, idleTimeout, maxID int64, k int) *Pool {
	t.Helper()
	p, err := NewPool(m0, idleTimeout, maxID, k)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	return p
}

func mustAdd(t *testing.T, p *Pool, now int64, id string) {
	t.Helper()
	if err := p.AddConn(now, id); err != nil {
		t.Fatalf("AddConn(%q): %v", id, err)
	}
}

func mustOpen(t *testing.T, p *Pool, now int64, req, wantID string, wantStream int64) {
	t.Helper()
	id, stream, err := p.Open(now, req)
	if err != nil {
		t.Fatalf("Open(%q): %v", req, err)
	}
	if id != wantID || stream != wantStream {
		t.Fatalf("Open(%q) = (%q,%d), want (%q,%d)", req, id, stream, wantID, wantStream)
	}
}

func wantErr(t *testing.T, err error, code ErrCode) {
	t.Helper()
	if CodeOf(err) != code {
		t.Fatalf("err = %v (code %v), want code %v", err, CodeOf(err), code)
	}
}

func wantState(t *testing.T, p *Pool, id string, want State) {
	t.Helper()
	snap, ok := p.Snapshot(id)
	if !ok {
		t.Fatalf("Snapshot(%q): not found", id)
	}
	if snap.State != want {
		t.Fatalf("conn %q state = %v, want %v", id, snap.State, want)
	}
}

func wantActive(t *testing.T, p *Pool, id string, want int) {
	t.Helper()
	snap, ok := p.Snapshot(id)
	if !ok {
		t.Fatalf("Snapshot(%q): not found", id)
	}
	if len(snap.Streams) != want {
		t.Fatalf("conn %q active = %d, want %d", id, len(snap.Streams), want)
	}
}

// TestWorkedExample replays the scenario from the specification:
// m0=2, conns A and B added at now=0.
func TestWorkedExample(t *testing.T) {
	p := mustPool(t, 2, 1_000_000, 1<<31-1, 2)
	mustAdd(t, p, 0, "A")
	mustAdd(t, p, 0, "B")

	mustOpen(t, p, 1, "r1", "A", 1)
	mustOpen(t, p, 2, "r2", "B", 1) // A already has 1 active stream
	mustOpen(t, p, 3, "r3", "A", 3) // tie 1:1, smaller id wins
	mustOpen(t, p, 4, "r4", "B", 3)

	_, _, err := p.Open(5, "r5")
	wantErr(t, err, ErrNoCapacity)

	// GOAWAY(A, 1): stream 3 (r3) is unprocessed, r1 continues; A drains.
	unprocessed, err := p.GoAway(6, "A", 1)
	if err != nil {
		t.Fatalf("GoAway(A,1): %v", err)
	}
	if !reflect.DeepEqual(unprocessed, []string{"r3"}) {
		t.Fatalf("GoAway(A,1) unprocessed = %v, want [r3]", unprocessed)
	}
	wantState(t, p, "A", Draining)
	wantActive(t, p, "A", 1)

	// A is not Active and B is full: still no capacity.
	_, _, err = p.Open(7, "r3")
	wantErr(t, err, ErrNoCapacity)

	// Shrinking the limit below the active count succeeds.
	if err := p.SetMaxConcurrent(8, "B", 1); err != nil {
		t.Fatalf("SetMaxConcurrent(B,1): %v", err)
	}
	if _, err := p.CloseStream(9, "B", 1, Done); err != nil {
		t.Fatalf("CloseStream(B,1,Done): %v", err)
	}
	wantActive(t, p, "B", 1) // 1 active stream, not strictly < 1
	_, _, err = p.Open(10, "r3")
	wantErr(t, err, ErrNoCapacity)

	if _, err := p.CloseStream(11, "B", 3, Done); err != nil {
		t.Fatalf("CloseStream(B,3,Done): %v", err)
	}
	mustOpen(t, p, 12, "r3", "B", 5)

	// goAwayLast is 1: a larger lastID is rejected, and the rejection
	// must not change any state.
	_, err = p.GoAway(13, "A", 3)
	wantErr(t, err, ErrGoAwayUp)
	wantState(t, p, "A", Draining)
	wantActive(t, p, "A", 1)

	// GOAWAY(A, 0): r1 (stream 1 > 0) is unprocessed; A empties and closes.
	unprocessed, err = p.GoAway(14, "A", 0)
	if err != nil {
		t.Fatalf("GoAway(A,0): %v", err)
	}
	if !reflect.DeepEqual(unprocessed, []string{"r1"}) {
		t.Fatalf("GoAway(A,0) unprocessed = %v, want [r1]", unprocessed)
	}
	wantState(t, p, "A", Closed)
}

// TestStreamIDExhaustion: with maxID=3, allocating stream 3 drains the
// connection while stream 3 itself is usable.
func TestStreamIDExhaustion(t *testing.T) {
	p := mustPool(t, 2, 1_000_000, 3, 2)
	mustAdd(t, p, 0, "A")

	mustOpen(t, p, 1, "r1", "A", 1)
	wantState(t, p, "A", Active)
	mustOpen(t, p, 2, "r2", "A", 3) // maxID=3 is the last allocatable stream
	wantState(t, p, "A", Draining)

	// Draining: no new streams even though the limit is not the blocker.
	_, _, err := p.Open(3, "r3")
	wantErr(t, err, ErrNoCapacity)

	// Streams already in flight still close normally; emptying a Draining
	// connection closes it.
	if _, err := p.CloseStream(4, "A", 1, Done); err != nil {
		t.Fatalf("CloseStream(A,1): %v", err)
	}
	wantState(t, p, "A", Draining)
	if _, err := p.CloseStream(5, "A", 3, Done); err != nil {
		t.Fatalf("CloseStream(A,3): %v", err)
	}
	wantState(t, p, "A", Closed)
}

// TestGoAwayMaxID: GOAWAY with lastID=maxID removes nothing but drains.
func TestGoAwayMaxID(t *testing.T) {
	p := mustPool(t, 2, 1_000_000, 3, 2)
	mustAdd(t, p, 0, "A")
	mustOpen(t, p, 1, "r1", "A", 1)
	mustOpen(t, p, 2, "r2", "A", 3)
	wantState(t, p, "A", Draining) // already draining from ID exhaustion

	unprocessed, err := p.GoAway(3, "A", 3)
	if err != nil {
		t.Fatalf("GoAway(A,3): %v", err)
	}
	if len(unprocessed) != 0 {
		t.Fatalf("GoAway(A,maxID) unprocessed = %v, want empty", unprocessed)
	}
	wantState(t, p, "A", Draining)
	wantActive(t, p, "A", 2)

	// lastID beyond the last allocated stream (and != maxID) is rejected.
	q := mustPool(t, 3, 1_000_000, 7, 2)
	mustAdd(t, q, 0, "X")
	mustOpen(t, q, 1, "q1", "X", 1)
	_, err = q.GoAway(2, "X", 3) // last allocated is 1, 3 != maxID=7
	wantErr(t, err, ErrGoAwayBeyond)
	// lastID == maxID is exempt from the beyond check.
	if _, err := q.GoAway(3, "X", 7); err != nil {
		t.Fatalf("GoAway(X,7=maxID): %v", err)
	}
	wantState(t, q, "X", Draining)
	wantActive(t, q, "X", 1)
}

// TestRefuseThreshold: K=2; a Done between two Refused prevents draining,
// two consecutive Refused trigger it, and Reset does not reset the counter.
func TestRefuseThreshold(t *testing.T) {
	p := mustPool(t, 10, 1_000_000, 1<<31-1, 2)
	mustAdd(t, p, 0, "A")

	// A keeper stream stays open so the connection never empties; otherwise
	// a Refused that drains the connection would immediately close it.
	keeper, err := func() (int64, error) { _, s, err := p.Open(0, "keeper"); return s, err }()
	if err != nil {
		t.Fatalf("Open(keeper): %v", err)
	}

	open := func(now int64, req string) int64 {
		t.Helper()
		_, s, err := p.Open(now, req)
		if err != nil {
			t.Fatalf("Open(%q): %v", req, err)
		}
		return s
	}
	close := func(now int64, s int64, kind CloseKind) bool {
		t.Helper()
		retry, err := p.CloseStream(now, "A", s, kind)
		if err != nil {
			t.Fatalf("CloseStream(A,%d,%v): %v", s, kind, err)
		}
		return retry
	}

	// Refused, Done, Refused: counter goes 1 -> 0 -> 1, never reaches K=2.
	if !close(1, open(1, "r1"), Refused) {
		t.Fatal("Refused must be retryable")
	}
	close(2, open(2, "r2"), Done)
	close(3, open(3, "r3"), Refused)
	wantState(t, p, "A", Active)

	// Reset leaves the counter at 1; the next Refused reaches K=2.
	if close(4, open(4, "r4"), Reset) {
		t.Fatal("Reset must not be retryable")
	}
	close(5, open(5, "r5"), Refused)
	wantState(t, p, "A", Draining)

	snap, _ := p.Snapshot("A")
	if snap.Refuse != 2 {
		t.Fatalf("refuse = %d, want 2", snap.Refuse)
	}

	// Draining connection: closing the keeper empties it and closes it.
	if _, err := p.CloseStream(6, "A", keeper, Done); err != nil {
		t.Fatalf("CloseStream(keeper): %v", err)
	}
	wantState(t, p, "A", Closed)
}

// TestCloseStreamAfterGoAway: a stream removed by GOAWAY is unknown to
// CloseStream afterwards.
func TestCloseStreamAfterGoAway(t *testing.T) {
	p := mustPool(t, 2, 1_000_000, 1<<31-1, 2)
	mustAdd(t, p, 0, "A")
	mustOpen(t, p, 1, "r1", "A", 1)
	mustOpen(t, p, 2, "r2", "A", 3)

	unprocessed, err := p.GoAway(3, "A", 1)
	if err != nil {
		t.Fatalf("GoAway: %v", err)
	}
	if !reflect.DeepEqual(unprocessed, []string{"r2"}) {
		t.Fatalf("unprocessed = %v, want [r2]", unprocessed)
	}
	_, err = p.CloseStream(4, "A", 3, Done)
	wantErr(t, err, ErrUnknownStream)
	// r2 is no longer in flight, so it can be re-opened... on another conn.
	mustAdd(t, p, 5, "B")
	mustOpen(t, p, 6, "r2", "B", 1)
}

// TestTickExactTimeout: Tick closes an idle Active connection exactly at
// idleTimeout, and not one millisecond earlier.
func TestTickExactTimeout(t *testing.T) {
	p := mustPool(t, 1, 100, 1<<31-1, 2)
	mustAdd(t, p, 0, "A") // idleSince = 0
	mustAdd(t, p, 0, "B")

	mustOpen(t, p, 10, "r1", "A", 1) // A no longer idle
	closed, err := p.Tick(99)
	if err != nil {
		t.Fatalf("Tick(99): %v", err)
	}
	if len(closed) != 0 {
		t.Fatalf("Tick(99) closed = %v, want []", closed)
	}
	closed, err = p.Tick(100)
	if err != nil {
		t.Fatalf("Tick(100): %v", err)
	}
	if !reflect.DeepEqual(closed, []string{"B"}) {
		t.Fatalf("Tick(100) closed = %v, want [B]", closed)
	}
	wantState(t, p, "B", Closed)

	// A finishes its stream at now=150: idleSince resets to 150.
	if _, err := p.CloseStream(150, "A", 1, Done); err != nil {
		t.Fatalf("CloseStream: %v", err)
	}
	closed, err = p.Tick(249)
	if err != nil {
		t.Fatalf("Tick(249): %v", err)
	}
	if len(closed) != 0 {
		t.Fatalf("Tick(249) closed = %v, want []", closed)
	}
	closed, err = p.Tick(250)
	if err != nil {
		t.Fatalf("Tick(250): %v", err)
	}
	if !reflect.DeepEqual(closed, []string{"A"}) {
		t.Fatalf("Tick(250) closed = %v, want [A]", closed)
	}
}

// TestBadConfig: constructor arguments out of range are rejected.
func TestBadConfig(t *testing.T) {
	cases := []struct {
		m0          int
		idleTimeout int64
		maxID       int64
		k           int
	}{
		{0, 1, 1, 1}, {1001, 1, 1, 1},
		{1, 0, 1, 1}, {1, 1_000_000_001, 1, 1},
		{1, 1, 0, 1}, {1, 1, 2, 1}, {1, 1, 1<<31 + 1, 1},
		{1, 1, 1, 0}, {1, 1, 1, 101},
	}
	for i, c := range cases {
		if _, err := NewPool(c.m0, c.idleTimeout, c.maxID, c.k); CodeOf(err) != ErrBadConfig {
			t.Fatalf("case %d: err = %v, want ErrBadConfig", i, err)
		}
	}
}

// TestRejectedCallsAreAtomic: invalid parameters, clock rollback and state
// errors are reported with distinct codes, in the specified precedence
// order, and a rejected call changes neither state nor clock.
func TestRejectedCallsAreAtomic(t *testing.T) {
	p := mustPool(t, 1, 100, 1<<31-1, 2)
	mustAdd(t, p, 10, "A")
	mustOpen(t, p, 20, "r1", "A", 1)

	snapEqual := func(want ConnSnapshot, id string) {
		t.Helper()
		got, ok := p.Snapshot(id)
		if !ok {
			t.Fatalf("Snapshot(%q): not found", id)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("state changed: got %+v, want %+v", got, want)
		}
	}
	before, _ := p.Snapshot("A")

	// Parameter errors take precedence over clock rollback (now=5 < 20).
	wantErr(t, p.AddConn(5, "A"), ErrDupConn)
	_, _, err := p.Open(5, "r1")
	wantErr(t, err, ErrDupReq)
	wantErr(t, p.SetMaxConcurrent(5, "ZZZ", 1), ErrUnknownConn)
	wantErr(t, p.SetMaxConcurrent(5, "A", 0), ErrBadMaxConc)
	_, err = p.CloseStream(5, "A", 99, Done)
	wantErr(t, err, ErrUnknownStream)
	_, err = p.CloseStream(5, "A", 1, CloseKind(7))
	wantErr(t, err, ErrBadKind)
	_, err = p.GoAway(5, "A", 2)
	wantErr(t, err, ErrBadLastID)
	_, err = p.GoAway(5, "ZZZ", 1)
	wantErr(t, err, ErrUnknownConn)

	// Clock rollback, now with valid parameters.
	wantErr(t, p.AddConn(5, "B"), ErrClockBackwards)
	_, _, err = p.Open(5, "r2")
	wantErr(t, err, ErrClockBackwards)
	wantErr(t, p.SetMaxConcurrent(5, "A", 1), ErrClockBackwards)
	_, err = p.CloseStream(5, "A", 1, Done)
	wantErr(t, err, ErrClockBackwards)
	_, err = p.GoAway(5, "A", 1)
	wantErr(t, err, ErrClockBackwards)
	_, err = p.Tick(5)
	wantErr(t, err, ErrClockBackwards)

	// Nothing changed, including the clock: now=20 is still acceptable and
	// now=19 is still a rollback.
	snapEqual(before, "A")
	_, err = p.Tick(19)
	wantErr(t, err, ErrClockBackwards)
	if _, err := p.Tick(20); err != nil {
		t.Fatalf("Tick(20): %v", err)
	}

	// State errors: GOAWAY / SetMaxConcurrent on a Closed connection.
	if _, err := p.CloseStream(21, "A", 1, Done); err != nil {
		t.Fatalf("CloseStream: %v", err)
	}
	closed, err := p.Tick(121) // idle since 21, timeout 100
	if err != nil || !reflect.DeepEqual(closed, []string{"A"}) {
		t.Fatalf("Tick(121) = %v, %v; want [A]", closed, err)
	}
	wantErr(t, p.SetMaxConcurrent(122, "A", 1), ErrConnClosed)
	_, err = p.GoAway(122, "A", 1)
	wantErr(t, err, ErrConnClosed)
	// A Closed id can never be reused.
	wantErr(t, p.AddConn(123, "A"), ErrDupConn)
}

// TestConcurrentCalls: operations and queries from many goroutines must be
// race-free and every accepted call must observe a consistent state. Run
// with `go test -race`.
func TestConcurrentCalls(t *testing.T) {
	p := mustPool(t, 4, 50, 1<<31-1, 3)
	mustAdd(t, p, 0, "A")
	mustAdd(t, p, 0, "B")
	mustAdd(t, p, 0, "C")

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			now := int64(0)
			for i := 0; i < 200; i++ {
				now++ // per-goroutine monotone; losers get ErrClockBackwards
				id, stream, err := p.Open(now, reqName(g, i))
				if err == nil {
					_, _ = p.CloseStream(now, id, stream, CloseKind(i%3))
				}
				_, _ = p.Tick(now)
				_, _ = p.Snapshot("A")
			}
		}(g)
	}
	wg.Wait()

	// Every in-flight request sits on exactly one stream of one connection.
	seen := map[string]int{}
	for _, id := range []string{"A", "B", "C"} {
		snap, _ := p.Snapshot(id)
		if snap.State != Closed && len(snap.Streams) > snap.MaxConc {
			t.Fatalf("conn %q: %d active streams > maxConc %d", id, len(snap.Streams), snap.MaxConc)
		}
		for _, req := range snap.Streams {
			seen[req]++
		}
	}
	for req, count := range seen {
		if count != 1 {
			t.Fatalf("req %q on %d streams", req, count)
		}
	}
}

func reqName(g, i int) string {
	return string(rune('a'+g)) + "-" + string(rune('A'+i%26)) + "-" + itoa(i)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [8]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}
