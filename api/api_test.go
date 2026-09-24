package api_test

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"sync"
	"testing"

	"ontology/api"
	"ontology/ord"
)

func naive(live map[string]int64, k int) []ord.Elem {
	all := make([]ord.Elem, 0, len(live))
	for id, sc := range live {
		all = append(all, ord.Elem{ID: id, Score: sc})
	}
	sort.Slice(all, func(i, j int) bool { return ord.Less(all[i], all[j]) })
	return all[:min(len(all), k)]
}
func ids(es []ord.Elem) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.ID
	}
	return out
}
func ck(t *testing.T, ok bool, f string, a ...any) {
	t.Helper()
	if !ok {
		t.Fatalf(f, a...)
	}
}
func TestNaiveReference(t *testing.T) {
	cases := []struct {
		seed       int64
		ops, k, cp int
	}{{1, 500, 1, 64}, {2, 1000, 3, 64}, {3, 1000, 5, 64}, {4, 2000, 8, 64}}
	for _, c := range cases {
		tk, err := api.New(c.k, c.cp)
		ck(t, err == nil, "New: %v", err)
		live := map[string]int64{}
		r := rand.New(rand.NewSource(c.seed))
		for i := 0; i < c.ops; i++ {
			id := fmt.Sprintf("id%02d", r.Intn(12)) // small alphabet forces ties
			if r.Intn(3) == 2 {
				_ = tk.Remove(id)
				delete(live, id)
			} else {
				sc := int64(r.Intn(6))
				if e := tk.Add(id, sc); e == nil {
					live[id] = sc
				}
			}
			ck(t, reflect.DeepEqual(tk.TopK(), naive(live, c.k)), "seed=%d step=%d %v", c.seed, i, tk.TopK())
			ck(t, tk.Count() == len(live), "count=%d want %d", tk.Count(), len(live))
		}
	}
}
func TestRemoveBackfill(t *testing.T) {
	tk, _ := api.New(2, 10)
	for _, e := range []ord.Elem{{ID: "a", Score: 1}, {ID: "b", Score: 2}, {ID: "c", Score: 3}, {ID: "d", Score: 4}} {
		_ = tk.Add(e.ID, e.Score)
	}
	for _, s := range []struct {
		rm   string
		want []string
	}{{"d", []string{"c", "b"}}, {"a", []string{"c", "b"}}} {
		_ = tk.Remove(s.rm)
		ck(t, reflect.DeepEqual(ids(tk.TopK()), s.want), "Remove(%s)=%v want %v", s.rm, ids(tk.TopK()), s.want)
	}
}
func TestTieOrderAndPrefix(t *testing.T) {
	steps := []struct {
		id  string
		sc  int64
		rem bool
	}{{"m", 10, false}, {"a", 10, false}, {"z", 20, false}, {"y", 20, false},
		{"b", 5, false}, {"z", 0, true}, {"k", 10, false}}
	want := [][]string{{"m"}, {"a", "m"}, {"z", "a", "m"}, {"y", "z", "a"},
		{"y", "z", "a"}, {"y", "a", "m"}, {"y", "a", "k"}}
	tk, _ := api.New(3, 100)
	live := map[string]int64{}
	for i, s := range steps {
		if s.rem {
			_ = tk.Remove(s.id)
			delete(live, s.id)
		} else {
			_ = tk.Add(s.id, s.sc)
			live[s.id] = s.sc
		}
		ck(t, reflect.DeepEqual(ids(tk.TopK()), want[i]), "step %d %v want %v", i+1, ids(tk.TopK()), want[i])
	}
	full := naive(live, len(live))
	for kp := 1; kp <= 3; kp++ { // prefix consistency across k' <= K
		p, _ := api.New(kp, 100)
		for id, sc := range live {
			_ = p.Add(id, sc)
		}
		ck(t, reflect.DeepEqual(ids(p.TopK()), ids(full[:kp])), "prefix k=%d %v", kp, ids(p.TopK()))
	}
}
func TestRejectedOpsNoTrace(t *testing.T) {
	for _, b := range [][2]int{{0, 2}, {2, 1}} {
		_, err := api.New(b[0], b[1])
		ck(t, errors.Is(err, api.ErrInvalidParams), "New(%v) err=%v", b, err)
	}
	tk, _ := api.New(1, 1)
	ck(t, errors.Is(tk.Add("", 1), api.ErrEmptyID), "empty Add not rejected")
	ck(t, errors.Is(tk.Remove(""), api.ErrEmptyID), "empty Remove not rejected")
	ck(t, tk.Add("a", 1) == nil, "Add(a) failed")
	ck(t, errors.Is(tk.Add("b", 2), api.ErrCapacity), "over-capacity Add not rejected")
	ck(t, tk.Count() == 1 && ids(tk.TopK())[0] == "a", "a rejected operation left a trace")
	ck(t, tk.Remove("ghost") == nil && tk.Count() == 1, "Remove of missing id not idempotent")
	ck(t, tk.Add("a", 9) == nil && ids(tk.TopK())[0] == "a", "instance not usable after rejections")
}
func TestConcurrentReaders(t *testing.T) {
	tk, _ := api.New(8, 500)
	for i := 0; i < 200; i++ {
		_ = tk.Add(fmt.Sprintf("id%03d", i), int64((i*7)%50))
	}
	want := tk.TopK()
	const n = 64
	var wg sync.WaitGroup
	bad := make(chan string, 1)
	wg.Add(n)
	for g := 0; g < n; g++ {
		go func() {
			defer wg.Done()
			for r := 0; r < 200; r++ {
				if !reflect.DeepEqual(tk.TopK(), want) || tk.Count() != 200 {
					bad <- "divergent concurrent read"
					return
				}
			}
		}()
	}
	wg.Wait()
	select {
	case msg := <-bad:
		t.Fatal(msg)
	default:
	}
	ck(t, tk.SelfCheck() == nil, "SelfCheck failed")
}
