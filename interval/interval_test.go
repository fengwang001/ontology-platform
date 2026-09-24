package interval

import (
	"errors"
	"testing"
)

func TestNewRejectsEmpty(t *testing.T) {
	cases := []struct {
		name       string
		start, end int64
		wantErr    bool
	}{
		{"normal", 0, 10, false},
		{"point is empty", 5, 5, true},
		{"reversed is empty", 10, 5, true},
		{"to infinity", 0, Infinity, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := New(c.start, c.end)
			if c.wantErr && !errors.Is(err, ErrEmpty) {
				t.Fatalf("want ErrEmpty, got %v", err)
			}
			if !c.wantErr && err != nil {
				t.Fatalf("unexpected err %v", err)
			}
		})
	}
}

func TestContainsBoundaries(t *testing.T) {
	iv, err := New(10, 20)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		at   int64
		want bool
	}{
		{9, false},
		{10, true}, // 起点命中
		{15, true},
		{20, false}, // 终点不命中
		{21, false},
	}
	for _, c := range cases {
		if got := iv.Contains(c.at); got != c.want {
			t.Errorf("Contains(%d)=%v, want %v", c.at, got, c.want)
		}
	}
}

func TestOverlaps(t *testing.T) {
	must := func(s, e int64) I {
		iv, err := New(s, e)
		if err != nil {
			t.Fatal(err)
		}
		return iv
	}
	cases := []struct {
		name string
		a, b I
		want bool
	}{
		{"disjoint before", must(0, 5), must(5, 10), false},
		{"disjoint after", must(10, 15), must(0, 10), false},
		{"partial", must(0, 6), must(5, 10), true},
		{"nested", must(0, 100), must(5, 10), true},
		{"same", must(0, 10), must(0, 10), true},
		{"infinity vs finite", must(0, Infinity), must(9, 10), true},
		{"infinity vs infinity", must(0, Infinity), must(100, Infinity), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.a.Overlaps(c.b); got != c.want {
				t.Errorf("Overlaps=%v, want %v", got, c.want)
			}
		})
	}
}

func TestInfinityComparesLikeFinite(t *testing.T) {
	iv, err := New(0, Infinity)
	if err != nil {
		t.Fatal(err)
	}
	for _, at := range []int64{0, 1, 1 << 40, Infinity - 1} {
		if !iv.Contains(at) {
			t.Errorf("Contains(%d)=false, want true", at)
		}
	}
	if iv.Contains(Infinity) {
		t.Error("Contains(Infinity)=true, want false (左闭右开)")
	}
}
