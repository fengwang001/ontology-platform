package span

import (
	"math"
	"testing"
)

func TestMapCases(t *testing.T) {
	tests := []struct {
		name string
		segs []Segment
		orig int
		out  int
	}{
		{"identity", nil, 3, 3},
		{"single", []Segment{{1, 2, 1}}, 3, 2},
		{"merged", []Segment{{1, 2, 1}}, 4, 2},
		{"multiple", []Segment{{1, 2, 1}, {4, 6, 3}}, 7, 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewMap(tt.orig, tt.out)
			for _, seg := range tt.segs {
				if err := m.Delete(seg.OrigStart, seg.OrigEnd, seg.OutStart); err != nil {
					t.Fatal(err)
				}
			}
			if tt.name == "merged" {
				if err := m.Delete(2, 3, 1); err != nil {
					t.Fatal(err)
				}
			}
			if got := m.SegmentCount(); got != len(tt.segs) {
				t.Fatalf("segments = %d, want %d", got, len(tt.segs))
			}
			for out := 0; out <= tt.out; out++ {
				orig, err := m.ToOrig(out)
				if err != nil || orig < 0 || orig > tt.orig {
					t.Fatalf("ToOrig(%d)=%d,%v", out, orig, err)
				}
				back, err := m.ToOut(orig)
				if err != nil || back != out {
					t.Fatalf("ToOut(ToOrig(%d))=%d,%v", out, back, err)
				}
			}
			prevOut := -1
			prevOrig := -1
			for orig := 0; orig <= tt.orig; orig++ {
				out, err := m.ToOut(orig)
				if err != nil || out < prevOut || out < 0 || out > tt.out {
					t.Fatalf("ToOut(%d)=%d,%v", orig, out, err)
				}
				prevOut = out
			}
			for out := 0; out <= tt.out; out++ {
				orig, _ := m.ToOrig(out)
				if orig < prevOrig {
					t.Fatalf("ToOrig not monotone at %d", out)
				}
				prevOrig = orig
			}
		})
	}
}

func TestBinarySearchBound(t *testing.T) {
	const deletions = 100000
	m := NewMap(deletions*3, deletions*2)
	for i := 0; i < deletions; i++ {
		if err := m.Delete(i*3+1, i*3+2, i*2+1); err != nil {
			t.Fatal(err)
		}
	}
	limit := 2*math.Log2(float64(deletions)) + 4
	for _, out := range []int{0, 1, m.OutLen() / 2, m.OutLen()} {
		m.lastScan = 0
		if _, err := m.ToOrig(out); err != nil {
			t.Fatal(err)
		}
		if float64(m.LastScan()) > limit {
			t.Fatalf("ToOrig scanned %d > %v", m.LastScan(), limit)
		}
	}
	for _, orig := range []int{0, 1, m.OrigLen() / 2, m.OrigLen()} {
		m.lastScan = 0
		if _, err := m.ToOut(orig); err != nil {
			t.Fatal(err)
		}
		if float64(m.LastScan()) > limit {
			t.Fatalf("ToOut scanned %d > %v", m.LastScan(), limit)
		}
	}
}
