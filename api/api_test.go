package api_test

import (
	"errors"
	"ontology/api"
	"ontology/keep"
	"sync"
	"testing"
)

func TestSpecScenario(t *testing.T) {
	rows := []struct {
		act                bool
		t                  int64
		sent, became, dead bool
		la                 int64
		probes             int
	}{
		{true, 0, false, false, false, 0, 0}, {false, 100, true, false, false, 0, 1},
		{false, 130, true, false, false, 0, 2}, {true, 145, false, false, false, 145, 0},
		{false, 245, true, false, false, 145, 1}, {false, 275, true, false, false, 145, 2},
		{false, 305, true, false, false, 145, 3}, {false, 335, false, true, true, 145, 3},
	}
	k, _ := api.New(100, 30, 3)
	for i, r := range rows {
		var sent, became bool
		var err error
		if r.act {
			err = k.Activity(r.t)
		} else {
			sent, became, err = k.Tick(r.t)
		}
		if err != nil || sent != r.sent || became != r.became || k.Dead() != r.dead ||
			k.LastActive() != r.la || k.Probes() != r.probes {
			t.Fatalf("step %d: sent=%v became=%v p=%d d=%v la=%d err=%v",
				i+1, sent, became, k.Probes(), k.Dead(), k.LastActive(), err)
		}
	}
}

func TestDeathAndReset(t *testing.T) {
	k, _ := api.New(100, 30, 3)
	_ = k.Activity(0)
	for _, at := range []int64{100, 130, 160} {
		if _, became, err := k.Tick(at); err != nil || became || k.Dead() {
			t.Fatalf("wrongly died at %d", at)
		}
	}
	if err := k.Activity(189); err != nil || k.Probes() != 0 || k.Dead() {
		t.Fatalf("reset did not abort: p=%d d=%v err=%v", k.Probes(), k.Dead(), err)
	}
	if _, became, _ := k.Tick(190); became || k.Dead() {
		t.Fatal("reset connection wrongly died at 190")
	}
	k2, _ := api.New(100, 30, 3)
	_ = k2.Activity(0)
	for _, at := range []int64{100, 130, 160, 190} {
		_, _, _ = k2.Tick(at)
	}
	if !k2.Dead() {
		t.Fatal("control connection should be dead at 190")
	}
}

func TestProbeTiming(t *testing.T) {
	cases := []struct {
		t      int64
		sent   bool
		probes int
	}{{99, false, 0}, {100, true, 1}, {129, false, 1}, {130, true, 2}}
	k, _ := api.New(100, 30, 3)
	_ = k.Activity(0)
	for _, c := range cases {
		sent, _, err := k.Tick(c.t)
		if err != nil || sent != c.sent || k.Probes() != c.probes {
			t.Fatalf("tick %d: sent=%v p=%d want %v/%d", c.t, sent, k.Probes(), c.sent, c.probes)
		}
	}
}

func TestRejectedOpsNoTrace(t *testing.T) {
	k, _ := api.New(100, 30, 3)
	_ = k.Activity(0)
	for _, at := range []int64{100, 130, 160, 190} {
		_, _, _ = k.Tick(at)
	}
	if err := k.Activity(191); !errors.Is(err, keep.ErrDead) || k.Probes() != 3 || !k.Dead() {
		t.Fatalf("dead activity: %v", err)
	}
	k2, _ := api.New(100, 30, 3)
	_ = k2.Activity(10)
	if _, _, err := k2.Tick(9); !errors.Is(err, keep.ErrClockBack) {
		t.Fatalf("tick back: %v", err)
	}
	if err := k2.Activity(8); !errors.Is(err, keep.ErrClockBack) {
		t.Fatalf("activity back: %v", err)
	}
	if k2.LastActive() != 10 || k2.Probes() != 0 || k2.Dead() {
		t.Fatal("rejection left a trace")
	}
	if _, _, err := k2.Tick(110); err != nil || k2.Probes() != 1 {
		t.Fatalf("unusable after reject: %v", err)
	}
	if _, e := api.New(0, 1, 1); !errors.Is(e, keep.ErrInvalidParam) ||
		errors.Is(e, keep.ErrDead) || errors.Is(e, keep.ErrClockBack) {
		t.Fatalf("sentinel errors not distinct: %v", e)
	}
}

func TestSelfCheck(t *testing.T) {
	k, _ := api.New(100, 30, 3)
	if err := k.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

func TestConcurrentReaders(t *testing.T) {
	k, _ := api.New(100, 30, 3)
	_ = k.Activity(0)
	_, _, _ = k.Tick(100)
	_, _, _ = k.Tick(130)
	const n = 32
	type snap struct {
		p  int
		d  bool
		la int64
	}
	res := make([]snap, n)
	gate := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-gate
			if err := k.SelfCheck(); err != nil {
				t.Errorf("concurrent SelfCheck: %v", err)
			}
			res[i] = snap{k.Probes(), k.Dead(), k.LastActive()}
		}(i)
	}
	close(gate)
	wg.Wait()
	for i, s := range res {
		if s != (snap{2, false, 0}) {
			t.Fatalf("reader %d got %+v", i, s)
		}
	}
}
