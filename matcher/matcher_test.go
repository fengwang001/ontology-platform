package matcher

import (
	"errors"
	"reflect"
	"sync"
	"testing"
)

type progSpec struct {
	cap   int
	prefs []int
}

func build(t *testing.T, applicants map[int][]int, programs map[int]progSpec) *Matcher {
	t.Helper()
	m := New()
	for id, prefs := range applicants {
		if err := m.AddApplicant(id, prefs); err != nil {
			t.Fatalf("AddApplicant(%d): %v", id, err)
		}
	}
	for id, p := range programs {
		if err := m.AddProgram(id, p.cap, p.prefs); err != nil {
			t.Fatalf("AddProgram(%d): %v", id, err)
		}
	}
	return m
}

func mustRun(t *testing.T, m *Matcher) (map[int]int, int) {
	t.Helper()
	result, proposals, err := m.Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return result, proposals
}

// Two applicants and two programs where the applicant-optimal and
// program-optimal stable matchings differ; Run must produce the
// applicant-optimal one.
func TestApplicantOptimalTwoByTwo(t *testing.T) {
	m := build(t,
		map[int][]int{1: {1, 2}, 2: {2, 1}},
		map[int]progSpec{1: {1, []int{2, 1}}, 2: {1, []int{1, 2}}},
	)
	result, proposals := mustRun(t, m)

	applicantOptimal := map[int]int{1: 1, 2: 2}
	programOptimal := map[int]int{1: 2, 2: 1}
	if !reflect.DeepEqual(result, applicantOptimal) {
		t.Fatalf("result = %v, want applicant-optimal %v", result, applicantOptimal)
	}
	if reflect.DeepEqual(result, programOptimal) {
		t.Fatalf("result = %v matches program-optimal %v, want applicant-optimal", result, programOptimal)
	}
	if proposals != 2 {
		t.Fatalf("proposals = %d, want 2", proposals)
	}
}

// A capacity-2 program kicks its least preferred tentative holder, who then
// proposes further down their own list.
func TestCapacityTwoKicksWorst(t *testing.T) {
	m := build(t,
		map[int][]int{1: {1, 2}, 2: {1, 2}, 3: {1, 2}},
		map[int]progSpec{1: {2, []int{1, 2, 3}}, 2: {1, []int{1, 2, 3}}},
	)
	result, proposals := mustRun(t, m)

	want := map[int]int{1: 1, 2: 1, 3: 2}
	if !reflect.DeepEqual(result, want) {
		t.Fatalf("result = %v, want %v", result, want)
	}
	if proposals != 4 {
		t.Fatalf("proposals = %d, want 4", proposals)
	}
}

// A proposal to a program whose own list does not contain the applicant is
// rejected and never occupies a slot.
func TestProposalRejectedWhenNotOnProgramList(t *testing.T) {
	m := build(t,
		map[int][]int{1: {1}, 2: {1}},
		map[int]progSpec{1: {1, []int{2}}},
	)
	result, proposals := mustRun(t, m)

	want := map[int]int{1: 0, 2: 1}
	if !reflect.DeepEqual(result, want) {
		t.Fatalf("result = %v, want %v", result, want)
	}
	if proposals != 2 {
		t.Fatalf("proposals = %d, want 2", proposals)
	}
}

// When a program's capacity is exactly full, a more preferred new applicant
// replaces the current worst holder.
func TestFullCapacityPreferredReplacesWorst(t *testing.T) {
	m := build(t,
		map[int][]int{1: {1, 2}, 2: {1}},
		map[int]progSpec{1: {1, []int{2, 1}}, 2: {1, []int{1}}},
	)
	result, proposals := mustRun(t, m)

	want := map[int]int{1: 2, 2: 1}
	if !reflect.DeepEqual(result, want) {
		t.Fatalf("result = %v, want %v", result, want)
	}
	if proposals != 3 {
		t.Fatalf("proposals = %d, want 3", proposals)
	}
}

