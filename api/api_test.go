package api_test

import "errors"
import "math/rand"
import "reflect"
import "slices"
import "sort"
import "strconv"
import "sync"
import "testing"
import "ontology/api"
import "ontology/cagg"
import "ontology/cwin"

type O = api.Out

func canon(o []O) []O {
	c := append([]O{}, o...)
	sort.Slice(c, func(i, j int) bool {
		x, y := c[i], c[j]
		return x.Start < y.Start || x.Start == y.Start && (x.End < y.End || x.End == y.End && x.Key < y.Key)
	})
	return c
}
func feedRand(w *api.Window, n int, seed, span int64, keys int) []cagg.Event {
	r := rand.New(rand.NewSource(seed))
	evs := make([]cagg.Event, n)
	for i := range evs {
		evs[i] = cagg.Event{Key: string(rune('A' + r.Intn(keys))), TS: r.Int63n(2*span) - span}
		w.Feed([]cagg.Event{evs[i]})
	}
	return evs
}
func oracle(raw []cagg.Event, M, step, delay int64) []O {
	ks, ss, ts := map[string]string{}, map[string]int64{}, map[string][]int64{}
	mx := int64(-1 << 62)
	for _, e := range raw {
		mx = max(mx, e.TS)
		s := cwin.Start(e.TS, M)
		em := cwin.End(s, step, cwin.MinSub(e.TS, s, step))
		if mx-delay >= em {
			continue
		}
		id := e.Key + "/" + strconv.FormatInt(s, 10)
		if ts[id] == nil {
			ks[id], ss[id] = e.Key, s
		}
		ts[id] = append(ts[id], e.TS)
	}
	var res []O
	for id, v := range ts {
		for j := 1; j <= cwin.SubCount(M, step); j++ {
			end := cwin.End(ss[id], step, j)
			var n int64
			for _, t := range v {
				if t < end {
					n++
				}
			}
			res = append(res, O{Key: ks[id], Start: ss[id], End: end, Count: n})
		}
	}
	return canon(res)
}
func TestBatchRecompute(t *testing.T) {
	for seed := int64(1); seed <= 40; seed++ {
		r := rand.New(rand.NewSource(seed))
		M, step, delay := int64(12), int64(4), int64(r.Intn(6))
		w, _ := api.New(M, step, delay, 100000)
		raw := feedRand(w, 60, seed, 30, 4)
		w.Flush()
		if got := canon(w.All()); !reflect.DeepEqual(got, oracle(raw, M, step, delay)) {
			t.Fatalf("seed=%d\n got=%v\nwant=%v", seed, got, oracle(raw, M, step, delay))
		}
	}
	w, err := api.New(1, 1, 0, 1)
	if err != nil || w.SelfCheck() != nil {
		t.Fatal("SelfCheck failed")
	}
}
func TestMonotone(t *testing.T) {
	for _, c := range []struct{ M, step int64 }{{12, 4}, {6, 2}, {1, 1}} {
		w, _ := api.New(c.M, c.step, 1, 1000)
		feedRand(w, 200, 7, 40, 3)
		w.Flush()
		last := map[[2]any]int64{}
		for _, o := range w.All() {
			if p := last[[2]any{o.Key, o.Start}]; o.Count < p {
				t.Fatalf("non-monotone count %d then %d", p, o.Count)
			}
			last[[2]any{o.Key, o.Start}] = o.Count
		}
	}
}
func TestWatermark(t *testing.T) {
	w, _ := api.New(12, 4, 2, 10)
	var mx int64
	for _, ts := range []int64{-5, 1, -2, 6, 3} {
		mx = max(mx, ts)
		outs, _ := w.Feed([]cagg.Event{{Key: "K", TS: ts}})
		if !sort.SliceIsSorted(outs, func(a, b int) bool { x, y := outs[a], outs[b]; return x.End < y.End || x.End == y.End && x.Key < y.Key }) {
			t.Fatalf("bad order: %v", outs)
		}
		if bad := slices.IndexFunc(outs, func(o O) bool { return o.End > mx-2 }); bad >= 0 {
			t.Fatalf("fired above wm: %v", outs[bad])
		}
	}
	if w.Dropped() != 1 {
		t.Fatalf("dropped=%d want 1", w.Dropped())
	}
}
func TestRejectedBatchNoTrace(t *testing.T) {
	for _, c := range []struct{ M, s, d int64 }{{0, 1, 0}, {12, 0, 0}, {12, 5, 0}, {12, 4, -1}} {
		if _, err := api.New(c.M, c.s, c.d, 1); !errors.Is(err, cagg.ErrInvalidParams) {
			t.Fatalf("%+v err=%v", c, err)
		}
	}
	w, _ := api.New(12, 4, 2, 1)
	w.Feed([]cagg.Event{{Key: "K", TS: -5}})
	before, drop := w.All(), w.Dropped()
	if _, e := w.Feed([]cagg.Event{{Key: "K", TS: 1}, {Key: "Z", TS: 2}}); !errors.Is(e, cagg.ErrTooManyWindows) {
		t.Fatal(e)
	}
	if _, e := w.Feed([]cagg.Event{{Key: "", TS: -5}}); !errors.Is(e, cagg.ErrEmptyKey) {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(w.All(), before) || w.Dropped() != drop {
		t.Fatal("rejected batch left a trace")
	}
	if _, err := w.Feed([]cagg.Event{{Key: "K", TS: -2}}); err != nil {
		t.Fatal(err)
	}
}
func TestConcurrentRead(t *testing.T) {
	w, _ := api.New(12, 4, 2, 1000)
	feedRand(w, 300, 11, 100, 5)
	w.Flush()
	ref := w.All()
	var wg sync.WaitGroup
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if got := w.All(); !reflect.DeepEqual(got, ref) {
				t.Errorf("concurrent read differs")
			}
		}()
	}
	wg.Wait()
}
