package topk

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"sync"
	"testing"
)

// dumpSequence prints the operation, the current canonical sequence and the
// stated reason so every test run is auditable.
func dumpSequence(t *testing.T, op, reason string, m *Maintainer) {
	t.Helper()
	seq := m.Snapshot()
	parts := make([]string, len(seq))
	for i, e := range seq {
		parts[i] = fmt.Sprintf("%d:{%s %g}", i+1, e.ID, e.Score)
	}
	t.Logf("op=%-22s count=%d seq=[%s] reason=%s", op, m.Count(), joinParts(parts), reason)
}

func joinParts(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += " "
		}
		out += p
	}
	return out
}

func mustUpsert(t *testing.T, m *Maintainer, id string, score float64) {
	t.Helper()
	if err := m.Upsert(id, score); err != nil {
		t.Fatalf("Upsert(%q,%g) unexpected error: %v", id, score, err)
	}
}

func elsToIDs(els []Element) []string {
	out := make([]string, len(els))
	for i, e := range els {
		out[i] = e.ID
	}
	return out
}

// naiveSort is the reference implementation: a full sort of the whole input
// using the documented ordering (score desc, id asc).
func naiveSort(in map[string]float64) []Element {
	out := make([]Element, 0, len(in))
	for id, score := range in {
		out = append(out, Element{ID: id, Score: score})
	}
	slices.SortFunc(out, func(a, b Element) int {
		return compareElement(a.ID, a.Score, b.ID, b.Score)
	})
	return out
}

func checkAgainstNaive(t *testing.T, m *Maintainer, want map[string]float64) {
	t.Helper()
	wantSeq := naiveSort(want)
	limit := min(m.k, len(wantSeq))
	got := m.Snapshot()
	if len(got) != len(wantSeq) {
		t.Fatalf("snapshot length = %d, want %d", len(got), len(wantSeq))
	}
	for i := range wantSeq {
		if got[i] != wantSeq[i] {
			t.Fatalf("position %d = {%s %g}, want {%s %g}",
				i, got[i].ID, got[i].Score, wantSeq[i].ID, wantSeq[i].Score)
		}
	}
	top, err := m.TopK(m.k)
	if err != nil {
		t.Fatalf("TopK error: %v", err)
	}
	if !slices.Equal(elsToIDs(top), elsToIDs(wantSeq[:limit])) {
		t.Fatalf("topK ids = %v, want %v", elsToIDs(top), elsToIDs(wantSeq[:limit]))
	}
	if count := m.Count(); count != len(wantSeq) {
		t.Fatalf("count = %d, want %d", count, len(wantSeq))
	}
}

func TestNewRejectsInvalidArguments(t *testing.T) {
	cases := []struct {
		name        string
		k, capacity int
		wantErr     error
	}{
		{"zero k", 0, 5, ErrNonPositiveK},
		{"negative k", -3, 5, ErrNonPositiveK},
		{"capacity below k", 3, 2, ErrKExceedsCap},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, err := New(tc.k, tc.capacity)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("New(%d,%d) err = %v, want %v", tc.k, tc.capacity, err, tc.wantErr)
			}
			if m != nil {
				t.Fatalf("New(%d,%d) returned non-nil maintainer on error", tc.k, tc.capacity)
			}
		})
	}
}

func TestTieBreakScoreThenID(t *testing.T) {
	m, err := New(5, 10)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]float64{}

	mustUpsert(t, m, "d", 10)
	want["d"] = 10
	dumpSequence(t, "upsert d=10", "single element", m)

	mustUpsert(t, m, "b", 10)
	want["b"] = 10
	dumpSequence(t, "upsert b=10", "same score: b<d so b precedes d", m)

	mustUpsert(t, m, "a", 10)
	want["a"] = 10
	mustUpsert(t, m, "c", 10)
	want["c"] = 10
	dumpSequence(t, "upsert a,c=10", "tie resolved by id asc: a b c d", m)

	mustUpsert(t, m, "e", 20)
	want["e"] = 20
	dumpSequence(t, "upsert e=20", "higher score jumps ahead of the tie group", m)

	checkAgainstNaive(t, m, want)

	top, err := m.TopK(3)
	if err != nil {
		t.Fatal(err)
	}
	if got := elsToIDs(top); !slices.Equal(got, []string{"e", "a", "b"}) {
		t.Fatalf("TopK(3) = %v, want [e a b]", got)
	}
	t.Logf("judgement: TopK(3) is prefix [e a b] of the canonical sequence")
}

