package fsm

import "testing"

func TestNewInitialState(t *testing.T) {
	m, err := New("idle", nil, []Transition{
		{From: "idle", Event: "go", To: "run"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := m.State(); got != "idle" {
		t.Fatalf("initial state = %q, want idle", got)
	}
}

func TestNewInitialOnlyAsTarget(t *testing.T) {
	// initial 只出现在 To 中也算合法。
	_, err := New("idle", nil, []Transition{
		{From: "run", Event: "back", To: "idle"},
	})
	if err != nil {
		t.Fatalf("initial appearing only as To should be valid: %v", err)
	}
}

func TestNewInitialNotInTable(t *testing.T) {
	_, err := New("ghost", nil, []Transition{
		{From: "idle", Event: "go", To: "run"},
	})
	if err == nil {
		t.Fatal("expected error when initial state is absent from table")
	}
}

func TestNewDuplicateTransition(t *testing.T) {
	_, err := New("idle", nil, []Transition{
		{From: "idle", Event: "go", To: "run"},
		{From: "idle", Event: "go", To: "other"},
	})
	if err == nil {
		t.Fatal("expected error on duplicate (From, Event)")
	}
}

func TestNewUnreferencedTerminalIsOK(t *testing.T) {
	// 终态不出现在任何转移里也不算错误。
	m, err := New("idle", []State{"unreachable"}, []Transition{
		{From: "idle", Event: "go", To: "run"},
	})
	if err != nil {
		t.Fatalf("unreferenced terminal should be allowed: %v", err)
	}
	if _, ok := m.terminals["unreachable"]; !ok {
		t.Fatal("terminal not registered")
	}
}

func TestNewEmptyTable(t *testing.T) {
	if _, err := New("idle", nil, nil); err == nil {
		t.Fatal("empty table with an initial state must error")
	}
}
