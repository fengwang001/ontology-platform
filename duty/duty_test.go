package duty

import (
	"errors"
	"fmt"
	"strconv"
	"sync"
	"testing"
)

// logf prints inputs/outputs and the reason behind each judgement so the test
// log can be audited end to end.
func logf(t *testing.T, format string, args ...any) {
	t.Helper()
	t.Logf(format, args...)
}

// naiveTimeline is the independent reference: scan every integer minute of
// [a,b), resolve the newest covering override per minute, and coalesce only
// adjacent minutes with identical member and source.
func naiveTimeline(sched *Schedule, a, b int64) []Segment {
	entries := sched.snapshot()
	want := func(x int64) (string, string) {
		if e, ok := winnerAt(entries, x); ok {
			return e.ov.Member, e.ov.ID
		}
		return sched.rotationMember(x), "rotation"
	}
	var out []Segment
	for x := a; x < b; x++ {
		m, src := want(x)
		if n := len(out); n > 0 && out[n-1].End == x &&
			out[n-1].Member == m && out[n-1].Source == src {
			out[n-1].End = x + 1
		} else {
			out = append(out, Segment{Start: x, End: x + 1, Member: m, Source: src})
		}
	}
	return out
}

// crosscheckMinute runs the independent minute-by-minute implementation,
// checks partition/abut/non-mergeable invariants, and asserts Who agrees with
// Timeline for every minute of [a,b).
func crosscheckMinute(t *testing.T, sched *Schedule, a, b int64) {
	t.Helper()
	got, err := sched.Timeline(a, b)
	if err != nil {
		t.Fatalf("Timeline(%d,%d) error: %v", a, b, err)
	}
	want := naiveTimeline(sched, a, b)
	logf(t, "naive cross-check [%d,%d): %d optimized segments vs %d scanned segments",
		a, b, len(got), len(want))
	if len(got) != len(want) {
		t.Fatalf("mismatch:\n optimized %v\n naive     %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("segment %d optimized %+v naive %+v", i, got[i], want[i])
		}
	}
	if len(got) == 0 || got[0].Start != a || got[len(got)-1].End != b {
		t.Fatalf("timeline does not exactly cover [%d,%d): %v", a, b, got)
	}
	for i := 1; i < len(got); i++ {
		if got[i-1].End != got[i].Start {
			t.Fatalf("gap/overlap at %d: %v", i, got)
		}
		if got[i-1].Member == got[i].Member && got[i-1].Source == got[i].Source {
			t.Fatalf("adjacent segments %d,%d could merge", i-1, i)
		}
	}
	for x := a; x < b; x++ {
		m, src := sched.Who(x)
		var seg *Segment
		for i := range got {
			if got[i].Start <= x && x < got[i].End {
				seg = &got[i]
				break
			}
		}
		if seg == nil || seg.Member != m || seg.Source != src {
			t.Fatalf("Who(%d)=(%s,%s) disagrees with timeline %+v", x, m, src, seg)
		}
	}
}

