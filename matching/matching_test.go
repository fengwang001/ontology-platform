package matching

import (
	"errors"
	"maps"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func mustAddApplicant(t *testing.T, m *Matcher, id int, prefs []int) {
	t.Helper()
	if err := m.AddApplicant(id, prefs); err != nil {
		t.Fatalf("AddApplicant(%d, %v): %v", id, prefs, err)
	}
}

func mustAddProgram(t *testing.T, m *Matcher, id, cap int, prefs []int) {
	t.Helper()
	if err := m.AddProgram(id, cap, prefs); err != nil {
		t.Fatalf("AddProgram(%d, %d, %v): %v", id, cap, prefs, err)
	}
}

func mustRun(t *testing.T, m *Matcher) {
	t.Helper()
	if err := m.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func mustResult(t *testing.T, m *Matcher) (map[int]int, int) {
	t.Helper()
	res, proposals, err := m.Result()
	if err != nil {
		t.Fatalf("Result: %v", err)
	}
	return res, proposals
}

// checkInvariants verifies the guarantees every Run result must satisfy:
// capacities respected, all pairs mutually acceptable, no blocking pairs,
// and the proposal total equal to the sum of per-applicant proposal counts.
func checkInvariants(t *testing.T, m *Matcher, res map[int]int, proposals int) {
	t.Helper()
	counts := map[int]int{}
	for a, p := range res {
		if p == 0 {
			continue
		}
		counts[p]++
		if !m.mutuallyAcceptable(a, p) {
			t.Errorf("result pair (%d, %d) is not mutually acceptable", a, p)
		}
	}
	for p, n := range counts {
		if n > m.programs[p].capacity {
			t.Errorf("program %d holds %d > cap %d", p, n, m.programs[p].capacity)
		}
	}
	pairs, err := m.Verify(res)
	if err != nil {
		t.Fatalf("Verify(result): %v", err)
	}
	if len(pairs) != 0 {
		t.Errorf("Verify(result) = %v, want no blocking pairs", pairs)
	}
	total := 0
	for a, p := range res {
		prefs := m.applicants[a].prefs
		if p == 0 {
			total += len(prefs)
			continue
		}
		idx := -1
		for i, q := range prefs {
			if q == p {
				idx = i
			}
		}
		if idx < 0 {
			t.Fatalf("applicant %d matched to unlisted program %d", a, p)
		}
		total += idx + 1
	}
	if total != proposals {
		t.Errorf("proposal count = %d, want %d (sum of per-applicant proposals)", proposals, total)
	}
}

// 2x2 instance where applicant-proposing and program-proposing deferred
// acceptance differ; Run must produce the applicant-optimal matching.
func TestApplicantOptimalTwoByTwo(t *testing.T) {
	m := New()
	mustAddApplicant(t, m, 1, []int{1, 2})
	mustAddApplicant(t, m, 2, []int{2, 1})
	mustAddProgram(t, m, 1, 1, []int{2, 1})
	mustAddProgram(t, m, 2, 1, []int{1, 2})
	mustRun(t, m)
	res, proposals := mustResult(t, m)
	want := map[int]int{1: 1, 2: 2} // program-proposing would give {1: 2, 2: 1}
	if !maps.Equal(res, want) {
		t.Errorf("result = %v, want applicant-optimal %v", res, want)
	}
	if proposals != 2 {
		t.Errorf("proposals = %d, want 2", proposals)
	}
	checkInvariants(t, m, res, proposals)
}

// A capacity-2 program evicts its least preferred holder, who then
// proposes further down their own list.
func TestCapacityTwoEvictsWorst(t *testing.T) {
	m := New()
	mustAddApplicant(t, m, 1, []int{1, 2})
	mustAddApplicant(t, m, 2, []int{1, 2})
	mustAddApplicant(t, m, 3, []int{1, 2})
	mustAddProgram(t, m, 1, 2, []int{1, 2, 3})
	mustAddProgram(t, m, 2, 1, []int{1, 2, 3})
	mustRun(t, m)
	res, proposals := mustResult(t, m)
	want := map[int]int{1: 1, 2: 1, 3: 2}
	if !maps.Equal(res, want) {
		t.Errorf("result = %v, want %v", res, want)
	}
	if proposals != 4 { // 1+1+2: applicant 3 is evicted from program 1 and proposes to 2
		t.Errorf("proposals = %d, want 4", proposals)
	}
	checkInvariants(t, m, res, proposals)
}

// A proposal to a program that does not list the applicant is rejected and
// never occupies a slot.
func TestRejectedWhenProgramDoesNotListApplicant(t *testing.T) {
	m := New()
	mustAddApplicant(t, m, 1, []int{1, 2})
	mustAddApplicant(t, m, 2, []int{1})
	mustAddProgram(t, m, 1, 1, []int{2})
	mustAddProgram(t, m, 2, 1, []int{1})
	mustRun(t, m)
	res, proposals := mustResult(t, m)
	want := map[int]int{1: 2, 2: 1}
	if !maps.Equal(res, want) {
		t.Errorf("result = %v, want %v", res, want)
	}
	if proposals != 3 {
		t.Errorf("proposals = %d, want 3", proposals)
	}
	checkInvariants(t, m, res, proposals)
}

// Verify reports blocking pairs for an unmatched applicant facing a
// program with a free slot, and for a full program that prefers a
// newcomer over its worst current holder.
func TestVerifyBlockingPairs(t *testing.T) {
	m := New()
	mustAddApplicant(t, m, 1, []int{1})
	mustAddApplicant(t, m, 2, []int{1})
	mustAddProgram(t, m, 1, 1, []int{2, 1})
	mustRun(t, m)

	// Everyone unmatched: program 1 has a free slot, so both mutually
	// acceptable applicants form blocking pairs with it.
	pairs, err := m.Verify(map[int]int{})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	want := []Pair{{Applicant: 1, Program: 1}, {Applicant: 2, Program: 1}}
	if !reflect.DeepEqual(pairs, want) {
		t.Errorf("Verify({}) = %v, want %v", pairs, want)
	}

	// Program 1 is full with applicant 1, but prefers applicant 2.
	pairs, err = m.Verify(map[int]int{1: 1})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	want = []Pair{{Applicant: 2, Program: 1}}
	if !reflect.DeepEqual(pairs, want) {
		t.Errorf("Verify({1:1}) = %v, want %v", pairs, want)
	}

	// The Run result itself is stable.
	res, _ := mustResult(t, m)
	if wantRes := (map[int]int{1: 0, 2: 1}); !maps.Equal(res, wantRes) {
		t.Fatalf("result = %v, want %v", res, wantRes)
	}
	pairs, err = m.Verify(res)
	if err != nil {
		t.Fatalf("Verify(result): %v", err)
	}
	if len(pairs) != 0 {
		t.Errorf("Verify(result) = %v, want no blocking pairs", pairs)
	}
}

// Verify rejects invalid matchings in the required priority order.
func TestVerifyErrorPriority(t *testing.T) {
	m := New()
	mustAddApplicant(t, m, 1, []int{1, 2})
	mustAddApplicant(t, m, 2, []int{1, 2})
	mustAddProgram(t, m, 1, 1, []int{1, 2})
	mustAddProgram(t, m, 2, 1, []int{2})
	mustRun(t, m)

	cases := []struct {
		name  string
		match map[int]int
		want  error
	}{
		{"unknown applicant", map[int]int{9: 1}, ErrUnknownMember},
		{"unknown program", map[int]int{1: 9}, ErrUnknownMember},
		{"unknown beats unacceptable", map[int]int{1: 2, 9: 9}, ErrUnknownMember},
		{"not mutually acceptable", map[int]int{1: 2}, ErrNotMutuallyAcceptable},
		{"unacceptable beats over capacity", map[int]int{1: 2, 2: 2}, ErrNotMutuallyAcceptable},
		{"over capacity", map[int]int{1: 1, 2: 1}, ErrOverCapacity},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := m.Verify(tc.match); !errors.Is(err, tc.want) {
				t.Errorf("Verify(%v) error = %v, want %v", tc.match, err, tc.want)
			}
		})
	}
	// First violation by ascending applicant: applicant 1's pair is
	// reported even though applicant 2's is also unacceptable.
	m2 := New()
	mustAddApplicant(t, m2, 1, []int{1})
	mustAddApplicant(t, m2, 2, []int{2})
	mustAddProgram(t, m2, 1, 1, []int{1})
	mustAddProgram(t, m2, 2, 1, []int{2})
	mustRun(t, m2)
	_, err := m2.Verify(map[int]int{1: 2, 2: 1})
	if !errors.Is(err, ErrNotMutuallyAcceptable) {
		t.Fatalf("Verify error = %v, want %v", err, ErrNotMutuallyAcceptable)
	}
	if got := err.Error(); !strings.Contains(got, "applicant 1") {
		t.Errorf("Verify error = %q, want first violation at applicant 1", got)
	}
}

