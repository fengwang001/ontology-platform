package fsm

import (
	"errors"
	"fmt"
	"testing"
)

// 语义 1：构造期校验。
func TestNewValidation(t *testing.T) {
	table := []Transition{
		{From: "A", Event: "go", To: "B"},
		{From: "B", Event: "back", To: "A"},
	}

	if _, err := New("A", nil, table); err != nil {
		t.Fatalf("valid table rejected: %v", err)
	}

	dup := append([]Transition{}, table...)
	dup = append(dup, Transition{From: "A", Event: "go", To: "A"})
	if _, err := New("A", nil, dup); err == nil {
		t.Fatal("duplicate (From, Event) should be rejected")
	}

	if _, err := New("Z", nil, table); err == nil {
		t.Fatal("initial absent from table should be rejected")
	}

	// initial 只出现在 To 也算出现；终态引用无关状态不算错误。
	if _, err := New("B", []State{"Z"}, table); err != nil {
		t.Fatalf("initial as To, unused terminal: %v", err)
	}
}

// 语义 2：非法事件零副作用；连续非法后合法事件仍可用。
func TestIllegalEventNoSideEffects(t *testing.T) {
	var calls int
	m, _ := New("A", nil, []Transition{{From: "A", Event: "go", To: "B"}})
	m.OnEntry("B", func() error { calls++; return nil })
	m.OnExit("A", func() { calls++ })
	sub := m.Observe()

	for i := 0; i < 100; i++ {
		s, err := m.Fire(Event(fmt.Sprintf("bad%d", i)))
		if !errors.Is(err, ErrNoTransition) || s != "A" {
			t.Fatalf("illegal event #%d: state=%v err=%v", i, s, err)
		}
	}
	if got := m.State(); got != "A" || len(m.Log()) != 0 || calls != 0 {
		t.Fatalf("side effects from illegal events: state=%v log=%d calls=%d", got, len(m.Log()), calls)
	}
	select {
	case s := <-sub:
		t.Fatalf("observer got unexpected state %v", s)
	default:
	}

	if s, err := m.Fire("go"); err != nil || s != "B" {
		t.Fatalf("legal event after 100 illegal ones failed: %v %v", s, err)
	}
}

// 语义 3：自转移不触发 entry/exit，但记 Log、通知观察者。
func TestSelfTransition(t *testing.T) {
	var calls int
	m, _ := New("A", nil, []Transition{{From: "A", Event: "tick", To: "A"}})
	m.OnEntry("A", func() error { calls++; return nil })
	m.OnExit("A", func() { calls++ })
	sub := m.Observe()

	if s, err := m.Fire("tick"); err != nil || s != "A" {
		t.Fatalf("self transition: %v %v", s, err)
	}
	if calls != 0 {
		t.Fatalf("self transition ran %d actions, want 0", calls)
	}
	if log := m.Log(); len(log) != 1 || log[0].To != "A" {
		t.Fatalf("self transition not logged: %+v", log)
	}
	if got := <-sub; got != "A" {
		t.Fatalf("observer got %v, want A", got)
	}
}

// 语义 4：entry 失败整体不迁移；重打时 exit 恰好再跑一次。
func TestEntryFailureRollback(t *testing.T) {
	boom := errors.New("boom")
	var exits, entriesA int
	entriesB := 0

	m, _ := New("A", nil, []Transition{{From: "A", Event: "go", To: "B"}})
	m.OnExit("A", func() { exits++ })
	m.OnEntry("A", func() error { entriesA++; return nil })
	m.OnEntry("B", func() error {
		entriesB++
		if entriesB == 1 {
			return boom
		}
		return nil
	})
	sub := m.Observe()

	_, err := m.Fire("go")
	if !errors.Is(err, ErrEntryFailed) || !errors.Is(err, boom) {
		t.Fatalf("want wrapped ErrEntryFailed, got %v", err)
	}
	if m.State() != "A" || len(m.Log()) != 0 {
		t.Fatalf("state/log changed after failed entry: %v %d", m.State(), len(m.Log()))
	}
	select {
	case s := <-sub:
		t.Fatalf("observer notified on failure: %v", s)
	default:
	}
	if exits != 1 {
		t.Fatalf("exit ran %d times before retry, want 1", exits)
	}

	if s, err := m.Fire("go"); err != nil || s != "B" {
		t.Fatalf("retry failed: %v %v", s, err)
	}
	if exits != 2 {
		t.Fatalf("exit total %d, want 2", exits)
	}
	if len(m.Log()) != 1 || <-sub != "B" {
		t.Fatal("successful retry not observed/logged")
	}
}
