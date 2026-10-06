package thinpool

import (
	"reflect"
	"testing"
)

func TestRunsetAddIdempotentAndMerged(t *testing.T) {
	r := newRunset()
	for _, v := range []int{3, 4, 5, 1, 2, 0} {
		if !r.add(v) {
			t.Fatalf("add(%d) should allocate", v)
		}
	}
	for _, v := range []int{0, 1, 2, 3, 4, 5} {
		if !r.contains(v) {
			t.Fatalf("missing %d", v)
		}
		if r.add(v) {
			t.Fatalf("readd(%d) should report already mapped", v)
		}
	}
	if r.contains(6) {
		t.Fatal("6 should be unmapped")
	}
	want := []interval{{0, 6}}
	if !reflect.DeepEqual(r.snapshot(), want) {
		t.Fatalf("runs=%v want %v", r.snapshot(), want)
	}
	if r.count() != 6 {
		t.Fatalf("count=%d", r.count())
	}
}

func TestRunsetReclaimShapes(t *testing.T) {
	// 映射 0..9，回收不同形状。
	cases := []struct {
		name    string
		initial []interval
		start   int
		length  int
		freed   int
		after   []interval
	}{
		{"zero length", []interval{{0, 10}}, 3, 0, 0, []interval{{0, 10}}},
		{"whole run", []interval{{0, 10}}, 0, 10, 10, []interval{}},
		{"left part", []interval{{0, 10}}, 0, 3, 3, []interval{{3, 10}}},
		{"right part", []interval{{0, 10}}, 7, 3, 3, []interval{{0, 7}}},
		{"middle split", []interval{{0, 10}}, 3, 4, 4, []interval{{0, 3}, {7, 10}}},
		{"unmapped holes", []interval{{0, 2}, {8, 10}}, 2, 6, 0, []interval{{0, 2}, {8, 10}}},
		{"cross runs", []interval{{0, 3}, {5, 8}}, 1, 6, 4, []interval{{0, 1}, {7, 8}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRunset()
			for _, in := range tc.initial {
				for v := in.start; v < in.end; v++ {
					if !r.add(v) {
						t.Fatalf("seed add %d", v)
					}
				}
			}
			if got := r.removeRange(tc.start, tc.length); got != tc.freed {
				t.Fatalf("freed=%d want %d", got, tc.freed)
			}
			if !reflect.DeepEqual(r.snapshot(), tc.after) {
				t.Fatalf("runs=%v want %v", r.snapshot(), tc.after)
			}
			wantCount := 0
			for _, in := range tc.after {
				wantCount += in.end - in.start
			}
			if r.count() != wantCount {
				t.Fatalf("count=%d want %d", r.count(), wantCount)
			}
			if got := r.countIn(tc.start, tc.start+tc.length+2); got < 0 {
				t.Fatal("countIn negative")
			}
		})
	}
}
