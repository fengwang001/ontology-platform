package api_test

import (
	"math/rand"
	"reflect"
	"sync"
	"testing"

	"ontology/api"
)

func batch(facts []api.Fact) map[api.Cell]int64 {
	m := map[api.Cell]int64{}
	for _, f := range facts {
		for mask := range 8 {
			c := api.Cell{AllA: mask&1 == 0, AllB: mask&2 == 0, AllC: mask&4 == 0}
			if !c.AllA {
				c.A = f.A
			}
			if !c.AllB {
				c.B = f.B
			}
			if !c.AllC {
				c.C = f.C
			}
			m[c] += f.V
		}
	}
	for c, s := range m {
		if s == 0 {
			delete(m, c)
		}
	}
	return m
}
func randFacts(r *rand.Rand, n int) []api.Fact {
	v := []string{"", "a", "b", "c", "d"}
	o := make([]api.Fact, n)
	for i := range o {
		o[i] = api.Fact{A: v[r.Intn(5)], B: v[r.Intn(5)], C: v[r.Intn(5)], V: int64(r.Intn(11) - 5)}
	}
	return o
}
func must(t *testing.T, err error) {
	if err != nil {
		t.Fatal(err)
	}
}
func TestBatchEquivalence(t *testing.T) {
	for _, n := range []int{1, 7, 50, 300} {
		for seed := int64(0); seed < 5; seed++ {
			r := rand.New(rand.NewSource(seed*1000 + int64(n)))
			x, _ := api.New(1 << 20)
			var live []api.Fact
			for i := 0; i < n; i++ {
				if len(live) > 0 && r.Intn(3) == 0 { // 移除一个已存在事实
					j := r.Intn(len(live))
					must(t, x.Remove(live[j]))
					live = append(live[:j], live[j+1:]...)
					continue
				}
				live = append(live, randFacts(r, 1)[0])
				must(t, x.Add(live[len(live)-1]))
			}
			got := map[api.Cell]int64{}
			for _, c := range x.View() {
				s := c.Sum
				c.Sum = 0 // 键不含 Sum
				got[c] = s
			}
			if want := batch(live); !reflect.DeepEqual(got, want) {
				t.Fatalf("n=%d seed=%d: got %v want %v", n, seed, got, want)
			}
		}
	}
}
func TestAddRemoveRoundTrip(t *testing.T) {
	for seed := int64(0); seed < 10; seed++ {
		r := rand.New(rand.NewSource(seed))
		x, _ := api.New(1 << 20)
		for _, f := range randFacts(r, 40) {
			must(t, x.Add(f))
		}
		before := x.View()
		for _, f := range randFacts(r, 20) {
			must(t, x.Add(f))
			must(t, x.Remove(f))
			if !reflect.DeepEqual(x.View(), before) {
				t.Fatalf("seed=%d: not restored after %+v", seed, f)
			}
		}
	}
}
func TestLevelInvariant(t *testing.T) {
	for _, f := range []api.Fact{{A: "a", B: "b", C: "c", V: 1}, {V: 2}, {B: "b", V: -3}} {
		x, _ := api.New(1 << 20)
		must(t, x.Add(f))
		var hist [4]int
		for _, c := range x.View() {
			hist[api.Level(c)]++
		}
		if hist != [4]int{1, 3, 3, 1} {
			t.Fatalf("%+v: level histogram %v, want [1 3 3 1]", f, hist)
		}
	}
}
func TestFailureAtomicity(t *testing.T) {
	for _, bad := range []int{0, -1, -100} {
		if _, err := api.New(bad); err != api.ErrMaxCells {
			t.Fatalf("New(%d): got %v", bad, err)
		}
	}
	x, _ := api.New(8)
	must(t, x.Add(api.Fact{A: "a", B: "b", C: "c", V: 2})) // 恰好 8 个 cell，不超限
	before := x.View()
	if x.Add(api.Fact{A: "x", B: "y", C: "z", V: 1}) != api.ErrCellLimit ||
		x.Remove(api.Fact{A: "a", B: "b", C: "c", V: 3}) != api.ErrFactNotFound ||
		api.ErrMaxCells == api.ErrCellLimit || api.ErrCellLimit == api.ErrFactNotFound {
		t.Fatal("error sentinels wrong")
	}
	if !reflect.DeepEqual(x.View(), before) {
		t.Fatal("rejected ops changed state")
	}
	must(t, x.Remove(api.Fact{A: "a", B: "b", C: "c", V: 2})) // 拒绝后仍可用
	if len(x.View()) != 0 {
		t.Fatal("expected empty view")
	}
}
func TestConcurrentViews(t *testing.T) {
	x, _ := api.New(1 << 20)
	for _, f := range randFacts(rand.New(rand.NewSource(1)), 200) {
		must(t, x.Add(f))
	}
	want := x.View()
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				if v := x.View(); api.Level(v[0]) != 0 || !reflect.DeepEqual(v, want) || x.SelfCheck() != nil {
					t.Error("concurrent read mismatch")
				}
			}
		}()
	}
	wg.Wait()
}
