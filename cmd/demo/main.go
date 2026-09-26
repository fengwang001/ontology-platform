package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/coord"
	"ontology/pt"
)

var failed bool

func check(name string, ok bool) {
	if ok {
		fmt.Println("OK  " + name)
	} else {
		fmt.Println("FAIL " + name)
		failed = true
	}
}

// sixSteps replays the S1..S6 scenario from NOTES.md (n=3).
func sixSteps() bool {
	c := coord.New(3)
	type want struct{ yes, no int }
	steps := []want{}
	for _, v := range []bool{true, true, false} { // S1..S3
		p := len(steps)
		if c.Vote(p, v) != nil {
			return false
		}
		y, n := c.Counts()
		steps = append(steps, want{y, n})
	}
	if steps[0] != (want{1, 0}) || steps[1] != (want{2, 0}) || steps[2] != (want{2, 1}) {
		return false
	}
	d, err := c.Decide() // S4: no vote exists -> Abort
	if err != nil || d != coord.Abort {
		return false
	}
	if !errors.Is(c.Vote(2, true), coord.ErrAlreadyVoted) { // S5: rejected
		return false
	}
	if y, n := c.Counts(); y != 2 || n != 1 { // state unchanged
		return false
	}
	if d, _ = c.Decide(); d != coord.Abort {
		return false
	}
	c2 := coord.New(3) // S6: new transaction, participant 2 never votes
	if c2.Vote(0, true) != nil || c2.Vote(1, true) != nil {
		return false
	}
	return c2.Recover() == coord.Abort
}

func main() {
	var s pt.State
	ok := s.Cast(false) == nil && s.Vote() == pt.No &&
		errors.Is(s.Cast(true), pt.ErrAlreadyVoted) && s.Vote() == pt.No
	check("pt: vote immutable", ok)

	check("coord: six-step S1..S6 (Abort,reject,Abort)", sixSteps())

	// Unanimity: only all-yes commits; a single no aborts.
	all := coord.New(5)
	for p := 0; p < 5; p++ {
		all.Vote(p, true)
	}
	dAll, _ := all.Decide()
	one := coord.New(5)
	for p := 0; p < 5; p++ {
		one.Vote(p, p != 3)
	}
	dOne, _ := one.Decide()
	check("coord: unanimity (all-yes Commit, one-no Abort)",
		dAll == coord.Commit && dOne == coord.Abort)

	// Three distinct, decidable rejection errors; state survives them.
	c := coord.New(2)
	c.Vote(0, true)
	e1 := c.Vote(-1, true)
	e2 := c.Vote(0, false)
	_, e3 := coord.New(2).Decide()
	y, _ := c.Counts()
	d, err := c.Decide()
	check("coord: 3 distinct errors, state unchanged",
		errors.Is(e1, coord.ErrOutOfRange) && errors.Is(e2, coord.ErrAlreadyVoted) &&
			errors.Is(e3, coord.ErrNotAllVoted) && e1 != e2 && e2 != e3 && e1 != e3 &&
			y == 1 && errors.Is(err, coord.ErrNotAllVoted) && d == coord.Undecided)

	// Large m: Decide stays correct (O(1) proof is coord's internal test).
	big := coord.New(10000)
	for p := 0; p < 10000; p++ {
		big.Vote(p, true)
	}
	dBig, _ := big.Decide()
	check("coord: m=10000 all-yes Decide==Commit (O(1), see test)", dBig == coord.Commit)

	// Concurrent votes on distinct participants, then Decide.
	cc := coord.New(64)
	var wg sync.WaitGroup
	for p := 0; p < 64; p++ {
		wg.Add(1)
		go func(p int) { defer wg.Done(); cc.Vote(p, true) }(p)
	}
	wg.Wait()
	dc, _ := cc.Decide()
	check("coord: 64 concurrent votes -> Commit", dc == coord.Commit)

	check("api: SelfCheck (4 invariants)", api.SelfCheck() == nil)

	if failed {
		os.Exit(1)
	}
}