func TestThresholdTieKeepsSmallerID(t *testing.T) {
	// k=3, four elements share one score: the three smallest ids are in
	// view; the largest id is the element just beyond the threshold.
	m, _ := New(3, 5)
	for _, id := range []string{"zeta", "alfa", "mike", "bravo"} {
		mustUpsert(t, m, id, 7)
	}
	dumpSequence(t, "upsert 4 tied at 7", "threshold tie: alfa bravo mike in view, zeta waits outside", m)

	top, _ := m.TopK(3)
	if got := elsToIDs(top); !slices.Equal(got, []string{"alfa", "bravo", "mike"}) {
		t.Fatalf("topK = %v, want [alfa bravo mike]", got)
	}
	if m.Count() != 4 {
		t.Fatalf("count = %d, want 4 (threshold element stays active)", m.Count())
	}

	m.Withdraw("bravo")
	dumpSequence(t, "withdraw bravo", "zeta is best beyond threshold and refills", m)
	top, _ = m.TopK(3)
	if got := elsToIDs(top); !slices.Equal(got, []string{"alfa", "mike", "zeta"}) {
		t.Fatalf("topK after refill = %v, want [alfa mike zeta]", got)
	}
}

func TestWithdrawRefillAndIdempotency(t *testing.T) {
	m, _ := New(2, 8)
	want := map[string]float64{}
	for _, e := range []Element{
		{ID: "x1", Score: 100},
		{ID: "x2", Score: 90},
		{ID: "x3", Score: 80},
		{ID: "x4", Score: 70},
	} {
		mustUpsert(t, m, e.ID, e.Score)
		want[e.ID] = e.Score
	}
	dumpSequence(t, "upsert x1..x4", "view holds x1 x2; x3 x4 wait outside", m)

	top, _ := m.TopK(2)
	if got := elsToIDs(top); !slices.Equal(got, []string{"x1", "x2"}) {
		t.Fatalf("topK = %v, want [x1 x2]", got)
	}

	m.Withdraw("x1")
	delete(want, "x1")
	dumpSequence(t, "withdraw x1", "best outside-view element x3 refills", m)
	checkAgainstNaive(t, m, want)

	top, _ = m.TopK(2)
	if got := elsToIDs(top); !slices.Equal(got, []string{"x2", "x3"}) {
		t.Fatalf("topK = %v, want [x2 x3]", got)
	}

	before := m.Snapshot()
	m.Withdraw("x1")
	m.Withdraw("never-existed")
	m.Withdraw("")
	dumpSequence(t, "withdraw x1/unknown/empty", "idempotent no-op: sequence unchanged", m)
	if !slices.Equal(m.Snapshot(), before) {
		t.Fatal("sequence changed after idempotent withdrawals")
	}

	m.Withdraw("x4")
	delete(want, "x4")
	dumpSequence(t, "withdraw x4 (outside view)", "visible top stays x2 x3", m)
	top, _ = m.TopK(2)
	if got := elsToIDs(top); !slices.Equal(got, []string{"x2", "x3"}) {
		t.Fatalf("topK after outside withdraw = %v, want [x2 x3]", got)
	}
}

