package span

import (
	"math"
	"strings"
	"testing"
)

func TestMapping(t *testing.T) {
	// "ab   \r\nc" -> "ab\nc": delete 3 spaces, delete \r, keep \n.
	tab := &Table{}
	tab.Keep(2) // ab
	tab.Delete(3)
	tab.Delete(1) // \r
	tab.Keep(2)   // \n c
	cases := []struct {
		i, wantOut int
	}{
		{0, 0}, {1, 1}, {2, 2}, {3, 2}, {4, 2}, {5, 2}, {6, 2}, {7, 3}, {8, 4},
	}
	for _, c := range cases {
		if got := tab.ToOut(c.i); got != c.wantOut {
			t.Errorf("ToOut(%d)=%d want %d", c.i, got, c.wantOut)
		}
	}
	for o := 0; o < tab.ULen; o++ {
		if got := tab.ToOut(tab.ToOrig(o)); got != o {
			t.Errorf("roundtrip at %d: back=%d", o, got)
		}
	}
	prev := -1
	for i := 0; i <= tab.OLen; i++ {
		if got := tab.ToOut(i); got < prev {
			t.Fatalf("ToOut not monotone at %d", i)
		} else {
			prev = got
		}
	}
	prev = -1
	for o := 0; o <= tab.ULen; o++ {
		if got := tab.ToOrig(o); got < prev {
			t.Fatalf("ToOrig not monotone at %d", o)
		} else {
			prev = got
		}
	}
	if n := len(tab.Runs()); n != 3 {
		t.Fatalf("runs=%d want 3", n)
	}
}

func TestInsertionEnd(t *testing.T) {
	tab := &Table{}
	tab.Keep(2)
	tab.Insert(1) // appended newline
	if got := tab.ToOut(2); got != 2 {
		t.Fatalf("ToOut(end)=%d want 2", got)
	}
	if got := tab.ToOrig(2); got != 2 {
		t.Fatalf("ToOrig(appended)=%d want 2", got)
	}
	if got := tab.ToOut(tab.ToOrig(1)); got != 1 {
		t.Fatalf("roundtrip o=1: %d", got)
	}
}

func TestBinarySearchBound(t *testing.T) {
	for _, tc := range []struct {
		name       string
		lines, avg int
	}{
		{"100k lines", 100_000, 100},
		{"10MB", 10_000, 1000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tab := buildMixed(t, tc.lines, tc.avg)
			bound := 2*math.Ceil(math.Log2(float64(len(tab.Runs())))) + 4
			for _, q := range []int{0, tab.OLen / 2, tab.OLen - 1, tab.OLen} {
				tab.ToOut(q)
				if tab.LastChecks() > int(bound) {
					t.Fatalf("ToOut checks %d > %v", tab.LastChecks(), bound)
				}
				tab.ToOrig(min(q, tab.ULen-1))
				if tab.LastChecks() > int(bound) {
					t.Fatalf("ToOrig checks %d > %v", tab.LastChecks(), bound)
				}
			}
		})
	}
}

func TestPureNewlinesConstantRuns(t *testing.T) {
	tab := &Table{}
	chunk := strings.Repeat("\n", 4096)
	for range 10 * 1024 * 1024 / len(chunk) {
		tab.Keep(len(chunk))
	}
	if n := len(tab.Runs()); n > 1 {
		t.Fatalf("runs=%d want <=1 for deletion-free text", n)
	}
}

func buildMixed(t *testing.T, lines, avg int) *Table {
	t.Helper()
	tab := &Table{}
	body := strings.Repeat("x", avg-2)
	eols := []string{"\r\n", "\r", "\n"}
	for i := range lines {
		tab.Keep(len(body))
		if i%3 == 0 {
			tab.Delete(2) // trailing spaces
		}
		e := eols[i%3]
		if e == "\r\n" {
			tab.Delete(1)
			tab.Keep(1)
		} else {
			tab.Keep(1)
		}
	}
	return tab
}
