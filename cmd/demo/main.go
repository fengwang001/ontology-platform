package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/mon"
)

type snap = struct {
	c, v, mn, mx int64
	avg          float64
	inf          int
	br           bool
}

func get(x *api.Monitor) snap {
	return snap{x.Count(), x.Violations(), x.MinLatency(), x.MaxLatency(),
		x.AvgLatency(), x.InFlight(), x.Breached()}
}

func main() {
	failed := false
	ok := func(name string, pass bool) {
		if pass {
			fmt.Println("OK  ", name)
		} else {
			fmt.Println("FAIL", name)
			failed = true
		}
	}

	// Seven-step sequence; verdict and Breached after every End.
	x, _ := api.New(10, 4, 3)
	steps := []struct {
		id     string
		b, e   int64
		viol   bool
		breach bool
	}{
		{"A", 0, 10, false, false}, {"B", 20, 35, true, false},
		{"C", 40, 45, false, false}, {"D", 50, 61, true, false},
		{"E", 70, 82, true, true}, {"F", 90, 94, false, false},
		{"G", 100, 120, true, true},
	}
	seven := true
	for _, s := range steps {
		beforeV := x.Violations()
		err := x.Begin(s.id, s.b)
		if err == nil {
			err = x.End(s.id, s.e)
		}
		stepViol := x.Violations() == beforeV+1
		seven = seven && err == nil && stepViol == s.viol && x.Breached() == s.breach
	}
	ok("7 steps: verdicts & Breached match NOTES table", seven)
	// Step 1 boundary: A latency 10 == T is OK; step 6 F(OK) clears the alarm.
	y, _ := api.New(10, 4, 3)
	_ = y.Begin("A", 0)
	_ = y.End("A", 10)
	boundary := y.Violations() == 0
	for _, s := range steps[1:5] { // B..E
		_ = y.Begin(s.id, s.b)
		_ = y.End(s.id, s.e)
	}
	_ = y.Begin("F", 90)
	_ = y.End("F", 94)
	ok("step1 lat==10==T is OK; E breaches then F(OK) clears it",
		boundary && x.Breached() /*after G*/ == true && !y.Breached())
	s := get(x)
	ok(fmt.Sprintf("final stats Count=%d Viol=%d Min=%d Max=%d Avg=%g InFlight=%d",
		s.c, s.v, s.mn, s.mx, s.avg, s.inf),
		s.c == 7 && s.v == 4 && s.mn == 4 && s.mx == 20 && s.avg == 11 && s.inf == 0)

	// Four distinct, decidable errors.
	_, e0 := api.New(10, 4, 5)
	_ = x.Begin("d", 0)
	e1 := x.Begin("d", 1)
	e2 := x.End("ghost", 1)
	_ = x.Begin("z", 5)
	e3 := x.End("z", 4)
	ok("four distinct errors invalid/dup/unknown/negative",
		errors.Is(e0, api.ErrInvalidParam) && errors.Is(e1, api.ErrDuplicateBegin) &&
			errors.Is(e2, api.ErrUnknown) && errors.Is(e3, api.ErrNegative))
	before := get(x)
	_ = x.Begin("d", 9)
	_ = x.End("ghost", 9)
	_ = x.End("z", 4)
	ok("rejected ops leave all state unchanged", get(x) == before)
	ok("window-update inspection count constant over m=100..10000", mon.ConstantWindowUpdate())

	// Concurrent read-only access: every goroutine sees the same snapshot.
	const n = 16
	reads := make([]snap, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for g := 0; g < n; g++ {
		go func(g int) { reads[g] = get(x); wg.Done() }(g)
	}
	wg.Wait()
	same := true
	for g := 1; g < n; g++ {
		same = same && reads[g] == reads[0]
	}
	ok("16 concurrent readers see identical stats & Breached", same)
	ok("SelfCheck", api.SelfCheck() == nil)

	if failed {
		os.Exit(1)
	}
}
