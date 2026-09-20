package fsm

import (
	"strconv"
	"testing"
)

func TestLogMatchesObservers(t *testing.T) {
	m, _ := New("a", []State{"d"}, []Transition{
		{From: "a", Event: "e1", To: "b"},
		{From: "b", Event: "e2", To: "c"},
		{From: "c", Event: "e3", To: "d"},
	})
	o1 := m.Observe()
	o2 := m.Observe() // 多次订阅互相独立

	for _, e := range []Event{"e1", "e2", "e3"} {
		if _, err := m.Fire(e); err != nil {
			t.Fatalf("Fire(%q): %v", e, err)
		}
	}

	log := m.Log()
	if len(log) != 3 {
		t.Fatalf("log len = %d", len(log))
	}
	for i, ch := range []<-chan State{o1, o2} {
		for j, tr := range log {
			select {
			case got := <-ch:
				if got != tr.To {
					t.Fatalf("observer %d msg %d = %q, want %q",
						i, j, got, tr.To)
				}
			default:
				t.Fatalf("observer %d missing msg %d", i, j)
			}
		}
	}
}

func TestLogReturnsCopy(t *testing.T) {
	m, _ := New("a", nil, []Transition{
		{From: "a", Event: "e", To: "b"},
	})
	if _, err := m.Fire("e"); err != nil {
		t.Fatalf("Fire: %v", err)
	}
	log := m.Log()
	log[0] = Transition{From: "x", Event: "y", To: "z"}
	log = append(log, Transition{})
	again := m.Log()
	if again[0] != (Transition{"a", "e", "b"}) || len(again) != 1 {
		t.Fatalf("internal log mutated: %v", again)
	}
}

func TestSlowObserverNeverBlocksFire(t *testing.T) {
	table := make([]Transition, 0, observerBuffer*4)
	for i := 0; i < observerBuffer*4; i++ {
		from := State("s" + strconv.Itoa(i))
		to := State("s" + strconv.Itoa(i+1))
		table = append(table, Transition{From: from, Event: "n", To: to})
	}
	m, err := New("s0", nil, table)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	slow := m.Observe() // 从不读取的慢观察者

	for range table {
		if _, err := m.Fire("n"); err != nil {
			t.Fatalf("Fire: %v", err)
		}
	}
	if len(m.Log()) != len(table) {
		t.Fatalf("log len = %d, want %d", len(m.Log()), len(table))
	}
	// 慢观察者收到的必须是完整状态序列的子序列且保持顺序。
	drain := []State{}
loop:
	for {
		select {
		case s := <-slow:
			drain = append(drain, s)
		default:
			break loop
		}
	}
	idx := 0
	for _, s := range drain {
		for idx < len(table) && table[idx].To != s {
			idx++
		}
		if idx >= len(table) {
			t.Fatalf("observed %q is not an in-order subsequence", s)
		}
		idx++
	}
}
