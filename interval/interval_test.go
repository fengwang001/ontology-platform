package interval

import (
	"errors"
	"testing"
)

func TestNew(t *testing.T) {
	cases := []struct {
		name       string
		start, end int64
		wantErr    bool
	}{
		{"normal", 0, 10, false},
		{"forever end", 0, Forever, false},
		{"empty", 5, 5, true},
		{"inverted", 10, 5, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			iv, err := New(c.start, c.end)
			if c.wantErr {
				if !errors.Is(err, ErrEmpty) {
					t.Fatalf("want ErrEmpty, got %v", err)
				}
				return
			}
			if err != nil || !iv.OK() {
				t.Fatalf("want valid interval, got %v %v", iv, err)
			}
		})
	}
}

func TestContainsCutPoints(t *testing.T) {
	iv := Interval{Start: 10, End: 20}
	for tpoint := iv.Start - 1; tpoint <= iv.End+1; tpoint++ {
		want := tpoint >= iv.Start && tpoint < iv.End
		if got := iv.Contains(tpoint); got != want {
			t.Errorf("Contains(%d) = %v, want %v", tpoint, got, want)
		}
	}
	inf := Interval{Start: 0, End: Forever}
	for _, tpoint := range []int64{0, 1, 1 << 40, Forever - 1} {
		if !inf.Contains(tpoint) {
			t.Errorf("forever interval should contain %d", tpoint)
		}
	}
}

func TestOverlaps(t *testing.T) {
	cases := []struct {
		name string
		a, b Interval
		want bool
	}{
		{"partial", Interval{0, 10}, Interval{5, 15}, true},
		{"nested", Interval{0, 10}, Interval{2, 3}, true},
		{"touching", Interval{0, 10}, Interval{10, 20}, false},
		{"disjoint", Interval{0, 5}, Interval{10, 20}, false},
		{"both forever", Interval{0, Forever}, Interval{100, Forever}, true},
		{"finite vs forever", Interval{0, 50}, Interval{100, Forever}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.a.Overlaps(c.b); got != c.want {
				t.Errorf("Overlaps = %v, want %v", got, c.want)
			}
			if got := c.b.Overlaps(c.a); got != c.want {
				t.Errorf("Overlaps (reversed) = %v, want %v", got, c.want)
			}
		})
	}
}
