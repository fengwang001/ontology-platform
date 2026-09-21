package alloc

import (
	"math/rand"
	"testing"
)

func TestDeterminismRepeated(t *testing.T) {
	amounts := []int64{100, 7, 1, -100, -7, -1, 0}
	weights := []int64{3, 1, 4, 1, 5, 9, 2, 6}
	var first []int64
	for round, amount := range amounts {
		got, err := Allocate(amount, weights)
		if err != nil {
			t.Fatalf("amount=%d: %v", amount, err)
		}
		got2, err := Allocate(amount, weights)
		if err != nil {
			t.Fatalf("amount=%d: %v", amount, err)
		}
		assertSlicesEqual(t, got, got2)
		if round == 0 {
			first = got
		}
	}
	if first == nil {
		t.Fatal("no cases ran")
	}
}

func TestTieBreakSmallestIndex(t *testing.T) {
	// 完全等权时，零头单位必须按索引从小到大分配。
	got, err := Allocate(10, []int64{1, 1, 1, 1, 1, 1, 1})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertSlicesEqual(t, got, []int64{2, 2, 2, 1, 1, 1, 1})
}

func TestTieBreakSmallestIndexNegative(t *testing.T) {
	// -10 等权 7 份：floor(-10/7) = -2，余 4，索引最小的四方 +1。
	got, err := Allocate(-10, []int64{1, 1, 1, 1, 1, 1, 1})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertSlicesEqual(t, got, []int64{-1, -1, -1, -1, -2, -2, -2})
}

func TestDeterminismNoMapOrderBias(t *testing.T) {
	// 大量不同输入下重复调用，结果必须逐字节一致。
	rng := rand.New(rand.NewSource(7))
	for iter := 0; iter < 1000; iter++ {
		n := 1 + rng.Intn(20)
		weights := make([]int64, n)
		for i := range weights {
			weights[i] = int64(rng.Intn(50))
		}
		amount := int64(rng.Intn(500) - 250)
		a, err := Allocate(amount, weights)
		if err != nil {
			continue
		}
		b, err := Allocate(amount, weights)
		if err != nil {
			t.Fatalf("second call errored: %v", err)
		}
		assertSlicesEqual(t, a, b)
	}
}
