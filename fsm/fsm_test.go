package fsm

import (
	"errors"
	"testing"
)

// sessionTable models a tiny session protocol:
//
//	idle --dial--> active --msg--> active (self) --bye--> closed (terminal)
func sessionTable() []Transition {
	return []Transition{
		{From: "idle", Event: "dial", To: "active"},
		{From: "active", Event: "msg", To: "active"},
		{From: "active", Event: "bye", To: "closed"},
	}
}

func newSession(t *testing.T) *Machine {
	t.Helper()
	m, err := New("idle", []State{"closed"}, sessionTable())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return m
}

func TestNewRejectsDuplicateTransition(t *testing.T) {
	table := append(sessionTable(), Transition{From: "idle", Event: "dial", To: "closed"})
	if _, err := New("idle", []State{"closed"}, table); err == nil {
		t.Fatal("expected error for duplicate (From, Event)")
	}
}

func TestNewRejectsInitialOutsideTable(t *testing.T) {
	if _, err := New("nowhere", []State{"closed"}, sessionTable()); err == nil {
		t.Fatal("expected error for initial state absent from table")
	}
}

func TestNewAllowsTerminalOutsideTable(t *testing.T) {
	if _, err := New("idle", []State{"closed", "archived"}, sessionTable()); err != nil {
		t.Fatalf("terminal absent from table must be allowed: %v", err)
	}
}

func TestIllegalEventHasZeroSideEffects(t *testing.T) {
	m := newSession(t)
	var entries, exits int
	m.OnEntry("active", func() error { entries++; return nil })
	m.OnExit("idle", func() { exits++ })
	ch := m.Observe()

	for i := 0; i < 100; i++ {
		if _, err := m.Fire("nope"); !errors.Is(err, ErrNoTransition) {
			t.Fatalf("fire %d: got %v, want ErrNoTransition", i, err)
		}
	}
	if m.State() != "idle" {
		t.Fatalf("state changed to %q", m.State())
	}
	if entries != 0 || exits != 0 {
		t.Fatalf("actions ran: entries=%d exits=%d", entries, exits)
	}
	if n := len(m.Log()); n != 0 {
		t.Fatalf("log grew to %d", n)
	}
	select {
	case s := <-ch:
		t.Fatalf("observer received %q", s)
	default:
	}
	if got, err := m.Fire("dial"); err != nil || got != "active" {
		t.Fatalf("legal event after 100 illegal ones: %q, %v", got, err)
	}
}

func TestSelfTransitionSkipsActions(t *testing.T) {
	m := newSession(t)
	if _, err := m.Fire("dial"); err != nil {
		t.Fatal(err)
	}
	var entries, exits int
	m.OnEntry("active", func() error { entries++; return nil })
	m.OnExit("active", func() { exits++ })
	ch := m.Observe()

	if _, err := m.Fire("msg"); err != nil {
		t.Fatal(err)
	}
	if entries != 0 || exits != 0 {
		t.Fatalf("self transition ran actions: entries=%d exits=%d", entries, exits)
	}
	if n := len(m.Log()); n != 2 {
		t.Fatalf("log length = %d, want 2", n)
	}
	if got := <-ch; got != "active" {
		t.Fatalf("observer got %q, want active", got)
	}
}
