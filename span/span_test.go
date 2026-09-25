package span

import (
	"math/bits"
	"testing"
)

func TestMapQueries(t *testing.T) {
	// orig: "ab" + deleted "xy" + "c" + CR-removed "\r" + "\n" + tail-deleted "zz"
	var b Builder
	b.Keep(2)      // ab
	b.DropOrig(2)  // xy
	b.Keep(1)      // c
	b.DropOrig(1)  // \r
	b.Keep(1)      // \n
	b.DropOrig(2)  // zz at end
	m := b.Build()

	// offsets: 0a 1b |2x 3y deleted| 4c |5r deleted| 6nl |7z 8z deleted
	toOut := map[int]int{0: 0, 1: 1, 2: 2, 3: 2, 4: 2, 5: 3, 6: 3, 7: 4, 8: 4}
	for i, want := range toOut {
		if got := m.ToOut(i); got != want {
			t.Errorf("ToOut(%d)=%d want %d", i, got, want)
		}
	}
	for o := 0; o <= m.LenOut(); o++ {
		i := m.ToOrig(o)
		if i < 0 || i > m.LenOrig() {
			t.Fatalf("ToOrig(%d)=%d out of range", o, i)
		}
		if got := m.ToOut(i); got != o {
			t.Errorf("roundtrip o=%d: ToOrig=%d ToOut=%d", o, i, got)
		}
	}
	prev := -1
	for o := 0; o <= m.LenOut(); o++ {
		if got := m.ToOrig(o); got < prev {
			t.Fatalf("ToOrig not monotone at %d", o)
		} else {
			prev = got
		}
	}
	prev = -1
	for i := 0; i <= m.LenOrig(); i++ {
		if got := m.ToOut(i); got < prev {
			t.Fatalf("ToOut not monotone at %d", i)
		} else {
			prev = got
		}
	}
}

func TestGrowShrink(t *testing.T) {
	cases := []struct {
		name    string
		build   func(*Builder)
		outLen  int
		origLen int
	}{
		{"shrink", func(b *Builder) { b.Keep(4); b.ShrinkOut(2) }, 2, 4},
		{"grow", func(b *Builder) { b.Keep(1); b.GrowOut(1) }, 2, 1},
		{"shrink-tail-del", func(b *Builder) { b.Keep(2); b.DropOrig(2); b.ShrinkOut(1) }, 1, 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var b Builder
			tc.build(&b)
			m := b.Build()
			if m.LenOut() != tc.outLen || m.LenOrig() != tc.origLen {
				t.Fatalf("lens out=%d orig=%d", m.LenOut(), m.LenOrig())
			}
			for i := 0; i <= m.LenOrig(); i++ {
				if m.ToOut(i) < 0 || m.ToOut(i) > m.LenOut() {
					t.Fatalf("bad ToOut %d", i)
				}
			}
		})
	}
}

func TestBinarySearchBoundAndDensity(t *testing.T) {
	const deletes = 100000
	var b Builder
	for i := 0; i < deletes; i++ {
		b.DropOrig(1)
		b.Keep(9)
	}
	m := b.Build()
	bound := 2*bits.Len(uint(m.NumSegs())) + 4
	worst := 0
	for o := 0; o <= m.LenOut(); o += 7919 {
		m.ToOrig(o)
		if m.LastChecked() > worst {
			worst = m.LastChecked()
		}
		m.ToOut(o % (m.LenOrig() + 1))
		if m.LastChecked() > worst {
			worst = m.LastChecked()
		}
	}
	if worst > bound {
		t.Fatalf("checked %d segments > bound %d (n=%d)", worst, bound, m.NumSegs())
	}

	// 10MB with nothing deletable: a single contiguous keep segment.
	var plain Builder
	plain.Keep(10_000_000)
	p := plain.Build()
	if p.NumSegs() > 2 {
			t.Fatalf("dense map has %d segments", p.NumSegs())
	}
}
