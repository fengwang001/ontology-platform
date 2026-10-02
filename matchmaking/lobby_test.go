package matchmaking

import (
	"errors"
	"reflect"
	"testing"
)

func mustLobby(t *testing.T, w0, g, wmax, cap int64) *Lobby {
	t.Helper()
	l, err := NewLobby(w0, g, wmax, cap)
	if err != nil {
		t.Fatalf("NewLobby(%d,%d,%d,%d): unexpected error %v", w0, g, wmax, cap, err)
	}
	return l
}

func mustJoin(t *testing.T, l *Lobby, id, rating, now int64) {
	t.Helper()
	if err := l.Join(id, rating, now); err != nil {
		t.Fatalf("Join(%d,%d,%d): unexpected error %v", id, rating, now, err)
	}
}

func queueIDs(l *Lobby) []int64 {
	ids := []int64{}
	for _, p := range l.Queue() {
		ids = append(ids, p.ID)
	}
	return ids
}

func pairIDs(pairs []Pair) [][2]int64 {
	out := [][2]int64{}
	for _, p := range pairs {
		out = append(out, [2]int64{p.A.ID, p.B.ID})
	}
	return out
}

// assertPostTickInvariant checks that no two players left in the queue could
// have been paired at the tick time.
func assertPostTickInvariant(t *testing.T, l *Lobby, now int64) {
	t.Helper()
	q := l.Queue()
	for i := 0; i < len(q); i++ {
		for j := i + 1; j < len(q); j++ {
			a, b := q[i], q[j]
			diff := a.Rating - b.Rating
			if diff < 0 {
				diff = -diff
			}
			wa := l.tolerance(a.Joined, now)
			wb := l.tolerance(b.Joined, now)
			limit := wa
			if wb < limit {
				limit = wb
			}
			if diff <= limit {
				t.Fatalf("post-tick invariant violated at now=%d: %+v and %+v still pairable (diff=%d, limit=%d)",
					now, a, b, diff, limit)
			}
		}
	}
}

func TestNewLobbyValidation(t *testing.T) {
	bad := [][4]int64{
		{-1, 0, 0, 2},                        // W0 < 0
		{0, -1, 0, 2},                        // G < 0
		{0, 1_000_001, 0, 2},                 // G > 1e6
		{10, 0, 5, 2},                        // Wmax < W0
		{1_000_000_001, 0, 1_000_000_001, 2}, // W0 > 1e9
		{0, 0, 1_000_000_001, 2},             // Wmax > 1e9
		{0, 0, 0, 1},                         // Cap < 2
	}
	for _, c := range bad {
		if _, err := NewLobby(c[0], c[1], c[2], c[3]); !errors.Is(err, ErrInvalidParam) {
			t.Errorf("NewLobby%v: got %v, want ErrInvalidParam", c, err)
		}
	}
	if _, err := NewLobby(0, 0, 0, 2); err != nil {
		t.Errorf("NewLobby(0,0,0,2): unexpected error %v", err)
	}
	if _, err := NewLobby(1_000_000_000, 1_000_000, 1_000_000_000, 2); err != nil {
		t.Errorf("boundary NewLobby: unexpected error %v", err)
	}
}

func TestDiffExactlyEqualsToleranceMatches(t *testing.T) {
	l := mustLobby(t, 10, 0, 100, 4)
	mustJoin(t, l, 1, 100, 0)
	mustJoin(t, l, 2, 110, 0) // diff == 10 == W0
	pairs, err := l.Tick(0)
	if err != nil {
		t.Fatal(err)
	}
	if got := pairIDs(pairs); !reflect.DeepEqual(got, [][2]int64{{1, 2}}) {
		t.Fatalf("pairs = %v, want [[1 2]]", got)
	}
	if pairs[0].Diff != 10 || pairs[0].At != 0 {
		t.Fatalf("pair meta = (diff=%d, at=%d), want (10, 0)", pairs[0].Diff, pairs[0].At)
	}
	if len(l.Queue()) != 0 {
		t.Fatalf("queue not empty: %v", l.Queue())
	}
}

