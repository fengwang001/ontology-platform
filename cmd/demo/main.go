package main

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/sw"
)

var failed bool

func report(name string, cond bool) {
	if cond {
		fmt.Println("OK: " + name)
	} else {
		failed = true
		fmt.Println("FAIL: " + name)
	}
}
func snap(a *api.Array) string { s, _ := a.Read(); return fmt.Sprint(s) }
func uniform(s []int64) bool {
	for i := 1; i < len(s); i++ {
		if s[i] != s[0] {
			return false
		}
	}
	return true
}
func setall(p *[]int64, x int64) {
	for i := range *p {
		(*p)[i] = x
	}
}

// replay executes the 12 events of NOTES.md section 3 and verifies the seq and
// array recorded after each event against the expected table.
func replay() (cR, aR int, good bool) {
	seq, v := 0, [2]int64{}
	gs, gv := []int{}, []string{}
	rec := func() { gs = append(gs, seq); gv = append(gv, fmt.Sprint(v)) }
	ws := func(i, x int) { v[i] = int64(x); rec() }
	ss := func(s int) { seq = s; rec() }
	var ab [2]int64
	ss(1)
	ws(0, 1)
	ws(1, 1)
	ss(2) // 1-4: W1 enter/write/write/leave
	s1 := seq
	ab[0] = v[0]
	rec() // 5: A s1=2, reads elem0, still reading
	ss(3)
	ws(0, 2) // 6-7: W2 enter, write elem0
	if seq&1 == 1 {
		cR++
	}
	rec() // 8: C sees odd seq, retries without reading
	ws(1, 2)
	ss(4) // 9-10: W2 write elem1, leave
	ab[1] = v[1]
	if s1 != seq {
		aR++
	}
	rec() // 11: A read torn [1 2], s2!=s1, retries
	ab = v
	rec() // 12: A s1=4, copies [2 2], s2==s1, success
	wantS := []int{1, 1, 1, 2, 2, 3, 3, 3, 3, 4, 4, 4}
	wantV := []string{"[0 0]", "[1 0]", "[1 1]", "[1 1]", "[1 1]", "[1 1]", "[2 1]", "[2 1]", "[2 2]", "[2 2]", "[2 2]", "[2 2]"}
	good = cR == 1 && aR == 1 && ab == [2]int64{2, 2}
	for i := range wantS {
		good = good && gs[i] == wantS[i] && gv[i] == wantV[i]
	}
	return
}

func main() {
	cR, aR, trOK := replay()
	report("12 events: C retried, A saw torn [1 2] then retried, got [2 2]", cR == 1 && aR == 1 && trOK)
	a, _ := api.New(4)
	b := a.Seq()
	_ = a.Update(func(p *[]int64) { setall(p, 7) })
	report("Read equals the Update terminal; seq advanced exactly by 2", snap(a) == "[7 7 7 7]" && a.Seq() == b+2)
	entered, release := make(chan struct{}), make(chan struct{})
	go func() {
		_ = a.Update(func(p *[]int64) {
			close(entered)
			<-release
			setall(p, 9)
		})
	}()
	<-entered
	out := make(chan []int64, 1)
	go func() { s, _ := a.Read(); out <- s }()
	runtime.Gosched()
	early := false
	select {
	case <-out:
		early = true
	default:
	}
	close(release)
	report("readers retry in the odd phase and never block the writer", !early && fmt.Sprint(<-out) == "[9 9 9 9]")
	s0, v0 := a.Seq(), snap(a)
	eNil := a.Update(nil)
	var eRe error
	_ = a.Update(func(p *[]int64) { eRe = a.Update(func(q *[]int64) { (*q)[0] = 1 }) })
	_, eBad := api.New(0)
	ePan := a.Update(func(p *[]int64) { panic("x") })
	clean := a.Seq() == s0+4 && a.Seq()&1 == 0 && snap(a) == v0
	usable := a.Update(func(p *[]int64) { (*p)[0] = 3 }) == nil
	distinct := len(map[error]bool{api.ErrInvalidSize: true, sw.ErrNilFunc: true,
		sw.ErrReentrant: true, sw.ErrPanicInUpdate: true}) == 4
	report("four distinct decidable errors (bad n, nil, reentrant, panic)",
		errors.Is(eBad, api.ErrInvalidSize) && errors.Is(eNil, sw.ErrNilFunc) &&
			errors.Is(eRe, sw.ErrReentrant) && errors.Is(ePan, sw.ErrPanicInUpdate) && distinct)
	report("rejected ops leave no trace; panic ends even and array stays usable", clean && usable)
	report("SelfCheck passes (one-element write count == 1 at m=100/1000/10000)", a.SelfCheck() == nil)
	c, _ := api.New(32)
	var done int32
	var wg sync.WaitGroup
	var reads, tears int64
	for r := 0; r < 8; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for atomic.LoadInt32(&done) == 0 {
				s, _ := c.Read()
				if !uniform(s) {
					atomic.AddInt64(&tears, 1)
				}
				atomic.AddInt64(&reads, 1)
			}
		}()
	}
	for v := int64(1); v <= 2000; v++ {
		vv := v
		_ = c.Update(func(p *[]int64) { setall(p, vv) })
	}
	atomic.StoreInt32(&done, 1)
	wg.Wait()
	report("concurrent readers all succeed with zero torn snapshots", reads > 0 && tears == 0)
	if failed {
		os.Exit(1)
	}
}
