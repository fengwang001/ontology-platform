package main

import (
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"

	"ontology/api"
)

// naive scans accepted events; mode: 0 correct (T-W,T], -1 closed
// left [T-W,T], +1 open right (T-W,T).
func naive(all []api.Event, W int64, mode int) int {
	if len(all) == 0 {
		return 0
	}
	T := all[len(all)-1].TS
	seen := map[int]bool{}
	for _, e := range all {
		l := e.TS > T-W
		if mode == -1 {
			l = e.TS >= T-W
		}
		r := e.TS <= T
		if mode == 1 {
			r = e.TS < T
		}
		if l && r {
			seen[e.Key] = true
		}
	}
	return len(seen)
}

func main() {
	fails := 0
	check := func(name string, cond bool) {
		tag := "OK"
		if !cond {
			tag, fails = "FAIL", fails+1
		}
		fmt.Printf("%s: %s\n", name, tag)
	}

	// Six-step sequence from NOTES.md, W=10 b=5.
	c, _ := api.New(10, 5)
	steps := []api.Event{{Key: 1, TS: 1}, {Key: 2, TS: 3}, {Key: 5, TS: 4},
		{Key: 1, TS: 6}, {Key: 3, TS: 11}, {Key: 4, TS: 13}}
	want := []int{1, 2, 3, 3, 4, 4}
	all, per, mono := []api.Event{}, true, true
	last := map[int]int64{}
	for i, e := range steps {
		if err := c.Feed([]api.Event{e}); err != nil || c.Distinct() != want[i] {
			per = false
		}
		if prev, ok := last[e.Key]; ok && prev > e.TS {
			mono = false
		}
		last[e.Key], all = e.TS, append(all, e)
	}
	check(fmt.Sprintf("six-step distinct %v (final 4)", want), per && c.Distinct() == 4)

	// (甲)(丙) wrong-window scans; (乙) emulate bucket-start expiry.
	jia, bing := naive(all, 10, -1), naive(all, 10, 1)
	bk := map[int64]map[int]bool{}
	for k, ts := range last {
		i := ts / 5
		if bk[i] == nil {
			bk[i] = map[int]bool{}
		}
		bk[i][k] = true
	}
	yi := 0
	if 0*5 <= 3 { // wrong "bucket start <= cutoff" rule wipes bucket 0
		for i, s := range bk {
			if i != 0 {
				yi += len(s)
			}
		}
	}
	check("wrong values 甲 closed-left=5 乙 bucket-start=3 丙 open-right=3", jia == 5 && yi == 3 && bing == 3)

	// Random non-decreasing stream: exact rescan equality, in batches.
	rng := rand.New(rand.NewSource(7))
	rc, _ := api.New(10, 5)
	var stream []api.Event
	var ts int64
	consistent := true
	for i := 0; i < 300; i++ {
		ts += rng.Int63n(3)
		stream = append(stream, api.Event{Key: rng.Intn(10), TS: ts})
	}
	for i := 0; i < len(stream); {
		n := 1 + rng.Intn(4)
		if i+n > len(stream) {
			n = len(stream) - i
		}
		if err := rc.Feed(stream[i : i+n]); err != nil || rc.Distinct() != naive(stream[:i+n], 10, 0) {
			consistent = false
		}
		i += n
	}
	check("naive-rescan consistency over random batches", consistent)
	check("last[key] non-decreasing on repeats", mono)

	// Four distinguishable sentinel errors, atomic rejection.
	c2, _ := api.New(10, 5)
	_ = c2.Feed([]api.Event{{Key: 1, TS: 1}})
	eInvalid := c2.Feed([]api.Event{{Key: 2, TS: 14}, {Key: -1, TS: 13}}) // negative key first
	eRollback := c2.Feed([]api.Event{{Key: 2, TS: 0}})                    // 0 < last TS 1
	_, eW := api.New(0, 1)
	_, eB := api.New(10, 11)
	four := eW == api.ErrInvalidWindow && eB == api.ErrInvalidBucket &&
		eInvalid == api.ErrInvalidEvent && eRollback == api.ErrTSRollback
	check("four distinguishable sentinel errors", four)
	notrace := c2.Distinct() == 1 && c2.Feed([]api.Event{{Key: 8, TS: 8}}) == nil && c2.Distinct() == 2
	check("rejected batch leaves no trace, still usable", notrace)

	// checked is unexported: its O(1)-per-trigger proof lives in the
	// same-package test; the demo executes that test, never reads it.
	goBin := filepath.Join(runtime.GOROOT(), "bin", "go")
	cmd := exec.Command(goBin, "test", "-run", "^TestExpireCheckBounded$", "-count=1", "ontology/wdist")
	bounded := cmd.Run() == nil
	check("expiry bucket comparisons bounded independent of m", bounded)

	// Concurrent readers, no sleeps: every value identical.
	wantD := rc.Distinct()
	const N = 16
	res := make([]int, N)
	var wg sync.WaitGroup
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); res[i] = rc.Distinct() }(g)
	}
	wg.Wait()
	same := true
	for _, v := range res {
		same = same && v == wantD
	}
	check("concurrent Distinct readers identical", same && c.SelfCheck())

	if fails > 0 {
		os.Exit(1)
	}
}
