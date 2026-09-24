package span

import (
	"math"
	"strings"
	"testing"
)

func TestAnchorsBasic(t *testing.T) {
	// Original "ab \n" : "ab" kept, " " deleted, "\n" kept.
	b := NewBuilder()
	b.Keep(2)
	b.Delete(1)
	b.Keep(1)
	m := b.Build()
	cases := []struct {
		off int
		out int
	}{
		{0, 0}, {1, 1}, {2, 2}, {3, 2}, {4, 3}, // ToOut incl. deleted ws pos 3
	}
	for _, c := range cases {
		if got := m.ToOut(c.off); got != c.out {
			t.Fatalf("ToOut(%d)=%d want %d", c.off, got, c.out)
		}
	}
	for o := 0; o <= 3; o++ {
		i := m.ToOrig(o)
		if got := m.ToOut(i); got != o {
			t.Fatalf("ToOut(ToOrig(%d))=%d want %d", o, got, o)
		}
	}
	// Inserted output run maps back to the next original anchor.
	bi := NewBuilder()
	bi.Keep(1)
	bi.Insert(1)
	bi.Keep(1)
	mi := bi.Build()
	if r := mi.ToOrig(1); r != 1 {
		t.Fatalf("inserted ToOrig=%d want 1", r)
	}
}

func TestTruncate(t *testing.T) {
	b := NewBuilder()
	b.Keep(3)
	b.Delete(2)
	b.Keep(1)
	b.Truncate(3, 3)
	m := b.Build()
	if got := m.ToOut(3); got != 3 {
		t.Fatalf("truncated ToOut(3)=%d want 3", got)
	}
	if len(m.Anchors()) != 2 {
		t.Fatalf("truncated anchors=%d want 2", len(m.Anchors()))
	}
}

func TestMerge(t *testing.T) {
	b1, b2 := NewBuilder(), NewBuilder()
	b1.Keep(2)
	b2.Delete(1)
	b2.Keep(1)
	m := Merge([]Map{b1.Build(), b2.Build()}, []Anchor{{0, 0}, {2, 2}})
	if m.ToOut(3) != 2 {
		t.Fatalf("merge ToOut(3)=%d want 2", m.ToOut(3))
	}
	if m.ToOut(4) != 3 {
		t.Fatalf("merge ToOut(4)=%d want 3", m.ToOut(4))
	}
}

func TestScaleProbes(t *testing.T) {
	scales := []struct {
		name string
		data string
	}{
		{"100k", strings.Repeat("xy \r\n", 100000)},
		{"10mb", strings.Repeat("0123456789 \r\n", 833334)},
	}
	for _, sc := range scales {
		t.Run(sc.name, func(t *testing.T) {
			data := sc.data
			b := NewBuilder()
			for k := 0; k < len(data); {
				if data[k] == ' ' || data[k] == '\r' {
					start := k
					for k < len(data) && (data[k] == ' ' || data[k] == '\r') {
						k++
					}
					b.Delete(k - start)
				} else {
					start := k
					for k < len(data) && data[k] != ' ' && data[k] != '\r' {
						k++
					}
					b.Keep(k - start)
				}
			}
			m := b.Build()
			n := len(m.Anchors())
			bound := int(2*math.Log2(float64(n))) + 4
			m.ToOut(len(data) / 2)
			if p := m.Probes(); p > bound {
				t.Fatalf("probes=%d > bound=%d (anchors=%d)", p, bound, n)
			}
			m.ToOrig(len(sc.data) / 4)
			if p := m.Probes(); p > bound {
				t.Fatalf("probes=%d > bound=%d", p, bound)
			}
		})
	}
}

func TestPureNewlineConstantAnchors(t *testing.T) {
	b := NewBuilder()
	b.Keep(10 * 1024 * 1024) // 10 MB of pure '\n': nothing deleted
	m := b.Build()
	if n := len(m.Anchors()); n > 2 {
		t.Fatalf("anchor count=%d, want <= 2 for undeleted stream", n)
	}
}
