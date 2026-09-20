package fsm

import (
	"errors"
	"slices"
	"testing"
)

func TestEntryFailureKeepsSourceState(t *testing.T) {
	m := newSession(t)
	boom := errors.New("boom")
	fail := true
	var exits, entries int
	m.OnExit("idle", func() { exits++ })
	m.OnEntry("active", func() error {
		entries++
		if fail {
			return boom
		}
		return nil
	})
	ch := m.Observe()

	if _, err := m.Fire("dial"); !errors.Is(err, ErrEntryFailed) || !errors.Is(err, boom) {
		t.Fatalf("got %v, want ErrEntryFailed wrapping boom", err)
	}
	if m.State() != "idle" {
		t.Fatalf("state = %q, want idle", m.State())
	}
	if len(m.Log()) != 0 {
		t.Fatal("log must not record failed transition")
	}
	select {
	case s := <-ch:
		t.Fatalf("observer received %q", s)
	default:
	}
	if exits != 1 || entries != 1 {
		t.Fatalf("exits=%d entries=%d, want 1/1", exits, entries)
	}

	fail = false
	if got, err := m.Fire("dial"); err != nil || got != "active" {
		t.Fatalf("retry: %q, %v", got, err)
	}
	if exits != 2 || entries != 2 {
		t.Fatalf("after retry exits=%d entries=%d, want 2/2", exits, entries)
	}
}

func TestTerminalAbsorbsEverything(t *testing.T) {
	m := newSession(t)
	var closedEntries, closedExits int
	m.OnEntry("closed", func() error { closedEntries++; return nil })
	m.OnExit("closed", func() { closedExits++ })
	if _, err := m.Fire("dial"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Fire("bye"); err != nil {
		t.Fatal(err)
	}
	if closedEntries != 1 {
		t.Fatalf("terminal entry ran %d times, want 1", closedEntries)
	}
	logLen := len(m.Log())
	for _, e := range []Event{"bye", "msg", "dial"} {
		if _, err := m.Fire(e); !errors.Is(err, ErrTerminal) {
			t.Fatalf("fire %q: got %v, want ErrTerminal", e, err)
		}
	}
	if m.State() != "closed" || len(m.Log()) != logLen {
		t.Fatalf("terminal state mutated: state=%q log=%d", m.State(), len(m.Log()))
	}
	if closedExits != 0 {
		t.Fatalf("terminal exit ran %d times, want 0", closedExits)
	}
}

func TestActionsRunOnceInOrder(t *testing.T) {
	m := newSession(t)
	var seq []string
	m.OnExit("idle", func() { seq = append(seq, "exit1") })
	m.OnExit("idle", func() { seq = append(seq, "exit2") })
	m.OnEntry("active", func() error { seq = append(seq, "entry1"); return nil })
	m.OnEntry("active", func() error { seq = append(seq, "entry2"); return nil })

	if _, err := m.Fire("dial"); err != nil {
		t.Fatal(err)
	}
	want := []string{"exit1", "exit2", "entry1", "entry2"}
	if !slices.Equal(seq, want) {
		t.Fatalf("seq = %v, want %v", seq, want)
	}
}

func TestLogMatchesObserversAndIsACopy(t *testing.T) {
	m := newSession(t)
	ch1, ch2 := m.Observe(), m.Observe()
	events := []Event{"dial", "msg", "msg", "bye"}
	for _, e := range events {
		if _, err := m.Fire(e); err != nil {
			t.Fatal(err)
		}
	}
	log := m.Log()
	if len(log) != len(events) {
		t.Fatalf("log length = %d, want %d", len(log), len(events))
	}
	for i, tr := range log {
		for _, ch := range []<-chan State{ch1, ch2} {
			if got := <-ch; got != tr.To {
				t.Fatalf("transition %d: observer got %q, log says %q", i, got, tr.To)
			}
		}
	}
	log[0].To = "corrupted"
	if m.Log()[0].To != "active" {
		t.Fatal("mutating returned log affected internal state")
	}
}
