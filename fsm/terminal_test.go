package fsm

import (
	"errors"
	"testing"
)

func TestTerminalAbsorbsEvents(t *testing.T) {
	m, err := New("idle", []State{"done"}, []Transition{
		{From: "idle", Event: "go", To: "run"},
		{From: "run", Event: "finish", To: "done"},
		{From: "done", Event: "go", To: "run"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	doneEntries, doneExits := 0, 0
	m.OnEntry("done", func() error { doneEntries++; return nil })
	m.OnExit("done", func() { doneExits++ })

	if _, err := m.Fire("go"); err != nil {
		t.Fatalf("go: %v", err)
	}
	if _, err := m.Fire("finish"); err != nil {
		t.Fatalf("finish: %v", err)
	}
	if m.State() != "done" || doneEntries != 1 {
		t.Fatalf("state=%q doneEntries=%d", m.State(), doneEntries)
	}

	// 终态上的事件（即使表里存在）一律 ErrTerminal，无副作用。
	for _, e := range []Event{"go", "finish", "anything"} {
		got, err := m.Fire(e)
		if !errors.Is(err, ErrTerminal) {
			t.Fatalf("Fire(%q) in terminal err = %v", e, err)
		}
		if got != "done" {
			t.Fatalf("state changed to %q", got)
		}
	}
	if len(m.Log()) != 2 {
		t.Fatalf("log = %v, want 2 transitions", m.Log())
	}
	if doneEntries != 1 || doneExits != 0 {
		t.Fatalf("terminal actions entries=%d exits=%d, want 1/0",
			doneEntries, doneExits)
	}
}

func TestInitialTerminal(t *testing.T) {
	// 初始状态即终态：任何事件都被吸收。
	m, err := New("done", []State{"done"}, []Transition{
		{From: "ready", Event: "x", To: "done"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := m.Fire("x"); !errors.Is(err, ErrTerminal) {
		t.Fatalf("err = %v, want ErrTerminal", err)
	}
}