func TestOverwriteReranks(t *testing.T) {
	m, _ := New(3, 6)
	want := map[string]float64{}
	for _, e := range []Element{
		{ID: "p", Score: 1},
		{ID: "q", Score: 2},
		{ID: "r", Score: 3},
		{ID: "s", Score: 4},
	} {
		mustUpsert(t, m, e.ID, e.Score)
		want[e.ID] = e.Score
	}
	dumpSequence(t, "upsert p1 q2 r3 s4", "canonical order s r q p; view s r q", m)

	mustUpsert(t, m, "s", 0)
	want["s"] = 0
	dumpSequence(t, "overwrite s=0", "s drops to tail; p moves into view", m)
	checkAgainstNaive(t, m, want)

	mustUpsert(t, m, "p", 99)
	want["p"] = 99
	dumpSequence(t, "overwrite p=99", "p jumps to head", m)
	checkAgainstNaive(t, m, want)

	mustUpsert(t, m, "q", 2)
	dumpSequence(t, "overwrite q=2", "same score keeps order", m)
	checkAgainstNaive(t, m, want)
}

func TestCapacityBoundaryAndAtomicFailure(t *testing.T) {
	m, _ := New(2, 3)
	mustUpsert(t, m, "a", 1)
	mustUpsert(t, m, "b", 2)
	mustUpsert(t, m, "c", 3)
	dumpSequence(t, "upsert a1 b2 c3", "capacity full at 3 active elements", m)

	cases := []struct {
		id      string
		score   float64
		wantErr error
		reason  string
	}{
		{"d", 99, ErrCapacityFull, "new id while full rejected even with winning score"},
		{"", 99, ErrEmptyID, "empty id rejected before touching capacity"},
		{"d", math.NaN(), ErrInvalidScore, "NaN score rejected"},
		{"d", math.Inf(1), ErrInvalidScore, "+Inf score rejected"},
	}
	snapshotBefore := m.Snapshot()
	countBefore := m.Count()
	for _, tc := range cases {
		err := m.Upsert(tc.id, tc.score)
		if !errors.Is(err, tc.wantErr) {
			t.Fatalf("Upsert(%q,%g) err = %v, want %v", tc.id, tc.score, err, tc.wantErr)
		}
		t.Logf("op=upsert-fail id=%q score=%g err=%v reason=%s", tc.id, tc.score, err, tc.reason)
	}
	if m.Count() != countBefore || !slices.Equal(m.Snapshot(), snapshotBefore) {
		t.Fatalf("state changed after failed upserts: before=%v after=%v", snapshotBefore, m.Snapshot())
	}

	if err := m.Upsert("a", 100); err != nil {
		t.Fatalf("overwrite at capacity failed: %v", err)
	}
	dumpSequence(t, "overwrite a=100", "existing id overwrite allowed while full", m)

	m.Withdraw("b")
	dumpSequence(t, "withdraw b", "one capacity slot released", m)
	if err := m.Upsert("d", 50); err != nil {
		t.Fatalf("upsert after withdraw failed: %v", err)
	}
	dumpSequence(t, "upsert d=50", "new id accepted in freed slot", m)
}

func TestTopKPrefixAndShortage(t *testing.T) {
	m, _ := New(5, 10)
	for _, e := range []Element{
		{ID: "one", Score: 5},
		{ID: "two", Score: 5},
		{ID: "three", Score: 9},
	} {
		mustUpsert(t, m, e.ID, e.Score)
	}
	full, _ := m.TopK(5)
	if len(full) != 3 {
		t.Fatalf("shortage: got %d elements, want all 3", len(full))
	}
	for n := 1; n <= 5; n++ {
		got, err := m.TopK(n)
		if err != nil {
			t.Fatalf("TopK(%d): %v", n, err)
		}
		limit := min(n, len(full))
		if !slices.Equal(got, full[:limit]) {
			t.Fatalf("TopK(%d) = %v, want prefix %v", n, got, full[:limit])
		}
		t.Logf("op=TopK(%d) prefix-ok ids=%v", n, elsToIDs(got))
	}
	if _, err := m.TopK(0); !errors.Is(err, ErrNonPositiveK) {
		t.Fatalf("TopK(0) err = %v, want %v", err, ErrNonPositiveK)
	}
	if _, err := m.TopK(6); !errors.Is(err, ErrKExceedsLimit) {
		t.Fatalf("TopK(6) err = %v, want %v", err, ErrKExceedsLimit)
	}
}