func TestDiffOneAboveToleranceDoesNotMatch(t *testing.T) {
	l := mustLobby(t, 10, 0, 100, 4)
	mustJoin(t, l, 1, 100, 0)
	mustJoin(t, l, 2, 111, 0) // diff == 11 > 10
	pairs, err := l.Tick(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(pairs) != 0 {
		t.Fatalf("pairs = %v, want none", pairIDs(pairs))
	}
	assertPostTickInvariant(t, l, 0)
}

func TestNarrowerSideGoverns(t *testing.T) {
	// a waits long (wide radius), b just joined (narrow radius == W0).
	l := mustLobby(t, 5, 10, 1000, 4)
	mustJoin(t, l, 1, 100, 0)  // at now=10: w = 5+10*10 = 105
	mustJoin(t, l, 2, 150, 10) // at now=10: w = 5 (fresh join)
	pairs, err := l.Tick(10)   // diff=50, min(105,5)=5 -> no match
	if err != nil {
		t.Fatal(err)
	}
	if len(pairs) != 0 {
		t.Fatalf("pairs = %v, want none (narrow side must govern)", pairIDs(pairs))
	}
	assertPostTickInvariant(t, l, 10)
}

func TestToleranceCappedByWmax(t *testing.T) {
	l := mustLobby(t, 0, 10, 50, 4)
	mustJoin(t, l, 1, 100, 0)
	mustJoin(t, l, 2, 150, 0) // diff == 50 == Wmax (uncapped radius would be 10*100=1000)
	pairs, err := l.Tick(100)
	if err != nil {
		t.Fatal(err)
	}
	if got := pairIDs(pairs); !reflect.DeepEqual(got, [][2]int64{{1, 2}}) {
		t.Fatalf("pairs = %v, want [[1 2]] (radius capped at Wmax=50)", got)
	}

	l2 := mustLobby(t, 0, 10, 50, 4)
	mustJoin(t, l2, 1, 100, 0)
	mustJoin(t, l2, 2, 151, 0) // diff == 51 > Wmax
	pairs, err = l2.Tick(100)
	if err != nil {
		t.Fatal(err)
	}
	if len(pairs) != 0 {
		t.Fatalf("pairs = %v, want none (diff exceeds capped radius)", pairIDs(pairs))
	}
}

func TestLongestWaitingFirstNotGlobalClosest(t *testing.T) {
	// A(100) waits longest. B(105), C(106) are the globally closest pair
	// (diff 1), but A is processed first and grabs B (diff 5 < 6 to C).
	l := mustLobby(t, 100, 0, 100, 8)
	mustJoin(t, l, 1, 100, 0)
	mustJoin(t, l, 2, 105, 1)
	mustJoin(t, l, 3, 106, 2)
	pairs, err := l.Tick(10)
	if err != nil {
		t.Fatal(err)
	}
	if got := pairIDs(pairs); !reflect.DeepEqual(got, [][2]int64{{1, 2}}) {
		t.Fatalf("pairs = %v, want [[1 2]] (longest waiter picks first)", got)
	}
	if got := queueIDs(l); !reflect.DeepEqual(got, []int64{3}) {
		t.Fatalf("queue = %v, want [3]", got)
	}
}

func TestTieBreakByJoinedThenID(t *testing.T) {
	// Equal diff: smaller joined wins.
	l := mustLobby(t, 100, 0, 100, 8)
	mustJoin(t, l, 1, 100, 0)
	mustJoin(t, l, 3, 105, 3) // diff 5, joined earlier
	mustJoin(t, l, 2, 105, 5) // same diff 5 as id=3, but joined later
	pairs, err := l.Tick(10)
	if err != nil {
		t.Fatal(err)
	}
	if got := pairIDs(pairs); !reflect.DeepEqual(got, [][2]int64{{1, 3}}) {
		t.Fatalf("pairs = %v, want [[1 3]] (tie on diff -> smaller joined)", got)
	}

	// Equal diff and equal joined: smaller id wins.
	l2 := mustLobby(t, 100, 0, 100, 8)
	mustJoin(t, l2, 1, 100, 0)
	mustJoin(t, l2, 3, 105, 5)
	mustJoin(t, l2, 2, 105, 5) // same rating & joined as id=3, smaller id
	pairs, err = l2.Tick(10)
	if err != nil {
		t.Fatal(err)
	}
	if got := pairIDs(pairs); !reflect.DeepEqual(got, [][2]int64{{1, 2}}) {
		t.Fatalf("pairs = %v, want [[1 2]] (tie on diff+joined -> smaller id)", got)
	}
}

func TestMatchedPlayersNotChosenAgainInSameTick(t *testing.T) {
	// Processing order 1,2,3,4. 1 pairs with 2; 3 must not consider 1 or 2
	// and pairs with 4.
	l := mustLobby(t, 100, 0, 100, 8)
	mustJoin(t, l, 1, 100, 0)
	mustJoin(t, l, 2, 100, 1)
	mustJoin(t, l, 3, 100, 2)
	mustJoin(t, l, 4, 100, 3)
	pairs, err := l.Tick(10)
	if err != nil {
		t.Fatal(err)
	}
	if got := pairIDs(pairs); !reflect.DeepEqual(got, [][2]int64{{1, 2}, {3, 4}}) {
		t.Fatalf("pairs = %v, want [[1 2] [3 4]]", got)
	}
	if len(l.Queue()) != 0 {
		t.Fatalf("queue not empty: %v", l.Queue())
	}
}

func TestFreshJoinRadiusEqualsW0(t *testing.T) {
	l := mustLobby(t, 7, 100, 1000, 4)
	mustJoin(t, l, 1, 100, 5)
	mustJoin(t, l, 2, 107, 5) // joined at now=5, tick at now=5: radius == W0 == 7
	pairs, err := l.Tick(5)
	if err != nil {
		t.Fatal(err)
	}
	if got := pairIDs(pairs); !reflect.DeepEqual(got, [][2]int64{{1, 2}}) {
		t.Fatalf("pairs = %v, want [[1 2]] (fresh radius == W0)", got)
	}

	l2 := mustLobby(t, 7, 100, 1000, 4)
	mustJoin(t, l2, 1, 100, 5)
	mustJoin(t, l2, 2, 108, 5) // diff 8 > W0 at the same instant
	pairs, err = l2.Tick(5)
	if err != nil {
		t.Fatal(err)
	}
	if len(pairs) != 0 {
		t.Fatalf("pairs = %v, want none", pairIDs(pairs))
	}
}

func TestJoinRejectionsOrderedAndAtomic(t *testing.T) {
	l := mustLobby(t, 0, 0, 0, 2)
	mustJoin(t, l, 1, 100, 10)

	cases := []struct {
		name            string
		id, rating, now int64
		want            error
	}{
		{"negative time", 2, 100, -1, ErrInvalidTime},
		{"time too large", 2, 100, 1_000_000_001, ErrInvalidTime},
		{"clock rollback", 2, 100, 5, ErrClockRollback},
		{"bad id", 0, 100, 10, ErrInvalidID},
		{"bad rating low", 2, -1, 10, ErrInvalidRating},
		{"bad rating high", 2, 5001, 10, ErrInvalidRating},
		{"duplicate id", 1, 100, 10, ErrDuplicateID},
	}
	for _, c := range cases {
		if err := l.Join(c.id, c.rating, c.now); !errors.Is(err, c.want) {
			t.Errorf("%s: Join(%d,%d,%d) = %v, want %v", c.name, c.id, c.rating, c.now, err, c.want)
		}
	}

	// Queue full: cap=2, one slot left; fill it, then the next join is full.
	mustJoin(t, l, 2, 200, 10)
	if err := l.Join(3, 300, 10); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("Join on full queue = %v, want ErrQueueFull", err)
	}

	// Priority: invalid time beats duplicate id; duplicate id beats full queue.
	if err := l.Join(1, 100, -1); !errors.Is(err, ErrInvalidTime) {
		t.Fatalf("priority time-vs-duplicate = %v, want ErrInvalidTime", err)
	}
	if err := l.Join(1, 100, 10); !errors.Is(err, ErrDuplicateID) {
		t.Fatalf("priority duplicate-vs-full = %v, want ErrDuplicateID", err)
	}

	// Rejections changed nothing: queue intact, maxNow still 10.
	if got := queueIDs(l); !reflect.DeepEqual(got, []int64{1, 2}) {
		t.Fatalf("queue = %v, want [1 2] after rejected joins", got)
	}
	if err := l.Join(4, 400, 10); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("maxNow moved by rejected ops: Join(4,400,10) = %v, want ErrQueueFull", err)
	}
}

