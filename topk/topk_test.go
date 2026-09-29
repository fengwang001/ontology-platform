package topk

import (
	"errors"
	"math"
	"reflect"
	"sync"
	"testing"
)

func mustNew(t *testing.T, rows, cols, capacity int, maxElement uint64) *Sketch {
	t.Helper()
	s, err := New(rows, cols, capacity, maxElement)
	if err != nil {
		t.Fatalf("New(%d, %d, %d, %d): %v", rows, cols, capacity, maxElement, err)
	}
	return s
}

func mustAdd(t *testing.T, s *Sketch, element, count uint64) {
	t.Helper()
	if err := s.Add(element, count); err != nil {
		t.Fatalf("Add(%d, %d): %v", element, count, err)
	}
}

func mustEstimate(t *testing.T, s *Sketch, element uint64) uint64 {
	t.Helper()
	est, err := s.Estimate(element)
	if err != nil {
		t.Fatalf("Estimate(%d): %v", element, err)
	}
	return est
}

// assertNoCollisions requires the elements to occupy pairwise distinct cells
// in every row, so sketch estimates equal exact counts.
func assertNoCollisions(t *testing.T, s *Sketch, elements []uint64) {
	t.Helper()
	for r := 0; r < s.rows; r++ {
		seen := map[int]uint64{}
		for _, e := range elements {
			c := s.locate(r, e)
			if other, ok := seen[c]; ok {
				t.Fatalf("elements %d and %d collide in row %d cell %d", other, e, r, c)
			}
			seen[c] = e
		}
	}
}

func TestNewRejectsInvalidParams(t *testing.T) {
	cases := []struct {
		name                 string
		rows, cols, capacity int
		maxElement           uint64
	}{
		{"zero rows", 0, 8, 4, 100},
		{"negative rows", -1, 8, 4, 100},
		{"zero cols", 2, 0, 4, 100},
		{"negative cols", 2, -3, 4, 100},
		{"zero capacity", 2, 8, 0, 100},
		{"negative capacity", 2, 8, -2, 100},
		{"zero maxElement", 2, 8, 4, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(tc.rows, tc.cols, tc.capacity, tc.maxElement)
			if !errors.Is(err, ErrInvalidParam) {
				t.Fatalf("got %v, want ErrInvalidParam", err)
			}
		})
	}
}

func TestCollisionsAndRepeatedArrivals(t *testing.T) {
	// A single column forces every element into the same cell per row, so
	// estimates are the stream total and strictly overestimate.
	s := mustNew(t, 3, 1, 4, 100)
	mustAdd(t, s, 5, 3)
	mustAdd(t, s, 9, 2)
	mustAdd(t, s, 5, 4) // repeated arrival accumulates

	if est := mustEstimate(t, s, 5); est != 9 {
		t.Fatalf("Estimate(5) = %d, want 9 (collision total), true count is 7", est)
	}
	if est := mustEstimate(t, s, 9); est != 9 {
		t.Fatalf("Estimate(9) = %d, want 9, true count is 2", est)
	}
	if est := mustEstimate(t, s, 42); est != 9 {
		t.Fatalf("Estimate(42) = %d, want 9 for an element that never arrived", est)
	}
	cands := s.Candidates()
	if len(cands) != 2 || cands[0].Element != 5 || cands[0].Estimate != 9 {
		t.Fatalf("candidates = %+v, want [{5 9} {9 9}]", cands)
	}
}

func TestTieRankingByElementAscending(t *testing.T) {
	// Collision-free geometry with equal counts gives equal estimates, so
	// the total order is decided purely by element value ascending.
	s := mustNew(t, 2, 64, 5, 100)
	elements := []uint64{7, 3, 9, 5}
	assertNoCollisions(t, s, elements)
	for _, e := range []uint64{7, 3, 9, 5} {
		mustAdd(t, s, e, 1)
	}
	cands := s.Candidates()
	want := []Candidate{{3, 1}, {5, 1}, {7, 1}, {9, 1}}
	if !reflect.DeepEqual(cands, want) {
		t.Fatalf("candidates = %+v, want %+v", cands, want)
	}
}

func TestStaleRecordsRefreshOnlyOnArrival(t *testing.T) {
	// One column: every estimate is the stream total. A candidate's
	// recorded estimate is the snapshot at its own last arrival and stays
	// stale until it arrives again.
	s := mustNew(t, 2, 1, 5, 100)
	for _, e := range []uint64{7, 3, 9, 5} {
		mustAdd(t, s, e, 1)
	}
	want := []Candidate{{5, 4}, {9, 3}, {3, 2}, {7, 1}}
	if got := s.Candidates(); !reflect.DeepEqual(got, want) {
		t.Fatalf("stale records: got %+v, want %+v", got, want)
	}
	mustAdd(t, s, 7, 1) // 7 refreshes to the current total and re-ranks
	want = []Candidate{{7, 5}, {5, 4}, {9, 3}, {3, 2}}
	if got := s.Candidates(); !reflect.DeepEqual(got, want) {
		t.Fatalf("after refresh: got %+v, want %+v", got, want)
	}
}

