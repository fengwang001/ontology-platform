package alloc

import (
	"errors"
	"math"
	"math/bits"
	"math/rand"
	"testing"
)

// wouldOverflow reports whether Allocate(amount, weights) must fail
// with ErrOverflow: any product |amount|*weights[i] exceeding int64.
func wouldOverflow(amount int64, weights []int64) bool {
	mag := uint64(amount)
	if amount < 0 {
		mag = 0 - mag
	}
	limit := uint64(math.MaxInt64)
	if amount < 0 {
		limit++
	}
	for _, w := range weights {
		hi, lo := bits.Mul64(mag, uint64(w))
		if hi != 0 || lo > limit {
			return true
		}
	}
	return false
}

func randomAmount(r *rand.Rand) int64 {
	switch r.Intn(4) {
	case 0:
		return r.Int63n(2001) - 1000 // small, lots of remainders
	case 1:
		return int64(r.Intn(1<<31)) - (1 << 30)
	case 2:
		return r.Int63()
	default:
		extremes := []int64{math.MaxInt64, math.MinInt64 + 1, -1, 0, 1}
		return extremes[r.Intn(len(extremes))]
	}
}

func TestAllocateConservationRandomized(t *testing.T) {
	r := rand.New(rand.NewSource(20260921))
	for iter := 0; iter < 20000; iter++ {
		n := 1 + r.Intn(12)
		weights := make([]int64, n)
		for i := range weights {
			// Mix zeros, small and large weights; keep the sum and
			// every product amount*weights[i] within int64.
			switch r.Intn(3) {
			case 0:
				weights[i] = 0
			case 1:
				weights[i] = r.Int63n(100)
			default:
				weights[i] = r.Int63n(1 << 40)
			}
		}
		var total int64
		for _, w := range weights {
			total += w
		}
		if total == 0 {
			weights[r.Intn(n)] = 1
		}
		amount := randomAmount(r)

		got, err := Allocate(amount, weights)
		if wouldOverflow(amount, weights) {
			if !errors.Is(err, ErrOverflow) {
				t.Fatalf("iter %d: expected ErrOverflow, got %v", iter, err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("iter %d: Allocate(%d, %v) error: %v", iter, amount, weights, err)
		}
		if len(got) != n {
			t.Fatalf("iter %d: result length %d, want %d", iter, len(got), n)
		}
		if sum(got) != amount {
			t.Fatalf("iter %d: conservation violated: Allocate(%d, %v)=%v sums to %d",
				iter, amount, weights, got, sum(got))
		}
		for i := range got {
			if weights[i] == 0 && got[i] != 0 {
				t.Fatalf("iter %d: zero weight got %d", iter, got[i])
			}
			for j := 0; j < n; j++ {
				if weights[i] > weights[j] && abs(got[i]) < abs(got[j]) {
					t.Fatalf("iter %d: monotonicity violated: %v weights %v",
						iter, got, weights)
				}
			}
		}
	}
}

func TestSplitConservationRandomized(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	for iter := 0; iter < 20000; iter++ {
		amount := randomAmount(r)
		n := 1 + r.Intn(100)
		got, err := Split(amount, n)
		if err != nil {
			t.Fatalf("iter %d: Split(%d, %d) error: %v", iter, amount, n, err)
		}
		if sum(got) != amount {
			t.Fatalf("iter %d: conservation violated: Split(%d, %d)=%v sums to %d",
				iter, amount, n, got, sum(got))
		}
	}
}
