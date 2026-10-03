package redlock

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

func mustNew(t *testing.T, n int, tn, d, maxTTL int64) *Engine {
	t.Helper()
	e, err := New(n, tn, d, maxTTL)
	if err != nil {
		t.Fatalf("New(%d, %d, %d, %d): unexpected error: %v", n, tn, d, maxTTL, err)
	}
	return e
}

func mustAcquire(t *testing.T, e *Engine, res string, ttl, start int64, rtt []int64) AcquireResult {
	t.Helper()
	r, err := e.Acquire(res, ttl, start, rtt)
	if err != nil {
		t.Fatalf("Acquire(%q, %d, %d, %v): unexpected error: %v", res, ttl, start, rtt, err)
	}
	return r
}

func mustCount(t *testing.T, e *Engine, res string, k, now int64) int {
	t.Helper()
	c, err := e.Count(res, k, now)
	if err != nil {
		t.Fatalf("Count(%q, %d, %d): unexpected error: %v", res, k, now, err)
	}
	return c
}

// TestSpecExample replays the worked example from the specification.
func TestSpecExample(t *testing.T) {
	e := mustNew(t, 5, 50, 10, 1000)

	r1 := mustAcquire(t, e, "r", 1000, 0, []int64{10, 10, 10, -1, 10})
	want1 := AcquireResult{Token: 1, Success: true, Grants: 4, Validity: 898, Until: 988, Cend: 90}
	if r1 != want1 {
		t.Fatalf("acquire r: got %+v, want %+v", r1, want1)
	}
	if got := mustCount(t, e, "r", 1, 1004); got != 4 {
		t.Fatalf("Count(r,1,1004) = %d, want 4", got)
	}
	if got := mustCount(t, e, "r", 1, 1005); got != 3 {
		t.Fatalf("Count(r,1,1005) = %d, want 3", got)
	}

	r2 := mustAcquire(t, e, "s", 100, 100, []int64{40, 40, 40, 40, 40})
	want2 := AcquireResult{Token: 2, Success: false, Grants: 5, Validity: -103, Until: 197, Cend: 300}
	if r2 != want2 {
		t.Fatalf("acquire s: got %+v, want %+v", r2, want2)
	}
	if got := mustCount(t, e, "s", 2, 300); got != 0 {
		t.Fatalf("Count(s,2,300) = %d, want 0 (failed acquire must release all nodes)", got)
	}

	r3 := mustAcquire(t, e, "t", 1000, 300, []int64{70, 10, 10, 10, 10})
	want3 := AcquireResult{Token: 3, Success: true, Grants: 4, Validity: 898, Until: 1288, Cend: 390}
	if r3 != want3 {
		t.Fatalf("acquire t: got %+v, want %+v", r3, want3)
	}
	if got := mustCount(t, e, "t", 3, 400); got != 5 {
		t.Fatalf("Count(t,3,400) = %d, want 5 (timed-out node keeps its record on success)", got)
	}
}

// TestArrivalFloorParity checks a = c + floor(rtt/2) for odd and even rtt
// by observing the stored expiry a+ttl through Count boundaries.
func TestArrivalFloorParity(t *testing.T) {
	e := mustNew(t, 1, 100, 0, 100000)

	// Odd rtt 7: arrival a = 0 + 3, expiry = 3 + 10 = 13.
	r := mustAcquire(t, e, "odd", 10, 0, []int64{7})
	if !r.Success || r.Cend != 7 {
		t.Fatalf("odd rtt acquire: %+v", r)
	}
	if got := mustCount(t, e, "odd", 1, 12); got != 1 {
		t.Fatalf("Count(odd,1,12) = %d, want 1 (expiry 13 not yet reached)", got)
	}
	if got := mustCount(t, e, "odd", 1, 13); got != 0 {
		t.Fatalf("Count(odd,1,13) = %d, want 0 (expiry 13 is not > 13)", got)
	}

	// Even rtt 8: arrival a = 7 + 4 = 11, expiry = 11 + 20 = 31.
	r2 := mustAcquire(t, e, "even", 20, 7, []int64{8})
	if !r2.Success || r2.Cend != 15 {
		t.Fatalf("even rtt acquire: %+v", r2)
	}
	if got := mustCount(t, e, "even", 2, 30); got != 1 {
		t.Fatalf("Count(even,2,30) = %d, want 1 (expiry 31 not yet reached)", got)
	}
	if got := mustCount(t, e, "even", 2, 31); got != 0 {
		t.Fatalf("Count(even,2,31) = %d, want 0 (expiry 31 is not > 31)", got)
	}
}

