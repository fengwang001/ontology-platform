package splitdeque

import (
	"reflect"
	"testing"
)

func mustNew(t *testing.T, capacity, sharedLimit, privateReserve, freshness int) *SplitDeque {
	t.Helper()
	d, err := New(capacity, sharedLimit, privateReserve, freshness)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return d
}

func assertState(t *testing.T, d *SplitDeque, wantTop, wantSplit, wantBottom int64, wantRequested bool, wantAge, wantMissing int64) {
	t.Helper()
	got := []int64{d.top, d.split, d.bottom, d.age, d.missing}
	want := []int64{wantTop, wantSplit, wantBottom, wantAge, wantMissing}
	if !reflect.DeepEqual(got, want) || d.requested != wantRequested {
		t.Fatalf("state = top/split/bottom/age/missing %v requested=%v; want %v requested=%v",
			got, d.requested, want, wantRequested)
	}
}

func TestMainExample(t *testing.T) {
	d := mustNew(t, 8, 4, 1, 3)

	for value := int64(1); value <= 6; value++ {
		if err := d.Push(value); err != nil {
			t.Fatalf("Push(%d) error = %v", value, err)
		}
	}

	if got, err := d.Steal(2); err != nil || len(got) != 0 {
		t.Fatalf("Steal(2) = %v, %v; want empty, nil", got, err)
	}
	assertState(t, d, 0, 0, 6, true, 0, 2)

	if err := d.Push(7); err != nil {
		t.Fatalf("Push(7) error = %v", err)
	}
	assertState(t, d, 0, 3, 7, false, 0, 0)

	got, err := d.Steal(5)
	if err != nil || !reflect.DeepEqual(got, []int64{1, 2, 3}) {
		t.Fatalf("Steal(5) = %v, %v; want [1 2 3], nil", got, err)
	}
	assertState(t, d, 3, 3, 7, true, 0, 2)

	if value, ok := d.Pop(); !ok || value != 7 {
		t.Fatalf("Pop() = %d, %v; want 7, true", value, ok)
	}
	assertState(t, d, 3, 5, 6, false, 0, 0)

	if value, ok := d.Pop(); !ok || value != 6 {
		t.Fatalf("Pop() = %d, %v; want 6, true", value, ok)
	}
	if value, ok := d.Pop(); !ok || value != 5 {
		t.Fatalf("Pop() = %d, %v; want 5, true", value, ok)
	}
	if value, ok := d.Pop(); !ok || value != 4 {
		t.Fatalf("Pop() = %d, %v; want 4, true", value, ok)
	}
	if value, ok := d.Pop(); ok {
		t.Fatalf("Pop() = %d, true; want empty", value)
	}

	assertState(t, d, 3, 3, 3, false, 0, 0)
	if got := d.Stats(); got != (Stats{Pushed: 7, Popped: 4, Stolen: 3, FailedSteals: 2, Releases: 2, Reclaims: 2}) {
		t.Fatalf("Stats() = %+v", got)
	}
	if d.movedElements != 0 || d.copiedElements != 3 {
		t.Fatalf("moved/copied = %d/%d; want 0/3", d.movedElements, d.copiedElements)
	}
}

func TestStealRequestAndMissing(t *testing.T) {
	d := mustNew(t, 8, 4, 1, 3)
	for value := int64(1); value <= 6; value++ {
		if err := d.Push(value); err != nil {
			t.Fatal(err)
		}
	}

	if got, err := d.Steal(4); err != nil || len(got) != 0 {
		t.Fatalf("Steal(4) = %v, %v; want empty", got, err)
	}
	assertState(t, d, 0, 0, 6, true, 0, 4)

	if err := d.Push(7); err != nil {
		t.Fatal(err)
	}
	assertState(t, d, 0, 4, 7, false, 0, 0)

	got, err := d.Steal(4)
	if err != nil || !reflect.DeepEqual(got, []int64{1, 2, 3, 4}) {
		t.Fatalf("exact Steal(4) = %v, %v", got, err)
	}
	assertState(t, d, 4, 4, 7, false, 0, 0)

	if got, err := d.Steal(1); err != nil || len(got) != 0 {
		t.Fatalf("zero Steal(1) = %v, %v", got, err)
	}
	if got, err := d.Steal(3); err != nil || len(got) != 0 {
		t.Fatalf("pending Steal(3) = %v, %v", got, err)
	}
	assertState(t, d, 4, 4, 7, true, 0, 3)
}

