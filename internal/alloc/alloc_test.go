package alloc

import "testing"

func TestAllocateExact(t *testing.T) {
	got, err := Allocate(100, []int64{1, 1, 2})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []int64{25, 25, 50}
	assertSlicesEqual(t, got, want)
}

func TestAllocateLargestRemainder(t *testing.T) {
	// 10 按 1:1:1 分，每份 floor=3，余 1，索引最小者多得 1。
	got, err := Allocate(10, []int64{1, 1, 1})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertSlicesEqual(t, got, []int64{4, 3, 3})
}

func TestAllocateRemainderOrdering(t *testing.T) {
	// 权重 3,2,1，金额 10：保底 5,3,1，余 1；
	// 余数分别为 0,2,4（10*w mod 6），索引 2 最大。
	got, err := Allocate(10, []int64{3, 2, 1})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertSlicesEqual(t, got, []int64{5, 3, 2})
}

func TestAllocateNegativeFloor(t *testing.T) {
	// -7 按 1:1:1：floor(-7/3) = -3（不是截断的 -2），余 2。
	// 保底 -3,-3,-3（合计 -9），最小索引的两方各加 1。
	got, err := Allocate(-7, []int64{1, 1, 1})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertSlicesEqual(t, got, []int64{-2, -2, -3})
}

func TestAllocateNegativeLargestRemainder(t *testing.T) {
	// -10，权重 3,2,1：floor 保底 -5,-4,-2（合计 -11），余 1。
	// floorMod 分别为 0,4,2，欠账最多的索引 1 得到 +1。
	got, err := Allocate(-10, []int64{3, 2, 1})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertSlicesEqual(t, got, []int64{-5, -3, -2})
}

func TestAllocateZeroAmount(t *testing.T) {
	got, err := Allocate(0, []int64{1, 2, 3})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertSlicesEqual(t, got, []int64{0, 0, 0})
}

func TestAllocateSingleWeight(t *testing.T) {
	got, err := Allocate(12345, []int64{7})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertSlicesEqual(t, got, []int64{12345})
}

func assertSlicesEqual(t *testing.T, got, want []int64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("length mismatch: got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("index %d: got %v, want %v", i, got, want)
		}
	}
}
