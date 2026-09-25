package api_test

import (
	"cmp"
	"errors"
	"math/rand"
	"slices"
	"sort"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/api"
)

func ev(s byte, t int64) api.Event { return api.Event{Stream: s, TS: t} }

func naive(evs []api.Event) ([]api.Event, int) {
	var acc []api.Event
	var m [2]int64
	var seen [2]bool
	drops := 0
	for _, e := range evs {
		k := int(e.Stream - 'A')
		if seen[k] && e.TS < m[k] {
			drops++
			continue
		}
		seen[k], m[k] = true, max(m[k], e.TS)
		acc = append(acc, e)
	}
	sort.SliceStable(acc, func(i, j int) bool { return acc[i].TS < acc[j].TS })
	return acc, drops
}

func scenarios() [][]api.Event { // 首档为八步基准，其余循环生成随机交错
	out := [][]api.Event{{ev('A', 1), ev('A', 2), ev('B', 1), ev('A', 3), ev('B', 2), ev('B', 5), ev('A', 4), ev('A', 2)}}
	rng := rand.New(rand.NewSource(7))
	for c := 0; c < 30; c++ {
		var evs []api.Event
		for i, n := 0, 20+rng.Intn(60); i < n; i++ {
			evs = append(evs, ev(byte('A'+rng.Intn(2)), int64(rng.Intn(15))))
		}
		out = append(out, evs)
	}
	return out
}

func run(t *testing.T, evs []api.Event) *api.Aligner {
	a := api.New()
	for _, e := range evs {
		a.Feed(e) // 场景只含合法事件，不会报错
	}
	a.Close()
	return a
}

func TestNaiveConsistency(t *testing.T) {
	for i, evs := range scenarios() {
		a := run(t, evs)
		want, drops := naive(evs)
		got := a.View()
		if !slices.Equal(got, want) {
			t.Fatalf("case %d:\n got %v\nwant %v", i, got, want)
		}
		if a.Dropped() != drops {
			t.Fatalf("case %d: dropped %d want %d", i, a.Dropped(), drops)
		}
		if !slices.IsSortedFunc(got, func(x, y api.Event) int { return cmp.Compare(x.TS, y.TS) }) {
			t.Fatalf("case %d: not non-decreasing: %v", i, got)
		}
	}
}

func TestNoPrematureEmission(t *testing.T) {
	for i, evs := range scenarios() {
		a := api.New()
		var m [2]int64
		var seen [2]bool
		for j, e := range evs {
			out, err := a.Feed(e)
			if err != nil {
				t.Fatal(err)
			}
			k := int(e.Stream - 'A')
			seen[k], m[k] = true, max(m[k], e.TS)
			for _, o := range out {
				if !seen[0] || !seen[1] || o.TS > min(m[0], m[1]) {
					t.Fatalf("case %d step %d: premature %v", i, j, o)
				}
			}
		}
	}
}

func TestRejectedNoStateChange(t *testing.T) {
	a := api.New()
	a.Feed(ev('A', 2))
	v0, d0 := a.View(), a.Dropped()
	if _, err := a.Feed(ev('X', 1)); !errors.Is(err, api.ErrBadStream) {
		t.Fatalf("bad stream: %v", err)
	}
	if _, err := a.Feed(ev('A', -1)); !errors.Is(err, api.ErrNegativeTS) {
		t.Fatalf("negative ts: %v", err)
	}
	if !slices.Equal(a.View(), v0) || a.Dropped() != d0 {
		t.Fatal("rejected feed changed state")
	}
	if _, err := a.Feed(ev('B', 2)); err != nil {
		t.Fatalf("unusable after reject: %v", err)
	}
	a.Close()
	v1 := a.View()
	if _, err := a.Close(); !errors.Is(err, api.ErrClosed) {
		t.Fatalf("re-close: %v", err)
	}
	if _, err := a.Feed(ev('A', 9)); !errors.Is(err, api.ErrClosed) {
		t.Fatalf("feed after close: %v", err)
	}
	if !slices.Equal(a.View(), v1) {
		t.Fatal("post-close op changed state")
	}
	if errors.Is(api.ErrBadStream, api.ErrNegativeTS) || errors.Is(api.ErrBadStream, api.ErrClosed) || errors.Is(api.ErrNegativeTS, api.ErrClosed) {
		t.Fatal("sentinels not distinct")
	}
}

func TestConcurrentView(t *testing.T) {
	a := run(t, scenarios()[0])
	want := a.View()
	var wg sync.WaitGroup
	var bad atomic.Bool
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for r := 0; r < 50; r++ {
				if !slices.Equal(a.View(), want) || a.Dropped() != 1 || a.SelfCheck() != nil {
					bad.Store(true)
				}
			}
		}()
	}
	wg.Wait()
	if bad.Load() {
		t.Fatal("inconsistent read")
	}
}
