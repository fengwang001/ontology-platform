package cepmatch_test

import (
	"errors"
	"reflect"
	"sync"
	"testing"

	"ontology/cepmatch"
	"ontology/cepwin"
)

type ev = cepwin.Event
type mt = cepmatch.Match

func E(k, ty string, ts int64) ev { return ev{Key: k, Type: ty, TS: ts} }
func P(a, b int64) mt             { return mt{A: E("k", "A", a), B: E("k", "B", b)} }

func matches(t *testing.T, mode cepwin.Mode, maxP int, evs []ev) []mt {
	t.Helper()
	e, err := cepmatch.New(mode, 5, maxP)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Feed(evs); err != nil {
		t.Fatal(err)
	}
	return e.Matches()
}

func TestTenEvents(t *testing.T) {
	ten := []ev{E("k", "A", 1), E("k", "C", 2), E("k", "B", 3), E("k", "A", 4), E("k", "A", 6),
		E("k", "B", 9), E("k", "B", 11), E("k", "A", 12), E("z", "A", 14), E("k", "B", 17)}
	for _, c := range []struct {
		mode cepwin.Mode
		want []mt
	}{{cepwin.Relaxed, []mt{P(1, 3), P(4, 9), P(6, 11), P(12, 17)}},
		{cepwin.Strict, []mt{P(6, 9), P(12, 17)}}} {
		if got := matches(t, c.mode, 8, ten); !reflect.DeepEqual(got, c.want) {
			t.Errorf("mode=%d: got %v want %v", c.mode, got, c.want)
		}
	}
}

func TestWindowEdge(t *testing.T) {
	for _, c := range []struct {
		a, b  int64
		match bool
	}{{1, 6, true}, {1, 7, false}, {5, 5, true}, {0, 5, true}, {0, 6, false}} {
		for _, mode := range []cepwin.Mode{cepwin.Strict, cepwin.Relaxed} {
			got := matches(t, mode, 4, []ev{E("k", "A", c.a), E("k", "B", c.b)})
			if (len(got) != 0) != c.match {
				t.Errorf("mode=%d a=%d b=%d: match=%v want %v", mode, c.a, c.b, len(got) != 0, c.match)
			}
		}
	}
}

func TestSentinelErrors(t *testing.T) {
	full, _ := cepmatch.New(cepwin.Relaxed, 5, 1)
	_, _ = full.Feed([]ev{E("k", "A", 1)})
	reg, _ := cepmatch.New(cepwin.Strict, 5, 1)
	_, _ = reg.Feed([]ev{E("k", "C", 5)})
	_, qerr := full.Feed([]ev{E("k", "A", 2)})
	_, rerr := reg.Feed([]ev{E("k", "A", 4)})
	_, kerr := feed(cepwin.Relaxed, E("", "A", 1))
	_, terr := feed(cepwin.Relaxed, E("k", "", 1))
	_, nerr1 := cepmatch.New(cepwin.Relaxed, -1, 1)
	_, nerr2 := cepmatch.New(cepwin.Relaxed, 1, 0)
	_, nerr3 := cepmatch.New(cepwin.Mode(9), 1, 1)
	cases := []struct {
		err  error
		want error
	}{{nerr1, cepwin.ErrNegativeT}, {nerr2, cepwin.ErrInvalidPending},
		{nerr3, cepwin.ErrInvalidMode}, {kerr, cepwin.ErrInvalidEvent},
		{terr, cepwin.ErrInvalidEvent}, {rerr, cepwin.ErrTimeRegression},
		{qerr, cepwin.ErrQueueFull}}
	for i, c := range cases {
		if !errors.Is(c.err, c.want) {
			t.Errorf("case %d: got %v want %v", i, c.err, c.want)
		}
		for j, d := range cases {
			if c.want != d.want && errors.Is(c.err, d.want) {
				t.Errorf("cases %d and %d not distinguishable", i, j)
			}
		}
	}
}

func feed(m cepwin.Mode, evs ...ev) ([]mt, error) {
	e, _ := cepmatch.New(m, 5, 4)
	return e.Feed(evs)
}

func TestBatchAtomic(t *testing.T) {
	bads := [][]ev{
		{E("k", "A", 2), E("q", "", 3)},
		{E("k", "A", 2), E("k", "A", 0)},
		{E("k", "A", 2), E("k", "A", 2), E("k", "A", 2)},
	}
	for i, bad := range bads {
		e, _ := cepmatch.New(cepwin.Relaxed, 5, 2)
		_, _ = e.Feed([]ev{E("k", "A", 1)})
		snap := e.Matches()
		if _, err := e.Feed(bad); err == nil {
			t.Fatalf("case %d: bad batch accepted", i)
		}
		if !reflect.DeepEqual(e.Matches(), snap) {
			t.Fatalf("case %d: state changed by rejected batch", i)
		}
		if got, err := e.Feed([]ev{E("k", "B", 3)}); err != nil || len(got) != 1 {
			t.Fatalf("case %d: unusable after rejection: %v %v", i, got, err)
		}
	}
}

func TestConcurrentReaders(t *testing.T) {
	e, _ := cepmatch.New(cepwin.Relaxed, 5, 64)
	var evs []ev
	for i := range 40 {
		evs = append(evs, E("k", []string{"A", "B", "C"}[i%3], int64(i)))
	}
	if _, err := e.Feed(evs); err != nil {
		t.Fatal(err)
	}
	want := e.Matches()
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make([][]mt, 8)
	for g := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for range 50 {
				errs[g] = e.Matches()
			}
		}()
	}
	close(start)
	wg.Wait()
	for g := range 8 {
		if !reflect.DeepEqual(errs[g], want) {
			t.Fatalf("goroutine %d saw different matches", g)
		}
	}
}
