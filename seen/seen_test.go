package seen

import "testing"

func TestObserveFloorAndDup(t *testing.T) {
	table := New(3)
	if !table.Observe(2, 1, 1) {
		t.Fatal("first sequence must be accepted")
	}
	if table.Observe(2, 1, 1) {
		t.Fatal("same sequence must be a duplicate")
	}
	if table.Floor(2, 1) != 1 {
		t.Fatalf("floor = %d, want 1", table.Floor(2, 1))
	}
	if !table.Observe(2, 1, 2) {
		t.Fatal("next sequence must be accepted")
	}
	if table.Floor(2, 1) != 2 {
		t.Fatalf("floor = %d, want 2", table.Floor(2, 1))
	}
}

func TestObserveSequenceGapPanics(t *testing.T) {
	table := New(3)
	defer func() {
		if recover() == nil {
			t.Fatal("sequence gap must panic")
		}
	}()
	table.Observe(1, 2, 3)
}