func TestConcurrentReadsAreElementwiseIdentical(t *testing.T) {
	m, _ := New(8, 64)
	for i := range 40 {
		id := fmt.Sprintf("id-%02d", i)
		mustUpsert(t, m, id, float64((i*7)%13))
	}

	const readers = 16
	var wg sync.WaitGroup
	results := make([][]Element, readers)
	counts := make([]int, readers)
	wg.Add(readers)
	for r := range readers {
		go func(idx int) {
			defer wg.Done()
			results[idx], _ = m.TopK(8)
			counts[idx] = m.Count()
		}(r)
	}
	wg.Wait()

	for r := 1; r < readers; r++ {
		if counts[r] != counts[0] {
			t.Fatalf("reader %d count = %d, want %d", r, counts[r], counts[0])
		}
		if !slices.EqualFunc(results[r], results[0], func(a, b Element) bool {
			return a == b
		}) {
			t.Fatalf("reader %d saw %v, reader 0 saw %v", r, results[r], results[0])
		}
	}
	t.Logf("op=%d concurrent readers all saw identical topK=%v count=%d",
		readers, elsToIDs(results[0]), counts[0])
}

// TestRandomizedAgainstNaive drives a randomized operation mix and compares
// the maintainer against a plain map + full sort after every step.
func TestRandomizedAgainstNaive(t *testing.T) {
	const k, cap = 4, 12
	m, _ := New(k, cap)
	ref := make(map[string]float64)

	rng := newStepRNG(20260929)
	for step := range 300 {
		roll := rng.intn(10)
		id := fmt.Sprintf("e%02d", rng.intn(cap+2))
		switch {
		case roll < 7:
			score := float64(rng.intn(6))
			_, existed := ref[id]
			err := m.Upsert(id, score)
			if !existed && len(ref) >= cap {
				if !errors.Is(err, ErrCapacityFull) {
					t.Fatalf("step %d: want ErrCapacityFull, got %v", step, err)
				}
				break
			}
			if err != nil {
				t.Fatalf("step %d: unexpected upsert error: %v", step, err)
			}
			ref[id] = score
		default:
			m.Withdraw(id)
			delete(ref, id)
		}

		wantSeq := naiveSort(ref)
		got := m.Snapshot()
		if len(got) != len(wantSeq) {
			t.Fatalf("step %d: len = %d, want %d", step, len(got), len(wantSeq))
		}
		for i := range wantSeq {
			if got[i] != wantSeq[i] {
				t.Fatalf("step %d: pos %d = {%s %g}, want {%s %g}",
					step, i, got[i].ID, got[i].Score, wantSeq[i].ID, wantSeq[i].Score)
			}
		}
		for n := 1; n <= k; n++ {
			top, err := m.TopK(n)
			if err != nil {
				t.Fatalf("step %d: TopK(%d): %v", step, n, err)
			}
			limit := min(n, len(wantSeq))
			if !slices.Equal(elsToIDs(top), elsToIDs(wantSeq[:limit])) {
				t.Fatalf("step %d: TopK(%d) = %v, want %v",
					step, n, elsToIDs(top), elsToIDs(wantSeq[:limit]))
			}
		}
	}
	t.Logf("randomized: 300 steps matched naive full-sort reference; final seq=%v",
		elsToIDs(m.Snapshot()))
}

// stepRNG is a tiny deterministic LCG so failures are reproducible.
type stepRNG struct{ state uint64 }

func newStepRNG(seed uint64) *stepRNG { return &stepRNG{state: seed} }

func (r *stepRNG) intn(n int) int {
	r.state = r.state*6364136223846793005 + 1442695040888963407
	return int((r.state >> 33) % uint64(n))
}
