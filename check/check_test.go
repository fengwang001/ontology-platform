package check

import (
	"errors"
	"math"
	"sort"
	"sync"
	"testing"

	"ontology/arr"
	"ontology/cnt"
)

func TestKnownCounts(t *testing.T) {
	cases := []struct {
		name string
		in   []int
		want int64
	}{
		{"nil", nil, 0},
		{"empty", []int{}, 0},
		{"single", []int{7}, 0},
		{"pinned example", []int{2, 4, 1, 3, 5}, 3},
		{"sorted ascending", []int{1, 2, 3, 4, 5}, 0},
		{"fully descending", []int{5, 4, 3, 2, 1}, 10},
		{"all equal is stable", []int{3, 3, 3, 3}, 0},
		{"ties do not invert", []int{2, 2, 1, 1}, 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := cnt.CountInversions(tc.in)
			if err != nil || got != tc.want {
				t.Fatalf("CountInversions(%v) = (%d, %v), want %d", tc.in, got, err, tc.want)
			}
		})
	}
}

func TestMatchesNaive(t *testing.T) {
	cases := []struct {
		name string
		in   []int
	}{
		{"small reversed", []int{4, 3, 2, 1}},
		{"with ties", []int{3, 1, 3, 1, 2}},
		{"sorted", []int{1, 2, 3, 4, 5, 6}},
		{"two elements", []int{2, 1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !Agrees(tc.in) {
				t.Fatalf("cnt disagrees with naive on %v (naive=%d)", tc.in, NaiveCount(tc.in))
			}
		})
	}
}

// wrongCount 内联事故中的错误实现：取右半元素时累加「右半已取走数量」，
// 而不是「左半剩余数量」。
func wrongCount(input []int) int64 {
	work := append([]int(nil), input...)
	aux := make([]int, len(work))
	var wrong func(lo, hi int) int64
	wrong = func(lo, hi int) int64 {
		if lo >= hi {
			return 0
		}
		mid := (lo + hi) / 2
		total := wrong(lo, mid) + wrong(mid+1, hi)
		i, j, k, takenRight := lo, mid+1, lo, 0
		for i <= mid && j <= hi {
			if work[i] <= work[j] {
				aux[k], i = work[i], i+1
			} else {
				total += int64(takenRight)
				aux[k], j, takenRight = work[j], j+1, takenRight+1
			}
			k++
		}
		for i <= mid {
			aux[k], i, k = work[i], i+1, k+1
		}
		for j <= hi {
			aux[k], j, k = work[j], j+1, k+1
		}
		copy(work[lo:hi+1], aux[lo:hi+1])
		return total
	}
	if len(work) == 0 {
		return 0
	}
	return wrong(0, len(work)-1)
}

func TestWrongImplIsActuallyWrong(t *testing.T) {
	in := []int{2, 4, 1, 3, 5}
	correct, err := cnt.CountInversions(in)
	if err != nil {
		t.Fatal(err)
	}
	wrong := wrongCount(in)
	if correct != 3 {
		t.Fatalf("correct count = %d, want 3", correct)
	}
	if wrong == correct {
		t.Fatalf("wrong impl unexpectedly agrees: both %d", wrong)
	}
	if wrong != 0 {
		t.Fatalf("wrong impl = %d, want pinned wrong value 0", wrong)
	}
}

func TestStableMerge(t *testing.T) {
	type item struct {
		key int
		id  int
	}
	in := []item{{2, 1}, {2, 2}, {1, 3}, {2, 4}, {1, 5}}
	keys := make([]int, len(in))
	for i, it := range in {
		keys[i] = it.key
	}
	idsByKey := func(s []item) map[int][]int {
		m := map[int][]int{}
		for _, it := range s {
			m[it.key] = append(m[it.key], it.id)
		}
		return m
	}
	sorted := append([]item(nil), in...)
	sort.SliceStable(sorted, func(a, b int) bool { return sorted[a].key < sorted[b].key })
	got, err := cnt.CountInversions(keys)
	if err != nil || got != 5 {
		t.Fatalf("count = (%d, %v), want 5", got, err)
	}
	for key, ids := range idsByKey(sorted) {
		want := idsByKey(in)[key]
		if len(ids) != len(want) {
			t.Fatalf("key %d group changed", key)
		}
		for i := range ids {
			if ids[i] != want[i] {
				t.Fatalf("merge unstable for key %d: %v, want %v", key, ids, want)
			}
		}
	}
}

func TestComparisonBound(t *testing.T) {
	const n = 100_000
	in := make([]int, n)
	for i := range in {
		in[i] = n - i
	}
	_, comparisons, err := cnt.CountInversionsWithStats(in)
	if err != nil {
		t.Fatal(err)
	}
	bound := float64(n)*math.Log2(float64(n)) + float64(n)
	if float64(comparisons) > bound {
		t.Fatalf("comparisons %d exceed n*log2(n)+n = %.0f", comparisons, bound)
	}
}

func TestSentinelErrors(t *testing.T) {
	long := make([]int, arr.MaxLen+1)
	cases := []struct {
		name string
		in   []int
		want error
	}{
		{"nil rejected", nil, arr.ErrNilSlice},
		{"too long rejected", long, arr.ErrTooLong},
		{"negative rejected", []int{1, -2, 3}, arr.ErrNegativeValue},
		{"empty accepted", []int{}, nil},
		{"valid accepted", []int{3, 1, 2}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := arr.Count(tc.in)
			if tc.want == nil {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want errors.Is %v", err, tc.want)
			}
		})
	}
}

func TestConcurrentPure(t *testing.T) {
	const goroutines = 32
	in := []int{2, 4, 1, 3, 5}
	var wg sync.WaitGroup
	errs := make(chan error, goroutines)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := cnt.CountInversions(in)
			if err != nil || got != 3 {
				errs <- errors.New("bad concurrent result")
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}
