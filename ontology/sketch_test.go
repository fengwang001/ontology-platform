package hotcount

import (
	"errors"
	"math"
	"sync"
	"testing"
)

// refCandidates is the naive linear reference that replays exactly the same
// maintenance rule as the sketch:
//   - only the arriving element's estimate is refreshed;
//   - insert while the list is not full;
//   - otherwise replace the worst element when the newcomer ranks higher.
func refCandidates(s *Sketch, tracked []Candidate, element uint64, maxK int) []Candidate {
	est, err := s.Estimate(element)
	if err != nil {
		panic(err)
	}
	if idx := indexOf(tracked, element); idx >= 0 {
		tracked[idx].Estimate = est
	} else if len(tracked) < maxK {
		tracked = append(tracked, Candidate{Element: element, Estimate: est})
	} else {
		worst := 0
		for i := 1; i < len(tracked); i++ {
			if compareCandidate(tracked[worst], tracked[i]) < 0 {
				worst = i
			}
		}
		if betterCandidate(Candidate{Element: element, Estimate: est}, tracked[worst]) {
			tracked[worst] = Candidate{Element: element, Estimate: est}
		}
	}
	sortCandidates(tracked)
	return tracked
}

func sortCandidates(list []Candidate) {
	for i := 1; i < len(list); i++ {
		for j := i; j > 0 && compareCandidate(list[j-1], list[j]) > 0; j-- {
			list[j-1], list[j] = list[j], list[j-1]
		}
	}
}

// collisionFreeWidth returns a width that places all elements in distinct
// columns on every row, so estimates equal exact counts.
func collisionFreeWidth(t *testing.T, rows int, elements []uint64) int {
	t.Helper()
	for width := len(elements); width < 4096; width++ {
		ok := true
		for r := 0; r < rows; r++ {
			seen := map[int]bool{}
			for _, e := range elements {
				c := bucket(e, r, width)
				if seen[c] {
					ok = false
					break
				}
				seen[c] = true
			}
			if !ok {
				break
			}
		}
		if ok {
			return width
		}
	}
	t.Fatal("no collision-free width found")
	return 0
}

// findCollision returns two distinct elements sharing a column in some row.
func findCollision(rows, width, maxElement uint64) (uint64, uint64, int) {
	for a := uint64(0); a <= maxElement; a++ {
		for b := a + 1; b <= maxElement; b++ {
			for r := 0; r < int(rows); r++ {
				if bucket(a, r, int(width)) == bucket(b, r, int(width)) {
					return a, b, r
				}
			}
		}
	}
	return 0, 0, 0
}

func sameCandidates(a, b []Candidate) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func estOf(s *Sketch, e uint64) uint64 {
	v, _ := s.Estimate(e)
	return v
}

func decision(list []Candidate, e uint64) string {
	for _, c := range list {
		if c.Element == e {
			return "kept/refreshed"
		}
	}
	return "rejected/not-ranked"
}

func TestReferenceAgreement(t *testing.T) {
	const rows, maxK = 4, 3
	arrivals := []struct {
		element uint64
		count   uint64
	}{
		{10, 1}, {20, 2}, {30, 3}, {40, 1}, {10, 1},
		{20, 1}, {50, 2}, {30, 2}, {10, 3}, {50, 1},
	}
	elements := []uint64{10, 20, 30, 40, 50}
	width := collisionFreeWidth(t, rows, elements)
	s, err := New(Config{Rows: rows, Width: width, MaxCandidates: maxK, MaxElement: 100})
	if err != nil {
		t.Fatal(err)
	}

	var tracked []Candidate
	for step, a := range arrivals {
		if err := s.Add(a.element, a.count); err != nil {
			t.Fatalf("step %d: %v", step, err)
		}
		tracked = refCandidates(s, tracked, a.element, maxK)
		got := s.Candidates()
		t.Logf("step=%d add=(e=%d,w=%d) est=%d decision=%s candidates=%v",
			step, a.element, a.count, estOf(s, a.element), decision(got, a.element), got)
		if !sameCandidates(got, tracked) {
			t.Fatalf("step %d: sketch %v != reference %v", step, got, tracked)
		}
	}
}

