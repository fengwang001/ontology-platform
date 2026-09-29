package quantile

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"sort"
	"sync"
	"testing"
)

// logState prints the operation, centroid list, total count and the basis for
// the verdict, as required for auditability.
func logState(t *testing.T, op string, m *Maintainer, basis string) {
	t.Helper()
	cs := m.Centroids()
	parts := make([]string, len(cs))
	for i, c := range cs {
		parts[i] = fmt.Sprintf("(mean=%v,weight=%d)", c.Mean(), c.Weight())
	}
	t.Logf("op=%s total=%d centroids=%s basis=%s", op, m.Count(), parts, basis)
}

func mustNew(t *testing.T, budget int) *Maintainer {
	t.Helper()
	m, err := New(budget)
	if err != nil {
		t.Fatalf("New(%d) unexpected error: %v", budget, err)
	}
	return m
}

func mustAdd(t *testing.T, m *Maintainer, values ...float64) {
	t.Helper()
	for _, v := range values {
		if err := m.Add(v); err != nil {
			t.Fatalf("Add(%v) unexpected error: %v", v, err)
		}
	}
}

func centroidPairs(m *Maintainer) [][2]float64 {
	cs := m.Centroids()
	out := make([][2]float64, len(cs))
	for i, c := range cs {
		out[i] = [2]float64{c.Mean(), float64(c.Weight())}
	}
	return out
}

func TestInvalidBudget(t *testing.T) {
	for _, budget := range []int{0, -1, -100} {
		if _, err := New(budget); !errors.Is(err, ErrInvalidBudget) {
			t.Fatalf("New(%d) err=%v, want ErrInvalidBudget", budget, err)
		}
	}
}

func TestCompressionMergesMinimumWeightAdjacentPair(t *testing.T) {
	m := mustNew(t, 2)

	mustAdd(t, m, 1, 2, 3)
	// Three weight-1 centroids: all adjacent sums tie at 2, so the leftmost
	// pair (1,2) is merged into (1.5,2).
	want := [][2]float64{{1.5, 2}, {3, 1}}
	logState(t, "add 1,2,3", m, "leftmost minimum-sum pair (w1+w2=2)")
	if got := centroidPairs(m); !reflect.DeepEqual(got, want) {
		t.Fatalf("centroids=%v, want %v", got, want)
	}

	if err := m.Add(4); err != nil {
		t.Fatal(err)
	}
	// Pairs are (2+1)=3 and (1+1)=2: the minimum pair is the rightmost one,
	// proving selection is by weight sum rather than position.
	want = [][2]float64{{1.5, 2}, {3.5, 2}}
	logState(t, "add 4", m, "pair sums 3 vs 2; merge (3,4) with weighted mean 3.5")
	if got := centroidPairs(m); !reflect.DeepEqual(got, want) {
		t.Fatalf("centroids=%v, want %v", got, want)
	}

	if err := m.Add(5); err != nil {
		t.Fatal(err)
	}
	// Pair sums are 4 and 3: merge (3.5,2) with (5,1) into (3.5*2+5)/3.
	want = [][2]float64{{1.5, 2}, {4, 3}}
	logState(t, "add 5", m, "pair sums 4 vs 3; weighted mean (3.5*2+5*1)/3 = 4")
	if got := centroidPairs(m); !reflect.DeepEqual(got, want) {
		t.Fatalf("centroids=%v, want %v", got, want)
	}
	if err := m.Check(); err != nil {
		t.Fatalf("integrity check failed: %v", err)
	}
}