// TestQuorumExactAndBelow checks that with N=4 the quorum is 3: g=3
// succeeds while g=2 fails.
func TestQuorumExactAndBelow(t *testing.T) {
	e := mustNew(t, 4, 50, 0, 10000)

	// Three reachable grants, one unreachable: g = 3 = floor(4/2)+1.
	r1 := mustAcquire(t, e, "q3", 1000, 0, []int64{10, 10, 10, -1})
	if !r1.Success || r1.Grants != 3 {
		t.Fatalf("g=3 with N=4 must succeed: %+v", r1)
	}
	if r1.Cend != 80 { // 10+10+10+50
		t.Fatalf("cend = %d, want 80", r1.Cend)
	}

	// Two reachable grants, two unreachable: g = 2 < 3.
	r2 := mustAcquire(t, e, "q2", 1000, 80, []int64{10, 10, -1, -1})
	if r2.Success || r2.Grants != 2 {
		t.Fatalf("g=2 with N=4 must fail: %+v", r2)
	}
	if got := mustCount(t, e, "q2", 2, 200); got != 0 {
		t.Fatalf("Count(q2,2,200) = %d, want 0 after failure release", got)
	}
}

// TestValidityZeroAndOne checks v == 0 fails while v == 1 succeeds.
func TestValidityZeroAndOne(t *testing.T) {
	e := mustNew(t, 1, 1000, 0, 100000)

	// D=0 so dr=2; elapsed 98 makes v = 100-98-2 = 0.
	r0 := mustAcquire(t, e, "zero", 100, 0, []int64{98})
	if r0.Success || r0.Validity != 0 || r0.Grants != 1 {
		t.Fatalf("v=0 must fail: %+v", r0)
	}
	if got := mustCount(t, e, "zero", 1, 98); got != 0 {
		t.Fatalf("failed acquire must release: Count = %d, want 0", got)
	}

	// Elapsed 97 makes v = 100-97-2 = 1.
	r1 := mustAcquire(t, e, "one", 100, 98, []int64{97})
	if !r1.Success || r1.Validity != 1 {
		t.Fatalf("v=1 must succeed: %+v", r1)
	}
	if r1.Until != r1.Cend+r1.Validity {
		t.Fatalf("Until %d != cend+v %d", r1.Until, r1.Cend+r1.Validity)
	}
}

// TestDriftFloorPlusTwo checks dr = floor(ttl*D/1000) + 2.
func TestDriftFloorPlusTwo(t *testing.T) {
	// D=0: dr = 0 + 2 = 2.
	e0 := mustNew(t, 1, 1000, 0, 100000)
	r := mustAcquire(t, e0, "d0", 100, 0, []int64{0})
	if r.Validity != 98 || r.Until != 98 {
		t.Fatalf("D=0 must give dr=2: %+v", r)
	}

	e := mustNew(t, 1, 1000, 10, 100000)
	// ttl=199, D=10: floor(1990/1000)=1, dr=3.
	r1 := mustAcquire(t, e, "d199", 199, 0, []int64{0})
	if r1.Validity != 196 || r1.Until != 196 {
		t.Fatalf("ttl=199 D=10 must give dr=3: %+v", r1)
	}
	// ttl=200, D=10: floor(2000/1000)=2, dr=4.
	r2 := mustAcquire(t, e, "d200", 200, 0, []int64{0})
	if r2.Validity != 196 || r2.Until != 196 {
		t.Fatalf("ttl=200 D=10 must give dr=4: %+v", r2)
	}
}

