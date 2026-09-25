package rewrite

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"

	"ontology/pred"
)

func baseRows() []Row {
	return []Row{
		{"east", 50, 2020}, {"east", 120, 2020}, {"east", 200, 2021}, {"west", 80, 2020},
		{"west", 150, 2021}, {"north", 100, 2021}, {"east", 90, 2022}, {"west", 300, 2022},
	}
}
func disjointCatalog(m int) *Catalog {
	c := NewCatalog(baseRows())
	for i := 0; i < m; i++ {
		r := fmt.Sprintf("r%d", i)
		c.Add("f"+r, pred.Pred{Region: pred.RegionEq(r)}, [3]bool{true}, [3]bool{}, false)
	}
	return c
}

// TestComparesBounded pins hash-bucket lookup: equality-region comparisons are
// bounded by a constant independent of m; region-free queries compare all m.
func TestComparesBounded(t *testing.T) {
	ms := []int{100, 1000, 10000}
	var base int64
	for i, m := range ms {
		c := disjointCatalog(m)
		c.Query(pred.Pred{Region: pred.RegionEq(fmt.Sprintf("r%d", m/2))}, [3]bool{true}, nil)
		if n := c.compares.Load(); n > 2 {
			t.Fatalf("m=%d compared %d views, want <= 2", m, n)
		} else if i == 0 {
			base = n
		} else if n != base {
			t.Fatalf("compares grew with m: %d vs baseline %d", n, base)
		}
		c.Query(pred.Pred{Amount: pred.IGt(0)}, [3]bool{true, true}, nil)
		if n := c.compares.Load(); n != int64(m) {
			t.Fatalf("region-free compared %d views, want %d", n, m)
		}
	}
}
func TestBucketIsolation(t *testing.T) {
	c := disjointCatalog(500)
	for _, k := range []int{0, 249, 499} {
		q := pred.Pred{Region: pred.RegionEq(fmt.Sprintf("r%d", k))}
		_, name, _ := c.Query(q, [3]bool{true}, nil)
		if name != fmt.Sprintf("fr%d", k) || c.compares.Load() != 1 {
			t.Fatalf("r%d -> view %q, compares %d", k, name, c.compares.Load())
		}
	}
}
func TestContainment(t *testing.T) {
	if !pred.Contains(pred.Pred{Amount: pred.IGt(100)}, pred.Pred{Amount: pred.IGe(100)}) ||
		pred.Contains(pred.Pred{Amount: pred.IGe(100)}, pred.Pred{Amount: pred.IGt(100)}) {
		t.Fatal(">100 contained in >=100 one way only (the 甲 boundary)")
	}
	if pred.Contains(pred.Pred{Year: pred.IGe(2021)}, pred.Pred{Year: pred.IEq(2021)}) {
		t.Fatal("[2021,+inf) not contained in [2021,2021]")
	}
	if !pred.Contains(pred.Pred{Region: pred.RegionEq("east"), Amount: pred.IEq(50)},
		pred.Pred{Region: pred.RegionEq("east")}) {
		t.Fatal("conjunctive narrowing must be contained")
	}
}

// TestResidualEquivalence pins Vp AND residual == P on every base row.
func TestResidualEquivalence(t *testing.T) {
	ps := []struct{ vp, p pred.Pred }{
		{pred.Pred{Region: pred.RegionEq("east")}, pred.Pred{Region: pred.RegionEq("east"), Amount: pred.IGe(100)}},
		{pred.Pred{Amount: pred.IGe(100)}, pred.Pred{Amount: pred.IGt(100)}},
		{pred.Pred{}, pred.Pred{Region: pred.RegionEq("east"), Year: pred.IEq(2022)}},
		{pred.Pred{Year: pred.IGe(2021)}, pred.Pred{Year: pred.IEq(2022)}},
	}
	for _, x := range ps {
		res := pred.Residual(x.p, x.vp)
		for _, r := range baseRows() {
			g := x.vp.Match(r.Region, r.Amount, r.Year) && res.Match(r.Region, r.Amount, r.Year)
			if g != x.p.Match(r.Region, r.Amount, r.Year) {
				t.Fatalf("vp&res != P: %v row %v", x, r)
			}
		}
	}
}

// TestEquivRewrite compares the view catalog against a view-less full scan
// over a random grid, for ordinary and aggregate queries (invariants 1-3).
func TestEquivRewrite(t *testing.T) {
	views := NewCatalog(baseRows())
	views.Add("V1", pred.Pred{Region: pred.RegionEq("east")}, [3]bool{true, true, false}, [3]bool{}, false)
	views.Add("V2", pred.Pred{Amount: pred.IGe(100)}, [3]bool{true, true, true}, [3]bool{}, false)
	views.Add("V3", pred.Pred{}, [3]bool{}, [3]bool{true, false, false}, true)
	views.Add("V4", pred.Pred{}, [3]bool{}, [3]bool{true, false, true}, true)
	bare := NewCatalog(baseRows())
	rng := rand.New(rand.NewSource(7))
	ia := func() pred.Atom {
		switch rng.Intn(6) {
		case 1:
			return pred.IEq(100)
		case 2:
			return pred.ILt(150)
		case 3:
			return pred.ILe(100)
		case 4:
			return pred.IGt(100)
		case 5:
			return pred.IGe(200)
		}
		return pred.Atom{}
	}
	pms := [][3]bool{{true, true, false}, {true, true, true}}
	ags := []*Agg{nil, &Agg{}, &Agg{Region: true}, &Agg{Year: true}, &Agg{Region: true, Year: true}}
	for i := 0; i < 500; i++ {
		p := pred.Pred{Amount: ia(), Year: ia()}
		if rng.Intn(2) == 0 {
			p.Region = pred.RegionEq([]string{"east", "west", "north"}[rng.Intn(3)])
		}
		if rng.Intn(2) == 0 {
			m := pms[rng.Intn(2)]
			g, _, _ := views.Query(p, m, nil)
			w, _, _ := bare.Query(p, m, nil)
			if !reflect.DeepEqual(g, w) {
				t.Fatalf("ordinary p=%v got %v want %v", p, g, w)
			}
		} else {
			a := ags[rng.Intn(len(ags))]
			g, _, _ := views.Query(p, [3]bool{}, a)
			w, _, _ := bare.Query(p, [3]bool{}, a)
			if !reflect.DeepEqual(g, w) {
				t.Fatalf("agg p=%v a=%v got %v want %v", p, a, g, w)
			}
		}
	}
}
