package interval

import (
	"errors"
	"testing"
	"time"
)

func ts(n int64) time.Time { return time.Unix(n, 0).UTC() }

func TestNewAndContains(t *testing.T) {
	cases := []struct {
		name     string
		start    time.Time
		end      time.Time
		wantErr  error
		probe    time.Time
		contains bool
	}{
		{"finite", ts(10), ts(20), nil, ts(10), true},
		{"at end excluded", ts(10), ts(20), nil, ts(20), false},
		{"before start", ts(10), ts(20), nil, ts(9), false},
		{"open end contains far", ts(10), time.Time{}, nil, ts(1 << 30), true},
		{"empty equal ends", ts(10), ts(10), ErrEmptyInterval, time.Time{}, false},
		{"reversed", ts(20), ts(10), ErrEmptyInterval, time.Time{}, false},
		{"zero start", time.Time{}, ts(10), ErrEmptyInterval, time.Time{}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			iv, err := New(c.start, c.end)
			if !errors.Is(err, c.wantErr) {
				t.Fatalf("err = %v, want %v", err, c.wantErr)
			}
			if c.wantErr == nil && iv.Contains(c.probe) != c.contains {
				t.Fatalf("Contains(%v) = %v, want %v", c.probe.Unix(), !c.contains, c.contains)
			}
		})
	}
}

func TestOverlapsIntersectMinus(t *testing.T) {
	cases := []struct {
		name       string
		a, b       [2]int64 // 0 means +inf
		overlap    bool
		inter      [2]int64 // inter; {0,0} with !overlap unused
		residCount int
	}{
		{"partial", [2]int64{1, 5}, [2]int64{3, 7}, true, [2]int64{3, 5}, 1},
		{"touch", [2]int64{1, 5}, [2]int64{5, 9}, false, [2]int64{0, 0}, 1},
		{"contain", [2]int64{1, 9}, [2]int64{3, 5}, true, [2]int64{3, 5}, 2},
		{"disjoint", [2]int64{1, 3}, [2]int64{5, 7}, false, [2]int64{0, 0}, 1},
		{"openended", [2]int64{1, 0}, [2]int64{5, 7}, true, [2]int64{5, 7}, 2},
		{"cover open", [2]int64{3, 5}, [2]int64{1, 0}, true, [2]int64{3, 5}, 0},
	}
	mk := func(p [2]int64) Interval {
		var e time.Time
		if p[1] != 0 {
			e = ts(p[1])
		}
		iv, _ := New(ts(p[0]), e)
		return iv
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a, b := mk(c.a), mk(c.b)
			if a.Overlaps(b) != c.overlap {
				t.Fatalf("Overlaps = %v, want %v", !c.overlap, c.overlap)
			}
			if c.overlap {
				got, ok := a.Intersect(b)
				if !ok || !got.Start.Equal(ts(c.inter[0])) {
					t.Fatalf("intersect start = %v ok=%v", got.Start.Unix(), ok)
				}
				if (got.End.IsZero() && c.inter[1] != 0) ||
					(!got.End.IsZero() && got.End.Unix() != c.inter[1]) {
					t.Fatalf("intersect end = %v, want %v", got.End.Unix(), c.inter[1])
				}
			}
			if res := a.Minus(b); len(res) != c.residCount {
				t.Fatalf("minus residuals = %d, want %d", len(res), c.residCount)
			}
		})
	}
}