// TestTimeoutGrantNotCountedAndReleased checks that a timed-out node
// (rtt > Tn) still executes and stores the record, but does not count
// toward g; on failure its record is released too.
func TestTimeoutGrantNotCountedAndReleased(t *testing.T) {
	e := mustNew(t, 5, 50, 0, 100000)

	// Nodes 0-2 time out (70 > Tn=50) but grant; nodes 3,4 count: g=2 < 3.
	r := mustAcquire(t, e, "to", 10000, 0, []int64{70, 70, 70, 10, 10})
	if r.Success || r.Grants != 2 {
		t.Fatalf("timed-out grants must not count: %+v", r)
	}
	if r.Cend != 170 { // 50+50+50+10+10
		t.Fatalf("cend = %d, want 170", r.Cend)
	}
	// Failure release must cover the timed-out nodes as well.
	if got := mustCount(t, e, "to", 1, 170); got != 0 {
		t.Fatalf("Count(to,1,170) = %d, want 0: timed-out grants must be released", got)
	}

	// Success variant: N=3, one timed-out grant, g=2 = quorum.
	e2 := mustNew(t, 3, 50, 0, 100000)
	r2 := mustAcquire(t, e2, "ok", 10000, 0, []int64{70, 10, 10})
	if !r2.Success || r2.Grants != 2 {
		t.Fatalf("g=2 with N=3 must succeed: %+v", r2)
	}
	// On success the timed-out node's record persists.
	if got := mustCount(t, e2, "ok", 1, 70); got != 3 {
		t.Fatalf("Count(ok,1,70) = %d, want 3 (timed-out node keeps record on success)", got)
	}
}

// TestUnreachableRelease checks unreachable nodes cost Tn on the client
// clock, are skipped during acquire, and are included in the failure
// release fan-out (a no-op for them since they never stored the token).
func TestUnreachableRelease(t *testing.T) {
	e := mustNew(t, 3, 50, 0, 10000)

	r := mustAcquire(t, e, "u", 1000, 0, []int64{-1, -1, 10})
	if r.Success || r.Grants != 1 {
		t.Fatalf("g=1 with N=3 must fail: %+v", r)
	}
	if r.Cend != 110 { // 50+50+10
		t.Fatalf("cend = %d, want 110", r.Cend)
	}
	if got := mustCount(t, e, "u", 1, 110); got != 0 {
		t.Fatalf("Count(u,1,110) = %d, want 0 after failure release", got)
	}
	// The failed acquire consumed token 1; the next call gets token 2.
	r2 := mustAcquire(t, e, "u", 1000, 110, []int64{10, 10, 10})
	if r2.Token != 2 || !r2.Success {
		t.Fatalf("next acquire must take token 2: %+v", r2)
	}
}

// TestExpiryBoundaryNX checks that arrival exactly at the old record's
// expiry overwrites it, while arrival one tick earlier is NX-rejected,
// even for the same client's own old token.
func TestExpiryBoundaryNX(t *testing.T) {
	// Arrival == expiry: grant.
	e := mustNew(t, 1, 1000, 0, 100000)
	mustAcquire(t, e, "r", 100, 0, []int64{0}) // token 1, expiry 100
	r := mustAcquire(t, e, "r", 100, 100, []int64{0})
	if !r.Success || r.Grants != 1 || r.Token != 2 {
		t.Fatalf("arrival at exactly expiry must grant: %+v", r)
	}

	// Arrival == expiry-1: NX reject, same client's old token rejected too.
	e2 := mustNew(t, 1, 1000, 0, 100000)
	mustAcquire(t, e2, "r", 100, 0, []int64{0}) // token 1, expiry 100
	r2 := mustAcquire(t, e2, "r", 100, 99, []int64{0})
	if r2.Success || r2.Grants != 0 || r2.Token != 2 {
		t.Fatalf("arrival one tick before expiry must be NX-rejected: %+v", r2)
	}
	// The old record survived the failed attempt (release only deletes
	// records carrying the failed token 2).
	if got := mustCount(t, e2, "r", 1, 99); got != 1 {
		t.Fatalf("Count(r,1,99) = %d, want 1: old token must survive", got)
	}
}

