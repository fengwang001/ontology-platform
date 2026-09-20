// Command demo exercises every documented semantic of the fsm package
// and prints one OK/FAIL verdict line per semantic.
package main

import (
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"

	"ontology/fsm"
)

func table() []fsm.Transition {
	return []fsm.Transition{
		{From: "idle", Event: "dial", To: "active"},
		{From: "active", Event: "msg", To: "active"},
		{From: "active", Event: "bye", To: "closed"},
	}
}

func session() *fsm.Machine {
	m, err := fsm.New("idle", []fsm.State{"closed"}, table())
	if err != nil {
		panic(err)
	}
	return m
}

func checkConstructor() bool {
	dup := append(table(), fsm.Transition{From: "idle", Event: "dial", To: "closed"})
	if _, err := fsm.New("idle", nil, dup); err == nil {
		return false
	}
	if _, err := fsm.New("ghost", nil, table()); err == nil {
		return false
	}
	_, err := fsm.New("idle", []fsm.State{"closed", "archived"}, table())
	return err == nil
}

func checkIllegalEvent() bool {
	m := session()
	var actions int
	m.OnEntry("active", func() error { actions++; return nil })
	m.OnExit("idle", func() { actions++ })
	ch := m.Observe()
	for i := 0; i < 100; i++ {
		if _, err := m.Fire("nope"); !errors.Is(err, fsm.ErrNoTransition) {
			return false
		}
	}
	ok := m.State() == "idle" && actions == 0 && len(m.Log()) == 0 && len(ch) == 0
	_, err := m.Fire("dial")
	return ok && err == nil
}

func checkSelfTransition() bool {
	m := session()
	m.Fire("dial") //nolint:errcheck
	var actions int
	m.OnEntry("active", func() error { actions++; return nil })
	m.OnExit("active", func() { actions++ })
	ch := m.Observe()
	if _, err := m.Fire("msg"); err != nil {
		return false
	}
	return actions == 0 && len(m.Log()) == 2 && <-ch == "active"
}

func checkEntryFailure() bool {
	m := session()
	boom := errors.New("boom")
	fail := true
	var exits int
	m.OnExit("idle", func() { exits++ })
	m.OnEntry("active", func() error {
		if fail {
			return boom
		}
		return nil
	})
	ch := m.Observe()
	_, err := m.Fire("dial")
	if !errors.Is(err, fsm.ErrEntryFailed) || !errors.Is(err, boom) {
		return false
	}
	ok := m.State() == "idle" && len(m.Log()) == 0 && len(ch) == 0 && exits == 1
	fail = false
	_, err = m.Fire("dial")
	return ok && err == nil && exits == 2
}

func checkTerminal() bool {
	m := session()
	var entries, exits int
	m.OnEntry("closed", func() error { entries++; return nil })
	m.OnExit("closed", func() { exits++ })
	m.Fire("dial") //nolint:errcheck
	m.Fire("bye")  //nolint:errcheck
	logLen := len(m.Log())
	for _, e := range []fsm.Event{"bye", "msg"} {
		if _, err := m.Fire(e); !errors.Is(err, fsm.ErrTerminal) {
			return false
		}
	}
	return entries == 1 && exits == 0 && m.State() == "closed" && len(m.Log()) == logLen
}

func checkActionOrder() bool {
	m := session()
	var seq []string
	m.OnExit("idle", func() { seq = append(seq, "x1") })
	m.OnExit("idle", func() { seq = append(seq, "x2") })
	m.OnEntry("active", func() error { seq = append(seq, "n1"); return nil })
	m.OnEntry("active", func() error { seq = append(seq, "n2"); return nil })
	if _, err := m.Fire("dial"); err != nil {
		return false
	}
	return slices.Equal(seq, []string{"x1", "x2", "n1", "n2"})
}

func checkLogObserve() bool {
	m := session()
	ch1, ch2 := m.Observe(), m.Observe()
	for _, e := range []fsm.Event{"dial", "msg", "bye"} {
		m.Fire(e) //nolint:errcheck
	}
	log := m.Log()
	if len(log) != 3 {
		return false
	}
	for _, tr := range log {
		if <-ch1 != tr.To || <-ch2 != tr.To {
			return false
		}
	}
	log[0].To = "corrupted"
	return m.Log()[0].To == "active"
}

func checkConcurrency() bool {
	m, err := fsm.New("a", nil, []fsm.Transition{
		{From: "a", Event: "ping", To: "b"},
		{From: "b", Event: "pong", To: "a"},
	})
	if err != nil {
		return false
	}
	m.Observe() // slow observer: never drained, must not block Fire
	var successes atomic.Int64
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				e := fsm.Event("ping")
				if (g+i)%2 == 1 {
					e = "pong"
				}
				if _, err := m.Fire(e); err == nil {
					successes.Add(1)
				}
			}
		}(g)
	}
	wg.Wait()
	return int64(len(m.Log())) == successes.Load()
}

func main() {
	checks := []struct {
		name string
		fn   func() bool
	}{
		{"1 constructor validation", checkConstructor},
		{"2 illegal event zero side effects", checkIllegalEvent},
		{"3 self transition skips actions", checkSelfTransition},
		{"4 entry failure keeps source", checkEntryFailure},
		{"5 terminal absorbs events", checkTerminal},
		{"6 actions exactly once, in order", checkActionOrder},
		{"7 log matches observers, log is a copy", checkLogObserve},
		{"8 concurrent fire, exact log", checkConcurrency},
	}
	for _, c := range checks {
		verdict := "OK"
		if !c.fn() {
			verdict = "FAIL"
		}
		fmt.Printf("%-42s %s\n", c.name, verdict)
	}
}