func TestCollisionOverestimateAndRepeated(t *testing.T) {
	const rows, width = 3, 4
	a, b, row := findCollision(rows, width, 64)
	if a == b {
		t.Fatal("expected a colliding pair")
	}
	s, err := New(Config{Rows: rows, Width: width, MaxCandidates: 5, MaxElement: 64})
	if err != nil {
		t.Fatal(err)
	}

	exact := map[uint64]uint64{}
	for _, w := range []uint64{2, 3, 1, 4} {
		if err := s.Add(a, w); err != nil {
			t.Fatal(err)
		}
		exact[a] += w
		if err := s.Add(b, w); err != nil {
			t.Fatal(err)
		}
		exact[b] += w
		ea, _ := s.Estimate(a)
		eb, _ := s.Estimate(b)
		t.Logf("add a=%d b=%d w=%d collideRow=%d est(a)=%d>=%d est(b)=%d>=%d",
			a, b, w, row, ea, exact[a], eb, exact[b])
		if ea < exact[a] || eb < exact[b] {
			t.Fatalf("underestimate: est(a)=%d exact=%d est(b)=%d exact=%d",
				ea, exact[a], eb, exact[b])
		}
	}
	if err := s.Add(a, 1); err != nil {
		t.Fatal(err)
	}
	exact[a]++
	if ea, _ := s.Estimate(a); ea < exact[a] {
		t.Fatalf("underestimate after repeat: %d < %d", ea, exact[a])
	}
	if err := s.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

func TestTieOrdering(t *testing.T) {
	const rows, maxK = 3, 3
	elements := []uint64{7, 13, 29}
	width := collisionFreeWidth(t, rows, elements)
	s, err := New(Config{Rows: rows, Width: width, MaxCandidates: maxK, MaxElement: 100})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range elements {
		if err := s.Add(e, 5); err != nil {
			t.Fatal(err)
		}
	}
	got := s.Candidates()
	t.Logf("tie candidates=%v", got)
	if len(got) != 3 || got[0].Element != 7 || got[1].Element != 13 || got[2].Element != 29 {
		t.Fatalf("ties must break by ascending element, got %v", got)
	}
	if err := s.Add(42, 5); err != nil {
		t.Fatal(err)
	}
	got = s.Candidates()
	if got[2].Element == 42 {
		t.Fatalf("equal-rank newcomer must not replace, got %v", got)
	}
}

func TestRefreshAndReplace(t *testing.T) {
	const rows, maxK = 2, 2
	elements := []uint64{1, 2, 3, 4}
	width := collisionFreeWidth(t, rows, elements)
	s, err := New(Config{Rows: rows, Width: width, MaxCandidates: maxK, MaxElement: 100})
	if err != nil {
		t.Fatal(err)
	}
	mustAdd := func(e uint64, w uint64) {
		t.Helper()
		if err := s.Add(e, w); err != nil {
			t.Fatal(err)
		}
	}

	mustAdd(1, 4)
	mustAdd(2, 1)
	mustAdd(3, 2)
	got := s.Candidates()
	t.Logf("after replace candidates=%v", got)
	if len(got) != 2 || got[0].Element != 1 || got[1].Element != 3 {
		t.Fatalf("unexpected candidates after replace: %v", got)
	}

	mustAdd(4, 1)
	got = s.Candidates()
	if len(got) != 2 || got[1].Element != 3 {
		t.Fatalf("lower-rank arrival changed list: %v", got)
	}

	mustAdd(3, 5)
	got = s.Candidates()
	e1, _ := s.Estimate(1)
	t.Logf("after refresh est(3)=7 est(1)=%d candidates=%v", e1, got)
	if got[0].Element != 3 || got[0].Estimate != 7 {
		t.Fatalf("refresh did not reorder: %v", got)
	}
}

func TestInvalidConfig(t *testing.T) {
	cases := []Config{
		{Rows: 0, Width: 4, MaxCandidates: 2, MaxElement: 10},
		{Rows: 2, Width: 0, MaxCandidates: 2, MaxElement: 10},
		{Rows: 2, Width: 4, MaxCandidates: 0, MaxElement: 10},
		{Rows: -1, Width: 4, MaxCandidates: 2, MaxElement: 10},
	}
	for i, cfg := range cases {
		if _, err := New(cfg); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("case %d: got %v want ErrInvalidConfig", i, err)
		}
	}
}

func TestRejectedArrivalsLeaveNoTrace(t *testing.T) {
	s, err := New(Config{Rows: 3, Width: 8, MaxCandidates: 2, MaxElement: 9})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Add(1, 5); err != nil {
		t.Fatal(err)
	}
	if err := s.Add(2, 3); err != nil {
		t.Fatal(err)
	}

	snapshot := s.Candidates()

	bad := []struct {
		name    string
		element uint64
		count   uint64
		want    error
	}{
		{"element out of range high", 10, 1, ErrElementOutOfRange},
		{"element out of range far", math.MaxUint64, 1, ErrElementOutOfRange},
		{"zero count", 1, 0, ErrNonPositiveCount},
		{"overflow", 1, math.MaxUint64, ErrCountOverflow},
	}
	for _, tc := range bad {
		err := s.Add(tc.element, tc.count)
		if !errors.Is(err, tc.want) {
			t.Fatalf("%s: got %v want %v", tc.name, err, tc.want)
		}
		if after := s.Candidates(); !sameCandidates(after, snapshot) {
			t.Fatalf("%s: candidates mutated: before=%v after=%v", tc.name, snapshot, after)
		}
		if v, _ := s.Estimate(1); v != 5 {
			t.Fatalf("%s: sketch counters mutated, est(1)=%d", tc.name, v)
		}
		t.Logf("rejected %-24s err=%q candidates=%v est(1)=5", tc.name, err.Error(), snapshot)
	}

	if _, err := s.Estimate(math.MaxUint64); !errors.Is(err, ErrElementOutOfRange) {
		t.Fatalf("Estimate must reject out-of-range element, got %v", err)
	}
	if err := s.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

func TestErrorCategoriesDistinct(t *testing.T) {
	all := []error{ErrInvalidConfig, ErrElementOutOfRange, ErrNonPositiveCount, ErrCountOverflow, ErrSelfCheck}
	for i := range all {
		for j := i + 1; j < len(all); j++ {
			if all[i] == all[j] {
				t.Fatalf("error categories must be distinct: %v", all[i])
			}
		}
	}
}

func TestConcurrent(t *testing.T) {
	s, err := New(Config{Rows: 4, Width: 32, MaxCandidates: 4, MaxElement: 31})
	if err != nil {
		t.Fatal(err)
	}

	const goroutines, rounds = 16, 500
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(seed uint64) {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				e := (seed + uint64(i)*7) % 32
				switch i % 4 {
				case 0:
					_ = s.Add(e, 1)
				case 1:
					_, _ = s.Estimate(e)
				case 2:
					_ = s.Candidates()
				default:
					_ = s.SelfCheck()
				}
			}
		}(uint64(g))
	}
	wg.Wait()

	if err := s.SelfCheck(); err != nil {
		t.Fatal(err)
	}
	t.Logf("concurrent final candidates=%v", s.Candidates())
}
