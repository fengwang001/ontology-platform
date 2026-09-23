package acc

import (
	"math"
	"testing"
)

func build(vals ...float64) State {
	s := Zero()
	for _, v := range vals {
		s = s.Add(v)
	}
	return s
}

func sameBits(a, b float64) bool { return math.Float64bits(a) == math.Float64bits(b) }

func TestAddAndMerge(t *testing.T) {
	cases := []struct {
		name                      string
		a, b                      []float64
		wantCount                 uint64
		wantSum, wantMin, wantMax float64
	}{
		{"empty+one", nil, []float64{5}, 1, 5, 5, 5},
		{"split", []float64{1, 2}, []float64{3, 4}, 4, 10, 1, 4},
		{"negatives", []float64{-3, -1}, []float64{-2}, 3, -6, -3, -1},
		{"inf", []float64{math.Inf(1)}, []float64{1}, 2, math.Inf(1), 1, math.Inf(1)},
		{"neginf", []float64{math.Inf(-1)}, []float64{1}, 2, math.Inf(-1), math.Inf(-1), 1},
		{"zeros", []float64{0}, []float64{math.Copysign(0, -1)}, 2, 0, 0, 0},
	}
	for _, c := range cases {
		got := build(c.a...).Merge(build(c.b...))
		if got.Count != c.wantCount || !sameBits(got.Sum, c.wantSum) ||
			!sameBits(got.Min, c.wantMin) || !sameBits(got.Max, c.wantMax) {
			t.Errorf("%s: got %+v", c.name, got)
		}
	}
}

func TestMergeAssociativeCommutative(t *testing.T) {
	a, b, c := build(1, 7, -2), build(3), build(-5, 9, 0)
	left := a.Merge(b).Merge(c)
	right := a.Merge(b.Merge(c))
	if left != right {
		t.Errorf("associativity: %+v != %+v", left, right)
	}
	if b.Merge(a) != a.Merge(b) {
		t.Errorf("commutativity failed")
	}
	if id := a.Merge(Zero()); id != a {
		t.Errorf("identity: %+v != %+v", id, a)
	}
	m := build(2, 8)
	mm := m.Merge(m)
	if mm.Min != m.Min || mm.Max != m.Max {
		t.Errorf("min/max not idempotent: %+v", mm)
	}
	if mm.Count == m.Count || mm.Sum == m.Sum {
		t.Errorf("count/sum must not be idempotent: %+v", mm)
	}
}

func TestStateCodec(t *testing.T) {
	cases := []struct {
		key string
		st  State
	}{
		{"", Zero()},
		{"k", build(1.5, -2, 1e300)},
		{"键", build(math.Inf(-1), -3)},
	}
	for _, c := range cases {
		key, st, err := DecodeState(EncodeState(c.key, c.st))
		if err != nil || key != c.key || st != c.st {
			t.Errorf("roundtrip %q: %q %+v %v", c.key, key, st, err)
		}
	}
	bad := [][]byte{nil, {1}, EncodeState("k", build(1))[:10]}
	for i, b := range bad {
		if _, _, err := DecodeState(b); err == nil {
			t.Errorf("bad case %d: want error", i)
		}
	}
}