// Result and Verify before Run report ErrNotRun.
func TestNotRun(t *testing.T) {
	m := New()
	if _, _, err := m.Result(); !errors.Is(err, ErrNotRun) {
		t.Errorf("Result error = %v, want %v", err, ErrNotRun)
	}
	if _, err := m.Verify(nil); !errors.Is(err, ErrNotRun) {
		t.Errorf("Verify error = %v, want %v", err, ErrNotRun)
	}
}

// Registration rejections are distinguishable, reported in the required
// priority order, and leave the registry unchanged.
func TestRegistrationRejectionPriority(t *testing.T) {
	m := New()
	mustAddApplicant(t, m, 1, []int{1})
	mustAddProgram(t, m, 1, 1, []int{1})

	cases := []struct {
		name string
		add  func() error
		want error
	}{
		{"applicant id < 1", func() error { return m.AddApplicant(0, []int{0, 0}) }, ErrInvalidID},
		{"program id < 1", func() error { return m.AddProgram(-1, 0, []int{0}) }, ErrInvalidID},
		{"id beats cap", func() error { return m.AddProgram(0, 0, nil) }, ErrInvalidID},
		{"cap < 1", func() error { return m.AddProgram(2, 0, []int{0, 0}) }, ErrInvalidCapacity},
		{"cap beats duplicate id", func() error { return m.AddProgram(1, 0, nil) }, ErrInvalidCapacity},
		{"duplicate applicant", func() error { return m.AddApplicant(1, []int{0}) }, ErrDuplicateID},
		{"duplicate program", func() error { return m.AddProgram(1, 1, []int{0}) }, ErrDuplicateID},
		{"duplicate beats bad pref", func() error { return m.AddApplicant(1, []int{0}) }, ErrDuplicateID},
		{"pref id < 1", func() error { return m.AddApplicant(2, []int{1, -3, 1}) }, ErrInvalidPreference},
		{"bad pref beats duplicate pref", func() error { return m.AddApplicant(2, []int{3, 3, 0}) }, ErrInvalidPreference},
		{"duplicate pref", func() error { return m.AddApplicant(2, []int{1, 1}) }, ErrDuplicatePreference},
		{"program pref id < 1", func() error { return m.AddProgram(3, 1, []int{0}) }, ErrInvalidPreference},
		{"program duplicate pref", func() error { return m.AddProgram(3, 1, []int{1, 1}) }, ErrDuplicatePreference},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.add(); !errors.Is(err, tc.want) {
				t.Errorf("error = %v, want %v", err, tc.want)
			}
		})
	}

	// None of the rejected calls changed the registry: the matcher still
	// holds exactly applicant 1 and program 1 and runs accordingly.
	mustRun(t, m)
	res, proposals := mustResult(t, m)
	if want := (map[int]int{1: 1}); !maps.Equal(res, want) {
		t.Errorf("result = %v, want %v", res, want)
	}
	if proposals != 1 {
		t.Errorf("proposals = %d, want 1", proposals)
	}

	// After a successful Run the registry is frozen.
	if err := m.AddApplicant(5, nil); !errors.Is(err, ErrFrozen) {
		t.Errorf("AddApplicant after Run = %v, want %v", err, ErrFrozen)
	}
	if err := m.AddProgram(5, 1, nil); !errors.Is(err, ErrFrozen) {
		t.Errorf("AddProgram after Run = %v, want %v", err, ErrFrozen)
	}
	// Frozen beats every other rejection reason.
	if err := m.AddApplicant(0, []int{0}); !errors.Is(err, ErrFrozen) {
		t.Errorf("AddApplicant after Run = %v, want %v", err, ErrFrozen)
	}
	if err := m.AddProgram(0, 0, []int{0}); !errors.Is(err, ErrFrozen) {
		t.Errorf("AddProgram after Run = %v, want %v", err, ErrFrozen)
	}
}

