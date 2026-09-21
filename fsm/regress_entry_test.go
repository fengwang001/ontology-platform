package fsm

import (
	"errors"
	"testing"
)

// 回归：entry 失败后机器曾错误地迁移到目标状态。
// 根因：Fire 在 entry 失败分支里先执行了 m.state = to 再返回错误。
func TestRegressionEntryFailureStaysOnSource(t *testing.T) {
	m := newTestMachine(t)
	boom := errors.New("boom")
	fail := true
	m.OnEntry("active", func() error {
		if fail {
			return boom
		}
		return nil
	})
	ch := m.Observe()

	s, err := m.Fire("connect")
	if !errors.Is(err, ErrEntryFailed) {
		t.Fatalf("err = %v, want ErrEntryFailed", err)
	}
	if s != "idle" || m.State() != "idle" {
		t.Fatalf("state = %q (returned %q), want idle", m.State(), s)
	}
	if len(m.Log()) != 0 {
		t.Fatalf("log len = %d, want 0", len(m.Log()))
	}
	if _, ok := recvWithTimeout(t, ch); ok {
		t.Fatal("observer must not receive anything after failed entry")
	}

	fail = false
	if s, err := m.Fire("connect"); err != nil || s != "active" {
		t.Fatalf("retry = %q, %v", s, err)
	}
}
