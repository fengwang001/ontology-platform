package delegation

import (
	"strings"
	"testing"
)

// TestLoggingCompleteness 验证每次调用的输入、最终输出与委托链依据
// 都被完整记录。
func TestLoggingCompleteness(t *testing.T) {
	clk := NewManualClock(t0)
	var entries []LogEntry
	s := NewService(
		WithClock(clk),
		WithLogger(LoggerFunc(func(e LogEntry) { entries = append(entries, e) })),
	)

	s.GrantBase("alice", perm("doc", []string{"a"}, nil))
	id := mustDelegate(t, s, DelegationRequest{
		Delegator: "alice", Delegatee: "bob",
		Subset: perm("doc", []string{"a"}, nil),
		Start:  t0, End: hour(10),
	})
	d := decide(s, "bob", "doc", hour(1), "a")
	s.ShrinkBase("alice", perm("doc", []string{"a"}, nil))
	_, _ = s.Delegate(DelegationRequest{ // 会被拒绝的调用也要记录
		Delegator: "alice", Delegatee: "bob",
		Subset: perm("doc", []string{"a"}, nil),
		Start:  t0, End: hour(10),
	})
	_ = s.Revoke(id)

	if len(entries) != 6 {
		t.Fatalf("应有 6 条日志, 实际 %d", len(entries))
	}
	ops := []string{"GrantBase", "Delegate", "Decide", "ShrinkBase", "Delegate", "Revoke"}
	for i, op := range ops {
		if entries[i].Op != op {
			t.Fatalf("第 %d 条日志应为 %s, 实际 %s", i, op, entries[i].Op)
		}
		if entries[i].Input == nil {
			t.Fatalf("第 %d 条日志缺少输入", i)
		}
	}
	// Decide 日志必须带输出与见证链。
	if entries[2].Output == nil || len(entries[2].Witness) != 1 || entries[2].Witness[0] != id {
		t.Fatalf("Decide 日志缺少输出或见证链: %+v", entries[2])
	}
	// 被拒绝的 Delegate 必须带错误。
	if entries[4].Error == "" || !strings.Contains(entries[4].Error, "exceeds") {
		t.Fatalf("被拒绝的调用应记录错误: %+v", entries[4])
	}
	if !d.Allowed {
		t.Fatal("判定应允许")
	}
}
