package api_test

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"sync"
	"testing"

	"ontology/api"
)

func idOf(i int) string                { return "e" + fmt.Sprint(i) }
func it(id string, w float64) api.Item { return api.Item{ID: id, W: w} }
func must(t *testing.T, ok bool, f string, a ...any) {
	t.Helper()
	if !ok {
		t.Fatalf(f, a...)
	}
}
func offer(t *testing.T, s *api.Sampler, ws []float64) {
	for i, w := range ws {
		must(t, s.Offer([]api.Item{it(idOf(i), w)}) == nil, "offer %d", i)
	}
}
func offerN(t *testing.T, s *api.Sampler, n int) {
	for i := 0; i < n; i++ {
		must(t, s.Offer([]api.Item{it(idOf(i), 1)}) == nil, "offer %d", i)
	}
}
func TestSampleMatchesBatch(t *testing.T) {
	wss := [][]float64{{1, 2, .5, 4, 1, 2, .5}, {3, 1, 1, 5, .2}, {1, 1, 1, 1}}
	us := []float64{.30, .49, .81, .0625, .72, .64, .9}
	for _, k := range []int{1, 2, 3, 10} {
		for ci, ws := range wss {
			s, _ := api.New(k, us)
			offer(t, s, ws)
			// 参照：对全部元素算键，按到达序稳定排序后取前 k。
			idx := make([]int, len(ws))
			for i := range idx {
				idx[i] = i
			}
			sort.SliceStable(idx, func(a, b int) bool {
				return math.Pow(us[idx[a]], 1/ws[idx[a]]) > math.Pow(us[idx[b]], 1/ws[idx[b]])
			})
			got := s.Sample()
			must(t, len(got) == min(k, len(idx)), "case %d k %d len", ci, k)
			for i := range got {
				must(t, got[i].ID == idOf(idx[i]), "case %d k %d pos %d %s!=e%d", ci, k, i, got[i].ID, idx[i])
			}
		}
	}
}
func TestRandomConservation(t *testing.T) {
	us := []float64{.31, .42, .83, .07, .77, .66}
	for _, n := range []int{1, 3, 6} {
		s, _ := api.New(10, us)
		offerN(t, s, n)
		must(t, s.Consumed() == n, "n %d consumed %d", n, s.Consumed())
		have := map[float64]bool{}
		for _, e := range s.Sample() {
			have[e.Key] = true
		}
		for i := 0; i < n; i++ {
			must(t, have[us[i]], "element %d bad u", i)
		}
		must(t, s.SelfCheck() == nil, "selfcheck")
	}
}
func TestPoolSizeAndUniqueIDs(t *testing.T) {
	type kn struct{ k, n int }
	for _, c := range []kn{{1, 0}, {1, 1}, {2, 7}, {20, 25}, {20, 3}} {
		us := make([]float64, c.n)
		for i := range us {
			us[i] = float64(i+1) / float64(c.n+1)
		}
		s, _ := api.New(c.k, us)
		offerN(t, s, c.n)
		got := s.Sample()
		must(t, len(got) == min(c.k, c.n), "k %d n %d size", c.k, c.n)
		seen := map[string]bool{}
		for _, e := range got {
			must(t, !seen[e.ID], "dup %s", e.ID)
			seen[e.ID] = true
		}
	}
}
func TestRejectedBatchNoTrace(t *testing.T) {
	_, e0 := api.New(0, nil)
	_, e1 := api.New(1, []float64{1})
	must(t, errors.Is(e0, api.ErrInvalidInput) && errors.Is(e1, api.ErrInvalidInput), "New")
	must(t, api.ErrInvalidInput != api.ErrZeroWeight && api.ErrZeroWeight != api.ErrBadWeight &&
		api.ErrBadWeight != api.ErrExhausted && api.ErrInvalidInput != api.ErrExhausted, "sentinels distinct")
	us := []float64{.4, .6, .8, .9}
	cases := []struct {
		items []api.Item
		want  error
	}{
		{[]api.Item{it("ok", 1), it("", 1)}, api.ErrInvalidInput},
		{[]api.Item{it("ok", 1), it("ok", 1)}, api.ErrInvalidInput},
		{[]api.Item{it("seed", 1)}, api.ErrInvalidInput},
		{[]api.Item{it("", 0)}, api.ErrInvalidInput},
		{[]api.Item{it("c", 0)}, api.ErrZeroWeight},
		{[]api.Item{it("c", 0), it("d", math.NaN())}, api.ErrZeroWeight},
		{[]api.Item{it("c", -1)}, api.ErrBadWeight},
		{[]api.Item{it("c", math.NaN())}, api.ErrBadWeight},
		{[]api.Item{it("c", math.Inf(-1))}, api.ErrBadWeight},
		{[]api.Item{it("c", 1), it("d", 1), it("f", 1), it("g", 1)}, api.ErrExhausted},
	}
	for ci, c := range cases {
		s, _ := api.New(2, us)
		must(t, s.Offer([]api.Item{it("seed", 1)}) == nil, "seed")
		c0, n0 := s.Consumed(), len(s.Sample())
		must(t, errors.Is(s.Offer(c.items), c.want), "case %d error", ci)
		must(t, s.Consumed() == c0 && len(s.Sample()) == n0, "case %d trace", ci)
		must(t, s.Offer([]api.Item{it("after", 1)}) == nil && s.Consumed() == c0+1, "case %d unusable", ci)
	}
}
func TestConcurrentOffer(t *testing.T) {
	const n, per = 12, 50
	tot := n * per
	us := make([]float64, tot)
	for i := range us {
		us[i] = float64(2*i+1) / float64(2*tot+1)
	}
	s, _ := api.New(tot, us)
	var wg sync.WaitGroup
	for g := 0; g < n; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			b := make([]api.Item, per)
			for j := range b {
				b[j] = it(fmt.Sprintf("g%d-%d", g, j), 1)
			}
			s.Offer(b)
		}(g)
	}
	wg.Wait()
	must(t, s.Consumed() == tot && len(s.Sample()) == tot && s.SelfCheck() == nil, "invariants")
	in, uk, ui := map[float64]bool{}, map[float64]bool{}, map[string]bool{}
	for _, u := range us {
		in[u] = true
	}
	for _, e := range s.Sample() {
		must(t, !uk[e.Key] && !ui[e.ID] && in[e.Key], "bijection")
		uk[e.Key], ui[e.ID] = true, true
	}
}
