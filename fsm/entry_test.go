package fsm

import (
	"errors"
	"testing"
)

var errBoom = errors.New("boom")

func TestEntryFailureKeepsState(t *testing.T) {
	m, err := New("idle", nil, []Transition{
		{From: "idle", Event: "go", To: "run"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	idleExits, runEntries := 0, 0
	m.OnExit("idle", func() { idleExits++ })
	shouldFail := true
	m.OnEntry("run", func() error {
		runEntries++
		if shouldFail {
			return errBoom
		}
		return nil
	})
	obs := m.Observe()

	got, err := m.Fire("go")
	if !errors.Is(err, ErrEntryFailed) {
		t.Fatalf("err = %v, want ErrEntryFailed", err)
	}
	if !errors.Is(err, errBoom) {
		t.Fatalf("entry error %v should wrap the action error", err)
	}
	if got != "idle" || m.State() != "idle" {
		t.Fatalf("state = %q, want idle", got)
	}
	if idleExits != 1 || runEntries != 1 {
		t.Fatalf("after fail exits=%d entries=%d, want 1/1", idleExits, runEntries)
	}
	if len(m.Log()) != 0 {
		t.Fatalf("log grew: %v", m.Log())
	}
	select {
	case s := <-obs:
		t.Fatalf("observer got %q on failed entry", s)
	default:
	}

	// 重打同一事件：exit 恰好再跑一次（总计两次），这次 entry 成功。
	shouldFail = false
	got, err = m.Fire("go")
	if err != nil || got != "run" {
		t.Fatalf("retry = %q, %v; want run, nil", got, err)
	}
	if idleExits != 2 {
		t.Fatalf("exit ran %d times total, want 2", idleExits)
	}
	if len(m.Log()) != 1 {
		t.Fatalf("log = %v, want 1 entry", m.Log())
	}
}

func TestEntryFailureThenOtherEventStillWorks(t *testing.T) {
	m, _ := New("idle", nil, []Transition{
		{From: "idle", Event: "go", To: "run"},
		{From: "idle", Event: "jump", To: "fly"},
	})
	m.OnEntry("run", func() error { return errBoom })
	ok := false
	m.OnEntry("fly", func() error { ok = true; return nil })

	if _, err := m.Fire("go"); !errors.Is(err, ErrEntryFailed) {
		t.Fatalf("err = %v", err)
	}
	if _, err := m.Fire("jump"); err != nil {
		t.Fatalf("jump after failed go: %v", err)
	}
	if !ok || m.State() != "fly" {
		t.Fatalf("state = %q ok = %v", m.State(), ok)
	}
}
