package bf

import (
	"errors"
	"testing"
)

func TestNewInvalidM(t *testing.T) {
	for _, m := range []int{0, -1, -8} {
		b, err := New(m)
		if b != nil || !errors.Is(err, ErrInvalidM) {
			t.Fatalf("New(%d) = (%v,%v), want nil,ErrInvalidM", m, b, err)
		}
	}
	if b, err := New(1); err != nil || b == nil {
		t.Fatalf("New(1) = (%v,%v), want non-nil,nil", b, err)
	}
}

func TestAddPositionsAndContains(t *testing.T) {
	cases := []struct {
		key  string
		bits [2]int // 规则：x=字节和，h1=x mod m，h2=(5x+3) mod m，m=8
	}{
		{"a", [2]int{0, 1}}, {"b", [2]int{2, 5}}, {"c", [2]int{2, 3}},
		{"d", [2]int{4, 7}}, {"i", [2]int{0, 1}}, {"f", [2]int{1, 6}},
		{"ab", [2]int{3, 2}}, // 195 mod 8 = 3，978 mod 8 = 2
	}
	for _, c := range cases {
		b, _ := New(8)
		b.Add(c.key)
		for _, p := range c.bits {
			if got := b.bits[p>>6] & (1 << uint(p&63)); got == 0 {
				t.Errorf("Add(%q): bit %d not set", c.key, p)
			}
		}
		if set := popcount(b); set != 2 {
			t.Errorf("Add(%q): %d bits set, want 2", c.key, set)
		}
		if !b.Contains(c.key) {
			t.Errorf("Contains(%q) = false after Add, want true", c.key)
		}
	}
}

func TestContainsFalseBeforeAdd(t *testing.T) {
	b, _ := New(64)
	for _, key := range []string{"a", "zzz", "ab"} {
		if b.Contains(key) {
			t.Errorf("empty filter Contains(%q) = true", key)
		}
	}
}

func TestMergeIsOR(t *testing.T) {
	dst, _ := New(8)
	src, _ := New(8)
	dst.Add("a") // {0,1}
	src.Add("b") // {2,5}
	dst.Merge(src)
	for _, key := range []string{"a", "b"} {
		if !dst.Contains(key) {
			t.Errorf("after Merge, dst missing %q", key)
		}
	}
	if !src.Contains("b") || popcount(src) != 2 {
		t.Errorf("Merge mutated src")
	}
	if got := dst.FillRatio(); got != 0.5 {
		t.Errorf("FillRatio = %v, want 0.5", got)
	}
}

func TestReset(t *testing.T) {
	b, _ := New(8)
	b.Add("a")
	b.Add("b")
	b.Reset()
	if b.Count() != 0 || b.FillRatio() != 0 || b.Contains("a") {
		t.Fatalf("Reset did not clear state: n=%d fill=%v", b.Count(), b.FillRatio())
	}
}

func TestCountIncludesCollisions(t *testing.T) {
	b, _ := New(8)
	for _, key := range []string{"a", "i"} { // 同位碰撞，计数仍各加一
		b.Add(key)
	}
	if b.Count() != 2 {
		t.Errorf("Count = %d, want 2", b.Count())
	}
}

func popcount(b *Bloom) int {
	n := 0
	for _, w := range b.bits {
		for w != 0 {
			w &= w - 1
			n++
		}
	}
	return n
}
