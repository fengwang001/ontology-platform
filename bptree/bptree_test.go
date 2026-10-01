package bptree

import (
	"fmt"
	"strings"
	"testing"
)

func mustKey(i int) string {
	return fmt.Sprintf("k%04d", i)
}

func keysN(n int) []string {
	ks := make([]string, n)
	for i := range ks {
		ks[i] = mustKey(i)
	}
	return ks
}

// canonical serializes a Loader tree into a stable per-level string.
func canonical(levels [][]Page) string {
	var b strings.Builder
	for li, pages := range levels {
		fmt.Fprintf(&b, "L%d:", li)
		for _, pg := range pages {
			if pg.Leaf {
				fmt.Fprintf(&b, "[%d:%s]", len(pg.Keys), strings.Join(pg.Keys, ","))
			} else {
				fmt.Fprintf(&b, "{keys:%s kids:%v}", strings.Join(pg.Keys, ","), pg.Children)
			}
		}
		b.WriteString("|")
	}
	return b.String()
}

func refCanonical(t *refTree) string {
	var b strings.Builder
	for li, pages := range t.levels {
		var idx map[*refPage]int
		if li > 0 {
			idx = map[*refPage]int{}
			for i, p := range t.levels[li-1] {
				idx[p] = i
			}
		}
		fmt.Fprintf(&b, "L%d:", li)
		for _, p := range pages {
			if p.leaf {
				fmt.Fprintf(&b, "[%d:%s]", len(p.keys), strings.Join(p.keys, ","))
			} else {
				kids := make([]int, len(p.children))
				for ci, ch := range p.children {
					kids[ci] = idx[ch]
				}
				fmt.Fprintf(&b, "{keys:%s kids:%v}", strings.Join(p.keys, ","), kids)
			}
		}
		b.WriteString("|")
	}
	return b.String()
}

func paramsFor(C, B, p int) (mL, mI, tL, tI int) {
	mL = C / 2
	mI = (B + 1) / 2
	tL = ceilDiv(C*p, 100)
	if tL < mL {
		tL = mL
	}
	tI = ceilDiv(B*p, 100)
	if tI < mI {
		tI = mI
	}
	return
}

// checkInvariants verifies that every non-root page respects min/max
// occupancy and that internal separators equal child subtree minima.
func checkInvariants(t *testing.T, l *Loader, C, B int) {
	t.Helper()
	mL := C / 2
	mI := (B + 1) / 2
	levels := l.Levels()
	for li, pages := range levels {
		rootLevel := li == len(levels)-1
		min := mL
		cap := C
		if li > 0 {
			min = mI
			cap = B
		}
		for pi, pg := range pages {
			occ := len(pg.Keys)
			if !pg.Leaf {
				occ = len(pg.Children)
			}
			if occ > cap {
				t.Fatalf("level %d page %d overflow: %d > %d", li, pi, occ, cap)
			}
			if !rootLevel && occ < min {
				t.Fatalf("level %d non-root page %d underflow: %d < %d", li, pi, occ, min)
			}
			if !pg.Leaf {
				if len(pg.Keys) != len(pg.Children)-1 {
					t.Fatalf("level %d page %d: %d keys vs %d children", li, pi, len(pg.Keys), len(pg.Children))
				}
				for j := 1; j < len(pg.Children); j++ {
					childMin := subtreeMin(levels, li-1, pg.Children[j])
					if pg.Keys[j-1] != childMin {
						t.Fatalf("level %d page %d sep[%d]=%q != child min %q",
							li, pi, j-1, pg.Keys[j-1], childMin)
					}
				}
			}
		}
	}
}

func subtreeMin(levels [][]Page, level, idx int) string {
	pg := levels[level][idx]
	if pg.Leaf {
		if len(pg.Keys) == 0 {
			return ""
		}
		return pg.Keys[0]
	}
	return subtreeMin(levels, level-1, pg.Children[0])
}

func finishLoader(t *testing.T, C, B, p int, keys []string) *Loader {
	t.Helper()
	l, err := New(C, B, p)
	if err != nil {
		t.Fatalf("New(%d,%d,%d): %v", C, B, p, err)
	}
	if err := l.AddMany(keys); err != nil {
		t.Fatalf("AddMany: %v", err)
	}
	if err := l.Finish(); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	return l
}

// TestSweep0To200 builds every key count 0..200 under several parameter
// sets and compares page-by-page against the naive definition.
func TestSweep0To200(t *testing.T) {
	combos := []struct{ C, B, p int }{
		{2, 3, 100},
		{2, 3, 1},
		{2, 4, 50},
		{3, 3, 100},
		{3, 4, 30},
		{4, 3, 60},
		{4, 5, 100},
		{5, 4, 1},
		{5, 6, 70},
		{6, 5, 33},
		{7, 4, 90},
		{8, 7, 55},
		{10, 5, 100},
		{10, 6, 1},
	}
	for _, cb := range combos {
		cb := cb
		t.Run(fmt.Sprintf("C%dB%dp%d", cb.C, cb.B, cb.p), func(t *testing.T) {
			_, mI, tL, tI := paramsFor(cb.C, cb.B, cb.p)
			_ = mI
			for n := 0; n <= 200; n++ {
				keys := keysN(n)
				l := finishLoader(t, cb.C, cb.B, cb.p, keys)
				ref := naiveLoad(cb.C, cb.B, cb.p, keys)

				got := canonical(l.Levels())
				want := refCanonical(ref)
				if got != want {
					t.Fatalf("n=%d tree mismatch\n got %s\nwant %s", n, got, want)
				}
				if h := l.TreeHeight(); h != ref.height {
					t.Fatalf("n=%d height %d != %d", n, h, ref.height)
				}
				checkInvariants(t, l, cb.C, cb.B)

				// Every inserted key and a few absent keys: existence and
				// visited pages match the reference and height.
				h := ref.height
				for _, k := range keys {
					ex, pages, err := l.Get(k)
					if err != nil || !ex || pages != h {
						t.Fatalf("n=%d Get(%q) = %v,%d,%v want true,%d", n, k, ex, pages, err, h)
					}
					rex, rpages := refGet(ref, k)
					if rex != ex || rpages != pages {
						t.Fatalf("n=%d Get(%q) disagrees with ref: %v/%d vs %v/%d",
							n, k, ex, pages, rex, rpages)
					}
				}
				for _, k := range []string{"", "a", "zzz", mustKey(n), mustKey(n + 5)} {
					ex, pages, err := l.Get(k)
					if err != nil {
						t.Fatalf("Get(%q) error: %v", k, err)
					}
					rex, rpages := refGet(ref, k)
					if ex != rex || pages != rpages || pages != h {
						t.Fatalf("n=%d absent Get(%q): %v/%d want %v/%d (height %d)",
							n, k, ex, pages, rex, rpages, h)
					}
				}

				t.Logf("判定 n=%d tL=%d tI=%d 占用=%s 判定=与朴素装载逐页一致 Get访问=%d层",
					n, tL, tI, occupancyString(l.Levels()), h)
			}
		})
	}
}

func occupancyString(levels [][]Page) string {
	parts := make([]string, len(levels))
	for li, pages := range levels {
		occs := make([]string, len(pages))
		for i, pg := range pages {
			if pg.Leaf {
				occs[i] = fmt.Sprintf("%d", len(pg.Keys))
			} else {
				occs[i] = fmt.Sprintf("%d", len(pg.Children))
			}
		}
		parts[li] = "[" + strings.Join(occs, ",") + "]"
	}
	return strings.Join(parts, "<")
}
