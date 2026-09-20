package fsm

import (
	"errors"
	"testing"
)

// sampleTable 描述一个简化的会话流程：
// idle --dial--> ringing --answer--> talking --hangup--> idle
// talking --end--> closed（终态）
func sampleTable() []Transition {
	return []Transition{
		{From: "idle", Event: "dial", To: "ringing"},
		{From: "ringing", Event: "answer", To: "talking"},
		{From: "talking", Event: "hangup", To: "idle"},
		{From: "talking", Event: "end", To: "closed"},
	}
}

func mustNew(t *testing.T, initial State, terminals []State, table []Transition) *Machine {
	t.Helper()
	m, err := New(initial, terminals, table)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return m
}

func mustFire(t *testing.T, m *Machine, e Event) State {
	t.Helper()
	s, err := m.Fire(e)
	if err != nil {
		t.Fatalf("Fire(%q): %v", e, err)
	}
	return s
}

func TestNewDuplicateTransition(t *testing.T) {
	table := append(sampleTable(), Transition{From: "idle", Event: "dial", To: "talking"})
	if _, err := New("idle", nil, table); err == nil {
		t.Fatal("expected error for duplicate (From, Event), got nil")
	}
}

func TestNewInitialNotReferenced(t *testing.T) {
	if _, err := New("nowhere", nil, sampleTable()); err == nil {
		t.Fatal("expected error for unreferenced initial state, got nil")
	}
}

func TestNewUnreferencedTerminalOK(t *testing.T) {
	m := mustNew(t, "idle", []State{"closed", "archived"}, sampleTable())
	if m.State() != "idle" {
		t.Fatalf("State() = %q, want %q", m.State(), "idle")
	}
}

func TestIllegalEventZeroSideEffects(t *testing.T) {
	m := mustNew(t, "idle", []State{"closed"}, sampleTable())
	var entries, exits int
	m.OnEntry("idle", func() error { entries++; return nil })
	m.OnExit("idle", func() { exits++ })
	ch := m.Observe()

	for i := 0; i < 100; i++ {
		got, err := m.Fire("bogus")
		if !errors.Is(err, ErrNoTransition) {
			t.Fatalf("Fire(bogus) err = %v, want ErrNoTransition", err)
		}
		if got != "idle" {
			t.Fatalf("state after illegal event = %q, want %q", got, "idle")
		}
	}
	if entries != 0 || exits != 0 {
		t.Fatalf("actions ran on illegal event: entries=%d exits=%d, want 0/0", entries, exits)
	}
	if n := len(m.Log()); n != 0 {
		t.Fatalf("log length = %d, want 0", n)
	}
	select {
	case s := <-ch:
		t.Fatalf("observer received %q on illegal event", s)
	default:
	}

	// 连续非法事件之后，合法事件仍正常工作。
	if got := mustFire(t, m, "dial"); got != "ringing" {
		t.Fatalf("state after legal event = %q, want %q", got, "ringing")
	}
}

func TestSelfTransitionNoActions(t *testing.T) {
	table := []Transition{
		{From: "a", Event: "tick", To: "a"},
		{From: "a", Event: "go", To: "b"},
	}
	m := mustNew(t, "a", nil, table)
	var entries, exits int
	m.OnEntry("a", func() error { entries++; return nil })
	m.OnExit("a", func() { exits++ })
	ch := m.Observe()

	if got := mustFire(t, m, "tick"); got != "a" {
		t.Fatalf("state = %q, want %q", got, "a")
	}
	if entries != 0 || exits != 0 {
		t.Fatalf("self-transition ran actions: entries=%d exits=%d, want 0/0", entries, exits)
	}
	log := m.Log()
	if len(log) != 1 || log[0] != (Transition{From: "a", Event: "tick", To: "a"}) {
		t.Fatalf("log = %v, want single self-transition", log)
	}
	select {
	case s := <-ch:
		if s != "a" {
			t.Fatalf("observer received %q, want %q", s, "a")
		}
	default:
		t.Fatal("observer did not receive self-transition notification")
	}
}