func TestQuantileInterpolationBetweenCentroids(t *testing.T) {
	// Layout (mean=1.5,w=2),(mean=3.5,w=2): anchors are ranks 0.5 and 2.5 on
	// the rank axis [0, N-1]=[0,3].
	m := mustNew(t, 2)
	mustAdd(t, m, 1, 2, 3, 4)
	logState(t, "add 1,2,3,4", m, "anchor ranks 0.5 and 2.5")

	cases := []struct {
		q     float64
		want  float64
		basis string
	}{
		{0, 1.5, "q=0 targets rank 0, clamped to first anchor"},
		{1.0 / 6.0, 1.5, "target rank 0.5 equals first anchor"},
		{0.5, 2.5, "target rank 1.5 sits midway between anchors 0.5 and 2.5"},
		{5.0 / 6.0, 3.5, "target rank 2.5 equals last anchor"},
		{1, 3.5, "q=1 targets rank 3, clamped to last anchor"},
	}
	for _, tc := range cases {
		got, err := m.Quantile(tc.q)
		if err != nil {
			t.Fatalf("Quantile(%v) unexpected error: %v", tc.q, err)
		}
		logState(t, fmt.Sprintf("quantile q=%v => %v", tc.q, got), m, tc.basis)
		if got != tc.want {
			t.Fatalf("Quantile(%v)=%v, want %v", tc.q, got, tc.want)
		}
	}
}

func TestRetractRebuildsFromExactSet(t *testing.T) {
	m := mustNew(t, 2)
	mustAdd(t, m, 1, 2, 3, 4, 5)
	logState(t, "add 1..5", m, "compressed state before retraction")

	if err := m.Retract(3); err != nil {
		t.Fatalf("Retract(3) unexpected error: %v", err)
	}

	// Reference maintainer that never observed 3.
	ref := mustNew(t, 2)
	mustAdd(t, ref, 1, 2, 4, 5)
	logState(t, "retract 3", m, "rebuilt from ascending exact values {1,2,4,5}")
	logState(t, "reference 1,2,4,5", ref, "freshly compressed reference")

	if m.Count() != 4 || ref.Count() != 4 {
		t.Fatalf("counts=%d,%d, want 4", m.Count(), ref.Count())
	}
	if got, want := centroidPairs(m), centroidPairs(ref); !reflect.DeepEqual(got, want) {
		t.Fatalf("rebuilt centroids=%v, want %v", got, want)
	}
	if err := m.Check(); err != nil {
		t.Fatalf("integrity check failed: %v", err)
	}

	// Retracting all values yields an empty maintainer that rejects queries.
	for _, v := range []float64{1, 2, 4, 5} {
		if err := m.Retract(v); err != nil {
			t.Fatalf("Retract(%v) unexpected error: %v", v, err)
		}
	}
	if _, err := m.Quantile(0.5); !errors.Is(err, ErrEmpty) {
		t.Fatalf("Quantile after drain err=%v, want ErrEmpty", err)
	}
}

func TestRetractMissingIsRejectedAndAtomic(t *testing.T) {
	m := mustNew(t, 4)
	mustAdd(t, m, 1, 1, 2)
	before := centroidPairs(m)

	if err := m.Retract(99); !errors.Is(err, ErrValueNotFound) {
		t.Fatalf("Retract(99) err=%v, want ErrValueNotFound", err)
	}
	if err := m.Retract(math.NaN()); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("Retract(NaN) err=%v, want ErrInvalidValue", err)
	}
	logState(t, "failed retract 99 and NaN", m, "state must be untouched after validation failure")
	if got := centroidPairs(m); !reflect.DeepEqual(got, before) {
		t.Fatalf("state changed after failed retract: %v vs %v", got, before)
	}
	if m.Count() != 3 {
		t.Fatalf("count=%d, want 3", m.Count())
	}
}

