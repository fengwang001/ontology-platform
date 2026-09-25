package api_test

import (
	"errors"
	"math/rand"
	"reflect"
	"strconv"
	"sync"
	"testing"

	"ontology/api"
)

type report struct {
	batch, cur  map[api.Cell]int64
	plus, minus map[api.Cell]int
	bad         bool
}

func analyze(t *testing.T, seed int64) (*api.Counter, *report) {
	rng := rand.New(rand.NewSource(seed))
	W, B := int64(1+rng.Intn(8)), int64(1+rng.Intn(5))
	c, _ := api.New(W, B)
	evs := make([]api.Event, 1+rng.Intn(80))
	for i := range evs {
		evs[i] = api.Event{Key: "k" + strconv.Itoa(rng.Intn(4)), TS: rng.Int63n(120)}
	}
	if _, err := c.Feed(evs); err != nil {
		t.Fatal(err)
	}
	c.Flush()
	r := &report{
		batch: map[api.Cell]int64{},
		cur:   map[api.Cell]int64{},
		plus:  map[api.Cell]int{},
		minus: map[api.Cell]int{},
	}
	for _, e := range evs {
		r.batch[api.Cell{Key: e.Key, K: e.TS / W}]++
	}
	for _, ch := range c.Emitted() {
		id := api.Cell{Key: ch.Key, K: ch.Win.K}
		if ch.Plus {
			if _, seen := r.cur[id]; seen {
				r.bad = true
			}
			r.cur[id] = ch.Value
			r.plus[id]++
		} else {
			if r.cur[id] != ch.Value {
				r.bad = true
			}
			delete(r.cur, id)
			r.minus[id]++
		}
	}
	return c, r
}
func TestViewMatchesBatch(t *testing.T) {
	for s := 0; s < 25; s++ {
		c, r := analyze(t, int64(s))
		if !reflect.DeepEqual(c.View(), r.batch) {
			t.Fatalf("seed %d view %v != batch %v", s, c.View(), r.batch)
		}
	}
}
func TestChangelogPrefixes(t *testing.T) {
	for s := 0; s < 25; s++ {
		if _, r := analyze(t, int64(s)); r.bad || !reflect.DeepEqual(r.cur, r.batch) {
			t.Fatalf("seed %d prefix or tail of changelog is inconsistent", s)
		}
	}
}
func TestPlusOnce(t *testing.T) {
	for s := 0; s < 25; s++ {
		_, r := analyze(t, int64(s))
		for id, p := range r.plus {
			if p != r.minus[id]+1 {
				t.Fatalf("seed %d cell %v: %d plus, %d minus", s, id, p, r.minus[id])
			}
		}
	}
}
func TestSentinelDistinct(t *testing.T) {
	_, errW := api.New(0, 3)
	_, errB := api.New(3, 0)
	ce, _ := api.New(10, 3)
	_, errE := ce.Feed([]api.Event{{Key: "", TS: 1}})
	if !errors.Is(errW, api.ErrWNonPositive) ||
		!errors.Is(errB, api.ErrBNonPositive) ||
		!errors.Is(errE, api.ErrEmptyKey) {
		t.Fatal("each invalid input must map to its own sentinel")
	}
	if errors.Is(errW, errB) || errors.Is(errW, errE) || errors.Is(errB, errE) {
		t.Fatal("the three sentinels must be mutually distinct")
	}
}
func TestRejectedNoTrace(t *testing.T) {
	c, _ := api.New(10, 3)
	good, next := []api.Event{{Key: "a", TS: 5}, {Key: "a", TS: 12}, {Key: "a", TS: 20}}, []api.Event{{Key: "a", TS: 7}, {Key: "a", TS: 22}, {Key: "a", TS: 25}}
	_, e1 := c.Feed(good)
	log0, view0 := c.Emitted(), c.View()
	_, eBad := c.Feed([]api.Event{{Key: "a", TS: 7}, {Key: "", TS: 1}})
	if !errors.Is(eBad, api.ErrEmptyKey) ||
		!reflect.DeepEqual(c.Emitted(), log0) || !reflect.DeepEqual(c.View(), view0) {
		t.Fatal("rejected batch traced or wrong sentinel")
	}
	f, _ := api.New(10, 3)
	_, e2 := c.Feed(next)
	_, e3 := f.Feed(good)
	_, e4 := f.Feed(next)
	if e1 != nil || e2 != nil || e3 != nil || e4 != nil {
		t.Fatal("unexpected feed error")
	}
	if !reflect.DeepEqual(c.Emitted(), f.Emitted()) {
		t.Fatal("behavior diverged from a fresh instance after rejection")
	}
}
func TestConcurrentReaders(t *testing.T) {
	c, _ := analyze(t, 42)
	const N = 16
	var wg sync.WaitGroup
	start := make(chan struct{})
	views := make([]map[api.Cell]int64, N)
	logs := make([][]api.Change, N)
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func(i int) {
			defer wg.Done()
			<-start
			if err := c.SelfCheck(); err != nil {
				t.Error(err)
			}
			views[i], logs[i] = c.View(), c.Emitted()
		}(i)
	}
	close(start)
	wg.Wait()
	for i := 1; i < N; i++ {
		if !reflect.DeepEqual(views[i], views[0]) || !reflect.DeepEqual(logs[i], logs[0]) {
			t.Fatal("concurrent readers disagree")
		}
	}
}
