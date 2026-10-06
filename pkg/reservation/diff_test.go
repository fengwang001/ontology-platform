package reservation

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"testing"
)

type stringLogger struct{ b strings.Builder }

func (l *stringLogger) Logf(format string, args ...any) {
	fmt.Fprintf(&l.b, format+"\n", args...)
}

type resState struct {
	feeder    int
	start     int
	end       int
	power     int
	state     ReservationState
	createdAt int
	expiresAt int
}

func snapshot(m map[int64]*Reservation) map[int64]resState {
	out := make(map[int64]resState, len(m))
	for id, r := range m {
		out[id] = resState{r.Feeder, r.Start, r.End, r.Power, r.State, r.CreatedAt, r.ExpiresAt}
	}
	return out
}

func compareStates(t *testing.T, got map[int64]*Reservation, want map[int64]*Reservation, logText string) {
	t.Helper()
	g, w := snapshot(got), snapshot(want)
	if len(g) != len(w) {
		t.Fatalf("reservation count %d != %d\n%s", len(g), len(w), logText)
	}
	for id, gs := range g {
		ws, ok := w[id]
		if !ok {
			t.Fatalf("id %d missing in naive model\n%s", id, logText)
		}
		if gs != ws {
			t.Fatalf("id %d mismatch:\n got  %+v\n want %+v\n%s", id, gs, ws, logText)
		}
	}
}

type opKind int

const (
	opAdvance opKind = iota
	opCreate
	opConfirm
	opReschedule
	opRelease
	opChangeCap
)

// runDiff drives both implementations with one identical random operation
// stream and compares both the per-operation result and the full state.
func runDiff(t *testing.T, seed int64, iterations int) {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	feeders := map[int]int{1: 8, 2: 5}
	initial := []CapacityRecord{{EffectiveAt: 0, Capacity: 12}}

	log := &stringLogger{}
	cfg := Config{HoldDuration: 3, Log: log}
	fast := NewSystem(feeders, initial, cfg)
	naive := NewNaiveSystem(feeders, initial, Config{HoldDuration: 3, Log: nopLogger{}})

	var liveIDs []int64
	maxID := int64(1)

	failf := func(format string, args ...any) {
		t.Fatalf(format+"\n判定日志:\n%s", append(args, log.b.String())...)
	}

	for i := 0; i < iterations; i++ {
		now := fast.Now()
		op := opKind(rng.Intn(int(opChangeCap) + 1))
		switch op {
		case opAdvance:
			nn := now + rng.Intn(4)
			log.Logf("--- step %d Advance(%d)", i, nn)
			e1, e2 := fast.Advance(nn), naive.Advance(nn)
			if errCmp(e1, e2) {
				failf("step %d Advance(%d): %v vs %v", i, nn, e1, e2)
			}
		case opCreate:
			fd := 1 + rng.Intn(3) // include unknown feeder 3 sometimes
			start := now + rng.Intn(6) - 1
			end := start + 1 + rng.Intn(6)
			power := 1 + rng.Intn(7)
			log.Logf("--- step %d Create(now=%d f=%d [%d,%d) p=%d)", i, now, fd, start, end, power)
			id1, e1 := fast.Create(now, fd, Interval{start, end}, power)
			id2, e2 := naive.Create(now, fd, Interval{start, end}, power)
			if errCmp(e1, e2) {
				failf("step %d Create: %v vs %v", i, e1, e2)
			}
			if e1 == nil {
				if id1 != id2 {
					failf("step %d Create id mismatch %d != %d", i, id1, id2)
				}
				liveIDs = append(liveIDs, id1)
				if id1 >= maxID {
					maxID = id1 + 1
				}
			}
		case opConfirm, opReschedule, opRelease:
			id := pickID(rng, liveIDs, maxID)
			if op == opConfirm {
				log.Logf("--- step %d Confirm(now=%d id=%d)", i, now, id)
				e1, e2 := fast.Confirm(now, id), naive.Confirm(now, id)
				if errCmp(e1, e2) {
					failf("step %d Confirm(%d): %v vs %v", i, id, e1, e2)
				}
			} else if op == opRelease {
				log.Logf("--- step %d Release(now=%d id=%d)", i, now, id)
				e1, e2 := fast.Release(now, id), naive.Release(now, id)
				if errCmp(e1, e2) {
					failf("step %d Release(%d): %v vs %v", i, id, e1, e2)
				}
			} else {
				start := now + rng.Intn(6) - 2
				end := start + 1 + rng.Intn(6)
				power := 1 + rng.Intn(7)
				log.Logf("--- step %d Reschedule(now=%d id=%d [%d,%d) p=%d)", i, now, id, start, end, power)
				e1 := fast.Reschedule(now, id, Interval{start, end}, power)
				e2 := naive.Reschedule(now, id, Interval{start, end}, power)
				if errCmp(e1, e2) {
					failf("step %d Reschedule(%d): %v vs %v", i, id, e1, e2)
				}
			}
		case opChangeCap:
			eff := now + rng.Intn(4)
			cap := 1 + rng.Intn(14)
			log.Logf("--- step %d ChangeCapacity(now=%d eff=%d cap=%d)", i, now, eff, cap)
			c1, e1 := fast.ChangeCapacity(now, CapacityRecord{eff, cap})
			c2, e2 := naive.ChangeCapacity(now, CapacityRecord{eff, cap})
			if errCmp(e1, e2) {
				failf("step %d ChangeCapacity: %v vs %v", i, e1, e2)
			}
			if e1 == nil && !int64SliceEqual(c1, c2) {
				failf("step %d ChangeCapacity cancelled mismatch %v vs %v", i, c1, c2)
			}
		}
		if fast.Now() != naive.Now() {
			failf("clock drift: %d vs %d", fast.Now(), naive.Now())
		}
		naiveMap := make(map[int64]*Reservation, len(naive.rs))
		for _, r := range naive.rs {
			naiveMap[r.ID] = r
		}
		compareStates(t, fast.store, naiveMap, log.b.String())
	}
}

