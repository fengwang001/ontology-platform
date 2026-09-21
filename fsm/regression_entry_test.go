package fsm

import (
	"errors"
	"testing"
)

// 回归：entry 失败时机器必须停在原状态。
// 根因：entry 出错的分支里执行了 m.state = to，把机器提交到了目标状态。
func TestEntryFailureStaysInSourceState(t *testing.T) {
	table := []Transition{
		{From: "s0", Event: "a", To: "s1"},
		{From: "s1", Event: "b", To: "s2"},
	}
	m, err := New("s0", nil, table)
	if err != nil {
		t.Fatal(err)
	}
	boom := errors.New("boom")
	m.OnEntry("s1", func() error { return boom })

	// 失败后 Fire 的返回状态与 State() 都必须是原状态 s0。
	if s, err := m.Fire("a"); !errors.Is(err, ErrEntryFailed) || s != "s0" {
		t.Fatalf("Fire(a) = %q, %v; want s0, ErrEntryFailed", s, err)
	}
	if m.State() != "s0" {
		t.Fatalf("state = %q, want s0", m.State())
	}
	// 若机器被错误提交到 s1，这里会错误地接受 b；停在 s0 时必须拒绝。
	if _, err := m.Fire("b"); !errors.Is(err, ErrNoTransition) {
		t.Fatalf("Fire(b) err = %v, want ErrNoTransition", err)
	}
	if len(m.Log()) != 0 {
		t.Fatalf("log len = %d, want 0", len(m.Log()))
	}
}