func TestLeaveRejectionsOrdered(t *testing.T) {
	l := mustLobby(t, 100, 0, 100, 8)
	mustJoin(t, l, 1, 100, 10)
	mustJoin(t, l, 2, 100, 10)
	mustJoin(t, l, 3, 5000, 10) // stays queued (diff too large)

	if _, err := l.Tick(10); err != nil {
		t.Fatal(err)
	}
	// After the tick: 1 and 2 are matched, 3 is still queued.
	cases := []struct {
		name    string
		id, now int64
		want    error
	}{
		{"negative time", 3, -1, ErrInvalidTime},
		{"time too large", 3, 1_000_000_001, ErrInvalidTime},
		{"clock rollback", 3, 5, ErrClockRollback},
		{"unknown id", 99, 10, ErrUnknownID},
		{"already matched", 1, 10, ErrAlreadyMatched},
	}
	for _, c := range cases {
		if err := l.Leave(c.id, c.now); !errors.Is(err, c.want) {
			t.Errorf("%s: Leave(%d,%d) = %v, want %v", c.name, c.id, c.now, err, c.want)
		}
	}

	// A real leave succeeds, then leaving again reports already-left.
	if err := l.Leave(3, 10); err != nil {
		t.Fatalf("Leave(3,10) = %v, want nil", err)
	}
	if err := l.Leave(3, 10); !errors.Is(err, ErrAlreadyLeft) {
		t.Fatalf("Leave(3,10) again = %v, want ErrAlreadyLeft", err)
	}
	// Priority: invalid time beats unknown id; unknown id beats already-matched.
	if err := l.Leave(99, -1); !errors.Is(err, ErrInvalidTime) {
		t.Fatalf("priority time-vs-unknown = %v, want ErrInvalidTime", err)
	}
	// Rejections changed nothing: maxNow still 10, queue empty.
	if got := queueIDs(l); len(got) != 0 {
		t.Fatalf("queue = %v, want empty", got)
	}
	if err := l.Leave(1, 10); !errors.Is(err, ErrAlreadyMatched) {
		t.Fatalf("maxNow moved by rejected ops: Leave(1,10) = %v, want ErrAlreadyMatched", err)
	}
}

