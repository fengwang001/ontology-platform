// Command demo exercises the retransmission timer and prints OK/FAIL lines.
package main

import (
	"errors"
	"fmt"
	"sync"

	"ontology/internal/api"
	"ontology/internal/rto"
	"ontology/internal/rtx"
)

func report(name string, ok bool) {
	if ok {
		fmt.Println("OK   " + name)
	} else {
		fmt.Println("FAIL " + name)
	}
}

// eightStep verifies every NOTES.md row (rto/deadline/armed/timeout/seq).
func eightStep() bool {
	s := rto.New(10)
	ops := [][3]int64{
		{'s', 0, 0}, {'t', 0, 10}, {'t', 0, 20}, {'t', 0, 30},
		{'a', 1, 40}, {'s', 1, 50}, {'t', 0, 60}, {'a', 2, 60},
	}
	want := [][5]int64{
		{10, 10, 1, 0, 0}, {20, 30, 1, 1, 0}, {20, 30, 1, 0, 0}, {40, 70, 1, 1, 0},
		{10, 0, 0, 0, 0}, {10, 60, 1, 0, 0}, {20, 80, 1, 1, 1}, {10, 0, 0, 0, 0},
	}
	bit := func(v bool) int64 {
		if v {
			return 1
		}
		return 0
	}
	for i, o := range ops {
		var to bool
		var seq int64
		switch o[0] {
		case 's':
			s.Send(o[1], o[2])
		case 'a':
			s.ApplyAck(o[1], o[2])
		case 't':
			to, seq = s.Probe(o[2])
		}
		got := [5]int64{s.RTO(), s.Deadline(), bit(s.HasDeadline()), bit(to), seq}
		if got != want[i] {
			fmt.Printf("step %d got %v want %v\n", i+1, got, want[i])
			return false
		}
	}
	return true
}

// sameInstant checks (乙): at now==deadline event order decides the outcome.
func sameInstant() bool {
	e1, _ := rtx.New(10) // Tick then Ack: left-closed equality fires
	if e1.Send(1, 50) != nil {
		return false
	}
	to, r, err := e1.Tick(60)
	if err != nil || !to || r != 1 || e1.Ack(2, 60) != nil {
		return false
	}
	e2, _ := rtx.New(10) // Ack then Tick: segment gone, no avoidable retransmit
	if e2.Send(1, 50) != nil || e2.Ack(2, 60) != nil {
		return false
	}
	to, _, err = e2.Tick(60)
	return err == nil && !to && !e2.State().HasDeadline()
}

// failures checks the four distinct sentinels, no-trace, and reuse.
func failures() bool {
	if _, err := rtx.New(0); !errors.Is(err, rtx.ErrInvalidRTO) {
		return false
	}
	e, _ := rtx.New(10)
	if e.Send(0, 0) != nil {
		return false
	}
	snap := func() [4]int64 {
		st := e.State()
		return [4]int64{st.Base(), st.RTO(), st.Deadline(), int64(st.PendingCount())}
	}
	cases := []struct {
		err error
		fn  func() error
	}{
		{rtx.ErrSeqOutOfOrder, func() error { return e.Send(0, 0) }},
		{rtx.ErrNegativeAck, func() error { return e.Ack(-1, 0) }},
		{rtx.ErrClockRollback, func() error { return e.Send(1, -1) }},
	}
	seen := map[error]bool{rtx.ErrInvalidRTO: true}
	for _, c := range cases {
		before := snap()
		if !errors.Is(c.fn(), c.err) || snap() != before {
			return false
		}
		seen[c.err] = true
	}
	return len(seen) == 4 && e.Send(1, 5) == nil
}

// concurrentReaders checks (六): identical reads, barrier, no sleeps.
func concurrentReaders() bool {
	t, _ := api.New(10)
	if t.Send(0, 0) != nil {
		return false
	}
	if to, _ := t.Tick(10); !to {
		return false
	}
	type view struct {
		rto, base, dl int64
		armed         bool
	}
	views := make([]view, 64)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			views[i] = view{t.RTO(), t.Base(), t.Deadline(), t.HasDeadline()}
		}(i)
	}
	close(start)
	wg.Wait()
	for i := 1; i < 64; i++ {
		if views[i] != views[0] {
			return false
		}
	}
	return views[0] == view{20, 0, 30, true}
}

func main() {
	report("eight-step table rto/deadline + steps 2/4/7 timeout/seq", eightStep())
	report("probe inspects only earliest (m=100..10000); Ack resets rto", rto.VerifyProbeBound() == nil)
	report("same-instant: Tick->Ack retransmits, Ack->Tick avoids", sameInstant())
	report("4 distinct errors, no trace on reject, still usable", failures())
	t, _ := api.New(10)
	report("SelfCheck I1..I4; 64 concurrent readers identical", t.SelfCheck() == nil && concurrentReaders())
}