func TestReplaceAndRefresh(t *testing.T) {
	s := mustNew(t, 3, 64, 3, 1000)
	elements := []uint64{10, 20, 30, 40}
	assertNoCollisions(t, s, elements)

	mustAdd(t, s, 10, 5)
	mustAdd(t, s, 20, 4)
	mustAdd(t, s, 30, 3)
	if got := s.Candidates(); !reflect.DeepEqual(got, []Candidate{{10, 5}, {20, 4}, {30, 3}}) {
		t.Fatalf("after fill: %+v", got)
	}

	// List full and 40 ranks below the tail: ignored.
	mustAdd(t, s, 40, 1)
	if got := s.Candidates(); !reflect.DeepEqual(got, []Candidate{{10, 5}, {20, 4}, {30, 3}}) {
		t.Fatalf("after low-ranked arrival: %+v", got)
	}

	// 40 now outranks the tail and replaces 30.
	mustAdd(t, s, 40, 10)
	if got := s.Candidates(); !reflect.DeepEqual(got, []Candidate{{40, 11}, {10, 5}, {20, 4}}) {
		t.Fatalf("after replacement: %+v", got)
	}

	// 20 arrives again: its recorded estimate refreshes and it re-ranks.
	mustAdd(t, s, 20, 3)
	if got := s.Candidates(); !reflect.DeepEqual(got, []Candidate{{40, 11}, {20, 7}, {10, 5}}) {
		t.Fatalf("after refresh: %+v", got)
	}

	// 30 was evicted; its sketch estimate is still exact and never drops
	// below its true count.
	if est := mustEstimate(t, s, 30); est != 3 {
		t.Fatalf("Estimate(30) = %d, want 3", est)
	}
}

func snapshot(t *testing.T, s *Sketch, maxElement uint64) ([]Candidate, []uint64) {
	t.Helper()
	ests := make([]uint64, maxElement)
	for e := uint64(0); e < maxElement; e++ {
		ests[e] = mustEstimate(t, s, e)
	}
	return s.Candidates(), ests
}

func assertStateEqual(t *testing.T, what string, beforeC []Candidate, beforeE, afterE []uint64, afterC []Candidate) {
	t.Helper()
	if !reflect.DeepEqual(beforeC, afterC) {
		t.Fatalf("%s changed candidates: before %+v after %+v", what, beforeC, afterC)
	}
	if !reflect.DeepEqual(beforeE, afterE) {
		t.Fatalf("%s changed sketch estimates: before %v after %v", what, beforeE, afterE)
	}
}

func TestIllegalInputsRejectedAtomically(t *testing.T) {
	s := mustNew(t, 2, 8, 2, 10)
	mustAdd(t, s, 1, 2)
	mustAdd(t, s, 2, 3)

	rejections := []struct {
		name    string
		element uint64
		count   uint64
		want    error
	}{
		{"element out of range", 10, 1, ErrElementOutOfRange},
		{"element far out of range", math.MaxUint64, 1, ErrElementOutOfRange},
		{"zero count", 1, 0, ErrNonPositiveCount},
		{"out of range and zero count", 10, 0, ErrElementOutOfRange},
	}
	for _, tc := range rejections {
		t.Run(tc.name, func(t *testing.T) {
			beforeC, beforeE := snapshot(t, s, 10)
			err := s.Add(tc.element, tc.count)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Add(%d, %d) = %v, want %v", tc.element, tc.count, err, tc.want)
			}
			afterC, afterE := snapshot(t, s, 10)
			assertStateEqual(t, tc.name, beforeC, beforeE, afterE, afterC)
		})
	}

	t.Run("count overflow", func(t *testing.T) {
		mustAdd(t, s, 3, math.MaxUint64-10) // fills cells near the top
		beforeC, beforeE := snapshot(t, s, 10)
		err := s.Add(3, 11)
		if !errors.Is(err, ErrCountOverflow) {
			t.Fatalf("Add overflow = %v, want ErrCountOverflow", err)
		}
		afterC, afterE := snapshot(t, s, 10)
		assertStateEqual(t, "count overflow", beforeC, beforeE, afterE, afterC)
	})

	t.Run("estimate out of range", func(t *testing.T) {
		if _, err := s.Estimate(10); !errors.Is(err, ErrElementOutOfRange) {
			t.Fatalf("Estimate(10) = %v, want ErrElementOutOfRange", err)
		}
	})

	// The four categories are mutually distinguishable.
	all := []error{ErrInvalidParam, ErrElementOutOfRange, ErrNonPositiveCount, ErrCountOverflow}
	for i, a := range all {
		for j, b := range all {
			if i != j && errors.Is(a, b) {
				t.Fatalf("error categories %v and %v are not distinguishable", a, b)
			}
		}
	}
}