func TestLeaveRemovesFromQueue(t *testing.T) {
	l := mustLobby(t, 0, 0, 0, 4)
	mustJoin(t, l, 1, 100, 0)
	mustJoin(t, l, 2, 200, 0)
	if err := l.Leave(1, 0); err != nil {
		t.Fatal(err)
	}
	if got := queueIDs(l); !reflect.DeepEqual(got, []int64{2}) {
		t.Fatalf("queue = %v, want [2]", got)
	}
	// A left id cannot rejoin.
	if err := l.Join(1, 100, 0); !errors.Is(err, ErrDuplicateID) {
		t.Fatalf("rejoin after leave = %v, want ErrDuplicateID", err)
	}
}

func TestTickRejections(t *testing.T) {
	l := mustLobby(t, 0, 0, 0, 4)
	mustJoin(t, l, 1, 100, 10)
	if _, err := l.Tick(-1); !errors.Is(err, ErrInvalidTime) {
		t.Fatalf("Tick(-1) = %v, want ErrInvalidTime", err)
	}
	if _, err := l.Tick(1_000_000_001); !errors.Is(err, ErrInvalidTime) {
		t.Fatalf("Tick(1e9+1) = %v, want ErrInvalidTime", err)
	}
	if _, err := l.Tick(5); !errors.Is(err, ErrClockRollback) {
		t.Fatalf("Tick(5) = %v, want ErrClockRollback", err)
	}
	// Rejected ticks did not move maxNow: Tick(10) still works.
	if _, err := l.Tick(10); err != nil {
		t.Fatalf("Tick(10) = %v, want nil", err)
	}
}

func TestQueueOrdering(t *testing.T) {
	l := mustLobby(t, 0, 0, 0, 8)
	mustJoin(t, l, 5, 100, 0)
	mustJoin(t, l, 2, 100, 0) // same joined, smaller id sorts first
	mustJoin(t, l, 9, 100, 3)
	mustJoin(t, l, 1, 100, 3)
	got := queueIDs(l)
	if !reflect.DeepEqual(got, []int64{2, 5, 1, 9}) {
		t.Fatalf("queue = %v, want [2 5 1 9] (joined, then id)", got)
	}
}
