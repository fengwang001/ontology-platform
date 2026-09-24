package cpt

import (
	"strconv"
	"testing"
)

// TestOutsideVisitCounter proves the interval is located directly by site
// (binary search) rather than by scanning the log: after appending m+1 entries
// and compacting the tiny interval [m,m+2) (only the last two entries), the
// number of OUTSIDE-interval surviving entries touched by that Compact must be
// bounded by a constant independent of m. skippedOutside is read here from
// inside package cpt only; it is unreachable from any exported API.
func TestOutsideVisitCounter(t *testing.T) {
	cases := []struct {
		name string
		m    int64
	}{
		{"m=100", 100},
		{"m=1000", 1000},
		{"m=10000", 10000},
	}
	const bound = 2 // constant independent of m
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			l := NewLog()
			for i := int64(0); i <= c.m; i++ {
				l.Append(key(i), int(i))
			}
			l.Compact(c.m, c.m+2) // folds only seq m and seq m+1
			if l.skippedOutside > bound {
				t.Fatalf("outside entries visited=%d, want <=%d (m=%d)",
					l.skippedOutside, bound, c.m)
			}
			if v, ok := l.Read(1, key(0)); !ok || v != 0 {
				t.Fatalf("Read(1,K0)=(%d,%v), want 0,true", v, ok)
			}
			if v, ok := l.Read(c.m-1, key(c.m-2)); !ok || v != int(c.m-2) {
				t.Fatalf("Read(m-1,Km-2)=(%d,%v), want %d,true", v, ok, c.m-2)
			}
			if v, ok := l.Read(c.m+1, key(c.m)); !ok || v != int(c.m) {
				t.Fatalf("Read(m+1,Km)=(%d,%v), want %d,true", v, ok, c.m)
			}
			l.Compact(c.m, c.m+2) // re-compact keeps the counter bounded
			if l.skippedOutside > bound {
				t.Fatalf("re-compact outside visited=%d, want <=%d",
					l.skippedOutside, bound)
			}
		})
	}
}

// TestReCompactSameInterval: compacting the same interval again must keep the
// folded value (the interval's last write per key) and stay addressable.
func TestReCompactSameInterval(t *testing.T) {
	l := NewLog()
	add := func(k string, v int) { l.Append(k, v) }
	add("K1", 10)
	add("K2", 20)
	add("K1", 30)
	add("K3", 40)
	add("K1", 50)
	add("K2", 60)
	add("K4", 70)
	for i := 0; i < 3; i++ {
		l.Compact(3, 7)
		for at := int64(3); at < 7; at++ {
			if v, ok := l.Read(at, "K1"); !ok || v != 50 {
				t.Fatalf("iter %d at=%d K1=(%d,%v) want 50", i, at, v, ok)
			}
			if v, ok := l.Read(at, "K2"); !ok || v != 60 {
				t.Fatalf("iter %d at=%d K2=(%d,%v) want 60", i, at, v, ok)
			}
			if v, ok := l.Read(at, "K3"); !ok || v != 40 {
				t.Fatalf("iter %d at=%d K3=(%d,%v) want 40", i, at, v, ok)
			}
		}
		if v, ok := l.Read(2, "K1"); !ok || v != 10 { // before lo untouched
			t.Fatalf("iter %d Read(2,K1)=(%d,%v) want 10", i, v, ok)
		}
		if v, ok := l.Read(7, "K4"); !ok || v != 70 { // seq7 outside
			t.Fatalf("iter %d Read(7,K4)=(%d,%v) want 70", i, v, ok)
		}
	}
}

func key(i int64) string { return "K" + strconv.FormatInt(i, 10) }
