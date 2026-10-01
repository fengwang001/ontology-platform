package failover

import (
	"errors"
	"strconv"
	"sync"
	"testing"
)

func mustNew(t *testing.T, L, F, D int, caps []int) *Allocator {
	t.Helper()
	a, err := New(L, F, D, caps)
	if err != nil {
		t.Fatalf("New(%d,%d,%d,%v): %v", L, F, D, caps, err)
	}
	return a
}

func mustAdd(t *testing.T, a *Allocator, level int, id string, healthy bool) {
	t.Helper()
	if err := a.AddHost(level, id, healthy); err != nil {
		t.Fatalf("AddHost(%d,%s): %v", level, id, err)
	}
}

func addN(t *testing.T, a *Allocator, level, n, healthy int) {
	t.Helper()
	for i := 0; i < n; i++ {
		mustAdd(t, a, level, "L"+strconv.Itoa(level)+"H"+strconv.Itoa(i), i < healthy)
	}
}

func expectLoads(t *testing.T, a *Allocator, want []int) []int {
	t.Helper()
	got, err := a.Loads()
	if err != nil {
		t.Fatalf("Loads: %v, want %v", err, want)
	}
	if !equalInts(got, want) {
		t.Fatalf("Loads = %v, want %v", got, want)
	}
	return got
}

func expectErr(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
}

