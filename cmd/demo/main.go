// Command demo runs in-process checks of the retractable top-K maintainer.
// It takes no arguments and uses no network; exit code is 0 on all-pass.
package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"sort"
	"sync"

	"ontology/api"
	"ontology/ord"
	"ontology/topk"
)

func ids(es []ord.Elem) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.ID
	}
	return out
}
func naive(live map[string]int64, k int) []ord.Elem {
	all := make([]ord.Elem, 0, len(live))
	for id, sc := range live {
		all = append(all, ord.Elem{ID: id, Score: sc})
	}
	sort.Slice(all, func(i, j int) bool { return ord.Less(all[i], all[j]) })
	return all[:min(len(all), k)]
}

func main() {
	fails := 0
	ok := func(name string, good bool, detail string) {
		if good {
			fmt.Printf("%s: OK\n", name)
		} else {
			fmt.Printf("%s: FAIL %s\n", name, detail)
			fails++
		}
	}

	// Section-3 seven steps; record every step's TopK and compare both to
	// the fixed answers and to a naive sorted map reference.
	tk, _ := api.New(3, 100)
	live := map[string]int64{}
	steps := []struct {
		id  string
		sc  int64
		rem bool
	}{{"m", 10, false}, {"a", 10, false}, {"z", 20, false}, {"y", 20, false},
		{"b", 5, false}, {"z", 0, true}, {"k", 10, false}}
	want := [][]string{{"m"}, {"a", "m"}, {"z", "a", "m"}, {"y", "z", "a"},
		{"y", "z", "a"}, {"y", "a", "m"}, {"y", "a", "k"}}
	snaps, stepGood, naiveGood := []string{}, true, true
	for i, s := range steps {
		if s.rem {
			_ = tk.Remove(s.id)
			delete(live, s.id)
		} else {
			_ = tk.Add(s.id, s.sc)
			live[s.id] = s.sc
		}
		got := tk.TopK()
		snaps = append(snaps, fmt.Sprint(ids(got)))
		stepGood = stepGood && reflect.DeepEqual(ids(got), want[i])
		naiveGood = naiveGood && reflect.DeepEqual(got, naive(live, 3))
	}
	ok(fmt.Sprintf("seven steps %v", snaps), stepGood, "wrong step result")
	ok("naive reference", naiveGood, "diverged from sorted reference")

	// Removing a threshold element backfills the best below it.
	bf, _ := api.New(2, 10)
	for _, e := range []ord.Elem{{ID: "a", Score: 1}, {ID: "b", Score: 2}, {ID: "c", Score: 3}, {ID: "d", Score: 4}} {
		_ = bf.Add(e.ID, e.Score)
	}
	_ = bf.Remove("d")
	ok("backfill on remove", reflect.DeepEqual(ids(bf.TopK()), []string{"c", "b"}), fmt.Sprint(ids(bf.TopK())))

	// Ties at 20 and 10 resolve by ascending id.
	ok("tie id ascending", reflect.DeepEqual(ids(tk.TopK()), []string{"y", "a", "k"}), fmt.Sprint(ids(tk.TopK())))

	// The three distinct, decidable sentinel errors.
	_, e0 := api.New(0, 2)
	_, e1 := api.New(2, 1)
	errsDistinct := errors.Is(e0, api.ErrInvalidParams) && errors.Is(e1, api.ErrInvalidParams) &&
		errors.Is(tk.Add("", 1), api.ErrEmptyID) && errors.Is(tk.Remove(""), api.ErrEmptyID)
	ok("three sentinel errors", errsDistinct, "an error was missing or wrong")

	// Capacity rejection leaves no trace; missing-id Remove is idempotent.
	cp, _ := api.New(1, 1)
	_ = cp.Add("a", 1)
	capErr := cp.Add("b", 2)
	noTrace := errors.Is(capErr, api.ErrCapacity) && cp.Count() == 1 && ids(cp.TopK())[0] == "a"
	ok("no trace after reject", noTrace, "state changed on rejection")
	before := cp.Count()
	idemp := cp.Remove("ghost") == nil && cp.Count() == before
	ok("idempotent remove", idemp, "missing-id remove changed state")

	// Per-operation heap work is O(log n), not O(n) (verdict only).
	ok("O(log n) heap ops", topk.ComplexityCheck() == nil, "heap ops grew with N")

	// Many goroutines read one filled instance: all results identical.
	ro, _ := api.New(8, 500)
	for i := 0; i < 200; i++ {
		_ = ro.Add(fmt.Sprintf("id%03d", i), int64((i*7)%50))
	}
	ref := ro.TopK()
	var wg sync.WaitGroup
	var mu sync.Mutex
	concGood := true
	for g := 0; g < 64; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for r := 0; r < 100; r++ {
				if !reflect.DeepEqual(ro.TopK(), ref) || ro.Count() != 200 {
					mu.Lock()
					concGood = false
					mu.Unlock()
					return
				}
			}
		}()
	}
	wg.Wait()
	ok("concurrent reads", concGood, "readers diverged")

	if fails > 0 {
		os.Exit(1)
	}
}
