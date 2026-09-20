package fsm

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

// 语义 5：终态吸收；终态 entry 执行一次，exit 永不执行。
func TestTerminalAbsorbs(t *testing.T) {
	var entryD, exitD int
	m, _ := New("A", []State{"D"}, []Transition{
		{From: "A", Event: "go", To: "D"},
		{From: "D", Event: "go", To: "A"}, // 终态里的转移也必须被吸收
	})
	m.OnEntry("D", func() error { entryD++; return nil })
	m.OnExit("D", func() { exitD++ })

	if s, err := m.Fire("go"); err != nil || s != "D" {
		t.Fatalf("enter terminal: %v %v", s, err)
	}
	for i := 0; i < 5; i++ {
		s, err := m.Fire("go")
		if !errors.Is(err, ErrTerminal) || s != "D" {
			t.Fatalf("terminal event #%d: %v %v", i, s, err)
		}
		s, err = m.Fire("nope")
		if !errors.Is(err, ErrTerminal) || s != "D" {
			t.Fatalf("terminal illegal event #%d: %v %v", i, s, err)
		}
	}
	if entryD != 1 || exitD != 0 || len(m.Log()) != 1 {
		t.Fatalf("terminal actions/log wrong: entry=%d exit=%d log=%d", entryD, exitD, len(m.Log()))
	}
}

// 语义 6：先 exit 后 entry，同状态多动作按注册顺序全部执行。
func TestActionOrdering(t *testing.T) {
	var got []string
	rec := func(name string) func() {
		return func() { got = append(got, name) }
	}
	m, _ := New("A", nil, []Transition{{From: "A", Event: "go", To: "B"}})
	m.OnExit("A", rec("exitA1"))
	m.OnExit("A", rec("exitA2"))
	m.OnEntry("B", func() error { got = append(got, "entryB1"); return nil })
	m.OnEntry("B", func() error { got = append(got, "entryB2"); return nil })

	if _, err := m.Fire("go"); err != nil {
		t.Fatal(err)
	}
	want := []string{"exitA1", "exitA2", "entryB1", "entryB2"}
	if len(got) != len(want) {
		t.Fatalf("actions %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("actions %v, want %v", got, want)
		}
	}
}

// 语义 7：Log 与观察者序列一一对应；Log 返回副本。
func TestLogObserveConsistency(t *testing.T) {
	table := []Transition{
		{From: "A", Event: "e1", To: "B"},
		{From: "B", Event: "e2", To: "C"},
		{From: "C", Event: "e3", To: "A"},
	}
	m, _ := New("A", nil, table)
	sub := m.Observe()

	events := []Event{"e1", "e2", "e3"}
	for _, e := range events {
		if _, err := m.Fire(e); err != nil {
			t.Fatal(err)
		}
	}
	log := m.Log()
	if len(log) != 3 {
		t.Fatalf("log len %d", len(log))
	}
	for i, tr := range log {
		select {
		case s := <-sub:
			if s != tr.To {
				t.Fatalf("observed %v != log[%d].To %v", s, i, tr.To)
			}
		default:
			t.Fatalf("missing observation at index %d", i)
		}
	}

	log[0] = Transition{}
	if m.Log()[0].Event != "e1" {
		t.Fatal("mutating returned Log affected internal state")
	}
}

// 语义 8：并发 Fire 下 race 干净，Log 不重不漏；慢观察者不阻塞。
func TestConcurrentFire(t *testing.T) {
	const workers = 16
	const perWorker = 200
	// A --inc--> B --back--> A：只有 inc 在 A 时成功，
	// 成功迁移次数固定，成功后必须 back 才能再次成功。
	m, _ := New("A", nil, []Transition{
		{From: "A", Event: "inc", To: "B"},
		{From: "B", Event: "back", To: "A"},
	})

	// 订阅后故意彻底不读：缓冲很快打满，用于验证 Fire 不会
	// 被慢观察者阻塞（丢弃逻辑在并发压力下生效）。
	_ = m.Observe()

	var wg sync.WaitGroup
	var incs atomic.Int64
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				if s, err := m.Fire("inc"); err == nil && s == "B" {
					incs.Add(1)
					m.Fire("back")
				}
			}
		}()
	}
	wg.Wait()

	log := m.Log()
	want := int(2 * incs.Load())
	if len(log) != want || incs.Load() == 0 {
		t.Fatalf("log len %d, want %d (incs=%d)", len(log), want, incs.Load())
	}
	for i, tr := range log {
		wantTo := State("B")
		if i%2 == 1 {
			wantTo = "A"
		}
		if tr.To != wantTo {
			t.Fatalf("log[%d] = %+v, want To=%v", i, tr, wantTo)
		}
	}
	if m.State() != "A" {
		t.Fatalf("final state %v, want A", m.State())
	}
}