func assertTimeline(t *testing.T, sched *Schedule, a, b int64, want []Segment) {
	t.Helper()
	got, err := sched.Timeline(a, b)
	if err != nil {
		t.Fatalf("Timeline(%d,%d) unexpected error: %v", a, b, err)
	}
	logf(t, "input Timeline(%d,%d)\noutput %v\nbasis  %v", a, b, got, want)
	if len(got) != len(want) {
		t.Fatalf("segment count = %d, want %d: got %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("segment %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func addOK(t *testing.T, sched *Schedule, o Override) {
	t.Helper()
	if err := sched.Add(o); err != nil {
		t.Fatalf("Add(%+v): %v", o, err)
	}
	logf(t, "input Add(%+v) -> ok; basis: newest active override wins", o)
}

func TestRotationExtrapolation(t *testing.T) {
	// members A,B,C ; T0=10 ; L=5. t=T0-1 belongs to the last member C;
	// t=T0-L=5: floorDiv(-5,5)=-1, floorMod(-1,3)=2 => C as well.
	sched, err := New([]string{"A", "B", "C"}, 10, 5)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		t         int64
		member    string
		rationale string
	}{
		{10, "A", "exactly T0"},
		{14, "A", "last minute of first shift"},
		{15, "B", "shift boundary"},
		{25, "A", "wraps around"},
		{9, "C", "T0-1 extrapolates to last member"},
		{5, "C", "T0-L: floorMod(-1,3)=2"},
		{4, "B", "floorDiv(-6,5)=-2, mod 3 = 1"},
		{-5, "A", "floorDiv(-15,5)=-3, mod 3 = 0"},
	}
	for _, c := range cases {
		m, src := sched.Who(c.t)
		logf(t, "input Who(%d) -> (%s,%s); basis: %s", c.t, m, src, c.rationale)
		if src != "rotation" || m != c.member {
			t.Fatalf("Who(%d)=(%s,%s), want (%s,rotation)", c.t, m, src, c.member)
		}
	}
	crosscheckMinute(t, sched, 0, 40)
	crosscheckMinute(t, sched, -20, 25)
}

func TestBoundaryCoincidence(t *testing.T) {
	// Override endpoints coincide with rotation boundaries; [s,e) is half-open.
	sched, _ := New([]string{"A", "B", "C"}, 10, 5)
	addOK(t, sched, Override{ID: "o1", Member: "B", Start: 15, End: 25})
	assertTimeline(t, sched, 10, 30, []Segment{
		{10, 15, "A", "rotation"},
		{15, 25, "B", "o1"},
		{25, 30, "A", "rotation"},
	})
	crosscheckMinute(t, sched, 0, 40)
}

func TestNestedAndCrossingOverrides(t *testing.T) {
	// Three overrides: o1 outer, o2 nested inside o1, o3 crosses both.
	// Add order o1,o2,o3: at every overlap the newest wins.
	sched, _ := New([]string{"A", "B", "C", "M1", "M2", "M3"}, 0, 10)
	addOK(t, sched, Override{ID: "o1", Member: "M1", Start: 10, End: 50})
	addOK(t, sched, Override{ID: "o2", Member: "M2", Start: 20, End: 40})
	addOK(t, sched, Override{ID: "o3", Member: "M3", Start: 30, End: 70})
	assertTimeline(t, sched, 0, 80, []Segment{
		{0, 10, "A", "rotation"},
		{10, 20, "M1", "o1"},
		{20, 30, "M2", "o2"},
		{30, 70, "M3", "o3"},
		{70, 80, "B", "rotation"},
	})
	// Remove newest o3: earlier winner returns on [30,40), M1 resumes to 50.
	if err := sched.Remove("o3"); err != nil {
		t.Fatal(err)
	}
	logf(t, "input Remove(o3) -> ok; basis: deleted interval restores earlier winner")
	assertTimeline(t, sched, 0, 80, []Segment{
		{0, 10, "A", "rotation"},
		{10, 20, "M1", "o1"},
		{20, 40, "M2", "o2"},
		{40, 50, "M1", "o1"},
		{50, 60, "M3", "rotation"},
		{60, 70, "A", "rotation"},
		{70, 80, "B", "rotation"},
	})
	// Remove o2 too: o1 covers the whole outer interval again.
	if err := sched.Remove("o2"); err != nil {
		t.Fatal(err)
	}
	assertTimeline(t, sched, 0, 80, []Segment{
		{0, 10, "A", "rotation"},
		{10, 50, "M1", "o1"},
		{50, 60, "M3", "rotation"},
		{60, 70, "A", "rotation"},
		{70, 80, "B", "rotation"},
	})
	crosscheckMinute(t, sched, 0, 80)
}

func TestDeleteLetsEarlierOverrideWin(t *testing.T) {
	sched, _ := New([]string{"A"}, 0, 4)
	addOK(t, sched, Override{ID: "old", Member: "A", Start: 0, End: 20})
	addOK(t, sched, Override{ID: "new", Member: "A", Start: 0, End: 20})
	if m, src := sched.Who(10); m != "A" || src != "new" {
		t.Fatalf("Who(10)=(%s,%s), want (A,new)", m, src)
	}
	if err := sched.Remove("new"); err != nil {
		t.Fatal(err)
	}
	if m, src := sched.Who(10); m != "A" || src != "old" {
		t.Fatalf("after remove Who(10)=(%s,%s), want (A,old)", m, src)
	}
	logf(t, "basis: removing newest lets the older still-active override win")
	// The same id may be re-added and becomes the brand-new last added entry.
	addOK(t, sched, Override{ID: "new", Member: "A", Start: 0, End: 20})
	if _, src := sched.Who(10); src != "new" {
		t.Fatalf("re-added id should win, got source %s", src)
	}
	crosscheckMinute(t, sched, 0, 20)
}

func TestSingleRosterMerges(t *testing.T) {
	// One-member roster: every rotation shift merges into a single piece.
	sched, _ := New([]string{"SOLO"}, 0, 3)
	addOK(t, sched, Override{ID: "o1", Member: "SOLO", Start: 10, End: 13})
	assertTimeline(t, sched, 0, 20, []Segment{
		{0, 10, "SOLO", "rotation"},
		{10, 13, "SOLO", "o1"},
		{13, 20, "SOLO", "rotation"},
	})
	logf(t, "basis: same member but different source never merges")
	crosscheckMinute(t, sched, -6, 25)
}

func TestSameMemberDifferentIDsDoNotMerge(t *testing.T) {
	sched, _ := New([]string{"A", "B", "Z"}, 0, 100)
	addOK(t, sched, Override{ID: "x", Member: "Z", Start: 0, End: 10})
	addOK(t, sched, Override{ID: "y", Member: "Z", Start: 10, End: 20})
	assertTimeline(t, sched, 0, 20, []Segment{
		{0, 10, "Z", "x"},
		{10, 20, "Z", "y"},
	})
	crosscheckMinute(t, sched, 0, 20)
}

func TestRotationRestoredAfterOverride(t *testing.T) {
	sched, _ := New([]string{"A", "B", "C"}, 0, 10)
	addOK(t, sched, Override{ID: "o1", Member: "A", Start: 5, End: 15})
	// Identical member on both sides never merges across different sources.
	assertTimeline(t, sched, 0, 30, []Segment{
		{0, 5, "A", "rotation"},
		{5, 15, "A", "o1"},
		{15, 20, "B", "rotation"},
		{20, 30, "C", "rotation"},
	})
	crosscheckMinute(t, sched, 0, 30)
}

func TestValidationAndCheckOrder(t *testing.T) {
	if _, err := New(nil, 0, 1); !errors.Is(err, ErrEmptyRoster) {
		t.Fatalf("empty roster: %v", err)
	}
	if _, err := New([]string{"A", "A"}, 0, 1); !errors.Is(err, ErrDuplicateMember) {
		t.Fatalf("duplicate member: %v", err)
	}
	if _, err := New([]string{"A"}, 0, 0); !errors.Is(err, ErrInvalidLength) {
		t.Fatalf("L=0: %v", err)
	}
	if _, err := New([]string{"A"}, 0, -3); !errors.Is(err, ErrInvalidLength) {
		t.Fatalf("L<0: %v", err)
	}
	sched, _ := New([]string{"A", "B"}, 0, 10)

	// Interval checks precede member/id checks.
	if err := sched.Add(Override{ID: "dup", Member: "GHOST", Start: 5, End: 5}); !errors.Is(err, ErrEmptyInterval) {
		t.Fatalf("empty interval: %v", err)
	}
	if err := sched.Add(Override{ID: "dup", Member: "GHOST", Start: 9, End: 5}); !errors.Is(err, ErrInvertedInterval) {
		t.Fatalf("inverted interval: %v", err)
	}
	if err := sched.Add(Override{ID: "dup", Member: "GHOST", Start: 0, End: 5}); !errors.Is(err, ErrUnknownMember) {
		t.Fatalf("unknown member before dup-id check: %v", err)
	}
	// A rejected add must not mutate the set: removing its id still fails.
	if err := sched.Remove("dup"); !errors.Is(err, ErrOverrideNotFound) {
		t.Fatalf("rejected add mutated set: %v", err)
	}
	addOK(t, sched, Override{ID: "dup", Member: "A", Start: 0, End: 5})
	if err := sched.Add(Override{ID: "dup", Member: "B", Start: 6, End: 9}); !errors.Is(err, ErrDuplicateOverride) {
		t.Fatalf("duplicate id: %v", err)
	}

	if _, err := sched.Timeline(5, 5); !errors.Is(err, ErrEmptyInterval) {
		t.Fatalf("empty timeline: %v", err)
	}
	if _, err := sched.Timeline(6, 5); !errors.Is(err, ErrInvertedInterval) {
		t.Fatalf("inverted timeline: %v", err)
	}
	if err := sched.Remove("missing"); !errors.Is(err, ErrOverrideNotFound) {
		t.Fatalf("remove missing: %v", err)
	}
	logf(t, "basis: interval(empty,inverted) -> member -> id order; rejected ops leave state intact")
}

func TestTimelineLimit(t *testing.T) {
	// Disjoint one-minute overrides, each a distinct source. Every override
	// minute and every gap is a separate segment, so counts are predictable.
	sched, _ := New([]string{"A", "B"}, 0, 1)
	// 5000 override minutes alternating with 5000 gap minutes => 10000 pieces.
	for i := int64(0); i < maxSegments/2; i++ {
		if err := sched.Add(Override{ID: fmtID(i), Member: "A", Start: 2 * i, End: 2*i + 1}); err != nil {
			t.Fatal(err)
		}
	}
	logf(t, "input: %d disjoint one-minute overrides on a 1-minute rotation", maxSegments/2)
	segs, err := sched.Timeline(0, maxSegments)
	if err != nil {
		t.Fatalf("boundary size: %v", err)
	}
	if len(segs) != maxSegments {
		t.Fatalf("segments = %d, want %d", len(segs), maxSegments)
	}
	// Extending by one minute appends one more unmergeable segment -> rejected.
	if _, err := sched.Timeline(0, maxSegments+1); !errors.Is(err, ErrTimelineTooLarge) {
		t.Fatalf("got %v, want ErrTimelineTooLarge", err)
	}
	logf(t, "basis: exactly %d segments accepted; one more rejected", maxSegments)
}

func fmtID(i int64) string {
	return "k" + strconv.FormatInt(i, 10)
}

func TestReplayDeterminism(t *testing.T) {
	// The same operation sequence replayed must yield identical Who/Timeline
	// results; each op only carries its inputs, never timing-dependent state.
	ops := []func(*Schedule){
		func(s *Schedule) {
			_ = s.Add(Override{ID: "o1", Member: "B", Start: -5, End: 12})
		},
		func(s *Schedule) { _ = s.Add(Override{ID: "o2", Member: "A", Start: 3, End: 30}) },
		func(s *Schedule) { _ = s.Remove("o1") },
		func(s *Schedule) {
			_ = s.Add(Override{ID: "o1", Member: "C", Start: 0, End: 50})
		},
	}
	run := func() string {
		s, _ := New([]string{"A", "B", "C"}, 10, 7)
		for _, op := range ops {
			op(s)
		}
		seg, _ := s.Timeline(-20, 60)
		out := ""
		for x := int64(-20); x < 60; x += 3 {
			m, src := s.Who(x)
			out += strconv.FormatInt(x, 10) + "=" + m + "/" + src + ";"
		}
		return fmt.Sprint(seg) + "|" + out
	}
	first := run()
	for i := 0; i < 5; i++ {
		if got := run(); got != first {
			t.Fatalf("replay %d differs:\n %s\n %s", i, got, first)
		}
	}
	logf(t, "basis: replay x6 identical: %d timeline chars", len(first))
}

func TestConcurrentAccess(t *testing.T) {
	// Mixed Add/Remove/Who/Timeline must be safe under -race and equivalent to
	// some serial order: every observed Who result must be a member the current
	// schedule can legitimately report (no torn reads).
	sched, _ := New([]string{"A", "B", "C", "D"}, 0, 11)
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			i := 0
			for i <= 2000 {
				id := "w" + strconv.Itoa(w) + "-" + strconv.Itoa(i%3)
				member := []string{"A", "B", "C", "D"}[(w+i)%4]
				_ = sched.Add(Override{ID: id, Member: member,
					Start: int64(i % 40), End: int64(i%40) + 5})
				_ = sched.Remove(id)
				i++
			}
		}(w)
	}
	for q := 0; q < 4; q++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 2000; i++ {
				m, src := sched.Who(int64(i%50) - 10)
				switch {
				case src == "rotation" && (m < "A" || m > "D"):
					t.Errorf("torn Who: %q %q", m, src)
					return
				case src != "rotation" && src[:1] != "w":
					t.Errorf("bogus source %q", src)
					return
				}
				if seg, err := sched.Timeline(0, 40); err == nil {
					if len(seg) == 0 || seg[0].Start != 0 || seg[len(seg)-1].End != 40 {
						t.Errorf("timeline not covering: %v", seg)
						return
					}
				}
			}
		}()
	}
	wg.Wait()
	logf(t, "basis: 4 writers + 4 readers interleave; -race clean, sources valid")
}
