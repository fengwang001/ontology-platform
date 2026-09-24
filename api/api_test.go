package api_test

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"

	"ontology/api"
	"ontology/ord"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func elems(cur map[string]int64) []ord.Element {
	es := make([]ord.Element, 0, len(cur))
	for id, sc := range cur {
		es = append(es, ord.Element{ID: id, Score: sc})
	}
	ord.Sort(es)
	return es
}
func consistent(t *testing.T, v *api.View, cur map[string]int64) {
	t.Helper()
	es := elems(cur)
	want := ord.Batch(es)
	for _, e := range es {
		rn, rk, dr, ok := v.Get(e.ID)
		w := want[e.ID]
		if !ok || rn != w.RowNumber || rk != w.Rank || dr != w.DenseRank {
			t.Fatalf("%s: got (%d,%d,%d) want %+v", e.ID, rn, rk, dr, w)
		}
	}
}
func TestBatchConsistency(t *testing.T) {
	for _, c := range [][2]int{{1, 300}, {2, 300}, {7, 500}} {
		v, rnd, cur := api.New(), rand.New(rand.NewSource(int64(c[0]))), map[string]int64{}
		for i := 0; i < c[1]; i++ {
			id := fmt.Sprintf("e%03d", rnd.Intn(40))
			if _, has := cur[id]; !has && rnd.Intn(10) < 7 {
				cur[id] = int64(rnd.Intn(6))
				must(t, v.Insert(id, cur[id]))
			} else if _, has := cur[id]; has {
				must(t, v.Delete(id))
				delete(cur, id)
			}
			if i%25 == 0 {
				consistent(t, v, cur)
			}
		}
	}
}
func TestDeleteRollback(t *testing.T) {
	base := map[string]int64{"A": 100, "B": 90, "C": 100, "D": 80, "E": 90}
	ids, scs := []string{"z", "m1", "m2", "a"}, []int64{100, 90, 90, 1}
	for i := range ids {
		v := api.New()
		for x, sc := range base {
			must(t, v.Insert(x, sc))
		}
		must(t, v.Insert(ids[i], scs[i]))
		must(t, v.Delete(ids[i]))
		consistent(t, v, base)
		if _, _, _, ok := v.Get(ids[i]); ok {
			t.Fatal("deleted element still present")
		}
	}
}
func TestTieConsistency(t *testing.T) {
	for _, seed := range []int64{11, 22, 33} {
		v, rnd, cur := api.New(), rand.New(rand.NewSource(seed)), map[string]int64{}
		for i := 0; i < 200; i++ {
			id := fmt.Sprintf("e%03d", rnd.Intn(30))
			if _, has := cur[id]; !has {
				cur[id] = int64(rnd.Intn(4))
				must(t, v.Insert(id, cur[id]))
			}
		}
		consistent(t, v, cur)
		es := elems(cur) // explicit invariant 3 between adjacent sorted elements
		for i := 1; i < len(es); i++ {
			_, ra, da, _ := v.Get(es[i-1].ID)
			_, rb, db, _ := v.Get(es[i].ID)
			same := es[i-1].Score == es[i].Score
			if same != (ra == rb && da == db) || (!same && (rb <= ra || db != da+1)) {
				t.Fatal("tie share or group strict/gapless ordering violated")
			}
		}
	}
}
func TestRejectedOpsNoTrace(t *testing.T) {
	v := api.New()
	must(t, v.Insert("A", 100))
	must(t, v.Insert("B", 90))
	base := map[string]int64{"A": 100, "B": 90}
	check := func(f func() error, e error) {
		if !errors.Is(f(), e) {
			t.Fatalf("want %v", e)
		}
		consistent(t, v, base)
	}
	check(func() error { return v.Insert("", 1) }, api.ErrEmptyID)
	check(func() error { return v.Insert("A", 1) }, api.ErrDuplicateID)
	check(func() error { return v.Delete("ghost") }, api.ErrIDNotFound)
	must(t, v.Insert("C", 95))
}
func readMonotonic(t *testing.T, v *api.View, n int, seed int64, stop chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()
	rnd, last := rand.New(rand.NewSource(seed)), map[string]int{}
	for {
		select {
		case <-stop:
			return
		default:
			id := fmt.Sprintf("id%04d", rnd.Intn(n))
			if rk, _, _, ok := v.Get(id); ok && rk < last[id] {
				t.Errorf("RANK of %s decreased to %d", id, rk)
			} else if ok {
				last[id] = rk
			}
		}
	}
}
func TestConcurrentInserts(t *testing.T) {
	const n = 200
	v, stop, start := api.New(), make(chan struct{}), make(chan struct{})
	var rwg, wwg sync.WaitGroup
	for r := 0; r < 4; r++ {
		rwg.Add(1)
		go readMonotonic(t, v, n, int64(r+99), stop, &rwg)
	}
	for i := 0; i < n; i++ {
		wwg.Add(1)
		go func(i int) { defer wwg.Done(); <-start; _ = v.Insert(fmt.Sprintf("id%04d", i), int64(n-i)) }(i)
	}
	close(start)
	wwg.Wait()
	close(stop)
	rwg.Wait()
	cur := map[string]int64{}
	for i := 0; i < n; i++ {
		cur[fmt.Sprintf("id%04d", i)] = int64(n - i)
	}
	consistent(t, v, cur)
}
