package chain

import (
	"math"
	"reflect"
	"testing"
)

func TestPrependVisible(t *testing.T) {
	c := New()
	c.Prepend("v1", 1)
	c.Prepend("v2", 2)
	c.Prepend("v3", 3)

	cases := []struct {
		s       int64
		want    string
		found   bool
		probesG int // probes must be far below chain length 3
	}{
		{0, "", false, 0},
		{1, "v1", true, 0},
		{2, "v2", true, 0},
		{3, "v3", true, 0},
		{4, "v3", true, 0}, // above head clamps to head
	}
	for _, tc := range cases {
		got, found := c.Visible(tc.s)
		if got != tc.want || found != tc.found {
			t.Errorf("Visible(%d)=(%q,%v), want (%q,%v)", tc.s, got, found, tc.want, tc.found)
		}
	}
	if got := c.Versions(); !reflect.DeepEqual(got, []int64{3, 2, 1}) {
		t.Errorf("Versions()=%v, want [3 2 1] (newest first)", got)
	}
	// Copy-on-write: the v1 node is still physically present.
	if v, f := c.Visible(1); !f || v != "v1" {
		t.Errorf("old v1 node lost after prepends: (%q,%v)", v, f)
	}
}

func TestProbesSublinear(t *testing.T) {
	// Chain sizes 100..10000; read at the middle and assert the compared
	// node count follows a logarithmic, not linear, curve.
	sizes := []int{100, 1000, 10000}
	prevSize, prevProbes := 0, 0
	for _, m := range sizes {
		c := New()
		for i := 1; i <= m; i++ {
			c.Prepend("v", int64(i))
		}
		if v, ok := c.Visible(int64(m / 2)); !ok || v != "v" {
			t.Fatalf("m=%d middle read failed: %q,%v", m, v, ok)
		}
		p := int(c.probes)
		bound := int(math.Ceil(math.Log2(float64(m)))) + 1
		if p > bound {
			t.Errorf("m=%d probes=%d exceeds log bound %d (linear scan?)", m, p, bound)
		}
		if p >= m/2 {
			t.Errorf("m=%d probes=%d looks linear in chain length", m, p)
		}
		if prevSize > 0 {
			if float64(p)/float64(prevProbes) >= float64(m)/float64(prevSize) {
				t.Errorf("probes grew linearly: %d@%d -> %d@%d", prevProbes, prevSize, p, m)
			}
		}
		prevSize, prevProbes = m, p
	}
}

func TestCollect(t *testing.T) {
	cases := []struct {
		name   string
		active []int64
		keep   []int64 // ascending versions that must survive
		got    int
	}{
		{"none active", nil, []int64{3}, 2},
		{"snapshot 1", []int64{1}, []int64{1, 3}, 1},
		{"snapshot 2", []int64{2}, []int64{2, 3}, 1},
		{"both active", []int64{1, 2}, []int64{1, 2, 3}, 0},
		{"future snapshot keeps all", []int64{99}, []int64{3}, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := New()
			c.Prepend("v1", 1)
			c.Prepend("v2", 2)
			c.Prepend("v3", 3)
			if n := c.Collect(tc.active); n != tc.got {
				t.Fatalf("Collect()=%d, want %d", n, tc.got)
			}
			got := c.Versions() // newest first
			want := make([]int64, len(tc.keep))
			for i, v := range tc.keep {
				want[len(tc.keep)-1-i] = v
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("survivors=%v, want %v", got, want)
			}
			if n := c.Collect(tc.active); n != 0 {
				t.Errorf("idempotent Collect removed %d more", n)
			}
		})
	}
}
