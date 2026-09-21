package alloc

import (
	"errors"
	"math"
	"math/rand"
	"testing"
)

func TestSplitEven(t *testing.T) {
	got, err := Split(100, 4)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertSlicesEqual(t, got, []int64{25, 25, 25, 25})
}

func TestSplitRemainderFrontLoaded(t *testing.T) {
	got, err := Split(10, 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertSlicesEqual(t, got, []int64{4, 3, 3})
}

func TestSplitNegative(t *testing.T) {
	got, err := Split(-10, 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertSlicesEqual(t, got, []int64{-3, -3, -4})
}

func TestSplitConservationRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(31337))
	for iter := 0; iter < 1000; iter++ {
		amount := rng.Int63() - rng.Int63()
		n := 1 + rng.Intn(32)
		got, err := Split(amount, n)
		if err != nil {
			t.Fatalf("iter=%d: %v", iter, err)
		}
		if len(got) != n {
			t.Fatalf("iter=%d: length %d, want %d", iter, len(got), n)
		}
		var sum int64
		for _, v := range got {
			sum += v
		}
		if sum != amount {
			t.Fatalf("iter=%d amount=%d n=%d: sum=%d", iter, amount, n, sum)
		}
		// 任意两份之差不超过 1。
		min, max := got[0], got[0]
		for _, v := range got[1:] {
			if v < min {
				min = v
			}
			if v > max {
				max = v
			}
		}
		if max-min > 1 {
			t.Fatalf("amount=%d n=%d: spread %d, result=%v", amount, n, max-min, got)
		}
	}
}

func TestSplitExtremes(t *testing.T) {
	got, err := Split(math.MaxInt64, 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 2^62 与 2^62-1 之和恰为 MaxInt64，零头给索引 0。
	if got[0] != 4611686018427387904 || got[1] != 4611686018427387903 {
		t.Fatalf("got %v", got)
	}

	got, err = Split(math.MinInt64, 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertSlicesEqual(t, got, []int64{-4611686018427387904, -4611686018427387904})
}

func TestSplitInvalidParts(t *testing.T) {
	for _, n := range []int{0, -1, -100} {
		got, err := Split(10, n)
		if !errors.Is(err, ErrInvalidParts) {
			t.Fatalf("n=%d: got %v, want ErrInvalidParts", n, err)
		}
		if got != nil {
			t.Fatalf("n=%d: got %v, want nil", n, got)
		}
	}
}
