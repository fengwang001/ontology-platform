package parking

import "testing"

func TestMinIDSetSparseConstantsLevels(t *testing.T) {
	set := newMinIDSet()
	ids := []int{1 << 35, (1 << 29) + 7, (1 << 13) + 9, 63, 1<<24 + 123}
	for _, id := range ids {
		set.Add(id)
	}
	for _, want := range []int{63, (1 << 13) + 9, 1<<24 + 123, (1 << 29) + 7, 1 << 35} {
		got, ok := set.Min()
		if !ok || got != want {
			t.Fatalf("Min = (%d,%v), want %d", got, ok, want)
		}
		set.Remove(want)
	}
	if _, ok := set.Min(); ok {
		t.Fatal("set should be empty")
	}
}