func equalInts(x, y []int) bool {
	if len(x) != len(y) {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

// TestSpecExample replays the full walkthrough given in the specification.
func TestSpecExample(t *testing.T) {
	a := mustNew(t, 2, 120, 30, []int{60, 100})
	addN(t, a, 0, 5, 2) // raw = floor(240/5) = 48
	addN(t, a, 1, 2, 1) // raw = floor(120/2) = 60

	t.Logf("input: L=2 F=120 D=30 caps=[60 100], level0 t=5 h=2, level1 t=2 h=1; " +
		"judgement: raw=[48 60] cur=[48 60] S=108>=100 greedy -> output [48 52]")
	expectLoads(t, a, []int{48, 52})

	if err := a.SetHealth("L0H2", true); err != nil {
		t.Fatal(err)
	}
	t.Logf("input: level0 h=3 -> raw=72; judgement: 50<72<78 inside hysteresis band, cur held")
	expectLoads(t, a, []int{48, 52})

	if err := a.SetHealth("L0H3", true); err != nil {
		t.Fatal(err)
	}
	t.Logf("input: level0 h=4 -> raw=96; judgement: 96>=78 cur=96, greedy [96 4], " +
		"level0 clipped by 36 to 60, X=36 refills level1 room=96")
	expectLoads(t, a, []int{60, 40})

	if err := a.SetHealth("L1H0", false); err != nil {
		t.Fatal(err)
	}
	t.Logf("input: level1 h=0 -> raw=0 immediate drop, S=96 scales to [100 0]; " +
		"judgement: level0 clipped X=40, no eligible layer -> capacity shortage")
	_, err := a.Loads()
	expectErr(t, err, ErrInsufficientCapacity)
	if c := a.snapshotCur(); !equalInts(c, []int{96, 60}) {
		t.Fatalf("cur after rejection = %v, want [96 60]", c)
	}
	t.Logf("output after rejected Loads: error=ErrInsufficientCapacity, cur=%v (frozen)",
		a.snapshotCur())
}

func TestConfigValidation(t *testing.T) {
	bad := []struct {
		l, f, d int
		caps    []int
	}{
		{0, 140, 10, []int{100}},
		{17, 140, 10, []int{100}},
		{2, 99, 10, []int{100, 100}},
		{2, 1001, 10, []int{100, 100}},
		{2, 140, 0, []int{100, 100}},
		{2, 140, 101, []int{100, 100}},
		{2, 140, 10, []int{100}},
		{2, 140, 10, []int{0, 100}},
		{2, 140, 10, []int{101, 100}},
		{2, 140, 10, []int{50, 49}},
		{1, 140, 10, nil},
	}
	for i, c := range bad {
		_, err := New(c.l, c.f, c.d, c.caps)
		expectErr(t, err, ErrInvalidConfig)
		t.Logf("input case %d: L=%d F=%d D=%d caps=%v -> output ErrInvalidConfig",
			i, c.l, c.f, c.d, c.caps)
	}
}

func TestAddHostRejectOrder(t *testing.T) {
	a := mustNew(t, 2, 140, 10, []int{100, 100})
	mustAdd(t, a, 0, "dup", true)

	expectErr(t, a.AddHost(5, "", true), ErrLevelOutOfRange)
	expectErr(t, a.AddHost(0, "", true), ErrEmptyID)
	expectErr(t, a.AddHost(0, "dup", true), ErrHostExists)
	expectErr(t, a.RemoveHost("nope"), ErrHostNotFound)
	expectErr(t, a.SetHealth("nope", true), ErrHostNotFound)

	if err := a.SetHealth("dup", true); err != nil {
		t.Fatalf("same-value SetHealth rejected: %v", err)
	}
	expectLoads(t, a, []int{100, 0})
}

// TestRawCeiling covers floor(h*F/t) == 100 exactly and above capped at 100.
func TestRawCeiling(t *testing.T) {
	a := mustNew(t, 1, 200, 50, []int{100})
	mustAdd(t, a, 0, "x", true)
	mustAdd(t, a, 0, "y", false)
	expectLoads(t, a, []int{100})
	if v := a.snapshotCur()[0]; v != 100 {
		t.Fatalf("cur = %d, want raw exactly 100", v)
	}
	if err := a.SetHealth("y", true); err != nil {
		t.Fatal(err)
	}
	t.Logf("input: F=200 t=2 h=2 -> floor(400/2)=200; judgement: raw capped at 100")
	expectLoads(t, a, []int{100})
	if v := a.snapshotCur()[0]; v != 100 {
		t.Fatalf("cur = %d, want capped 100", v)
	}
}

// F=100, t=100 makes raw equal the healthy count: exact band boundaries.
func newBandAllocator(t *testing.T, healthy int) *Allocator {
	t.Helper()
	a := mustNew(t, 1, 100, 30, []int{100})
	addN(t, a, 0, 100, healthy)
	expectLoads(t, a, []int{100})
	return a
}

// TestHysteresisBandEndpoints: raw == cur+D updates; cur+D-1 is held.
func TestHysteresisBandEndpoints(t *testing.T) {
	a := newBandAllocator(t, 50) // cur=50
	for i := 50; i < 79; i++ {
		if err := a.SetHealth("L0H"+strconv.Itoa(i), true); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("input: cur=50, raw=79; judgement: 50<79<80 (cur+D) -> held at 50")
	expectLoads(t, a, []int{100})
	if v := a.snapshotCur()[0]; v != 50 {
		t.Fatalf("cur = %d, want held 50", v)
	}

	b := newBandAllocator(t, 50) // cur=50
	for i := 50; i < 80; i++ {
		if err := b.SetHealth("L0H"+strconv.Itoa(i), true); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("input: cur=50, raw=80; judgement: raw==cur+D -> update to 80")
	expectLoads(t, b, []int{100})
	if v := b.snapshotCur()[0]; v != 80 {
		t.Fatalf("cur = %d, want 80", v)
	}
}

// TestRawEqualCurAndDrop: raw == cur keeps; any decrease is immediate.
func TestRawEqualCurAndDrop(t *testing.T) {
	a := newBandAllocator(t, 50)
	expectLoads(t, a, []int{100})
	if v := a.snapshotCur()[0]; v != 50 {
		t.Fatalf("cur = %d, want 50 (raw == cur)", v)
	}
	if err := a.SetHealth("L0H49", false); err != nil {
		t.Fatal(err)
	}
	t.Logf("input: cur=50, raw=49; judgement: decrease is immediate")
	expectLoads(t, a, []int{100})
	if v := a.snapshotCur()[0]; v != 49 {
		t.Fatalf("cur = %d, want immediate drop to 49", v)
	}
}

// TestRecoveryFromZeroNoHysteresis: cur=0 recovers without delay.
func TestRecoveryFromZeroNoHysteresis(t *testing.T) {
	a := mustNew(t, 1, 100, 30, []int{100})
	addN(t, a, 0, 10, 0)
	expectLoads(t, a, []int{100}) // panic fallback assigns level 0
	for i := 0; i < 5; i++ {
		if err := a.SetHealth("L0H"+strconv.Itoa(i), true); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("input: cur=0, raw=50; judgement: cur is 0 -> recovery immediate, no hysteresis")
	expectLoads(t, a, []int{100})
	if v := a.snapshotCur()[0]; v != 50 {
		t.Fatalf("cur = %d, want 50 without hysteresis", v)
	}
}

// TestSExactly100Greedy: S==100 takes the greedy branch.
func TestSExactly100Greedy(t *testing.T) {
	a := mustNew(t, 2, 100, 100, []int{100, 100})
	addN(t, a, 0, 100, 60)
	addN(t, a, 1, 100, 40)
	t.Logf("input: cur=[60 40] S=100; judgement: greedy branch -> [60 40]")
	expectLoads(t, a, []int{60, 40})
}

// TestS99ScalingAndRemainder: S=99 scales; lost shares go to smallest cur>0.
func TestS99ScalingAndRemainder(t *testing.T) {
	a := mustNew(t, 2, 100, 100, []int{100, 100})
	addN(t, a, 0, 100, 60)
	addN(t, a, 1, 100, 39)
	t.Logf("input: cur=[60 39] S=99; judgement: scaled floors [60 39], " +
		"remainder 1 awarded to smallest cur>0 level 0")
	expectLoads(t, a, []int{61, 39})
}

// TestRemainderSkipsZeroCur: level 0 cur=0, remainder moves down to level 1.
func TestRemainderSkipsZeroCur(t *testing.T) {
	b := mustNew(t, 3, 100, 100, []int{100, 100, 100})
	addN(t, b, 0, 10, 0)
	addN(t, b, 1, 100, 60)
	addN(t, b, 2, 100, 39)
	t.Logf("input: cur=[0 60 39] S=99; judgement: remainder 1 skips level 0 (cur=0) -> level 1")
	expectLoads(t, b, []int{0, 61, 39})
}

// TestGreedyStarvation: a large early level eats 100, later levels get 0.
func TestGreedyStarvation(t *testing.T) {
	a := mustNew(t, 3, 100, 100, []int{100, 100, 100})
	addN(t, a, 0, 100, 90)
	addN(t, a, 1, 100, 50)
	addN(t, a, 2, 100, 40)
	t.Logf("input: cur=[90 50 40] S=180; judgement: greedy gives level0 90, " +
		"level1 10, level2 0 (starved)")
	expectLoads(t, a, []int{90, 10, 0})
}

// TestPanicSkipsEmptyLevel: S=0, level 0 has no host -> level 1 takes all.
func TestPanicSkipsEmptyLevel(t *testing.T) {
	a := mustNew(t, 3, 140, 10, []int{100, 100, 100})
	addN(t, a, 1, 2, 0) // all unhealthy but hosts exist
	addN(t, a, 2, 2, 0)
	t.Logf("input: all cur=0, level0 has no host; judgement: panic assigns " +
		"100 to smallest level with a host (level 1)")
	expectLoads(t, a, []int{0, 100, 0})
}

// TestPanicCapRedistribution: panic target overflows its cap; spill follows
// host-presence (not cur) eligibility, and shortages are rejected.
func TestPanicCapRedistribution(t *testing.T) {
	a := mustNew(t, 3, 140, 10, []int{60, 40, 100})
	mustAdd(t, a, 0, "p0", false)
	mustAdd(t, a, 1, "p1", false)
	mustAdd(t, a, 2, "p2", false)
	t.Logf("input: panic, caps=[60 40 100]; judgement: 100 to level0 clipped to 60, " +
		"X=40 refills host-bearing levels 1 and 2 by room [40 ...] -> [60 40 0]")
	expectLoads(t, a, []int{60, 40, 0})

	b := mustNew(t, 3, 140, 10, []int{60, 30, 10})
	mustAdd(t, b, 0, "p0", false)
	mustAdd(t, b, 1, "p1", false)
	t.Logf("input: panic, hosts only on levels 0-1, caps=[60 30 10]; " +
		"judgement: X=40, level1 room 30, level2 has no host -> 10 unplaceable")
	_, err := b.Loads()
	expectErr(t, err, ErrInsufficientCapacity)
}

// TestCapSpillToLaterLevels: level 0 capped; overflow fills later levels in
// order, skipping ineligible ones.
func TestCapSpillToLaterLevels(t *testing.T) {
	a := mustNew(t, 3, 100, 100, []int{60, 100, 100})
	addN(t, a, 0, 100, 90)
	addN(t, a, 1, 100, 90)
	// level 2 exists with hosts but cur=0: ineligible for refill.
	addN(t, a, 2, 10, 0)
	// S=180 greedy: [60 cap would clip] initial greedy [90 10 0].
	// Clip level0 to 60 (X=30), refill: level1 room 90 takes all.
	t.Logf("input: greedy [90 10 0], caps=[60 100 100], level2 cur=0 ineligible; " +
		"judgement: X=30 all goes to level1 -> [60 40 0]")
	expectLoads(t, a, []int{60, 40, 0})

	// Multi-level spill: caps force flow past one full level.
	b := mustNew(t, 3, 100, 100, []int{50, 50, 100})
	addN(t, b, 0, 100, 100)
	addN(t, b, 1, 100, 50)
	addN(t, b, 2, 100, 50)
	// greedy [100 0 0] -> clip to 50 X=50; level1 room 50 -> [50 50 0].
	t.Logf("input: greedy [100 0 0], caps=[50 50 100]; judgement: X=50 fills level1 exactly")
	expectLoads(t, b, []int{50, 50, 0})
}

// TestPickLevelBoundaries: intervals are left-closed/right-open per prefix.
func TestPickLevelBoundaries(t *testing.T) {
	a := mustNew(t, 3, 100, 100, []int{100, 100, 100})
	addN(t, a, 0, 100, 20)
	addN(t, a, 1, 100, 30)
	addN(t, a, 2, 100, 50)
	// S=100 greedy: [20 30 50].
	want := []int{20, 30, 50}
	bounds := []int{0, 19, 20, 49, 50, 99}
	wantLevel := []int{0, 0, 1, 1, 2, 2}
	prefix := 0
	for i, r := range bounds {
		got, err := a.PickLevel(r)
		if err != nil {
			t.Fatalf("PickLevel(%d): %v", r, err)
		}
		if got != wantLevel[i] {
			t.Fatalf("PickLevel(%d) = %d, want %d (shares %v, prefix base %d)",
				r, got, wantLevel[i], want, prefix)
		}
		t.Logf("input r=%d -> output level=%d; judgement: interval of "+
			"[prefix,prefix+share) over shares %v", r, got, want)
	}

	expectErr(t, func() error { _, e := a.PickLevel(-1); return e }(), ErrInvalidPoint)
	expectErr(t, func() error { _, e := a.PickLevel(100); return e }(), ErrInvalidPoint)
}

// TestErrorPrecedenceAndNoMutation: rejected operations leave state intact.
func TestErrorPrecedenceAndNoMutation(t *testing.T) {
	empty := mustNew(t, 2, 140, 10, []int{60, 100})
	// PickLevel checks r before no-hosts.
	_, err := empty.PickLevel(100)
	expectErr(t, err, ErrInvalidPoint)
	_, err = empty.PickLevel(0)
	expectErr(t, err, ErrNoHosts)
	_, err = empty.Loads()
	expectErr(t, err, ErrNoHosts)

	// Capacity-shortage rejection freezes memory and hosts.
	a := mustNew(t, 2, 120, 30, []int{60, 100})
	addN(t, a, 0, 5, 4)              // raw 96
	addN(t, a, 1, 2, 1)              // raw 60
	expectLoads(t, a, []int{60, 40}) // cur=[96 60]
	if err := a.SetHealth("L1H0", false); err != nil {
		t.Fatal(err)
	}
	before := a.snapshotCur()
	if _, err := a.Loads(); !errors.Is(err, ErrInsufficientCapacity) {
		t.Fatalf("err = %v, want capacity", err)
	}
	if after := a.snapshotCur(); !equalInts(before, after) {
		t.Fatalf("cur changed on rejection: before %v after %v", before, after)
	}
	// Rejected add/remove/sethealth leave the host set usable as before.
	expectErr(t, a.AddHost(9, "z", true), ErrLevelOutOfRange)
	expectErr(t, a.RemoveHost("ghost"), ErrHostNotFound)
	expectErr(t, a.SetHealth("ghost", true), ErrHostNotFound)
}

// TestCurMatchesRawAfterSuccess: after any accepted Loads, cur==raw per level.
func TestCurMatchesRawAfterSuccess(t *testing.T) {
	a := mustNew(t, 3, 147, 23, []int{70, 80, 90})
	addN(t, a, 0, 7, 3)
	addN(t, a, 1, 11, 0)
	addN(t, a, 2, 3, 3)
	loads, err := a.Loads()
	if err != nil {
		t.Fatal(err)
	}
	total, _, raw := a.rawStats()
	_ = total
	if c := a.snapshotCur(); !equalInts(c, raw) {
		t.Fatalf("cur %v != raw %v after first Loads", c, raw)
	}
	sum := 0
	for p, v := range loads {
		sum += v
		if v < 0 || v > a.cap[p] {
			t.Fatalf("level %d share %d outside [0,%d]", p, v, a.cap[p])
		}
	}
	if sum != 100 {
		t.Fatalf("share sum = %d, want 100", sum)
	}
}

// TestConcurrentAccess verifies the serializable-access contract under -race.
func TestConcurrentAccess(t *testing.T) {
	a := mustNew(t, 4, 140, 20, []int{40, 60, 80, 100})
	for p := 0; p < 4; p++ {
		for i := 0; i < 8; i++ {
			mustAdd(t, a, p, "L"+strconv.Itoa(p)+"H"+strconv.Itoa(i), i%2 == 0)
		}
	}
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for k := 0; k < 200; k++ {
				id := "L" + strconv.Itoa(k%4) + "H" + strconv.Itoa(g%8)
				_ = a.SetHealth(id, k%3 != 0)
				loads, err := a.Loads()
				if err == nil {
					sum := 0
					for _, v := range loads {
						sum += v
					}
					if sum != 100 {
						t.Errorf("concurrent Loads sum = %d", sum)
						return
					}
				}
				if lvl, err := a.PickLevel(k % 100); err == nil {
					if lvl < 0 || lvl >= 4 {
						t.Errorf("PickLevel returned %d", lvl)
					}
				}
			}
		}(g)
	}
	wg.Wait()
}
