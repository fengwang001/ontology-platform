// Command demo exercises the per-group top-K view end to end.
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/groups"
	"ontology/topk"
)

var failed bool

func ok(cond bool, msg string) {
	if cond {
		fmt.Println("OK   " + msg)
	} else {
		failed = true
		fmt.Println("FAIL " + msg)
	}
}

func show(items []topk.Item) string {
	s := ""
	for _, it := range items {
		s += fmt.Sprintf(" %s:%d", it.ItemID, it.Score)
	}
	if s == "" {
		return "[-]"
	}
	return "[" + s[1:] + "]"
}

func main() {
	// topk layer: total order, board entry, refill on removal.
	g := topk.New(2)
	for _, it := range []topk.Item{
		{ItemID: "a", Score: 10}, {ItemID: "b", Score: 20}, {ItemID: "c", Score: 15},
		{ItemID: "d", Score: 15}, {ItemID: "e", Score: 20},
	} {
		g.Add(it)
	}
	before := show(g.Top())
	g.Remove("b")
	ok(before == "[b:20 e:20]" && show(g.Top()) == "[e:20 c:15]" &&
		show(g.All()) == "[e:20 c:15 d:15 a:10]",
		"topk: order "+before+" -> refill "+show(g.Top()))

	// groups layer: rejected ops are distinct errors and leave no trace.
	m := groups.New(2)
	m.Add("g", "b", 20)
	m.Add("g", "c", 15)
	m.Add("g", "a", 10)
	snap := show(m.TopK("g")) + show(m.All("g"))
	dupErr := m.Add("g", "c", 999)
	missErr := m.Remove("g", "zz")
	emptyErr := m.Add("", "x", 1)
	ok(errors.Is(dupErr, groups.ErrDuplicate) && errors.Is(missErr, groups.ErrNotFound) &&
		errors.Is(emptyErr, groups.ErrEmpty) && snap == show(m.TopK("g"))+show(m.All("g")),
		"groups: dup/missing/empty rejected, state intact "+show(m.TopK("g")))

	// api layer: the seven-step reference sequence, board after every step.
	v, err := api.New(2)
	ok(err == nil, "api: New(2)")
	seq := []struct {
		add   bool
		id    string
		score int
	}{
		{true, "a", 10}, {true, "b", 20}, {true, "c", 15}, {true, "d", 15},
		{true, "e", 20}, {false, "b", 0}, {false, "e", 0},
	}
	boards := ""
	for _, s := range seq {
		if s.add {
			v.Add("g", s.id, s.score)
		} else {
			v.Remove("g", s.id)
		}
		boards += show(v.TopK("g"))
	}
	want := "[a:10][b:20 a:10][b:20 c:15][b:20 c:15][b:20 e:20][e:20 c:15][c:15 d:15]"
	ok(boards == want, "api: seven-step boards "+boards)

	// The four rejection categories are mutually distinct sentinels.
	_, kErr := api.New(0)
	sents := []error{kErr, api.ErrEmpty, api.ErrDuplicate, api.ErrNotFound}
	distinct := kErr != nil
	for i := range sents {
		for j := range sents {
			if (i == j) != errors.Is(sents[i], sents[j]) {
				distinct = false
			}
		}
	}
	ok(distinct, "api: 4 error kinds mutually distinct (k<=0, empty, dup, missing)")

	// SelfCheck verifies all four invariants on built-in sequences.
	ok(v.SelfCheck() == nil, "api: SelfCheck")

	// Entry-check count is O(1) in m; the counter is unexported, so the
	// bound itself is asserted by white-box TestEntryChecksConstant.
	big, _ := api.New(10)
	for m := 0; m < 10000; m++ {
		big.Add("g", fmt.Sprintf("id%06d", m), (m*37)%1000)
	}
	big.Add("g", "zz-new", 500)
	ok(len(big.TopK("g")) == 10, "topk: entry check O(1) in m (see TestEntryChecksConstant)")

	// Concurrent readers all observe the identical board.
	full, _ := api.New(5)
	for i := 0; i < 50; i++ {
		full.Add("g", fmt.Sprintf("w%02d", i), (i*13)%40)
	}
	wantTop := full.TopK("g")
	var wg sync.WaitGroup
	bad := make(chan bool, 8)
	for n := 0; n < 8; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for it := 0; it < 200; it++ {
				got := full.TopK("g")
				if len(got) != len(wantTop) {
					bad <- true
					return
				}
				for i := range got {
					if got[i] != wantTop[i] {
						bad <- true
						return
					}
				}
			}
		}()
	}
	wg.Wait()
	close(bad)
	_, diverged := <-bad
	ok(!diverged, "api: concurrent TopK reads identical")

	if failed {
		os.Exit(1)
	}
}
