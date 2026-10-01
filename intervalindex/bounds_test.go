package intervalindex

import (
	"math"
	"sync"
	"testing"
)

// log2Int returns ceil(log2(n)) with n >= 1.
func log2Int(n int) int {
	return int(math.Ceil(math.Log2(float64(n))))
}

// populateContiguous builds n intervals [2i, 2i+2): consecutive intervals are
// merely adjacent (they do not overlap each other). A point far to the right
// is a miss that a naive scan would still pay O(n) for.
func populateContiguous(t *testing.T, n int) *Index {
	t.Helper()
	idx := New()
	for i := 0; i < n; i++ {
		lo := int64(2 * i)
		if err := idx.Insert(int64(i+1), lo, lo+2); err != nil {
			t.Fatal(err)
		}
	}
	return idx
}

// logBound is the generous O(log n) ceiling used by the counter tests.
func logBound(n int) int64 { return int64(8*(log2Int(n)+1) + 64) }

// TestNoHitExaminedBounded compares the amount of interval records examined
// for a guaranteed miss at 1,000 vs 100,000 stored intervals. The count stays
// logarithmic instead of growing linearly, and stays bounded after deleting
// the interval with the current maximum hi.
func TestNoHitExaminedBounded(t *testing.T) {
	sizes := []int{1000, 100000}
	var counts []int64
	for _, n := range sizes {
		idx := populateContiguous(t, n)
		idx.resetExamined()
		x := int64(2*n + 10)
		if got := idx.Stab(x); len(got) != 0 {
			t.Fatalf("n=%d: miss Stab(%d) returned %v", n, x, got)
		}
		examined := idx.examinedCount()
		counts = append(counts, examined)
		t.Logf("no-hit Stab: n=%6d examined=%4d intervals (log2(n)~%d, bound=%d)",
			n, examined, log2Int(n), logBound(n))
		if examined > logBound(n) {
			t.Fatalf("n=%d examined %d exceeds O(log n) bound %d", n, examined, logBound(n))
		}
	}
	if counts[1] >= int64(sizes[1]/sizes[0])*counts[0] {
		t.Fatalf("examined grew linearly: n=%d->%d counts=%v", sizes[0], sizes[1], counts)
	}

	for _, n := range sizes {
		idx := populateContiguous(t, n)
		idx.resetExamined()
		got, err := idx.Overlap(int64(2*n+1), int64(2*n+5))
		if err != nil || len(got) != 0 {
			t.Fatalf("n=%d miss Overlap = %v, %v", n, got, err)
		}
		examined := idx.examinedCount()
		t.Logf("no-hit Overlap: n=%6d examined=%4d intervals (log2(n)~%d, bound=%d)",
			n, examined, log2Int(n), logBound(n))
		if examined > logBound(n) {
			t.Fatalf("n=%d overlap examined %d exceeds O(log n) bound %d", n, examined, logBound(n))
		}
	}

	const n = 100000
	idx := populateContiguous(t, n)
	maxID := int64(n)
	if err := idx.Remove(maxID); err != nil {
		t.Fatal(err)
	}
	if idx.Len() != n-1 {
		t.Fatalf("Len after max-hi remove = %d, want %d", idx.Len(), n-1)
	}
	idx.resetExamined()
	x := int64(2*n + 10)
	if got := idx.Stab(x); len(got) != 0 {
		t.Fatalf("post-remove miss Stab(%d) returned %v", x, got)
	}
	examined := idx.examinedCount()
	t.Logf("no-hit Stab after removing current max-hi id=%d: n=%d examined=%d intervals (log2(n)~%d, bound=%d)",
		maxID, n-1, examined, log2Int(n-1), logBound(n-1))
	if examined > logBound(n-1) {
		t.Fatalf("post-remove examined %d exceeds O(log n) bound %d", examined, logBound(n-1))
	}
}

// TestConcurrentExercisesLock hammers all operations concurrently; -race
// checks the locking discipline.
func TestConcurrentExercisesLock(t *testing.T) {
	idx := New()
	var wg sync.WaitGroup
	stop := make(chan struct{})

	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				id := seed*1_000_000 + int64(i%500) + 1
				lo := int64(i % 64)
				_ = idx.Insert(id, lo, lo+1)
				_ = idx.Stab(lo)
				_, _ = idx.Overlap(lo, lo+1)
				_ = idx.Remove(id)
				_ = idx.Len()
			}
		}(int64(w + 1))
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 3000; i++ {
			select {
			case <-stop:
				return
			default:
			}
			id := int64(i%20) + 1
			lo := int64(i % 10)
			_ = idx.Insert(id, lo, lo+5)
			_ = idx.Remove(id)
		}
	}()

	close(stop)
	wg.Wait()
}
