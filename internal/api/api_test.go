package api_test

import (
	"errors"
	"math/rand"
	"sync"
	"testing"

	"ontology/internal/api"
)

type view struct {
	base, rto, dl, seq int64
	bo                 int
	armed, to          bool
}

func shot(tm *api.Timer) view {
	return view{base: tm.Base(), rto: tm.RTO(), dl: tm.Deadline(), bo: tm.Backoff(), armed: tm.HasDeadline()}
}

func must(t *testing.T, c bool, m string) {
	if !c {
		t.Fatal(m)
	}
}

var eightOps = [][3]int64{{'s', 0, 0}, {'t', 0, 10}, {'t', 0, 20}, {'t', 0, 30}, {'a', 1, 40}, {'s', 1, 50}, {'t', 0, 60}, {'a', 2, 60}}
var eightWant = []view{{0, 10, 10, 0, 0, true, false}, {0, 20, 30, 0, 1, true, true}, {0, 20, 30, 0, 1, true, false}, {0, 40, 70, 0, 2, true, true}, {1, 10, 0, 0, 0, false, false}, {1, 10, 60, 0, 0, true, false}, {1, 20, 80, 1, 1, true, true}, {2, 10, 0, 0, 0, false, false}}

// TestEightStepDerivation pins every NOTES.md row through the public API.
func TestEightStepDerivation(t *testing.T) {
	tm, _ := api.New(10)
	for i, o := range eightOps {
		to, seq := false, int64(0)
		switch o[0] {
		case 's':
			_ = tm.Send(o[1], o[2])
		case 'a':
			_ = tm.Ack(o[1], o[2])
		default:
			to, seq = tm.Tick(o[2])
		}
		g := shot(tm)
		g.to, g.seq = to, seq
		must(t, g == eightWant[i], "eight step")
	}
}

// TestNaiveReferenceRandom (I1): random legal interleavings vs an independent hand-derived model.
func TestNaiveReferenceRandom(t *testing.T) {
	for seed := int64(1); seed <= 3; seed++ {
		r := rand.New(rand.NewSource(seed))
		tm, _ := api.New(10)
		b, rv, dl, now, nxt := int64(0), int64(10), int64(0), int64(0), int64(0)
		bo, arm := 0, false
		var pend []int64 // strictly increasing live seqs
		for range 1000 {
			now += int64(r.Intn(12))
			switch r.Intn(3) {
			case 0:
				s := nxt
				nxt = s + 1 + int64(r.Intn(2))
				_ = tm.Send(s, now)
				if s >= b {
					if len(pend) == 0 {
						arm, dl = true, now+rv
					}
					pend = append(pend, s)
				}
			case 1:
				a := b
				if r.Intn(2) == 0 {
					a += int64(r.Intn(4))
				}
				_ = tm.Ack(a, now)
				if a > b {
					b, rv, bo = a, 10, 0
					for len(pend) > 0 && pend[0] < a {
						pend = pend[1:]
					}
					arm = len(pend) > 0
					if arm {
						dl = now + rv
					} else {
						dl = 0
					}
				}
			default:
				to, seq := tm.Tick(now)
				if arm && now >= dl {
					must(t, to && seq == pend[0], "earliest timeout")
					rv, bo, dl = rv*2, bo+1, now+rv*2
				} else {
					must(t, !to, "spurious timeout")
				}
			}
			must(t, shot(tm) == view{b, rv, dl, 0, bo, arm, false}, "state mismatch")
		}
	}
}

// TestTimerPointsAtEarliest (I3): the timer follows the earliest pending.
func TestTimerPointsAtEarliest(t *testing.T) {
	tm, _ := api.New(10)
	must(t, tm.Send(0, 0) == nil && tm.Send(1, 0) == nil && tm.Deadline() == 10, "only first arms")
	must(t, tm.Ack(1, 5) == nil && tm.Base() == 1 && tm.Deadline() == 15, "re-arm at earliest")
	to, seq := tm.Tick(15)
	must(t, to && seq == 1, "retransmit 1")
	must(t, tm.Ack(2, 35) == nil && !tm.HasDeadline(), "draining ack stops")
	must(t, tm.Send(2, 40) == nil && tm.Deadline() == 50, "fresh timer after drain")
}

// TestRejectedOpsLeaveNoTrace (I4): distinct sentinels, snapshot fixed.
func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	_, e := api.New(0)
	must(t, errors.Is(e, api.ErrInvalidRTO), "invalid rto")
	must(t, api.ErrInvalidRTO != api.ErrSeqOutOfOrder && api.ErrSeqOutOfOrder != api.ErrNegativeAck && api.ErrNegativeAck != api.ErrClockRollback, "distinct")
	tm, _ := api.New(10)
	_ = tm.Send(0, 0)
	cases := []struct {
		e error
		f func() error
	}{{api.ErrSeqOutOfOrder, func() error { return tm.Send(0, 0) }}, {api.ErrNegativeAck, func() error { return tm.Ack(-1, 0) }}, {api.ErrClockRollback, func() error { return tm.Ack(1, -1) }}}
	for _, c := range cases {
		z := shot(tm)
		must(t, errors.Is(c.f(), c.e) && shot(tm) == z, "no trace")
	}
	must(t, tm.Ack(1, 5) == nil && !tm.HasDeadline() && tm.SelfCheck() == nil, "usable after reject")
}

// TestConcurrentReaders (六): N goroutines read one fed instance identically.
func TestConcurrentReaders(t *testing.T) {
	tm, _ := api.New(10)
	_ = tm.Send(0, 0)
	to, _ := tm.Tick(10)
	must(t, to, "setup timeout")
	views := make([]view, 64)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range views {
		wg.Add(1)
		go func(i int) { defer wg.Done(); <-start; views[i] = shot(tm) }(i)
	}
	close(start)
	wg.Wait()
	for i := 1; i < len(views); i++ {
		must(t, views[i] == views[0], "concurrent read")
	}
}