// Verify reports blocking pairs for an unmatched applicant facing a program
// with a free slot, and for a full program that prefers a newcomer over its
// worst current holder.
func TestVerifyBlockingPairs(t *testing.T) {
	m := build(t,
		map[int][]int{1: {1, 2}, 2: {1}},
		map[int]progSpec{1: {1, []int{1, 2}}, 2: {1, []int{1}}},
	)
	mustRun(t, m)

	pairs, err := m.Verify(map[int]int{})
	if err != nil {
		t.Fatalf("Verify(empty): %v", err)
	}
	wantEmpty := []Pair{{1, 1}, {1, 2}, {2, 1}}
	if !reflect.DeepEqual(pairs, wantEmpty) {
		t.Fatalf("Verify(empty) = %v, want %v", pairs, wantEmpty)
	}

	pairs, err = m.Verify(map[int]int{1: 2, 2: 1})
	if err != nil {
		t.Fatalf("Verify(full): %v", err)
	}
	wantFull := []Pair{{1, 1}}
	if !reflect.DeepEqual(pairs, wantFull) {
		t.Fatalf("Verify(full) = %v, want %v", pairs, wantFull)
	}

	result, _ := mustRun(t, m)
	pairs, err = m.Verify(result)
	if err != nil {
		t.Fatalf("Verify(result): %v", err)
	}
	if len(pairs) != 0 {
		t.Fatalf("Verify(result) = %v, want no blocking pairs", pairs)
	}
}

// Rejected registrations must not alter the registry or the result.
func TestRejectedOperationsKeepState(t *testing.T) {
	m := build(t,
		map[int][]int{1: {1}},
		map[int]progSpec{1: {1, []int{1}}},
	)

	rejections := []error{
		m.AddApplicant(0, []int{1}),
		m.AddApplicant(1, []int{1}),
		m.AddApplicant(2, []int{0}),
		m.AddApplicant(2, []int{1, 1}),
		m.AddProgram(0, 1, []int{1}),
		m.AddProgram(2, 0, []int{1}),
		m.AddProgram(1, 1, []int{1}),
		m.AddProgram(2, 1, []int{0}),
		m.AddProgram(2, 1, []int{1, 1}),
	}
	for i, err := range rejections {
		if err == nil {
			t.Fatalf("rejection %d: got nil error", i)
		}
	}

	result, proposals := mustRun(t, m)
	want := map[int]int{1: 1}
	if !reflect.DeepEqual(result, want) || proposals != 1 {
		t.Fatalf("after rejections: result = %v proposals = %d, want %v, 1", result, proposals, want)
	}

	if err := m.AddApplicant(9, []int{1}); !errors.Is(err, ErrFrozen) {
		t.Fatalf("AddApplicant after Run = %v, want ErrFrozen", err)
	}
	if err := m.AddProgram(9, 1, []int{1}); !errors.Is(err, ErrFrozen) {
		t.Fatalf("AddProgram after Run = %v, want ErrFrozen", err)
	}

	again, againProposals := mustRun(t, m)
	if !reflect.DeepEqual(again, want) || againProposals != 1 {
		t.Fatalf("second Run = %v, %d, want %v, 1", again, againProposals, want)
	}
}

// Rejection reasons are reported with the documented priority, first match
// only.
func TestRegistrationErrorPriority(t *testing.T) {
	m := New()
	if err := m.AddApplicant(1, nil); err != nil {
		t.Fatal(err)
	}
	if err := m.AddProgram(1, 1, nil); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		err  error
		want error
	}{
		{"applicant invalid id", m.AddApplicant(-1, []int{0, 0}), ErrInvalidID},
		{"applicant duplicate id beats bad prefs", m.AddApplicant(1, []int{0}), ErrDuplicateID},
		{"applicant invalid pref beats duplicate pref", m.AddApplicant(2, []int{0, 0}), ErrInvalidPreference},
		{"applicant duplicate pref", m.AddApplicant(2, []int{1, 1}), ErrDuplicatePreference},
		{"program invalid id", m.AddProgram(-2, 0, []int{0}), ErrInvalidID},
		{"program invalid capacity beats duplicate id", m.AddProgram(1, 0, []int{0}), ErrInvalidCapacity},
		{"program duplicate id beats bad prefs", m.AddProgram(1, 1, []int{0}), ErrDuplicateID},
		{"program invalid pref beats duplicate pref", m.AddProgram(2, 1, []int{0, 0}), ErrInvalidPreference},
		{"program duplicate pref", m.AddProgram(2, 1, []int{1, 1}), ErrDuplicatePreference},
	}
	for _, c := range cases {
		if !errors.Is(c.err, c.want) {
			t.Fatalf("%s: got %v, want %v", c.name, c.err, c.want)
		}
	}

	if _, _, err := m.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if err := m.AddApplicant(0, []int{0}); !errors.Is(err, ErrFrozen) {
		t.Fatalf("frozen beats invalid id: got %v", err)
	}
	if err := m.AddProgram(0, 0, []int{0}); !errors.Is(err, ErrFrozen) {
		t.Fatalf("frozen beats invalid id/capacity: got %v", err)
	}
}

