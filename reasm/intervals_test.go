package reasm

import (
	"reflect"
	"testing"
)

func TestIntervalsNormalizedAndMerged(t *testing.T) {
	r, _ := setup(100)
	submit := func(off int, s string) {
		t.Helper()
		if _, _, err := r.Submit("m", off, []byte(s), 25); err != nil {
			t.Fatalf("submit %q@%d: %v", s, off, err)
		}
	}
	submit(0, "aaaaaaaaaa")
	submit(5, "aaaaabbbbb") // partial overlap -> [0,15)
	submit(15, "bbbbb")     // adjacent -> [0,20)

	want := []Interval{{Start: 0, End: 20}}
	if got := r.Intervals("m"); !reflect.DeepEqual(got, want) {
		t.Fatalf("intervals %v, want %v", got, want)
	}
	if got := r.Intervals("missing"); got != nil {
		t.Fatalf("unknown id intervals %v", got)
	}
}