func TestMergeCountsAndImmutability(t *testing.T) {
	m := mustNew(t, 10)
	mustAdd(t, m, 1, 2, 3, 4, 5)
	other := mustNew(t, 10)
	mustAdd(t, other, 6, 7, 8, 9, 10)
	otherBefore := centroidPairs(other)

	if err := m.Merge(other); err != nil {
		t.Fatalf("Merge unexpected error: %v", err)
	}
	logState(t, "merge {1..5} with {6..10}", m, "count must equal 5+5 and means stay ascending")

	if m.Count() != 10 {
		t.Fatalf("merged count=%d, want 10", m.Count())
	}
	if other.Count() != 5 {
		t.Fatalf("other count=%d, want 5 (merged side must not change)", other.Count())
	}
	if got := centroidPairs(other); !reflect.DeepEqual(got, otherBefore) {
		t.Fatalf("other changed after merge: %v vs %v", got, otherBefore)
	}
	if err := m.Check(); err != nil {
		t.Fatalf("integrity check failed: %v", err)
	}

	// Merging compressed maintainers recompresses to m's budget.
	left := mustNew(t, 3)
	mustAdd(t, left, 1, 2, 3, 4, 5)
	right := mustNew(t, 3)
	mustAdd(t, right, 6, 7, 8, 9, 10)
	if err := left.Merge(right); err != nil {
		t.Fatalf("Merge unexpected error: %v", err)
	}
	logState(t, "merge two budget-3 maintainers", left, "combined then compressed to 3 centroids, count 10")
	if left.Count() != 10 || len(left.Centroids()) != 3 {
		t.Fatalf("got count=%d centroids=%d, want 10 and 3", left.Count(), len(left.Centroids()))
	}

	if err := left.Merge(nil); !errors.Is(err, ErrNilMaintainer) {
		t.Fatalf("Merge(nil) err=%v, want ErrNilMaintainer", err)
	}
	if err := left.Merge(left); err != nil {
		t.Fatalf("Merge(self) err=%v, want nil", err)
	}
	if left.Count() != 10 {
		t.Fatalf("self-merge altered count: %d", left.Count())
	}
}

func TestRejectionsAreDistinguishableAndAtomic(t *testing.T) {
	m := mustNew(t, 3)
	mustAdd(t, m, 1, 2)
	before := centroidPairs(m)

	if err := m.Add(math.NaN()); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("Add(NaN) err=%v, want ErrInvalidValue", err)
	}
	if err := m.Add(math.Inf(1)); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("Add(+Inf) err=%v, want ErrInvalidValue", err)
	}
	for _, q := range []float64{-0.01, 1.01, math.NaN()} {
		if _, err := m.Quantile(q); !errors.Is(err, ErrInvalidQ) {
			t.Fatalf("Quantile(%v) err=%v, want ErrInvalidQ", q, err)
		}
	}

	empty := mustNew(t, 2)
	if _, err := empty.Quantile(0.5); !errors.Is(err, ErrEmpty) {
		t.Fatalf("Quantile on empty err=%v, want ErrEmpty", err)
	}

	reasons := []error{ErrInvalidBudget, ErrEmpty, ErrInvalidQ, ErrValueNotFound, ErrInvalidValue, ErrNilMaintainer}
	for i := range reasons {
		for j := range reasons {
			if i != j && errors.Is(reasons[i], reasons[j]) {
				t.Fatalf("error reasons %v and %v must be distinguishable", reasons[i], reasons[j])
			}
		}
	}

	logState(t, "invalid add and quantile calls", m, "all calls rejected before mutation")
	if got := centroidPairs(m); !reflect.DeepEqual(got, before) {
		t.Fatalf("state changed after rejected calls: %v vs %v", got, before)
	}
	if m.Count() != 2 {
		t.Fatalf("count=%d, want 2", m.Count())
	}
}