func TestFreshnessAndEmptyPopAges(t *testing.T) {
	d := mustNew(t, 8, 4, 1, 3)
	if err := d.Push(1); err != nil {
		t.Fatal(err)
	}
	if got, err := d.Steal(1); err != nil || len(got) != 0 {
		t.Fatalf("Steal(1) = %v, %v", got, err)
	}

	if value, ok := d.Pop(); !ok || value != 1 {
		t.Fatalf("Pop() = %d, %v; want 1", value, ok)
	}
	assertState(t, d, 0, 0, 0, true, 1, 1)

	if _, ok := d.Pop(); ok {
		t.Fatal("Pop() unexpectedly succeeded")
	}
	assertState(t, d, 0, 0, 0, true, 2, 1)

	if _, ok := d.Pop(); ok {
		t.Fatal("Pop() unexpectedly succeeded")
	}
	assertState(t, d, 0, 0, 0, false, 0, 0)

	if err := d.Push(2); err != nil {
		t.Fatal(err)
	}
	if err := d.Push(3); err != nil {
		t.Fatal(err)
	}
	assertState(t, d, 0, 0, 2, false, 0, 0)
}

func TestReleaseOrderAndReserveLimit(t *testing.T) {
	push := func(d *SplitDeque, values ...int64) {
		t.Helper()
		for _, value := range values {
			if err := d.Push(value); err != nil {
				t.Fatal(err)
			}
		}
	}

	t.Run("push releases after append", func(t *testing.T) {
		d := mustNew(t, 8, 1, 1, 3)
		push(d, 1)
		if _, err := d.Steal(1); err != nil {
			t.Fatal(err)
		}
		if err := d.Push(2); err != nil {
			t.Fatal(err)
		}
		assertState(t, d, 0, 1, 2, false, 0, 0)
	})

	t.Run("pop releases before taking", func(t *testing.T) {
		d := mustNew(t, 8, 2, 1, 3)
		push(d, 1, 2)
		if _, err := d.Steal(1); err != nil {
			t.Fatal(err)
		}
		if value, ok := d.Pop(); !ok || value != 2 {
			t.Fatalf("Pop() = %d, %v", value, ok)
		}
		assertState(t, d, 0, 1, 1, false, 0, 0)
	})

	t.Run("one private item with reserve one cannot release", func(t *testing.T) {
		d := mustNew(t, 8, 4, 1, 3)
		push(d, 1)
		if _, err := d.Steal(1); err != nil {
			t.Fatal(err)
		}
		if _, ok := d.Pop(); !ok {
			t.Fatal("Pop() failed")
		}
		assertState(t, d, 0, 0, 0, true, 1, 1)
	})

	t.Run("missing cannot exceed shared room", func(t *testing.T) {
		d := mustNew(t, 10, 2, 0, 3)
		push(d, 1, 2, 3, 4, 5, 6)
		if _, err := d.Steal(5); err != nil {
			t.Fatal(err)
		}
		if err := d.Push(7); err != nil {
			t.Fatal(err)
		}
		assertState(t, d, 0, 2, 7, false, 0, 0)
	})

	t.Run("missing cannot exceed private reserve", func(t *testing.T) {
		d := mustNew(t, 10, 8, 6, 3)
		push(d, 1, 2, 3, 4, 5, 6, 7)
		if _, err := d.Steal(5); err != nil {
			t.Fatal(err)
		}
		if err := d.Push(8); err != nil {
			t.Fatal(err)
		}
		assertState(t, d, 0, 2, 8, false, 0, 0)
	})
}