// TestFailedAcquireConsumesToken checks tokens are burned by failures.
func TestFailedAcquireConsumesToken(t *testing.T) {
	e := mustNew(t, 1, 1000, 0, 100000)

	f1 := mustAcquire(t, e, "a", 100, 0, []int64{99}) // v = -1, fails
	f2 := mustAcquire(t, e, "b", 100, 99, []int64{99})
	ok := mustAcquire(t, e, "c", 100, 198, []int64{0})
	if f1.Token != 1 || f1.Success {
		t.Fatalf("first acquire: %+v", f1)
	}
	if f2.Token != 2 || f2.Success {
		t.Fatalf("second acquire: %+v", f2)
	}
	if ok.Token != 3 || !ok.Success {
		t.Fatalf("third acquire must take token 3: %+v", ok)
	}
}

// TestRestartQuietBoundary checks that after Restart(i, now) node i
// rejects arrivals before now+MaxTTL and grants exactly at it.
func TestRestartQuietBoundary(t *testing.T) {
	// Arrival exactly at q = 400+1000 = 1400: grant.
	e := mustNew(t, 2, 100, 0, 1000)
	if err := e.Restart(1, 400); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	r := mustAcquire(t, e, "r", 1000, 1350, []int64{0, 100})
	if !r.Success || r.Grants != 2 {
		t.Fatalf("arrival at exactly q must grant: %+v", r)
	}
	if got := mustCount(t, e, "r", 1, 1500); got != 2 {
		t.Fatalf("Count(r,1,1500) = %d, want 2", got)
	}

	// Arrival at q-1 = 1399: rejected by the silent period.
	e2 := mustNew(t, 2, 100, 0, 1000)
	if err := e2.Restart(1, 400); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	r2 := mustAcquire(t, e2, "r", 1000, 1350, []int64{0, 98})
	if r2.Success || r2.Grants != 1 {
		t.Fatalf("arrival at q-1 must be rejected: %+v", r2)
	}

	// Restart also wipes existing records of the node.
	e3 := mustNew(t, 1, 100, 0, 1000)
	mustAcquire(t, e3, "w", 1000, 0, []int64{0})
	if err := e3.Restart(0, 10); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if got := mustCount(t, e3, "w", 1, 10); got != 0 {
		t.Fatalf("Count(w,1,10) = %d, want 0 after Restart wiped the node", got)
	}
	if got := e3.Clock(); got != 10 {
		t.Fatalf("T = %d, want 10 after Restart", got)
	}
}

// TestUnlockIgnoresExpiry checks Unlock deletes records regardless of
// whether they have already expired.
func TestUnlockIgnoresExpiry(t *testing.T) {
	e := mustNew(t, 2, 100, 0, 100000)
	mustAcquire(t, e, "r", 100, 0, []int64{0, 0}) // token 1, expiry 100 on both

	removed, err := e.Unlock("r", 1, 500) // long past expiry
	if err != nil {
		t.Fatalf("Unlock: %v", err)
	}
	if removed != 2 {
		t.Fatalf("Unlock removed %d, want 2 (expiry must not matter)", removed)
	}
	if got := mustCount(t, e, "r", 1, 500); got != 0 {
		t.Fatalf("Count(r,1,500) = %d, want 0", got)
	}
	if got := e.Clock(); got != 500 {
		t.Fatalf("T = %d, want 500 after Unlock", got)
	}
	// Unlocking a foreign token removes nothing.
	removed2, err := e.Unlock("r", 999, 600)
	if err != nil || removed2 != 0 {
		t.Fatalf("Unlock foreign token: removed=%d err=%v", removed2, err)
	}
}

// TestCountExpiryBoundary checks Count uses expiry > now strictly and
// does not move the engine clock.
func TestCountExpiryBoundary(t *testing.T) {
	e := mustNew(t, 2, 100, 0, 100000)
	mustAcquire(t, e, "r", 100, 0, []int64{0, 0}) // expiry 100 on both nodes

	if got := mustCount(t, e, "r", 1, 99); got != 2 {
		t.Fatalf("Count(r,1,99) = %d, want 2", got)
	}
	if got := mustCount(t, e, "r", 1, 100); got != 0 {
		t.Fatalf("Count(r,1,100) = %d, want 0 (expiry == now does not count)", got)
	}
	if got := e.Clock(); got != 0 {
		t.Fatalf("Count must not move T: got %d, want 0", got)
	}
	// Wrong token does not count.
	if got := mustCount(t, e, "r", 2, 0); got != 0 {
		t.Fatalf("Count(r,2,0) = %d, want 0", got)
	}
}

