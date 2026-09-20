package fsm

import (
	"errors"
	"testing"
)

func newTestMachine(t *testing.T) *Machine {
	t.Helper()
	m, err := New("idle", []State{"done"}, []Transition{
		{From: "idle", Event: "go", To: "run"},
		{From: "run", Event: "tick", To: "run"},
		{From: "run", Event: "finish", To: "done"},
		{From: "idle", Event: "stay", To: "idle"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return m
}

func TestFireValidTransition(t *testing.T) {
	m := newTestMachine(t)
	got, err := m.Fire("go")
	if err != nil || got != "run" {
		t.Fatalf("Fire(go) = %q, %v; want run, nil", got, err)
	}
	log := m.Log()
	if len(log) != 1 || log[0] != (Transition{"idle", "go", "run"}) {
		t.Fatalf("log = %v", log)
	}
}

func TestFireUnknownEventNoSideEffects(t *testing.T) {
	m := newTestMachine(t)
	exits, entries := 0, 0
	m.OnExit("idle", func() { exits++ })
	m.OnEntry("run", func() error { entries++; return nil })
	obs := m.Observe()

	for i := 0; i < 100; i++ {
		got, err := m.Fire("nope")
		if !errors.Is(err, ErrNoTransition) {
			t.Fatalf("Fire #%d err = %v, want ErrNoTransition", i, err)
		}
		if got != "idle" {
			t.Fatalf("state changed to %q", got)
		}
	}
	if exits != 0 || entries != 0 {
		t.Fatalf("actions ran: exits=%d entries=%d", exits, entries)
	}
	if len(m.Log()) != 0 {
		t.Fatalf("log grew: %v", m.Log())
	}
	select {
	case s := <-obs:
		t.Fatalf("observer got %q on illegal events", s)
	default:
	}

	// 非法事件之后合法事件必须照常工作。
	if _, err := m.Fire("go"); err != nil {
		t.Fatalf("legal event after illegal ones failed: %v", err)
	}
	if exits != 1 || entries != 1 {
		t.Fatalf("actions = exits:%d entries:%d, want 1/1", exits, entries)
	}
}

func TestSelfTransitionSkipsActions(t *testing.T) {
	m := newTestMachine(t)
	exits, entries := 0, 0
	m.OnExit("idle", func() { exits++ })
	m.OnEntry("idle", func() error { entries++; return nil })
	obs := m.Observe()

	got, err := m.Fire("stay")
	if err != nil || got != "idle" {
		t.Fatalf("self transition = %q, %v", got, err)
	}
	if exits != 0 || entries != 0 {
		t.Fatalf("self transition ran actions: exits=%d entries=%d", exits, entries)
	}
	if len(m.Log()) != 1 {
		t.Fatalf("self transition not logged: %v", m.Log())
	}
	select {
	case s := <-obs:
		if s != "idle" {
			t.Fatalf("observer got %q", s)
		}
	default:
		t.Fatal("observer missed self transition")
	}
}

func TestActionOrderAndMultiRegistration(t *testing.T) {
	m := newTestMachine(t)
	var order []string
	m.OnExit("idle", func() { order = append(order, "exit1") })
	m.OnExit("idle", func() { order = append(order, "exit2") })
	m.OnEntry("run", func() error { order = append(order, "entry1"); return nil })
	m.OnEntry("run", func() error { order = append(order, "entry2"); return nil })

	if _, err := m.Fire("go"); err != nil {
		t.Fatalf("Fire: %v", err)
	}
	want := []string{"exit1", "exit2", "entry1", "entry2"}
	if len(order) != len(want) {
		t.Fatalf("action order = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("action order = %v, want %v", order, want)
		}
	}
}
