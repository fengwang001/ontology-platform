package api_test

import (
	"math/rand"
	"reflect"
	"sync"
	"testing"

	"ontology/api"
	"ontology/dedup"
)

func reference(evs []api.Event) map[string]int64 {
	seen, m := map[int64]bool{}, map[string]int64{}
	for _, e := range evs {
		if !seen[e.Seq] {
			seen[e.Seq], m[e.Key] = true, m[e.Key]+1
		}
	}
	return m
}

func genEvents(n int, seed int64) []api.Event {
	rng := rand.New(rand.NewSource(seed))
	evs := make([]api.Event, n)
	for i := range evs {
		evs[i] = api.Event{Seq: int64(i), Key: string(rune('a' + i%5))}
	}
	rng.Shuffle(n, func(i, j int) { evs[i], evs[j] = evs[j], evs[i] })
	dup := append([]api.Event(nil), evs...)
	rng.Shuffle(n, func(i, j int) { dup[i], dup[j] = dup[j], dup[i] })
	return append(evs, dup...)
}

func feed(p *api.Processor, evs []api.Event, W int64, onlineFirst bool) {
	var bs []api.Event
	for _, e := range evs {
		if e.Seq < W {
			bs = append(bs, e)
		}
	}
	if onlineFirst {
		_ = p.Online(evs)
		_ = p.Backfill(bs)
	} else {
		_ = p.Backfill(bs)
		_ = p.Online(evs)
	}
}

func TestNaiveReference(t *testing.T) {
	for _, n := range []int{10, 100, 500} {
		evs := genEvents(n, int64(n))
		p := api.New(int64(n))
		feed(p, evs, int64(n), true)
		if got := p.View(); !reflect.DeepEqual(got, reference(evs)) || p.Seen() != n {
			t.Fatalf("n=%d view=%v seen=%d want %v/%d", n, got, p.Seen(), reference(evs), n)
		}
	}
}

// TestOrderIndependence pins invariant 2.
func TestOrderIndependence(t *testing.T) {
	for _, n := range []int{5, 50, 500} {
		evs := genEvents(n, int64(n)+7)
		var views [2]map[string]int64
		for i, first := range []bool{false, true} {
			p := api.New(int64(n))
			feed(p, evs, int64(n), first)
			pre := p.View()
			if err := p.CompleteBackfill(); err != nil || !reflect.DeepEqual(p.View(), pre) {
				t.Fatalf("n=%d cutover altered counts", n)
			}
			views[i] = p.View()
		}
		if !reflect.DeepEqual(views[0], views[1]) {
			t.Fatalf("n=%d interleaving changed View", n)
		}
	}
}

// TestIdempotent pins invariant 3: a Seq's second arrival is a pure no-op.
func TestIdempotent(t *testing.T) {
	p := api.New(10)
	for _, c := range []struct {
		seq  int64
		a, b string
	}{{1, "B", "O"}, {3, "B", "B"}, {12, "O", "O"}, {5, "B", "O"}} {
		e := []api.Event{{Seq: c.seq, Key: "k"}}
		put := func(via string) error {
			if via == "B" {
				return p.Backfill(e)
			}
			return p.Online(e)
		}
		if err := put(c.a); err != nil {
			t.Fatal(err)
		}
		v, s := p.View(), p.Seen()
		if err := put(c.b); err != nil || !reflect.DeepEqual(p.View(), v) || p.Seen() != s {
			t.Fatalf("seq %d changed state: %v/%d %v", c.seq, p.View(), p.Seen(), err)
		}
	}
	if p.Seen() != 4 || p.View()["k"] != 4 {
		t.Fatalf("Seen=%d k=%d want 4/4", p.Seen(), p.View()["k"])
	}
}

func TestProbeCostConstant(t *testing.T) {
	if err := dedup.CheckProbeCost(); err != nil {
		t.Fatal(err)
	}
}

// TestConcurrentReadOnly pins concurrent read consistency (run under -race).
func TestConcurrentReadOnly(t *testing.T) {
	p := api.New(1 << 40)
	evs := make([]api.Event, 500)
	for i := range evs {
		evs[i] = api.Event{Seq: int64(i + 1), Key: string(rune('a' + i%6))}
	}
	if err := p.Backfill(evs); err != nil {
		t.Fatal(err)
	}
	const N = 16
	var wg sync.WaitGroup
	views, seens := make([]map[string]int64, N), make([]int, N)
	start := make(chan struct{})
	wg.Add(N)
	for g := 0; g < N; g++ {
		go func(g int) {
			defer wg.Done()
			<-start
			for k := 0; k < 100; k++ {
				views[g], seens[g] = p.View(), p.Seen()
				if err := p.SelfCheck(); err != nil {
					t.Errorf("SelfCheck: %v", err)
					return
				}
			}
		}(g)
	}
	close(start)
	wg.Wait()
	for g := 1; g < N; g++ {
		if !reflect.DeepEqual(views[0], views[g]) || seens[0] != seens[g] {
			t.Fatalf("goroutine %d saw a different snapshot", g)
		}
	}
}
