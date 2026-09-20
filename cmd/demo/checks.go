package main

import (
	"errors"
	"sync"

	"ontology/fsm"
)

func checkConstruction() bool {
	if _, err := fsm.New("A", nil, []fsm.Transition{{From: "A", Event: "e", To: "B"}}); err != nil {
		return false
	}
	if _, err := fsm.New("A", nil, []fsm.Transition{
		{From: "A", Event: "e", To: "B"},
		{From: "A", Event: "e", To: "A"},
	}); err == nil {
		return false
	}
	_, err := fsm.New("Z", nil, []fsm.Transition{{From: "A", Event: "e", To: "B"}})
	if err == nil { // initial 未在表中出现，必须报错
		return false
	}
	// initial 只出现在 To、终态引用未出现状态，均合法。
	_, err = fsm.New("B", []fsm.State{"Z"}, []fsm.Transition{{From: "A", Event: "e", To: "B"}})
	return err == nil
}

func checkIllegal() bool {
	var calls int
	m, _ := fsm.New("A", nil, []fsm.Transition{{From: "A", Event: "go", To: "B"}})
	m.OnExit("A", func() { calls++ })
	m.OnEntry("B", func() error { calls++; return nil })
	sub := m.Observe()
	for i := 0; i < 100; i++ {
		if _, err := m.Fire("bad"); !errors.Is(err, fsm.ErrNoTransition) {
			return false
		}
	}
	select {
	case <-sub:
		return false
	default:
	}
	s, err := m.Fire("go")
	return s == "B" && err == nil && calls == 2 && len(m.Log()) == 1
}

func checkSelf() bool {
	var calls int
	m, _ := fsm.New("A", nil, []fsm.Transition{{From: "A", Event: "tick", To: "A"}})
	m.OnExit("A", func() { calls++ })
	m.OnEntry("A", func() error { calls++; return nil })
	sub := m.Observe()
	s, err := m.Fire("tick")
	return s == "A" && err == nil && calls == 0 && len(m.Log()) == 1 && <-sub == "A"
}

func checkEntryFail() bool {
	boom := errors.New("boom")
	var exits, tries int
	m, _ := fsm.New("A", nil, []fsm.Transition{{From: "A", Event: "go", To: "B"}})
	m.OnExit("A", func() { exits++ })
	m.OnEntry("B", func() error {
		tries++
		if tries == 1 {
			return boom
		}
		return nil
	})
	_, err := m.Fire("go")
	if !errors.Is(err, fsm.ErrEntryFailed) || !errors.Is(err, boom) || exits != 1 {
		return false
	}
	if m.State() != "A" || len(m.Log()) != 0 {
		return false
	}
	s, err := m.Fire("go")
	return s == "B" && err == nil && exits == 2 && len(m.Log()) == 1
}

func checkTerminal() bool {
	var entry, exit int
	m, _ := fsm.New("A", []fsm.State{"D"}, []fsm.Transition{
		{From: "A", Event: "go", To: "D"},
		{From: "D", Event: "go", To: "A"},
	})
	m.OnEntry("D", func() error { entry++; return nil })
	m.OnExit("D", func() { exit++ })
	if _, err := m.Fire("go"); err != nil {
		return false
	}
	for i := 0; i < 4; i++ {
		if _, err := m.Fire("go"); !errors.Is(err, fsm.ErrTerminal) {
			return false
		}
	}
	return m.State() == "D" && entry == 1 && exit == 0 && len(m.Log()) == 1
}

func checkOrdering() bool {
	var got []string
	add := func(tag string) func() { return func() { got = append(got, tag) } }
	m, _ := fsm.New("A", nil, []fsm.Transition{{From: "A", Event: "go", To: "B"}})
	m.OnExit("A", add("x1"))
	m.OnExit("A", add("x2"))
	m.OnEntry("B", func() error { got = append(got, "e1"); return nil })
	m.OnEntry("B", func() error { got = append(got, "e2"); return nil })
	if _, err := m.Fire("go"); err != nil {
		return false
	}
	want := []string{"x1", "x2", "e1", "e2"}
	if len(got) != 4 {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func checkLogObserve() bool {
	table := []fsm.Transition{
		{From: "A", Event: "e1", To: "B"},
		{From: "B", Event: "e2", To: "C"},
		{From: "C", Event: "e3", To: "A"},
	}
	m, _ := fsm.New("A", nil, table)
	sub := m.Observe()
	for _, e := range []fsm.Event{"e1", "e2", "e3"} {
		if _, err := m.Fire(e); err != nil {
			return false
		}
	}
	log := m.Log()
	if len(log) != 3 {
		return false
	}
	for _, tr := range log {
		if <-sub != tr.To {
			return false
		}
	}
	log[0] = fsm.Transition{}
	return m.Log()[0].Event == "e1"
}

func checkConcurrency() bool {
	// 并发正确性主要由 go test -race 覆盖；这里做一次小规模烟雾测试。
	done := make(chan struct{})
	go func() {
		defer close(done)
		m, _ := fsm.New("A", nil, []fsm.Transition{
			{From: "A", Event: "inc", To: "B"},
			{From: "B", Event: "back", To: "A"},
		})
		_ = m.Observe() // 不读取，验证丢弃不阻塞
		var wg sync.WaitGroup
		for w := 0; w < 8; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := 0; i < 100; i++ {
					if s, err := m.Fire("inc"); err == nil && s == "B" {
						m.Fire("back")
					}
				}
			}()
		}
		wg.Wait()
		if m.State() != "A" || len(m.Log())%2 != 0 {
			panic("inconsistent concurrent state")
		}
	}()
	<-done
	return true
}
