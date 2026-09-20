package fsm

import (
	"errors"
	"testing"
	"time"
)

func testTable() []Transition {
	return []Transition{
		{From: "idle", Event: "connect", To: "active"},
		{From: "active", Event: "ping", To: "active"},
		{From: "active", Event: "close", To: "done"},
		{From: "done", Event: "reset", To: "idle"},
	}
}

func newTestMachine(t *testing.T) *Machine {
	t.Helper()
	m, err := New("idle", []State{"done"}, testTable())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return m
}

func recvWithTimeout(t *testing.T, ch <-chan State) (State, bool) {
	t.Helper()
	select {
	case s := <-ch:
		return s, true
	case <-time.After(50 * time.Millisecond):
		return "", false
	}
}

// 语义 1：构造期校验。
func TestNewValidation(t *testing.T) {
	dup := append(testTable(), Transition{From: "idle", Event: "connect", To: "active"})
	if _, err := New("idle", []State{"done"}, dup); err == nil {
		t.Fatal("duplicate (From, Event) must be rejected")
	}

	// 终态未在任何转移中出现：合法。
	if _, err := New("idle", []State{"done", "archived"}, testTable()); err != nil {
		t.Fatalf("unreferenced terminal must be allowed: %v", err)
	}

	// initial 未出现在任何 From/To 中：报错。
	if _, err := New("nowhere", []State{"done"}, testTable()); err == nil {
		t.Fatal("initial not referenced by table must be rejected")
	}
}

// 语义 2：非法事件零副作用，且之后机器仍可用。
func TestIllegalEventHasNoSideEffects(t *testing.T) {
	m := newTestMachine(t)
	var entries, exits int
	m.OnEntry("idle", func() error { entries++; return nil })
	m.OnExit("idle", func() { exits++ })
	ch := m.Observe()

	for i := 0; i < 100; i++ {
		if _, err := m.Fire("bogus"); !errors.Is(err, ErrNoTransition) {
			t.Fatalf("Fire(bogus) err = %v, want ErrNoTransition", err)
		}
	}
	if m.State() != "idle" {
		t.Fatalf("state = %q, want idle", m.State())
	}
	if entries != 0 || exits != 0 {
		t.Fatalf("actions ran: entries=%d exits=%d, want 0/0", entries, exits)
	}
	if len(m.Log()) != 0 {
		t.Fatalf("log len = %d, want 0", len(m.Log()))
	}
	if _, ok := recvWithTimeout(t, ch); ok {
		t.Fatal("observer must not receive anything")
	}

	if s, err := m.Fire("connect"); err != nil || s != "active" {
		t.Fatalf("Fire(connect) = %q, %v", s, err)
	}
}

// 语义 3：自转移不触发 entry/exit，但记日志并通知观察者。
func TestSelfTransition(t *testing.T) {
	m := newTestMachine(t)
	if _, err := m.Fire("connect"); err != nil {
		t.Fatal(err)
	}
	var entries, exits int
	m.OnEntry("active", func() error { entries++; return nil })
	m.OnExit("active", func() { exits++ })
	ch := m.Observe()

	s, err := m.Fire("ping")
	if err != nil || s != "active" {
		t.Fatalf("Fire(ping) = %q, %v", s, err)
	}
	if entries != 0 || exits != 0 {
		t.Fatalf("self transition ran actions: entries=%d exits=%d", entries, exits)
	}
	log := m.Log()
	if len(log) != 2 || log[1] != (Transition{From: "active", Event: "ping", To: "active"}) {
		t.Fatalf("log = %+v", log)
	}
	if got, ok := recvWithTimeout(t, ch); !ok || got != "active" {
		t.Fatalf("observer got %q, %v", got, ok)
	}
}

// 语义 4：entry 失败整体不迁移，重试时 exit 恰好再跑一次。
func TestEntryFailureKeepsState(t *testing.T) {
	m := newTestMachine(t)
	boom := errors.New("boom")
	failEntry := true
	var exits int
	m.OnExit("idle", func() { exits++ })
	m.OnEntry("active", func() error {
		if failEntry {
			return boom
		}
		return nil
	})
	ch := m.Observe()

	if _, err := m.Fire("connect"); !errors.Is(err, ErrEntryFailed) || !errors.Is(err, boom) {
		t.Fatalf("err = %v, want ErrEntryFailed wrapping boom", err)
	}
	if m.State() != "idle" || len(m.Log()) != 0 || exits != 1 {
		t.Fatalf("state=%q log=%d exits=%d", m.State(), len(m.Log()), exits)
	}
	if _, ok := recvWithTimeout(t, ch); ok {
		t.Fatal("observer must not receive anything")
	}

	failEntry = false
	if s, err := m.Fire("connect"); err != nil || s != "active" {
		t.Fatalf("retry = %q, %v", s, err)
	}
	if exits != 2 {
		t.Fatalf("exits = %d, want exactly 2", exits)
	}
}

// 语义 5：终态吸收一切事件；终态 entry 执行一次，exit 永不执行。
func TestTerminalAbsorbs(t *testing.T) {
	m := newTestMachine(t)
	var doneEntries, doneExits int
	m.OnEntry("done", func() error { doneEntries++; return nil })
	m.OnExit("done", func() { doneExits++ })

	if _, err := m.Fire("connect"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Fire("close"); err != nil {
		t.Fatal(err)
	}
	logLen := len(m.Log())
	for _, e := range []Event{"reset", "connect", "anything"} {
		if _, err := m.Fire(e); !errors.Is(err, ErrTerminal) {
			t.Fatalf("Fire(%q) err = %v, want ErrTerminal", e, err)
		}
	}
	if m.State() != "done" || len(m.Log()) != logLen {
		t.Fatalf("state=%q log=%d", m.State(), len(m.Log()))
	}
	if doneEntries != 1 || doneExits != 0 {
		t.Fatalf("done entries=%d exits=%d, want 1/0", doneEntries, doneExits)
	}
}