// Run reports the first unknown reference (applicants ascending, then
// programs ascending, each in list order) without freezing or changing
// state; after fixing the registration, Run succeeds.
func TestRunUnknownReference(t *testing.T) {
	m := New()
	mustAddApplicant(t, m, 1, []int{7, 1})
	mustAddApplicant(t, m, 2, []int{1})
	mustAddProgram(t, m, 1, 1, []int{2, 9})

	err := m.Run()
	if !errors.Is(err, ErrUnknownReference) {
		t.Fatalf("Run error = %v, want %v", err, ErrUnknownReference)
	}
	if got := err.Error(); !strings.Contains(got, "applicant 1") || !strings.Contains(got, "program 7") {
		t.Errorf("Run error = %q, want first unknown reference (applicant 1, program 7)", got)
	}
	// Not frozen, not run: registration continues, queries still fail.
	if _, _, err := m.Result(); !errors.Is(err, ErrNotRun) {
		t.Errorf("Result after failed Run = %v, want %v", err, ErrNotRun)
	}
	mustAddProgram(t, m, 7, 1, []int{1})

	// Applicant references are now fine; program 1's reference to
	// applicant 9 is the next (and last) unknown reference.
	err = m.Run()
	if !errors.Is(err, ErrUnknownReference) {
		t.Fatalf("Run error = %v, want %v", err, ErrUnknownReference)
	}
	if got := err.Error(); !strings.Contains(got, "program 1") || !strings.Contains(got, "applicant 9") {
		t.Errorf("Run error = %q, want unknown reference (program 1, applicant 9)", got)
	}
	mustAddApplicant(t, m, 9, []int{1})

	mustRun(t, m)
	res, _ := mustResult(t, m)
	want := map[int]int{1: 7, 2: 1, 9: 0}
	if !maps.Equal(res, want) {
		t.Errorf("result = %v, want %v", res, want)
	}
}

