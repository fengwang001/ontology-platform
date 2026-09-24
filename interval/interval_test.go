package interval

import (
	"errors"
	"testing"
)

func TestNew(t *testing.T) {
	cases := []struct {
		name       string
		start, end int64
		wantErr    error
	}{
		{"normal", 1, 5, nil},
		{"empty rejected", 5, 5, ErrEmpty},
		{"inverted rejected", 9, 5, ErrEmpty},
		{"forever", 3, Infinity, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			iv, err := New(c.start, c.end)
			if !errors.Is(err, c.wantErr) {
				t.Fatalf("New(%d,%d) err=%v want %v", c.start, c.end, err, c.wantErr)
			}
			if err == nil && iv.Empty() {
				t.Fatalf("New(%d,%d) returned empty interval", c.start, c.end)
			}
		})
	}
}

func TestContainsCutPoints(t *testing.T) {
	iv, err := New(10, 20)
	if err != nil {
		t.Fatal(err)
	}
	for point := int64(0); point <= 30; point++ {
		want := point >= 10 && point < 20
		if got := iv.Contains(point); got != want {
			t.Errorf("Contains(%d)=%v want %v", point, got, want)
		}
	}
	if !iv.Contains(10) {
		t.Error("start point must be included")
	}
	if iv.Contains(20) {
		t.Error("end point must be excluded")
	}
}

func TestOverlaps(t *testing.T) {
	mk := func(s, e int64) Interval {
		iv, err := New(s, e)
		if err != nil {
			t.Fatal(err)
		}
		return iv
	}
	cases := []struct {
		name string
		a, b Interval
		want bool
	}{
		{"disjoint before", mk(0, 5), mk(5, 9), false},
		{"disjoint after", mk(10, 15), mk(0, 10), false},
		{"partial left", mk(0, 6), mk(4, 9), true},
		{"partial right", mk(4, 9), mk(0, 6), true},
		{"nested", mk(0, 10), mk(3, 4), true},
		{"identical", mk(2, 8), mk(2, 8), true},
		{"open ended vs finite", Forever(7), mk(0, 100), true},
		{"open ended vs ended before", Forever(7), mk(0, 7), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.a.Overlaps(c.b); got != c.want {
				t.Errorf("Overlaps=%v want %v", got, c.want)
			}
			if got := c.b.Overlaps(c.a); got != c.want {
				t.Errorf("Overlaps (swapped)=%v want %v", got, c.want)
			}
		})
	}
}

func TestInfinityBehavesLikeFinite(t *testing.T) {
	open := Forever(5)
	finite, err := New(5, Infinity-1)
	if err != nil {
		t.Fatal(err)
	}
	for point := int64(0); point < 100; point++ {
		if open.Contains(point) != finite.Contains(point) {
			t.Fatalf("Contains(%d) differs: open=%v finite=%v",
				point, open.Contains(point), finite.Contains(point))
		}
	}
	if !open.Contains(Infinity - 1) {
		t.Error("open-ended interval must contain arbitrarily large times")
	}
}
