package fsm

import (
	"errors"
	"reflect"
	"testing"
)

func TestEntryFailureStaysAndRetry(t *testing.T) {
	table := []Transition{
		{From: "a", Event: "go", To: "b"},
		{From: "b", Event: "back", To: "a"},
	}
	m := mustNew(t, "a", nil, table)
	var exits int
	m.OnExit("a", func() { exits++ })
	inner := errors.New("boom")
	calls := 0
	m.OnEntry("b", func() error {
		calls++
		if calls == 1 {
			return inner
		}
		return nil
	})
	ch := m.Observe()

	_, err := m.Fire("go")
	if !errors.Is(err, ErrEntryFailed) {
		t.Fatalf("err = %v, want ErrEntryFailed", err)
	}
	if !errors.Is(err, inner) {
		t.Fatalf("err = %v, want it to wrap the inner error", err)
	}
	if m.State() != "a" {
		t.Fatalf("state = %q, want %q after failed entry", m.State(), "a")
	}
	if n := len(m.Log()); n != 0 {
		t.Fatalf("log length = %d, want 0 after failed entry", n)
	}
	select {
	case s := <-ch:
		t.Fatalf("observer received %q after failed entry", s)
	default:
	}
	if exits != 1 {
		t.Fatalf("exits = %d, want 1 after failed attempt", exits)
	}

	// 重打同一事件：exit 恰好再跑一次（总计两次），迁移成功。
	if got := mustFire(t, m, "go"); got != "b" {
		t.Fatalf("state = %q, want %q after retry", got, "b")
	}
	if exits != 2 {
		t.Fatalf("exits = %d, want 2 after retry", exits)
	}
	if n := len(m.Log()); n != 1 {
		t.Fatalf("log length = %d, want 1 after retry", n)
	}
}

func TestTerminalAbsorbing(t *testing.T) {
	m := mustNew(t, "idle", []State{"closed"}, sampleTable())
	var termEntries, termExits int
	m.OnEntry("closed", func() error { termEntries++; return nil })
	m.OnExit("closed", func() { termExits++ })

	mustFire(t, m, "dial")
	mustFire(t, m, "answer")
	mustFire(t, m, "end")
	if termEntries != 1 {
		t.Fatalf("terminal entry ran %d times, want 1", termEntries)
	}
	logLen := len(m.Log())

	// 终态吸收：任何事件（包括本来有转移的）都被拒绝。
	for _, e := range []Event{"dial", "end", "anything"} {
		if _, err := m.Fire(e); !errors.Is(err, ErrTerminal) {
			t.Fatalf("Fire(%q) err = %v, want ErrTerminal", e, err)
		}
	}
	if m.State() != "closed" {
		t.Fatalf("state = %q, want %q", m.State(), "closed")
	}
	if n := len(m.Log()); n != logLen {
		t.Fatalf("log grew in terminal state: %d -> %d", logLen, n)
	}
	if termExits != 0 {
		t.Fatalf("terminal exit ran %d times, want 0", termExits)
	}
}

func TestActionOrderAndMultipleRegistrations(t *testing.T) {
	table := []Transition{
		{From: "a", Event: "go", To: "b"},
		{From: "b", Event: "stay", To: "b"},
	}
	m := mustNew(t, "a", nil, table)
	var order []string
	m.OnExit("a", func() { order = append(order, "exit1") })
	m.OnExit("a", func() { order = append(order, "exit2") })
	m.OnEntry("b", func() error { order = append(order, "entry1"); return nil })
	m.OnEntry("b", func() error { order = append(order, "entry2"); return nil })

	mustFire(t, m, "go")
	want := []string{"exit1", "exit2", "entry1", "entry2"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("action order = %v, want %v", order, want)
	}
}

func TestLogMatchesObserversAndIsCopied(t *testing.T) {
	m := mustNew(t, "idle", []State{"closed"}, sampleTable())
	ch1 := m.Observe()
	ch2 := m.Observe()
	events := []Event{"dial", "answer", "hangup", "dial", "answer", "end"}
	for _, e := range events {
		mustFire(t, m, e)
	}

	log := m.Log()
	if len(log) != len(events) {
		t.Fatalf("log length = %d, want %d", len(log), len(events))
	}
	for i, e := range events {
		if log[i].Event != e {
			t.Fatalf("log[%d].Event = %q, want %q", i, log[i].Event, e)
		}
		for j, ch := range []<-chan State{ch1, ch2} {
			if got := <-ch; got != log[i].To {
				t.Fatalf("observer %d item %d = %q, want %q", j, i, got, log[i].To)
			}
		}
	}

	// 调用方改动返回的切片不得影响内部状态。
	log[0].To = "corrupted"
	if got := m.Log()[0].To; got != "ringing" {
		t.Fatalf("internal log affected by caller mutation: log[0].To = %q", got)
	}
}
