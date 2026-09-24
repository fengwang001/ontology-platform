// Command demo exercises the sequence gap detector and prints one
// OK/FAIL line per requirement (at most 10 lines). No args, no network.
package main

import (
	"errors"
	"fmt"
	"math"
	"math/rand"
	"os"
	"strings"
	"sync"

	"ontology/api"
	"ontology/det"
)

var failed bool

func check(name string, ok bool) {
	if ok {
		fmt.Println("OK " + name)
	} else {
		fmt.Println("FAIL " + name)
		failed = true
	}
}

func snap(d *det.Detector) string {
	f := func(xs []int64) string {
		s := make([]string, len(xs))
		for i, x := range xs {
			s[i] = fmt.Sprint(x)
		}
		return strings.Join(s, " ")
	}
	return fmt.Sprintf("%d/{%s}/[%s]", d.Watermark(), f(d.Seen()), f(d.Gaps()))
}

func main() {
	// Mandated 11-event sequence (W=2); record state after every Feed.
	d := det.New(2)
	evs := []int64{1, 2, 3, 4, 6, 9, 2, 3, 6, 10, 11}
	states := make([]string, 11)
	for i, s := range evs {
		_ = d.Feed(s)
		states[i] = fmt.Sprintf("%d:%s", i+1, snap(d))
	}
	want := []string{
		"1:1/{}/[]", "2:2/{}/[]", "3:3/{}/[]", "4:4/{}/[]", "5:4/{6}/[]",
		"6:7/{9}/[5 7]", "7:7/{9}/[5 7]", "8:7/{9}/[5 7]", "9:7/{9}/[5 7]",
		"10:10/{}/[5 7 8]", "11:11/{}/[5 7 8]",
	}
	check("11-step states: "+strings.Join(states, " "), eq(states, want))
	check("reorder tolerated: Feed(6) judges no gap (6-5=1<W)", states[4] == "5:4/{6}/[]")
	check("gap size correct: gaps={5,7} (6 seen, 8 in window)", states[5] == "6:7/{9}/[5 7]")
	check("duplicate segment ignored: steps 7-9 leave state unchanged",
		state(states, 6) == state(states, 5) && state(states, 7) == state(states, 5) &&
			state(states, 8) == state(states, 5))

	pub, _ := api.New(2)
	for _, s := range []int64{1, 2, 3, 4, 6, 9} {
		_ = pub.Feed(s)
	}
	h0, g0 := pub.Watermark(), len(pub.Gaps())
	badW, errW := api.New(0)
	errSeq, errOver := pub.Feed(0), pub.Feed(math.MaxInt64)
	distinct := errors.Is(errSeq, api.ErrInvalidSeq) && errors.Is(errOver, api.ErrSeqOverflow) &&
		errors.Is(errW, api.ErrInvalidWindow) && badW == nil &&
		!errors.Is(errSeq, errOver) && !errors.Is(errSeq, errW) && !errors.Is(errOver, errW)
	check("3 distinguishable sentinel errors (seq/window/overflow)", distinct)
	check("rejections leave no trace and detector still usable",
		pub.Watermark() == h0 && len(pub.Gaps()) == g0 && pub.Feed(10) == nil)

	big, _ := api.New(2) // history grows; O(1) is asserted on the unexported
	for s := int64(1); s <= 100000; s++ {
		_ = big.Feed(s) // counter inside SelfCheck, never exported
	}
	check("Feed is O(1) at m=100..10000 (counter bounded, in-package SelfCheck)", big.SelfCheck() == nil)

	const n = 200
	cd, _ := api.New(int64(n)) // window N: no order can force a gap
	order := rand.New(rand.NewSource(7)).Perm(n)
	stop := make(chan struct{})
	var rwg, wg sync.WaitGroup
	mono := true
	rwg.Add(1)
	go func() {
		defer rwg.Done()
		prev := int64(-1)
		for {
			select {
			case <-stop:
				return
			default:
				v := cd.Watermark()
				if v < prev {
					mono = false
				}
				prev = v
			}
		}
	}()
	for _, i := range order {
		wg.Add(1)
		go func(s int64) { defer wg.Done(); _ = cd.Feed(s) }(int64(i) + 1)
	}
	wg.Wait()
	close(stop)
	rwg.Wait()
	check(fmt.Sprintf("concurrent feeders: H=%d gaps=0, readers monotonic", n),
		cd.Watermark() == n && len(cd.Gaps()) == 0 && mono)

	if failed {
		os.Exit(1)
	}
}

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// state drops the "step:" prefix, comparing only H/seen/gaps.
func state(ss []string, i int) string { return strings.SplitN(ss[i], ":", 2)[1] }
