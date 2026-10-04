package syncpt

import (
	"errors"
	"testing"
)

func TestSetTable(t *testing.T) {
	tests := []struct {
		name   string
		points []Point
		index  int
		err    error
		last   Point
	}{
		{name: "ordered", points: []Point{{10, 100}, {20, 200}}, index: 1, last: Point{20, 200}},
		{name: "unordered accepted", points: []Point{{20, 200}, {10, 100}}, index: 1, last: Point{20, 200}},
		{name: "duplicate", points: []Point{{10, 100}, {10, 101}}, index: 1, err: ErrDupSync},
		{name: "skew left", points: []Point{{10, 100}, {20, 100}}, index: 1, err: ErrSkew},
		{name: "skew right", points: []Point{{20, 200}, {10, 200}}, index: 1, err: ErrSkew},
		{name: "unordered skew both neighbors", points: []Point{{10, 100}, {30, 300}, {20, 301}}, index: 2, err: ErrSkew},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewSet()
			var err error
			for i, p := range tt.points {
				err = s.Add(p.K, p.W)
				if i != tt.index {
					if err != nil {
						t.Fatalf("Add(%d, %d): %v", p.K, p.W, err)
					}
				}
			}
			if !errors.Is(err, tt.err) {
				t.Fatalf("err = %v, want %v", err, tt.err)
			}
			if tt.err != nil {
				return
			}
			got, ok := s.Last()
			if !ok || got != tt.last {
				t.Fatalf("last = (%v,%v), want %v", got, ok, tt.last)
			}
			p, hasP := s.AtOrBelow(15)
			if !hasP || p.K != 10 {
				t.Fatalf("AtOrBelow = (%v,%v)", p, hasP)
			}
			n, hasN := s.AtOrAbove(15)
			if !hasN || n.K != 20 {
				t.Fatalf("AtOrAbove = (%v,%v)", n, hasN)
			}
			if exact, ok := s.AtOrBelow(20); !ok || exact.W != 200 {
				t.Fatalf("exact below = (%v,%v)", exact, ok)
			}
		})
	}
}
