package main

import (
	"errors"
	"fmt"
	"os"

	"ontology/api"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
		fmt.Println("FAIL:", name)
		return
	}
	fmt.Println("OK:", name)
}

func main() {
	log, err := api.New(3)
	if err != nil {
		fmt.Println("FAIL: new:", err)
		os.Exit(1)
	}
	want1 := [6]int64{110, 330, 660, 660, 660, 660}
	want2 := [6]int64{0, 0, 0, 440, 990, 1650}
	wantT := [6]int64{110, 330, 660, 1100, 1650, 2310}
	for i := int64(1); i <= 6; i++ {
		if err := log.Append(i, 10*i); err != nil {
			check(fmt.Sprintf("step%d", i), false)
			continue
		}
		s1, s2, tot := log.SegSum(1), log.SegSum(2), log.Total()
		check(fmt.Sprintf("step%d seg=%d,%d tot=%d", i, s1, s2, tot),
			s1 == want1[i-1] && s2 == want2[i-1] && tot == wantT[i-1])
	}
	r := log.SelfCheck()
	check(fmt.Sprintf("step7 recompute seg=%d,%d tot=%d", r.Seg1[6], r.Seg2[6], r.Tot[6]),
		r.Seg1[6] == 660 && r.Seg2[6] == 1710 && r.Tot[6] == 2370)
	check(fmt.Sprintf("step8 locate first=%d", r.First), r.First == 4)
	check(fmt.Sprintf("traps halfopen-miss=%d wrongE=%d,%d desc=%d", r.HalfMiss, r.WrongTot, r.WrongSeg2, r.Desc),
		r.HalfMiss == 5 && r.HalfSaysClean && r.WrongTot == 231 && r.WrongSeg2 == 165 && r.Desc == 5)
	_, e1 := api.New(0)
	e2 := log.Append(9, 90)
	_, _, e3 := log.Verify(0, 9)
	okErr := errors.Is(e1, api.ErrBadSegSize) && errors.Is(e2, api.ErrSeqGap) && errors.Is(e3, api.ErrBadRange)
	distinct := e1 != e2 && e2 != e3 && e1 != e3
	noTrace := log.Total() == 2310 && log.SegSum(2) == 1650
	check(fmt.Sprintf("errors distinct=%v notrace=%v inv=%v scale=%v conc=%v", distinct, noTrace, r.Inv, r.ScaleOK, r.ConcOK),
		okErr && distinct && noTrace && r.Inv == [4]bool{true, true, true, true} && r.ScaleOK && r.ConcOK)
	if failed {
		os.Exit(1)
	}
}
