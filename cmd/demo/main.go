package main

import (
	"errors"
	"fmt"
	"sync"

	"ontology/fsm"
)

var errBoom = errors.New("boom")

func report(no int, name string, ok bool) {
	status := "FAIL"
	if ok {
		status = "OK"
	}
	fmt.Printf("%d. %-28s %s\n", no, name, status)
}

func main() {
	results := make([]bool, 8)

	// 1. 构造期校验：重复转移/初始状态缺失报错，孤立终态合法。
	_, dupErr := fsm.New("idle", nil, []fsm.Transition{
		{From: "idle", Event: "go", To: "run"},
		{From: "idle", Event: "go", To: "x"},
	})
	_, missingErr := fsm.New("ghost", nil, []fsm.Transition{
		{From: "idle", Event: "go", To: "run"},
	})
	_, termErr := fsm.New("idle", []fsm.State{"never"}, []fsm.Transition{
		{From: "idle", Event: "go", To: "run"},
	})
	results[0] = dupErr != nil && missingErr != nil && termErr == nil

	// 2. 非法事件零副作用：连打 100 个后合法事件仍可用。
	m2 := mustNew("idle", nil, []fsm.Transition{
		{From: "idle", Event: "go", To: "run"},
	})
	hits := 0
	m2.OnExit("idle", func() { hits++ })
	illegalOK := true
	for i := 0; i < 100; i++ {
		s, err := m2.Fire("bad")
		if !errors.Is(err, fsm.ErrNoTransition) || s != "idle" {
			illegalOK = false
		}
	}
	_, goErr := m2.Fire("go")
	results[1] = illegalOK && hits == 0 && len(m2.Log()) == 1 && goErr == nil

	// 3. 自转移不触发动作，但记 Log、通知观察者。
	m3 := mustNew("idle", nil, []fsm.Transition{
		{From: "idle", Event: "stay", To: "idle"},
	})
	acts := 0
	m3.OnExit("idle", func() { acts++ })
	m3.OnEntry("idle", func() error { acts++; return nil })
	obs3 := m3.Observe()
	s3, _ := m3.Fire("stay")
	<-obs3
	results[2] = s3 == "idle" && acts == 0 && len(m3.Log()) == 1

	// 4. entry 失败整体不迁移；重试时 exit 总计恰好两次。
	m4 := mustNew("idle", nil, []fsm.Transition{
		{From: "idle", Event: "go", To: "run"},
	})
	exits := 0
	m4.OnExit("idle", func() { exits++ })
	fail := true
	m4.OnEntry("run", func() error {
		if fail {
			return errBoom
		}
		return nil
	})
	_, e4 := m4.Fire("go")
	fail = false
	_, retryErr := m4.Fire("go")
	results[3] = errors.Is(e4, fsm.ErrEntryFailed) && errors.Is(e4, errBoom) &&
		m4.State() == "run" && exits == 2 && len(m4.Log()) == 1 && retryErr == nil

	// 5. 终态吸收：任何事件 ErrTerminal，entry 一次、exit 零次。
	m5 := mustNew("idle", []fsm.State{"done"}, []fsm.Transition{
		{From: "idle", Event: "go", To: "done"},
		{From: "done", Event: "go", To: "idle"},
	})
	ent, ext := 0, 0
	m5.OnEntry("done", func() error { ent++; return nil })
	m5.OnExit("done", func() { ext++ })
	_, _ = m5.Fire("go")
	_, termErr2 := m5.Fire("go")
	results[4] = errors.Is(termErr2, fsm.ErrTerminal) &&
		m5.State() == "done" && len(m5.Log()) == 1 && ent == 1 && ext == 0

	// 6. 动作恰好一次、先 exit 后 entry、按注册顺序。
	m6 := mustNew("a", nil, []fsm.Transition{
		{From: "a", Event: "e", To: "b"},
	})
	var order []string
	m6.OnExit("a", func() { order = append(order, "x1") })
	m6.OnExit("a", func() { order = append(order, "x2") })
	m6.OnEntry("b", func() error { order = append(order, "n1"); return nil })
	m6.OnEntry("b", func() error { order = append(order, "n2"); return nil })
	_, _ = m6.Fire("e")
	results[5] = len(order) == 4 &&
		order[0] == "x1" && order[1] == "x2" && order[2] == "n1" && order[3] == "n2"

	// 7. Log 与观察者一一对应；Log 返回副本。
	m7 := mustNew("a", []fsm.State{"c"}, []fsm.Transition{
		{From: "a", Event: "1", To: "b"},
		{From: "b", Event: "2", To: "c"},
	})
	obs7 := m7.Observe()
	_, _ = m7.Fire("1")
	_, _ = m7.Fire("2")
	log7 := m7.Log()
	match := len(log7) == 2
	for _, tr := range log7 {
		if tr.To != <-obs7 {
			match = false
		}
	}
	log7[0] = fsm.Transition{}
	results[6] = match && m7.Log()[0].To == "b"

	// 8. 并发：多次并发 Fire 不重不漏，Log 严格有序。
	const n = 100
	table := make([]fsm.Transition, 0, n)
	for i := 0; i < n; i++ {
		table = append(table, fsm.Transition{
			From: fsm.State(fmt.Sprintf("s%d", i)),
			Event: fsm.Event(fmt.Sprintf("e%d", i)),
			To:   fsm.State(fmt.Sprintf("s%d", i+1)),
		})
	}
	m8 := mustNew("s0", nil, table)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		ev := fsm.Event(fmt.Sprintf("e%d", i))
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				if _, err := m8.Fire(ev); errors.Is(err, fsm.ErrNoTransition) {
					continue
				}
				return
			}
		}()
	}
	wg.Wait()
	allOK := len(m8.Log()) == n
	results[7] = allOK

	names := []string{
		"constructor validation",
		"illegal event no side effect",
		"self-transition skips actions",
		"entry failure no migration",
		"terminal absorbs events",
		"exactly-once action order",
		"log/observe consistency",
		"concurrent fire race-safe",
	}
	allPass := true
	for i, name := range names {
		report(i+1, name, results[i])
		allPass = allPass && results[i]
	}
	if allPass {
		fmt.Println("ALL SEMANTICS OK")
	} else {
		fmt.Println("SOME SEMANTICS FAILED")
	}
}

func mustNew(initial fsm.State, terminals []fsm.State, table []fsm.Transition) *fsm.Machine {
	m, err := fsm.New(initial, terminals, table)
	if err != nil {
		panic(err)
	}
	return m
}