func TestConcurrentReadsAreBitIdentical(t *testing.T) {
	m := mustNew(t, 16)
	lcg := uint64(0x12345678)
	for i := 0; i < 20000; i++ {
		lcg = lcg*6364136223846793005 + 1442695040888963407
		mustAdd(t, m, float64(lcg%100000)/13)
	}

	qs := []float64{0, 0.001, 0.25, 0.5, 0.75, 0.99, 1}
	refs := make([]uint64, len(qs))
	for i, q := range qs {
		v, err := m.Quantile(q)
		if err != nil {
			t.Fatal(err)
		}
		refs[i] = math.Float64bits(v)
	}

	var wg sync.WaitGroup
	for g := 0; g < 64; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for round := 0; round < 200; round++ {
				for i, q := range qs {
					v, err := m.Quantile(q)
					if err != nil {
						t.Errorf("Quantile(%v) error: %v", q, err)
						return
					}
					if bits := math.Float64bits(v); bits != refs[i] {
						t.Errorf("Quantile(%v)=%016x, want %016x (bitwise drift)", q, bits, refs[i])
						return
					}
				}
				if m.Count() != 20000 {
					t.Errorf("Count drift: %d", m.Count())
					return
				}
				if err := m.Check(); err != nil {
					t.Errorf("Check failed under concurrency: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	var maxWeight int64
	for _, c := range m.Centroids() {
		if c.Weight() > maxWeight {
			maxWeight = c.Weight()
		}
	}
	logState(t, "64 goroutines x 200 concurrent reads", m,
		fmt.Sprintf("every Quantile/Count/Check call returned identical bits; max centroid weight=%d", maxWeight))
}

func TestErrorBoundAgainstExactQuantiles(t *testing.T) {
	// Build both the approximate maintainer and the exact multiset.
	m := mustNew(t, 32)
	lcg := uint64(0xABCDEF)
	const n = 50000
	values := make([]float64, 0, n)
	for i := 0; i < n; i++ {
		lcg = lcg*2862933555777941757 + 3037000493
		// Bounded non-uniform distribution with deterministic spread.
		v := float64(lcg%1000) + math.Sin(float64(i)/97)*250
		if err := m.Add(v); err != nil {
			t.Fatal(err)
		}
		values = append(values, v)
	}
	sort.Float64s(values)

	var maxWeight int64
	for _, c := range m.Centroids() {
		if c.Weight() > maxWeight {
			maxWeight = c.Weight()
		}
	}

	// Rank-mass guarantee: the estimated answer may misclassify the CDF mass by
	// at most 2*wmax/N relative to the requested point (a safe envelope over
	// one centroid on each side; see quantile/README.md).
	rankBound := float64(2*maxWeight)/float64(n) + 1e-12
	worst := 0.0
	for k := 0; k <= 200; k++ {
		q := float64(k) / 200
		got, err := m.Quantile(q)
		if err != nil {
			t.Fatal(err)
		}
		exactMass := float64(sort.SearchFloat64s(values, got)) / float64(n)
		deviation := math.Abs(exactMass - q)
		if deviation > worst {
			worst = deviation
		}
		if deviation > rankBound {
			t.Fatalf("q=%v estimate=%v exact CDF mass=%v deviation=%v exceeds bound %v",
				q, got, exactMass, deviation, rankBound)
		}
	}

	// Value-domain spot check against nearest-rank exact quantiles: the
	// estimate must stay within the global data range trivially, and within a
	// local window derived from the rank bound.
	dataRange := values[n-1] - values[0]
	valueBound := rankBound * dataRange * 4 // generous envelope for non-uniform data
	for _, q := range []float64{0.1, 0.5, 0.9} {
		got, err := m.Quantile(q)
		if err != nil {
			t.Fatal(err)
		}
		idx := int(q * float64(n-1))
		if math.Abs(got-values[idx]) > valueBound {
			t.Fatalf("q=%v estimate=%v exact=%v gap=%v exceeds value bound %v",
				q, got, values[idx], math.Abs(got-values[idx]), valueBound)
		}
	}

	logState(t, fmt.Sprintf("exact audit over %d values, 201 quantile points", n), m,
		fmt.Sprintf("worst CDF-mass deviation=%.6f <= bound 2*wmax/N=%.6f (wmax=%d)", worst, rankBound, maxWeight))
	if err := m.Check(); err != nil {
		t.Fatalf("integrity check failed: %v", err)
	}
}