// TestInvalidConfig checks out-of-range constructor parameters reject
// the whole configuration.
func TestInvalidConfig(t *testing.T) {
	cases := []struct {
		n          int
		tn, d, max int64
	}{
		{0, 50, 10, 1000},
		{10, 50, 10, 1000},
		{5, 0, 10, 1000},
		{5, 1_000_001, 10, 1000},
		{5, 50, -1, 1000},
		{5, 50, 1001, 1000},
		{5, 50, 10, 0},
		{5, 50, 10, 1_000_000_001},
	}
	for _, c := range cases {
		if _, err := New(c.n, c.tn, c.d, c.max); !errors.Is(err, ErrInvalidConfig) {
			t.Errorf("New(%d, %d, %d, %d): err = %v, want ErrInvalidConfig", c.n, c.tn, c.d, c.max, err)
		}
	}
	// Boundary values are legal.
	if _, err := New(1, 1, 0, 1); err != nil {
		t.Errorf("New(1,1,0,1): %v", err)
	}
	if _, err := New(9, 1_000_000, 1000, 1_000_000_000); err != nil {
		t.Errorf("New(9,1e6,1000,1e9): %v", err)
	}
}

// TestRejectedOpsNoStateChange checks rejection categories, their
// precedence (argument > time > clock rewind), and that rejected calls
// leave records, quiet deadlines, T and the token counter untouched.
func TestRejectedOpsNoStateChange(t *testing.T) {
	e := mustNew(t, 2, 100, 10, 1000)
	mustAcquire(t, e, "ok", 1000, 0, []int64{0, 0})    // token 1
	mustAcquire(t, e, "ok2", 1000, 100, []int64{0, 0}) // token 2, T = 100

	type op func() error
	rejections := []struct {
		name string
		op   op
		want error
	}{
		// Acquire: argument errors.
		{"acquire empty res", func() error { _, err := e.Acquire("", 10, 100, []int64{0, 0}); return err }, ErrInvalidArgument},
		{"acquire ttl 0", func() error { _, err := e.Acquire("x", 0, 100, []int64{0, 0}); return err }, ErrInvalidArgument},
		{"acquire ttl too big", func() error { _, err := e.Acquire("x", 1001, 100, []int64{0, 0}); return err }, ErrInvalidArgument},
		{"acquire rtt length", func() error { _, err := e.Acquire("x", 10, 100, []int64{0}); return err }, ErrInvalidArgument},
		{"acquire rtt -2", func() error { _, err := e.Acquire("x", 10, 100, []int64{0, -2}); return err }, ErrInvalidArgument},
		{"acquire rtt too big", func() error { _, err := e.Acquire("x", 10, 100, []int64{0, 201}); return err }, ErrInvalidArgument},
		{"acquire arg beats time", func() error { _, err := e.Acquire("", 10, -1, []int64{0, 0}); return err }, ErrInvalidArgument},
		// Acquire: time errors.
		{"acquire start -1", func() error { _, err := e.Acquire("x", 10, -1, []int64{0, 0}); return err }, ErrInvalidTime},
		{"acquire start too big", func() error { _, err := e.Acquire("x", 10, 1_000_000_000_000_001, []int64{0, 0}); return err }, ErrInvalidTime},
		{"acquire time beats rewind", func() error { _, err := e.Acquire("x", 10, -1, []int64{0, 0}); return err }, ErrInvalidTime},
		// Acquire: clock rewind.
		{"acquire rewind", func() error { _, err := e.Acquire("x", 10, 99, []int64{0, 0}); return err }, ErrClockRewind},
		// Unlock.
		{"unlock empty res", func() error { _, err := e.Unlock("", 1, 100); return err }, ErrInvalidArgument},
		{"unlock token 0", func() error { _, err := e.Unlock("ok", 0, 100); return err }, ErrInvalidArgument},
		{"unlock arg beats time", func() error { _, err := e.Unlock("", 1, -1); return err }, ErrInvalidArgument},
		{"unlock now -1", func() error { _, err := e.Unlock("ok", 1, -1); return err }, ErrInvalidTime},
		{"unlock now too big", func() error { _, err := e.Unlock("ok", 1, 1_000_000_000_000_001); return err }, ErrInvalidTime},
		{"unlock rewind", func() error { _, err := e.Unlock("ok", 1, 99); return err }, ErrClockRewind},
		// Restart.
		{"restart node -1", func() error { return e.Restart(-1, 100) }, ErrInvalidArgument},
		{"restart node N", func() error { return e.Restart(2, 100) }, ErrInvalidArgument},
		{"restart arg beats time", func() error { return e.Restart(9, -1) }, ErrInvalidArgument},
		{"restart now -1", func() error { return e.Restart(0, -1) }, ErrInvalidTime},
		{"restart rewind", func() error { return e.Restart(0, 99) }, ErrClockRewind},
		// Count.
		{"count empty res", func() error { _, err := e.Count("", 1, 100); return err }, ErrInvalidArgument},
		{"count token 0", func() error { _, err := e.Count("ok", 0, 100); return err }, ErrInvalidArgument},
		{"count now too big", func() error { _, err := e.Count("ok", 1, 1_000_000_000_000_001); return err }, ErrInvalidTime},
		{"count rewind", func() error { _, err := e.Count("ok", 1, 99); return err }, ErrClockRewind},
	}
	for _, tc := range rejections {
		if err := tc.op(); !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", tc.name, err, tc.want)
		}
	}

	// Nothing changed: T, records, token counter.
	if got := e.Clock(); got != 100 {
		t.Fatalf("T = %d, want 100 after rejected ops", got)
	}
	if got := mustCount(t, e, "ok", 1, 100); got != 2 {
		t.Fatalf("Count(ok,1,100) = %d, want 2", got)
	}
	if got := mustCount(t, e, "ok2", 2, 100); got != 2 {
		t.Fatalf("Count(ok2,2,100) = %d, want 2", got)
	}
	r := mustAcquire(t, e, "next", 1000, 100, []int64{0, 0})
	if r.Token != 3 {
		t.Fatalf("token after rejected ops = %d, want 3", r.Token)
	}
}