func pickID(rng *rand.Rand, live []int64, maxID int64) int64 {
	if len(live) > 0 && rng.Intn(4) != 0 {
		return live[rng.Intn(len(live))]
	}
	return 1 + rng.Int63n(maxID+3)
}

func int64SliceEqual(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	aa, bb := append([]int64(nil), a...), append([]int64(nil), b...)
	sort.Slice(aa, func(i, j int) bool { return aa[i] < aa[j] })
	sort.Slice(bb, func(i, j int) bool { return bb[i] < bb[j] })
	for i := range aa {
		if aa[i] != bb[i] {
			return false
		}
	}
	return true
}

// errCmp reports whether two errors differ in kind/capacity detail.
func errCmp(a, b error) bool {
	return normErr(a) != normErr(b)
}

func normErr(err error) string {
	if err == nil {
		return ""
	}
	if oe, ok := err.(*OpError); ok {
		if oe.CapInfo != nil {
			return fmt.Sprintf("%d@%d@%d", oe.Kind, oe.CapInfo.Time, oe.CapInfo.Level)
		}
		return fmt.Sprintf("%d", oe.Kind)
	}
	return err.Error()
}

func TestRandomDifferential(t *testing.T) {
	for seed := int64(1); seed <= 120; seed++ {
		t.Run(fmt.Sprintf("seed%d", seed), func(t *testing.T) {
			runDiff(t, seed, 600)
		})
	}
}