func TestRejectedOperationsDoNotMutate(t *testing.T) {
	for _, tc := range [][4]int{
		{0, 1, 0, 1},
		{8, 0, 0, 1},
		{8, 9, 0, 1},
		{8, 1, -1, 1},
		{8, 1, 9, 1},
		{8, 1, 0, 0},
	} {
		if d, err := New(tc[0], tc[1], tc[2], tc[3]); err != ErrInvalidConfig || d != nil {
			t.Fatalf("New(%v) = %v, %v; want nil, ErrInvalidConfig", tc, d, err)
		}
	}

	d := mustNew(t, 2, 2, 0, 1)
	if err := d.Push(1); err != nil {
		t.Fatal(err)
	}
	if err := d.Push(2); err != nil {
		t.Fatal(err)
	}
	if err := d.Push(3); err != ErrFull {
		t.Fatalf("full Push error = %v; want ErrFull", err)
	}
	if _, err := d.Steal(0); err != ErrInvalidArgument {
		t.Fatalf("Steal(0) error = %v; want ErrInvalidArgument", err)
	}
	if _, err := d.Steal(3); err != ErrInvalidArgument {
		t.Fatalf("Steal(3) error = %v; want ErrInvalidArgument", err)
	}
	assertState(t, d, 0, 0, 2, false, 0, 0)
	if got := d.Stats(); got != (Stats{Pushed: 2}) {
		t.Fatalf("Stats() = %+v", got)
	}
}

func TestReclaimCeilingAndFifoLifoOrder(t *testing.T) {
	d, err := New(8, 4, 0, 3)
	if err != nil {
		t.Fatal(err)
	}
	for value := int64(1); value <= 4; value++ {
		if err := d.Push(value); err != nil {
			t.Fatal(err)
		}
	}
	d.split = 4
	d.bottom = 4

	if value, ok := d.Pop(); !ok || value != 4 {
		t.Fatalf("reclaim Pop() = %d, %v; want 4", value, ok)
	}
	if value, ok := d.Pop(); !ok || value != 3 {
		t.Fatalf("reclaimed private Pop() = %d, %v; want 3", value, ok)
	}
	got, err := d.Steal(2)
	if err != nil || !reflect.DeepEqual(got, []int64{1, 2}) {
		t.Fatalf("Steal(2) = %v, %v; want [1 2]", got, err)
	}
	if d.stats.Reclaims != 1 {
		t.Fatalf("Reclaims = %d; want 1", d.stats.Reclaims)
	}
}

func TestWraparoundFifoAndLifo(t *testing.T) {
	d := mustNew(t, 6, 4, 0, 1)
	for value := int64(1); value <= 6; value++ {
		if err := d.Push(value); err != nil {
			t.Fatal(err)
		}
	}
	d.split = 4
	got, err := d.Steal(4)
	if err != nil || !reflect.DeepEqual(got, []int64{1, 2, 3, 4}) {
		t.Fatalf("Steal(4) = %v, %v", got, err)
	}

	if got, err := d.Steal(2); err != nil || len(got) != 0 {
		t.Fatalf("wrapped failed Steal(2) = %v, %v; want empty", got, err)
	}

	if value, ok := d.Pop(); !ok || value != 6 {
		t.Fatalf("private Pop() = %d, %v; want 6", value, ok)
	}
	if value, ok := d.Pop(); !ok || value != 5 {
		t.Fatalf("private Pop() = %d, %v; want 5", value, ok)
	}

	for value := int64(7); value <= 10; value++ {
		if err := d.Push(value); err != nil {
			t.Fatal(err)
		}
	}

	got, err = d.Steal(2)
	if err != nil || len(got) != 0 {
		t.Fatalf("wrapped Steal(2) = %v, %v; want empty after expired request", got, err)
	}

	if value, ok := d.Pop(); !ok || value != 10 {
		t.Fatalf("wrapped LIFO Pop() = %d, %v; want 10", value, ok)
	}

	got, err = d.Steal(4)
	if err != nil || !reflect.DeepEqual(got, []int64{7, 8}) {
		t.Fatalf("wrapped final Steal(4) = %v, %v; want [7 8]", got, err)
	}
	assertState(t, d, 6, 6, 7, true, 0, 2)
}
