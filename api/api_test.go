package api_test

import (
	"errors"
	"math/rand"
	"ontology/api"
	"sync"
	"testing"
)

var eightEvents = []api.Event{api.Rec(0, 5), api.Rec(1, 10), api.Bar(0, 1), api.Rec(0, 7), api.Rec(1, 3), api.Bar(1, 1), api.Rec(0, 2), api.Rec(1, 1)}

func must(t *testing.T, err error) {
	if err != nil {
		t.Fatal(err)
	}
}
func genCase(rng *rand.Rand, C int) (evs []api.Event, ref, tot int) {
	it := make([]api.Event, C, C+4)
	for ch := range it {
		it[ch] = api.Bar(ch, 1)
	}
	for k := rng.Intn(4); k > 0; k-- {
		d := rng.Intn(21) - 10
		tot += d
		it = append(it, api.Rec(rng.Intn(C), d))
	}
	rng.Shuffle(len(it), func(i, j int) { it[i], it[j] = it[j], it[i] })
	seen := make([]bool, C)
	for _, ev := range it {
		if ev.IsBar {
			seen[ev.Ch] = true
		} else if !seen[ev.Ch] {
			ref += ev.Delta
		}
	}
	return it, ref, tot
}
func TestInvariantSnapshotMatchesBatchReference(t *testing.T) {
	for ti, C := range []int{2, 1, 3, 5, 4} {
		evs, ref, tot := eightEvents, 18, 28
		if ti > 0 {
			evs, ref, tot = genCase(rand.New(rand.NewSource(int64(ti+1))), C)
		}
		g, _ := api.New(C)
		must(t, g.Feed(evs))
		if g.Sum() != tot {
			t.Fatalf("case %d sum=%d want %d", ti, g.Sum(), tot)
		}
		if v, ok := g.Snapshot(1); !ok || v != ref {
			t.Fatalf("case %d snap[1]=%d,%v want %d", ti, v, ok, ref)
		}
	}
}
func TestInvariantExactlyOnce(t *testing.T) {
	g, _ := api.New(2)
	must(t, g.Feed(eightEvents))
	if g.Sum() != 28 {
		t.Fatalf("sum=%d want 28 (lost or double-counted record)", g.Sum())
	}
}
func TestInvariantBarrierBlocksOnlyOwnChannel(t *testing.T) {
	g, _ := api.New(2)
	for i, c := range []struct {
		ev        api.Event
		sum, snap int
		has       bool
	}{
		{api.Bar(0, 1), 0, 0, false},
		{api.Rec(0, 5), 0, 0, false}, // blocked own channel: buffered, sum unchanged
		{api.Rec(1, 7), 7, 0, false}, // other channel: admitted immediately
		{api.Bar(1, 1), 12, 7, true}, // snapshot 7 excludes in-flight +5, then drain
	} {
		must(t, g.Feed([]api.Event{c.ev}))
		v, ok := g.Snapshot(1)
		if g.Sum() != c.sum || ok != c.has || ok && v != c.snap {
			t.Fatalf("step %d sum=%d snap=%d,%v", i+1, g.Sum(), v, ok)
		}
	}
}
func TestSentinelErrorsDistinct(t *testing.T) {
	g, _ := api.New(2)
	_, e0 := api.New(0)
	seen := map[error]bool{}
	for i, c := range []struct{ got, want error }{
		{e0, api.ErrChannels},
		{g.Feed([]api.Event{{Ch: -1}}), api.ErrChannelRange},
		{g.Feed([]api.Event{api.Bar(0, 0)}), api.ErrBarrierID},
		{g.Feed([]api.Event{api.Bar(0, 1), api.Bar(0, 1)}), api.ErrBarrierOrder},
	} {
		if !errors.Is(c.got, c.want) || seen[c.want] {
			t.Fatalf("case %d got %v want %v", i, c.got, c.want)
		}
		seen[c.want] = true
	}
}
func TestFeedAtomicity(t *testing.T) {
	g, _ := api.New(2)
	if g.Feed([]api.Event{api.Rec(0, 9), api.Rec(9, 1)}) == nil || g.Sum() != 0 {
		t.Fatal("rejected batch changed state")
	}
}
func TestRejectedFeedLeavesNoTrace(t *testing.T) {
	g, _ := api.New(2)
	must(t, g.Feed([]api.Event{api.Rec(0, 4), api.Rec(1, 6), api.Bar(0, 1),
		api.Rec(0, 3), api.Bar(1, 1), api.Rec(1, 2)})) // sum 15, snap[1]=10
	for i, b := range [][]api.Event{
		{{Ch: -1, Delta: 1}},
		{api.Bar(0, 1)},                // duplicate id on ch0
		{api.Rec(0, 1), api.Bar(0, 0)}, // first rec must not land either
		{api.Rec(0, 9), api.Rec(9, 1)}, // atomicity across channels
	} {
		if g.Feed(b) == nil || g.Sum() != 15 {
			t.Fatalf("case %d left a trace: sum=%d", i, g.Sum())
		}
		if v, _ := g.Snapshot(1); v != 10 {
			t.Fatalf("case %d snap=%d want 10", i, v)
		}
	}
	must(t, g.Feed([]api.Event{api.Bar(0, 2), api.Bar(1, 2)}))
	if v, _ := g.Snapshot(2); v != 15 || g.Sum() != 15 {
		t.Fatalf("post-reject alignment snap=%d sum=%d want 15", v, g.Sum())
	}
}
func TestConcurrentReaders(t *testing.T) {
	g, _ := api.New(2)
	must(t, g.Feed([]api.Event{api.Bar(0, 1), api.Rec(0, 8), api.Rec(1, 3), api.Bar(1, 1)}))
	const N = 16
	got := make([][2]int, N)
	var wg sync.WaitGroup
	for i := range got {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s := g.Sum()
			v, _ := g.Snapshot(1)
			got[i] = [2]int{s, v}
			if i%2 == 0 && api.SelfCheck() != nil {
				t.Errorf("concurrent SelfCheck failed")
			}
		}(i)
	}
	wg.Wait()
	for i, r := range got {
		if r != [2]int{11, 3} {
			t.Fatalf("reader %d saw %v want {11 3}", i, r)
		}
	}
}