// TestConcurrent hammers the engine from many goroutines; run with
// -race. Every accepted acquire must receive a distinct token.
func TestConcurrent(t *testing.T) {
	e := mustNew(t, 5, 50, 10, 1_000_000_000)

	const workers = 8
	const rounds = 200
	tokens := make(chan int64, workers*rounds)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			res := fmt.Sprintf("res-%d", id%3)
			for i := 0; i < rounds; i++ {
				now := e.Clock()
				r, err := e.Acquire(res, 100000, now, []int64{10, 10, 10, 10, 10})
				if err == nil {
					tokens <- r.Token
					if r.Success {
						_, _ = e.Unlock(res, r.Token, e.Clock())
					}
				} else if !errors.Is(err, ErrClockRewind) {
					t.Errorf("unexpected acquire error: %v", err)
				}
				if _, err := e.Count(res, 1, e.Clock()); err != nil && !errors.Is(err, ErrClockRewind) {
					t.Errorf("unexpected count error: %v", err)
				}
				if i%50 == 0 {
					if err := e.Restart(id%5, e.Clock()); err != nil && !errors.Is(err, ErrClockRewind) {
						t.Errorf("unexpected restart error: %v", err)
					}
				}
			}
		}(w)
	}
	wg.Wait()
	close(tokens)

	seen := make(map[int64]bool)
	for tok := range tokens {
		if seen[tok] {
			t.Fatalf("duplicate token %d issued", tok)
		}
		seen[tok] = true
	}
	if len(seen) == 0 {
		t.Fatal("no acquire was accepted")
	}
	t.Logf("accepted %d acquires, all tokens distinct", len(seen))
}
