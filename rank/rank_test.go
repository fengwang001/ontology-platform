package rank

import (
	"fmt"
	"math/bits"
	"testing"
)

func TestOrdering(t *testing.T) {
	cases := []struct {
		name     string
		a, b     Row
		aBeforeB bool
	}{
		{"higher score first", Row{Key: "a", Score: 50}, Row{Key: "b", Score: 70}, false},
		{"higher score first 2", Row{Key: "b", Score: 70}, Row{Key: "a", Score: 50}, true},
		{"tie: smaller key first", Row{Key: "a", Score: 50}, Row{Key: "c", Score: 50}, true},
		{"tie: smaller key first 2", Row{Key: "c", Score: 50}, Row{Key: "a", Score: 50}, false},
		{"identical row not before itself", Row{Key: "a", Score: 50}, Row{Key: "a", Score: 50}, false},
	}
	for _, c := range cases {
		if got := Before(c.a, c.b); got != c.aBeforeB {
			t.Errorf("%s: Before=%v want %v", c.name, got, c.aBeforeB)
		}
	}
}

func TestIndexOrder(t *testing.T) {
	cases := []struct {
		name string
		add  []Row
		del  []Row
	}{
		{"scores only", []Row{{"a", 1}, {"b", 9}, {"c", 4}, {"d", 9}}, []Row{{"c", 4}}},
		{"many ties", []Row{{"aa", 5}, {"ab", 5}, {"ac", 5}, {"b", 5}, {"a0", 5}}, []Row{{"ab", 5}}},
		{"interleaved", []Row{{"k9", 0}, {"k1", 3}, {"k2", 3}, {"k3", 1}, {"k4", 7}}, []Row{{"k1", 3}, {"k4", 7}}},
	}
	for _, c := range cases {
		x := NewIndex()
		for _, r := range c.add {
			x.Insert(r)
		}
		for _, r := range c.del {
			if !x.Delete(r) {
				t.Fatalf("%s: Delete(%v)=false, want true", c.name, r)
			}
		}
		if x.Len() != len(c.add)-len(c.del) {
			t.Fatalf("%s: Len=%d want %d", c.name, x.Len(), len(c.add)-len(c.del))
		}
		assertSorted(t, c.name, x)
		if x.Delete(Row{Key: "missing", Score: 0}) {
			t.Fatalf("%s: deleting an absent row must report false", c.name)
		}
	}
}

func assertSorted(t *testing.T, name string, x *Index) {
	t.Helper()
	for i := 1; i < x.Len(); i++ {
		if !Before(x.At(i-1), x.At(i)) {
			t.Fatalf("%s: not strictly ordered at %d: %v !< %v", name, i-1, x.At(i-1), x.At(i))
		}
	}
}

func ceilLog2(n int) int { return bits.Len(uint(n - 1)) }

// TestComparisonBudget proves binary search rather than a scan: after m
// live rows, one entering insert and one in-board retract each compare no
// more than 4*ceil(log2(m+1))+4 rows, across several scales of m.
func TestComparisonBudget(t *testing.T) {
	ms := []int{100, 316, 1000, 3162, 10000}
	for _, m := range ms {
		x := NewIndex()
		for i := 0; i < m; i++ { // unique keys, partially tied scores
			x.Insert(Row{Key: fmt.Sprintf("k%05d", i), Score: int64(i % 17)})
		}
		oldTop := x.At(0)
		budget := 4*ceilLog2(m+1) + 4
		x.Insert(Row{Key: "z-new", Score: 1 << 40}) // a row that enters the board
		if x.cmps > budget {
			t.Fatalf("m=%d insert compared %d > budget %d", m, x.cmps, budget)
		}
		if x.cmps == 0 {
			t.Fatalf("m=%d counter never recorded a comparison", m)
		}
		if !x.Delete(oldTop) { // retract a row currently on the board
			t.Fatalf("m=%d: board row %v not found", m, oldTop)
		}
		if x.cmps > budget {
			t.Fatalf("m=%d retract compared %d > budget %d", m, x.cmps, budget)
		}
		assertSorted(t, fmt.Sprintf("m=%d", m), x)
	}
}