// reference is a naive linear model using exact counts and the same
// candidate maintenance rule as the sketch.
type reference struct {
	capacity int
	counts   map[uint64]uint64
	cands    []Candidate
}

func newReference(capacity int) *reference {
	return &reference{capacity: capacity, counts: map[uint64]uint64{}}
}

func (r *reference) add(element, count uint64) string {
	r.counts[element] += count
	est := r.counts[element]
	cand := Candidate{Element: element, Estimate: est}
	decision := "ignore: full and not above tail"
	for i := range r.cands {
		if r.cands[i].Element == element {
			r.cands[i].Estimate = est
			decision = "refresh: already a candidate"
			r.sort()
			return decision
		}
	}
	if len(r.cands) < r.capacity {
		r.cands = append(r.cands, cand)
		decision = "insert: list not full"
	} else if less(cand, r.cands[len(r.cands)-1]) {
		r.cands[len(r.cands)-1] = cand
		decision = "replace: outranks tail"
	}
	r.sort()
	return decision
}

func (r *reference) sort() {
	for i := 1; i < len(r.cands); i++ {
		for j := i; j > 0 && less(r.cands[j], r.cands[j-1]); j-- {
			r.cands[j], r.cands[j-1] = r.cands[j-1], r.cands[j]
		}
	}
}

func TestStepByStepAgainstLinearReference(t *testing.T) {
	const (
		capacity   = 6
		maxElement = 500
		steps      = 200
	)
	s := mustNew(t, 4, 1024, capacity, maxElement)
	// Greedily pick a collision-free universe so sketch estimates are exact.
	var universe []uint64
	for e := uint64(0); e < maxElement && len(universe) < 32; e++ {
		ok := true
		for r := 0; r < s.rows && ok; r++ {
			for _, other := range universe {
				if s.locate(r, e) == s.locate(r, other) {
					ok = false
					break
				}
			}
		}
		if ok {
			universe = append(universe, e)
		}
	}
	if len(universe) < 32 {
		t.Fatalf("could not gather a collision-free universe, got %d elements", len(universe))
	}

	ref := newReference(capacity)
	// Deterministic LCG keeps the arrival stream reproducible.
	var state uint64 = 0x123456789abcdef
	next := func() uint64 {
		state = state*6364136223846793005 + 1442695040888963407
		return state >> 33
	}
	for step := 0; step < steps; step++ {
		element := universe[next()%uint64(len(universe))]
		count := next()%5 + 1
		mustAdd(t, s, element, count)
		decision := ref.add(element, count)
		est := mustEstimate(t, s, element)
		t.Logf("step %3d: add(element=%d, count=%d) estimate=%d true=%d decision=%s",
			step, element, count, est, ref.counts[element], decision)

		if est != ref.counts[element] {
			t.Fatalf("step %d: estimate %d != exact %d", step, est, ref.counts[element])
		}
		if got := s.Candidates(); !reflect.DeepEqual(got, ref.cands) {
			t.Fatalf("step %d: candidates %+v != reference %+v", step, got, ref.cands)
		}
		for _, e := range universe {
			if got := mustEstimate(t, s, e); got < ref.counts[e] {
				t.Fatalf("step %d: Estimate(%d) = %d undercounts true %d", step, e, got, ref.counts[e])
			}
		}
		if err := s.SelfCheck(); err != nil {
			t.Fatalf("step %d: %v", step, err)
		}
	}
}

func TestConcurrentAccess(t *testing.T) {
	s := mustNew(t, 4, 64, 8, 1000)
	const adders = 4
	const perAdder = 100
	var wg sync.WaitGroup
	for g := 0; g < adders; g++ {
		wg.Add(1)
		go func(base uint64) {
			defer wg.Done()
			for i := 0; i < perAdder; i++ {
				if err := s.Add(base, 1); err != nil {
					t.Errorf("Add(%d, 1): %v", base, err)
				}
			}
		}(uint64(g))
	}
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				if _, err := s.Estimate(uint64(i % adders)); err != nil {
					t.Errorf("Estimate: %v", err)
				}
				cands := s.Candidates()
				for j := 1; j < len(cands); j++ {
					if !less(cands[j-1], cands[j]) {
						t.Errorf("candidates out of order: %+v", cands)
					}
				}
				if err := s.SelfCheck(); err != nil {
					t.Errorf("SelfCheck: %v", err)
				}
			}
		}()
	}
	wg.Wait()

	for e := uint64(0); e < adders; e++ {
		if est := mustEstimate(t, s, e); est < perAdder {
			t.Fatalf("Estimate(%d) = %d undercounts true %d", e, est, perAdder)
		}
	}
	if err := s.SelfCheck(); err != nil {
		t.Fatalf("final SelfCheck: %v", err)
	}
}