// A second Run after a successful one returns the same result without
// recomputing.
func TestRunIdempotent(t *testing.T) {
	m := New()
	mustAddApplicant(t, m, 1, []int{1, 2})
	mustAddApplicant(t, m, 2, []int{1})
	mustAddProgram(t, m, 1, 1, []int{2, 1})
	mustAddProgram(t, m, 2, 1, []int{1})
	mustRun(t, m)
	res1, prop1 := mustResult(t, m)
	mustRun(t, m)
	res2, prop2 := mustResult(t, m)
	if !maps.Equal(res1, res2) || prop1 != prop2 {
		t.Errorf("second Run = (%v, %d), want (%v, %d)", res2, prop2, res1, prop1)
	}
}

// Registration order does not matter: shuffling the registration sequence
// yields a field-identical result, and replaying the same sequence
// reproduces it exactly.
func TestDeterministicUnderShuffleAndReplay(t *testing.T) {
	applicants := map[int][]int{
		1: {3, 1, 2},
		2: {1, 3},
		3: {2, 3, 1},
		4: {2},
	}
	programs := map[int]struct {
		cap   int
		prefs []int
	}{
		1: {1, []int{2, 1, 3}},
		2: {2, []int{3, 4, 1}},
		3: {1, []int{1, 2, 3, 4}},
	}
	build := func(appOrder, progOrder []int) *Matcher {
		m := New()
		for _, a := range appOrder {
			mustAddApplicant(t, m, a, applicants[a])
		}
		for _, p := range progOrder {
			mustAddProgram(t, m, p, programs[p].cap, programs[p].prefs)
		}
		mustRun(t, m)
		return m
	}
	base := build([]int{1, 2, 3, 4}, []int{1, 2, 3})
	baseRes, baseProp := mustResult(t, base)
	checkInvariants(t, base, baseRes, baseProp)

	shuffled := [][]int{
		{4, 2, 3, 1}, {3, 1, 4, 2}, {2, 4, 1, 3},
	}
	shuffledProgs := [][]int{
		{3, 1, 2}, {2, 3, 1}, {1, 3, 2},
	}
	for i := range shuffled {
		m := build(shuffled[i], shuffledProgs[i])
		res, prop := mustResult(t, m)
		if !maps.Equal(res, baseRes) || prop != baseProp {
			t.Errorf("shuffle %d: got (%v, %d), want (%v, %d)", i, res, prop, baseRes, baseProp)
		}
	}
	for replay := 0; replay < 3; replay++ {
		m := build([]int{1, 2, 3, 4}, []int{1, 2, 3})
		res, prop := mustResult(t, m)
		if !maps.Equal(res, baseRes) || prop != baseProp {
			t.Errorf("replay %d: got (%v, %d), want (%v, %d)", replay, res, prop, baseRes, baseProp)
		}
	}
}

