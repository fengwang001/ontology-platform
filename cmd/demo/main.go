// Command demo exercises the incremental backfill + dedup + cutover view.
package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"

	"ontology/api"
	"ontology/dedup"
)

var failed bool

func check(label string, ok bool) {
	tag := "OK"
	if !ok {
		tag, failed = "FAIL", true
	}
	fmt.Printf("%s: %s\n", tag, label)
}

func main() {
	s := dedup.New()
	check("dedup: first Add applies, repeat is no-op; O(1) probe at m=100..10000",
		s.Add(7) && !s.Add(7) && s.Len() == 1 && dedup.CheckProbeCost() == nil)

	// Nine-event walk of NOTES section 3; replay via the public api.
	type st struct {
		via           string
		seq           int64
		key           string
		a, b, c, seen int64
		noop          bool
	}
	walk := []st{
		{"B", 1, "a", 1, 0, 0, 1, false},
		{"B", 3, "b", 1, 1, 0, 2, false},
		{"O", 12, "b", 1, 2, 0, 3, false},
		{"B", 5, "a", 2, 2, 0, 4, false},
		{"O", 5, "a", 2, 2, 0, 4, true},
		{"B", 7, "c", 2, 2, 1, 5, false},
		{"O", 11, "c", 2, 2, 2, 6, false},
		{"B", 9, "a", 3, 2, 2, 7, false},
		{"O", 3, "b", 3, 2, 2, 7, true},
	}
	p := api.New(10)
	trace, walkOK, noopOK := "", true, true
	for i, e := range walk {
		before := p.Seen()
		ev := []api.Event{{Seq: e.seq, Key: e.key}}
		var err error
		if e.via == "B" {
			err = p.Backfill(ev)
		} else {
			err = p.Online(ev)
		}
		v := p.View()
		got := st{e.via, e.seq, e.key, v["a"], v["b"], v["c"], int64(p.Seen()), p.Seen() == before}
		if err != nil || got.a != e.a || got.b != e.b || got.c != e.c || got.seen != e.seen {
			walkOK = false
		}
		if e.noop && p.Seen() != before {
			noopOK = false
		}
		trace += fmt.Sprintf("%d(a%db%dc%d/%d)", i+1, got.a, got.b, got.c, got.seen)
	}
	check("nine events step-by-step View/Seen: "+trace, walkOK)
	check("steps 5 and 9 (O(5,a), O(3,b)) are dedup no-ops", noopOK)

	errSeqW := p.Backfill([]api.Event{{Seq: 10, Key: "d"}})
	check("Backfill(10,d) at W=10 rejected with ErrBackfillOutOfRange",
		errors.Is(errSeqW, api.ErrBackfillOutOfRange) && p.View()["d"] == 0)
	errComplete := p.CompleteBackfill()
	errAfter := p.Backfill([]api.Event{{Seq: 2, Key: "a"}})
	check("post-CompleteBackfill Backfill(2,a) rejected with ErrBackfillCompleted, View unchanged",
		errComplete == nil && errors.Is(errAfter, api.ErrBackfillCompleted) && p.View()["a"] == 3 && p.Seen() == 7)

	errW := api.New(-1).Online([]api.Event{{Seq: 1, Key: "a"}})
	errKey := api.New(10).Online([]api.Event{{Seq: 1, Key: ""}})
	distinct := api.ErrInvalidW != api.ErrBackfillOutOfRange &&
		api.ErrInvalidW != api.ErrBackfillCompleted && api.ErrInvalidW != api.ErrEmptyKey &&
		api.ErrBackfillOutOfRange != api.ErrBackfillCompleted &&
		api.ErrBackfillOutOfRange != api.ErrEmptyKey && api.ErrBackfillCompleted != api.ErrEmptyKey
	check("four decidable, distinct errors (negative W / Seq>=W / completed / empty Key)",
		errors.Is(errW, api.ErrInvalidW) && errors.Is(errKey, api.ErrEmptyKey) && distinct)

	q := api.New(10)
	_ = q.Backfill([]api.Event{{Seq: 1, Key: "a"}, {Seq: 3, Key: "b"}})
	v0, s0 := q.View(), q.Seen()
	_ = q.Backfill([]api.Event{{Seq: 5, Key: "a"}, {Seq: 10, Key: "d"}})
	_ = q.Online([]api.Event{{Seq: 12, Key: ""}})
	check("rejected batches leave zero trace (state still usable afterwards)",
		reflect.DeepEqual(q.View(), v0) && q.Seen() == s0 &&
			q.Online([]api.Event{{Seq: 12, Key: "b"}}) == nil && q.View()["b"] == 2)

	fed := api.New(1 << 40)
	evs := make([]api.Event, 500)
	for i := range evs {
		evs[i] = api.Event{Seq: int64(i + 1), Key: string(rune('a' + i%6))}
	}
	_ = fed.Backfill(evs)
	const N = 16
	var wg sync.WaitGroup
	views := make([]map[string]int64, N)
	seens := make([]int, N)
	start := make(chan struct{})
	wg.Add(N)
	for g := 0; g < N; g++ {
		go func(g int) {
			defer wg.Done()
			<-start
			views[g], seens[g] = fed.View(), fed.Seen()
		}(g)
	}
	close(start)
	wg.Wait()
	concOK := true
	for g := 1; g < N; g++ {
		if !reflect.DeepEqual(views[0], views[g]) || seens[0] != seens[g] {
			concOK = false
		}
	}
	check("16 concurrent readers get field-by-field identical View and Seen", concOK)

	if failed {
		os.Exit(1)
	}
}
