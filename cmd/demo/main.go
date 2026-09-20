// demo 逐条验证 fsm 包的 8 项语义并打印 OK/FAIL。
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"ontology/fsm"
)

var failures int

func check(name string, ok bool) {
	verdict := "OK  "
	if !ok {
		verdict = "FAIL"
		failures++
	}
	fmt.Printf("%s %s\n", verdict, name)
}

func table() []fsm.Transition {
	return []fsm.Transition{
		{From: "idle", Event: "connect", To: "active"},
		{From: "active", Event: "ping", To: "active"},
		{From: "active", Event: "close", To: "done"},
		{From: "done", Event: "reset", To: "idle"},
	}
}

func newMachine() *fsm.Machine {
	m, err := fsm.New("idle", []fsm.State{"done"}, table())
	if err != nil {
		fmt.Println("FATAL", err)
		os.Exit(1)
	}
	return m
}

func main() {
	// 1. 构造期校验：重复 (From,Event) 报错；initial 不在表中报错；游离终态合法。
	dup := append(table(), fsm.Transition{From: "idle", Event: "connect", To: "active"})
	_, errDup := fsm.New("idle", []fsm.State{"done"}, dup)
	_, errInit := fsm.New("nowhere", nil, table())
	_, errFree := fsm.New("idle", []fsm.State{"done", "archived"}, table())
	check("1 构造期校验", errDup != nil && errInit != nil && errFree == nil)

	// 2. 非法事件零副作用，之后机器仍可用。
	m := newMachine()
	zero := true
	for i := 0; i < 100; i++ {
		_, err := m.Fire("bogus")
		zero = zero && errors.Is(err, fsm.ErrNoTransition)
	}
	untouched := m.State() == "idle" && len(m.Log()) == 0
	s2, err2 := m.Fire("connect")
	check("2 非法事件零副作用", zero && untouched && err2 == nil && s2 == "active")

	// 3. 自转移不触发 entry/exit，但记日志。
	var entries, exits int
	m.OnEntry("active", func() error { entries++; return nil })
	m.OnExit("active", func() { exits++ })
	_, err3 := m.Fire("ping")
	check("3 自转移不触发动作", err3 == nil && entries == 0 && exits == 0 && len(m.Log()) == 2)

	// 4. entry 失败整体不迁移，重试时 exit 恰好再跑一次。
	m4 := newMachine()
	fail := true
	exits4 := 0
	m4.OnExit("idle", func() { exits4++ })
	m4.OnEntry("active", func() error {
		if fail {
			return errors.New("boom")
		}
		return nil
	})
	_, err4 := m4.Fire("connect")
	stayed := errors.Is(err4, fsm.ErrEntryFailed) && m4.State() == "idle" && len(m4.Log()) == 0
	fail = false
	_, err4 = m4.Fire("connect")
	check("4 entry失败不迁移", stayed && err4 == nil && exits4 == 2)

	// 5. 终态吸收一切事件。
	m5 := newMachine()
	_, _ = m5.Fire("connect")
	_, _ = m5.Fire("close")
	logLen := len(m5.Log())
	absorbed := true
	for _, e := range []fsm.Event{"reset", "connect"} {
		_, err := m5.Fire(e)
		absorbed = absorbed && errors.Is(err, fsm.ErrTerminal)
	}
	check("5 终态吸收", absorbed && m5.State() == "done" && len(m5.Log()) == logLen)

	// 6. 动作恰好一次且先 exit 后 entry。
	m6 := newMachine()
	var seq []string
	m6.OnExit("idle", func() { seq = append(seq, "exit") })
	m6.OnEntry("active", func() error { seq = append(seq, "entry"); return nil })
	_, _ = m6.Fire("connect")
	check("6 动作恰好一次", fmt.Sprint(seq) == "[exit entry]")

	// 7. 日志与观察者一一对应。
	m7 := newMachine()
	ch := m7.Observe()
	for _, e := range []fsm.Event{"connect", "ping", "close"} {
		_, _ = m7.Fire(e)
	}
	consistent := true
	for _, tr := range m7.Log() {
		select {
		case s := <-ch:
			consistent = consistent && s == tr.To
		case <-time.After(100 * time.Millisecond):
			consistent = false
		}
	}
	check("7 日志与观察一致", consistent && len(m7.Log()) == 3)

	// 8. 并发 Fire 日志不重不漏，慢观察者不阻塞。
	m8, _ := fsm.New("s0", nil, []fsm.Transition{
		{From: "s0", Event: "go", To: "s1"},
		{From: "s1", Event: "go", To: "s0"},
	})
	_ = m8.Observe()
	var wg sync.WaitGroup
	var mu sync.Mutex
	succeeded := 0
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				if _, err := m8.Fire("go"); err == nil {
					mu.Lock()
					succeeded++
					mu.Unlock()
				}
			}
		}()
	}
	wg.Wait()
	check("8 并发安全", len(m8.Log()) == succeeded && succeeded > 0)

	if failures > 0 {
		fmt.Printf("%d 项失败\n", failures)
		os.Exit(1)
	}
	fmt.Println("全部 8 项语义通过")
}