// Concurrent registration, running, and querying behaves like some serial
// execution and stays consistent with the sequential result.
func TestConcurrentUse(t *testing.T) {
	want := New()
	for a := 1; a <= 8; a++ {
		mustAddApplicant(t, want, a, []int{1, 2, 3})
	}
	mustAddProgram(t, want, 1, 2, []int{1, 2, 3, 4, 5, 6, 7, 8})
	mustAddProgram(t, want, 2, 2, []int{8, 7, 6, 5, 4, 3, 2, 1})
	mustAddProgram(t, want, 3, 2, []int{1, 3, 5, 7})
	mustRun(t, want)
	wantRes, wantProp := mustResult(t, want)

	m := New()
	var wg sync.WaitGroup
	for a := 1; a <= 8; a++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := m.AddApplicant(a, []int{1, 2, 3}); err != nil {
				t.Errorf("AddApplicant(%d): %v", a, err)
			}
		}()
	}
	for p := 1; p <= 3; p++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			prefs := map[int][]int{1: {1, 2, 3, 4, 5, 6, 7, 8}, 2: {8, 7, 6, 5, 4, 3, 2, 1}, 3: {1, 3, 5, 7}}
			if err := m.AddProgram(p, 2, prefs[p]); err != nil {
				t.Errorf("AddProgram(%d): %v", p, err)
			}
		}()
	}
	wg.Wait()

	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := m.Run(); err != nil {
				t.Errorf("Run: %v", err)
			}
			res, prop, err := m.Result()
			if err != nil {
				t.Errorf("Result: %v", err)
				return
			}
			if !maps.Equal(res, wantRes) || prop != wantProp {
				t.Errorf("got (%v, %d), want (%v, %d)", res, prop, wantRes, wantProp)
			}
			if _, err := m.Verify(res); err != nil {
				t.Errorf("Verify(result): %v", err)
			}
			if err := m.AddApplicant(100, nil); !errors.Is(err, ErrFrozen) {
				t.Errorf("AddApplicant after freeze = %v, want %v", err, ErrFrozen)
			}
		}()
	}
	wg.Wait()
}

// With the cap exactly full, a more preferred newcomer replaces the worst
// current holder.
func TestFullProgramNewcomerReplacesWorst(t *testing.T) {
	m := New()
	mustAddApplicant(t, m, 1, []int{1, 2})
	mustAddApplicant(t, m, 2, []int{1})
	mustAddProgram(t, m, 1, 1, []int{2, 1})
	mustAddProgram(t, m, 2, 1, []int{1})
	mustRun(t, m)
	res, proposals := mustResult(t, m)
	want := map[int]int{1: 2, 2: 1}
	if !maps.Equal(res, want) {
		t.Errorf("result = %v, want %v", res, want)
	}
	if proposals != 3 {
		t.Errorf("proposals = %d, want 3", proposals)
	}
	checkInvariants(t, m, res, proposals)
}