// Run reports the first unknown reference (applicants ascending, then
// programs ascending, each in list order) without freezing or changing
// state.
func TestUnknownReferenceDoesNotFreeze(t *testing.T) {
	m := New()
	if err := m.AddApplicant(1, []int{7}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.Run(); !errors.Is(err, ErrUnknownReference) {
		t.Fatalf("Run = %v, want ErrUnknownReference", err)
	}

	if err := m.AddProgram(7, 2, []int{1, 9}); err != nil {
		t.Fatalf("registry must stay unfrozen after failed Run: %v", err)
	}
	if _, _, err := m.Run(); !errors.Is(err, ErrUnknownReference) {
		t.Fatalf("Run = %v, want ErrUnknownReference", err)
	}

	if err := m.AddApplicant(9, []int{7}); err != nil {
		t.Fatal(err)
	}
	result, _, err := m.Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := map[int]int{1: 7, 9: 7}
	if !reflect.DeepEqual(result, want) {
		t.Fatalf("result = %v, want %v", result, want)
	}
}

// Verify reports its rejection reasons with the documented priority.
func TestVerifyErrorPriority(t *testing.T) {
	m := build(t,
		map[int][]int{1: {1}, 2: {1}},
		map[int]progSpec{1: {1, []int{1, 2}}, 2: {1, []int{2}}},
	)

	if _, _, err := m.Result(); !errors.Is(err, ErrNotRun) {
		t.Fatalf("Result before Run = %v, want ErrNotRun", err)
	}
	if _, err := m.Verify(map[int]int{}); !errors.Is(err, ErrNotRun) {
		t.Fatalf("Verify before Run = %v, want ErrNotRun", err)
	}
	mustRun(t, m)

	if _, err := m.Verify(map[int]int{99: 1}); !errors.Is(err, ErrUnknownApplicant) {
		t.Fatalf("Verify unknown applicant = %v, want ErrUnknownApplicant", err)
	}
	if _, err := m.Verify(map[int]int{1: 99}); !errors.Is(err, ErrUnknownProgram) {
		t.Fatalf("Verify unknown program = %v, want ErrUnknownProgram", err)
	}
	if _, err := m.Verify(map[int]int{99: 99}); !errors.Is(err, ErrUnknownApplicant) {
		t.Fatalf("unknown applicant beats unknown program: got %v", err)
	}
	if _, err := m.Verify(map[int]int{1: 2}); !errors.Is(err, ErrNotMutuallyAcceptable) {
		t.Fatalf("Verify unacceptable pair = %v, want ErrNotMutuallyAcceptable", err)
	}
	if _, err := m.Verify(map[int]int{1: 1, 2: 1}); !errors.Is(err, ErrOverCapacity) {
		t.Fatalf("Verify over capacity = %v, want ErrOverCapacity", err)
	}
	if _, err := m.Verify(map[int]int{1: 2, 2: 1, 3: 1}); !errors.Is(err, ErrUnknownApplicant) {
		t.Fatalf("unknown applicant beats unacceptable/over-capacity: got %v", err)
	}
}

// Concurrent registration, runs and queries behave as some serial order.
func TestConcurrentAccess(t *testing.T) {
	m := New()
	var wg sync.WaitGroup
	for i := 1; i <= 8; i++ {
		wg.Add(2)
		go func(id int) {
			defer wg.Done()
			_ = m.AddApplicant(id, []int{1, 2})
		}(i)
		go func(id int) {
			defer wg.Done()
			_ = m.AddProgram(id, 2, []int{1, 2, 3, 4, 5, 6, 7, 8})
		}(i)
	}
	wg.Wait()

	want, wantProposals := mustRun(t, m)

	const workers = 16
	errs := make(chan error, workers*3)
	for i := 0; i < workers; i++ {
		wg.Add(3)
		go func() {
			defer wg.Done()
			got, _, err := m.Run()
			if err != nil || !reflect.DeepEqual(got, want) {
				errs <- err
			}
		}()
		go func() {
			defer wg.Done()
			got, gotProposals, err := m.Result()
			if err != nil || !reflect.DeepEqual(got, want) || gotProposals != wantProposals {
				errs <- err
			}
		}()
		go func() {
			defer wg.Done()
			pairs, err := m.Verify(want)
			if err != nil || len(pairs) != 0 {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent access: %v", err)
		}
		t.Fatal("concurrent access returned inconsistent result")
	}
}
