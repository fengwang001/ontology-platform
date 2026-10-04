package yard

import (
	"reflect"
	"testing"
)

func TestRestartScanFromHeadInternal(t *testing.T) {
	s, err := NewScheduler(20, 30, 15, 60, 2)
	if err != nil {
		t.Fatal(err)
	}
	s.SeedFreeReefersForTest("R1", "R2")
	s.SeedDryWaiterForTest("X", 0, 0)
	s.SeedReeferWaiterForTest("Y", 1, 1)
	s.SeedDryWaiterForTest("Z", 2, 2)
	s.waitingReefer = 1

	got := s.DispatchForTest(3)
	want := []Assignment{
		{Truck: []byte("Y"), Dock: []byte("R1")},
		{Truck: []byte("X"), Dock: []byte("R2")},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got=%v want=%v", got, want)
	}
}
