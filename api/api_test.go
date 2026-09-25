package api_test

import (
	"math/rand"
	"reflect"
	"sync"
	"testing"

	"ontology/api"
	"ontology/win"
)

type pair struct {
	k string
	w api.Window
}
type scenario struct {
	w, b int64
	evs  []api.Event
}

func seq(tss ...int64) (evs []api.Event) {
	for _, ts := range tss {
		evs = append(evs, api.Event{Key: "K", TS: ts})
	}
	return
}
func scenarios() []scenario {
	out := []scenario{{10, 3, seq(5, 12, 20, 7, 22, 25, 8, 31, 15, 9)}}
	rng := rand.New(rand.NewSource(468))
	for _, wb := range [][2]int64{{1, 1}, {7, 4}, {10, 3}, {16, 5}} {
		var evs []api.Event
		for i := 0; i < 180; i++ {
			evs = append(evs, api.Event{Key: string(rune('a' + rng.Intn(4))), TS: int64(rng.Intn(200))})
		}
		out = append(out, scenario{wb[0], wb[1], evs})
	}
	return out
}
func recompute(w int64, evs []api.Event) map[string]map[api.Window]int64 {
	v := map[string]map[api.Window]int64{}
	for _, ev := range evs {
		if v[ev.Key] == nil {
			v[ev.Key] = map[api.Window]int64{}
		}
		v[ev.Key][win.Of(win.Index(ev.TS, w), w)]++
	}
	return v
}
func run(t *testing.T, sc scenario) *api.Engine {
	eng, _ := api.New(sc.w, sc.b)
	if _, err := eng.Feed(sc.evs); err != nil {
		t.Fatal(err)
	}
	eng.Flush()
	return eng
}

// replay returns the first bad-retraction prefix and repeated '+' (-1: none).
func replay(log []api.Change) (badRetract, repeatPlus int) {
	badRetract, repeatPlus = -1, -1
	cur := map[pair]int64{}
	last := map[pair]byte{}
	for i, c := range log {
		p := pair{c.Key, c.Win}
		if c.Op == '+' {
			if last[p] != 0 && last[p] != '-' && repeatPlus < 0 {
				repeatPlus = i
			}
			cur[p] = c.Count
		} else {
			if n, ok := cur[p]; (last[p] != '+' || !ok || n != c.Count) && badRetract < 0 {
				badRetract = i
			}
			delete(cur, p)
		}
		last[p] = c.Op
	}
	return
}
func TestViewMatchesBatchRecompute(t *testing.T) {
	for _, sc := range scenarios() {
		if got := run(t, sc).View(); !reflect.DeepEqual(got, recompute(sc.w, sc.evs)) {
			t.Errorf("w=%d b=%d: view != batch recompute", sc.w, sc.b)
		}
	}
}
func TestChangelogPrefixesConsistent(t *testing.T) {
	for _, sc := range scenarios() {
		if bad, _ := replay(run(t, sc).Emitted()); bad >= 0 {
			t.Errorf("w=%d b=%d: entry %d retracts a non-current value", sc.w, sc.b, bad)
		}
	}
}
func TestWindowClosesOnce(t *testing.T) {
	for _, sc := range scenarios() {
		if _, repeat := replay(run(t, sc).Emitted()); repeat >= 0 {
			t.Errorf("w=%d b=%d: entry %d repeats a full '+'", sc.w, sc.b, repeat)
		}
	}
}
func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	bad := map[[2]int64]error{{0, 1}: api.ErrNonPositiveWindow, {-3, 1}: api.ErrNonPositiveWindow,
		{1, 0}: api.ErrNonPositiveBatch, {1, -2}: api.ErrNonPositiveBatch}
	for wb, want := range bad {
		if _, err := api.New(wb[0], wb[1]); err != want {
			t.Errorf("New(%d,%d): got %v, want %v", wb[0], wb[1], err, want)
		}
	}
	if api.ErrNonPositiveWindow == api.ErrNonPositiveBatch || api.ErrNonPositiveBatch == api.ErrEmptyKey || api.ErrNonPositiveWindow == api.ErrEmptyKey {
		t.Fatal("sentinel errors are not mutually distinct")
	}
	eng, _ := api.New(10, 3)
	eng.Feed(seq(5, 12, 20))
	before := eng.Emitted()
	if _, err := eng.Feed([]api.Event{{Key: "K", TS: 100}, {Key: "", TS: 8}}); err != api.ErrEmptyKey {
		t.Fatalf("got %v, want ErrEmptyKey", err)
	}
	got, _ := eng.Feed(seq(22, 25)) // buffer 2/3: nothing may fire
	if len(got) != 0 || !reflect.DeepEqual(eng.Emitted(), before) {
		t.Fatal("rejected feed altered wm/counts/log/batch")
	}
	if got, _ = eng.Feed(seq(31)); len(got) == 0 {
		t.Fatal("engine not usable after rejection")
	}
}
func TestConcurrentReadOnly(t *testing.T) {
	eng, _ := api.New(10, 3)
	eng.Feed(seq(5, 12, 20, 7, 22, 25, 8, 31, 15, 9))
	eng.Flush()
	want := eng.View()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				if !reflect.DeepEqual(eng.View(), want) {
					t.Error("goroutine sees a different view")
				}
				_ = eng.Emitted()
				if eng.SelfCheck() != nil {
					t.Error("SelfCheck failed")
				}
			}
		}()
	}
	wg.Wait()
}
