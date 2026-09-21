package fsm

import (
	"errors"
	"testing"
)

// 回归：自转移不得执行 exit/entry。
// 根因：Fire 里用 if true 无条件执行动作，缺少 from == to 的守卫。
func TestSelfTransitionSkipsFailingEntry(t *testing.T) {
	m := newTestMachine(t)
	if _, err := m.Fire("connect"); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("boom")
	var exits int
	// 若自转移误执行 entry，Fire 会返回 ErrEntryFailed。
	m.OnEntry("active", func() error { return boom })
	m.OnExit("active", func() { exits++ })

	s, err := m.Fire("ping")
	if err != nil || s != "active" {
		t.Fatalf("self transition Fire(ping) = %q, %v; want active, nil", s, err)
	}
	if exits != 0 {
		t.Fatalf("self transition ran exit %d times, want 0", exits)
	}
	if m.State() != "active" {
		t.Fatalf("state = %q, want active", m.State())
	}
}
