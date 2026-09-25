// Command demo exercises micro-batch triggered tumbling-window counting.
// No arguments, no network; exit code 0 only when every check passes.
package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"sync"

	"ontology/api"
	"ontology/mbatch"
)

func fmtc(cs []api.Change) string {
	s := ""
	for _, c := range cs {
		s += map[bool]string{true: "+", false: "-"}[c.Plus] +
			"[" + strconv.FormatInt(c.Win.Start, 10) + "," +
			strconv.FormatInt(c.Win.End, 10) + ")=" + strconv.FormatInt(c.Value, 10) + " "
	}
	if len(s) > 0 {
		s = s[:len(s)-1]
	}
	return s
}

func main() {
	ok := true
	check := func(name string, pass bool) {
		if pass {
			fmt.Println("OK  " + name)
		} else {
			ok = false
			fmt.Println("FAIL " + name)
		}
	}

	c, err := api.New(10, 3)
	if err != nil {
		fmt.Println("FAIL new:", err)
		os.Exit(1)
	}
	feed := func(ts ...int64) []api.Change {
		evs := make([]api.Event, len(ts))
		for i, t := range ts {
			evs[i] = api.Event{Key: "K", TS: t}
		}
		out, e := c.Feed(evs)
		if e != nil {
			panic(e)
		}
		return out
	}
	all := []int64{5, 12, 20, 7, 22, 25, 8, 31, 15, 9}
	// 甲: TS=20 is half-open in [20,30); batch1 closes [10,20) with count 1.
	b1 := feed(all[0:3]...)
	b2 := feed(all[3:6]...)
	// 乙: TS=8 late to [0,10) with old=2, so the delta is -2/+3.
	b3 := feed(all[6:9]...)
	b4 := append(feed(all[9]), c.Flush()...)
	check("b1 甲: "+fmtc(b1), fmtc(b1) == "+[0,10)=1 +[10,20)=1")
	check("b2: "+fmtc(b2), fmtc(b2) == "-[0,10)=1 +[0,10)=2")
	check("b3 乙: "+fmtc(b3), fmtc(b3) == "+[20,30)=3 -[0,10)=2 +[0,10)=3 -[10,20)=1 +[10,20)=2")
	check("flush: "+fmtc(b4), fmtc(b4) == "+[30,40)=1 -[0,10)=3 +[0,10)=4")

	want := map[api.Cell]int64{{Key: "K", K: 0}: 4, {Key: "K", K: 1}: 2, {Key: "K", K: 2}: 3, {Key: "K", K: 3}: 1}
	cur := map[api.Cell]int64{}
	prefixOK := true
	for _, ch := range c.Emitted() {
		id := api.Cell{Key: ch.Key, K: ch.Win.K}
		if ch.Plus {
			if _, seen := cur[id]; seen {
				prefixOK = false
			}
			cur[id] = ch.Value
		} else {
			if cur[id] != ch.Value {
				prefixOK = false
			}
			delete(cur, id)
		}
	}
	check("view==batch recompute && every prefix self-consistent", reflect.DeepEqual(c.View(), want) && prefixOK)

	_, eW := api.New(0, 3)
	_, eB := api.New(3, 0)
	bad, _ := api.New(10, 3)
	bad.Feed([]api.Event{{Key: "x", TS: 1}})
	snap := bad.Emitted()
	_, eK := bad.Feed([]api.Event{{Key: "", TS: 2}})
	check("three distinct sentinels && rejected batch leaves no trace",
		errors.Is(eW, api.ErrWNonPositive) && errors.Is(eB, api.ErrBNonPositive) &&
			errors.Is(eK, api.ErrEmptyKey) && reflect.DeepEqual(bad.Emitted(), snap))

	check("closure probes stay constant as m grows 100..10000", mbatch.ProbeBoundHolds())

	const N = 16
	var wg sync.WaitGroup
	start := make(chan struct{})
	views := make([]map[api.Cell]int64, N)
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func(i int) { defer wg.Done(); <-start; views[i] = c.View() }(i)
	}
	close(start)
	wg.Wait()
	agree := true
	for i := 1; i < N; i++ {
		if !reflect.DeepEqual(views[i], views[0]) {
			agree = false
		}
	}
	check("concurrent read-only views agree", agree)

	if !ok {
		os.Exit(1)
	}
}