// Concurrent identical ops must behave like some serial order: here every
// goroutine creates a full-limit reservation on the same feeder/interval, so
// exactly one must succeed.
func TestConcurrentSerializable(t *testing.T) {
	sys := NewSystem(
		map[int]int{1: 10},
		[]CapacityRecord{{EffectiveAt: 0, Capacity: 100}},
		Config{HoldDuration: 10},
	)
	const n = 64
	var wg sync.WaitGroup
	var mu sync.Mutex
	success, kinds := 0, map[ErrorKind]int{}
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := sys.Create(0, 1, Interval{0, 5}, 10)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				success++
			} else {
				kinds[err.(*OpError).Kind]++
			}
		}()
	}
	wg.Wait()
	if success != 1 {
		t.Fatalf("exactly one create must win, got %d (errors %v)", success, kinds)
	}
	if kinds[ErrCapacity] != n-1 {
		t.Fatalf("losers must all be capacity errors, got %v", kinds)
	}
}

// checkCostIndependentOfIrrelevantData verifies via observable work counters
// that validating a small window is unaffected by disjoint reservations and
// unrelated capacity records: the event treaps only expose keys inside the
// window, and the capacity lookup is binary search.
func TestCheckCostIndependentOfIrrelevantData(t *testing.T) {
	recs := []CapacityRecord{{EffectiveAt: 0, Capacity: 1_000_000}}
	for i := 1; i < 2000; i++ {
		recs = append(recs, CapacityRecord{EffectiveAt: 10_000 + i, Capacity: 1_000_000 + i})
	}
	sys := NewSystem(map[int]int{1: 1_000_000}, recs, Config{HoldDuration: 10})
	// 2000 reservations fully disjoint from the tiny window [0,3).
	for i := 0; i < 2000; i++ {
		base := 20_000 + i*10
		id, err := sys.Create(0, 1, Interval{base, base + 5}, 1)
		if err != nil {
			t.Fatal(err)
		}
		if err := sys.Confirm(0, id); err != nil {
			t.Fatal(err)
		}
	}
	// Candidate times inside [0,3) must be exactly one (the interval start):
	// none of the 2000 disjoint events or 2000 later records are enumerated.
	times := candidateTimes(
		Interval{0, 3},
		sys.occ.feeder[1],
		sys.occ.global,
		sys.cap,
	)
	if len(times) != 1 || times[0] != 0 {
		t.Fatalf("want exactly candidate {0}, got %v", times)
	}
	// Capacity lookup remains O(log n) and correct.
	// Latest record not after 25000 is EffectiveAt 11999.
	if got := sys.cap.at(25_000); got != 1_001_999 {
		t.Fatalf("binary capacity lookup wrong: %d", got)
	}
	// Validation itself succeeds quickly against an empty window.
	if cerr := sys.occ.check(Interval{0, 3}, 1, 1, 1_000_000, nil, sys.cap); cerr != nil {
		t.Fatalf("unexpected failure: %+v", cerr)
	}
}

// advanceSettleCost verifies the expiry heap drains only reservations that
// actually expire between the old and new clock.
func TestAdvanceSettleCost(t *testing.T) {
	sys2 := NewSystem(
		map[int]int{1: 1_000_000},
		[]CapacityRecord{{EffectiveAt: 0, Capacity: 1_000_000}},
		Config{HoldDuration: 5},
	)
	var shortIDs []int64
	for i := 0; i < 5; i++ {
		id, err := sys2.Create(0, 1, Interval{0, 100}, 1)
		if err != nil {
			t.Fatal(err)
		}
		shortIDs = append(shortIDs, id)
	}
	// Long-lived holds in another system emulate "unrelated" volume.
	if err := sys2.Advance(5); err != nil {
		t.Fatal(err)
	}
	for _, id := range shortIDs {
		r, ok := sys2.Get(id)
		if !ok || r.State != StateVoid {
			t.Fatalf("id %d should be void after settle, got %v", id, r.State)
		}
	}
	// drain's heap root must now be empty for times <= 5.
	if n := sys2.expires.root; n != nil {
		t.Fatalf("no expiry nodes <= 5 should remain, root=%+v", n)
	}
}
