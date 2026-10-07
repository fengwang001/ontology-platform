package ontology

import "testing"

// errOf 从结果中取出已分类错误，失败时终止测试。
func errOf(t *testing.T, res ConsumeResult) *ConsumeError {
	t.Helper()
	if res.Outcome != OutcomeError || res.Err == nil {
		t.Fatalf("应为错误结果，得到 %v (err=%v)", res.Outcome, res.Err)
	}
	return res.Err
}

func TestErrorClasses(t *testing.T) {
	t.Run("标识无法判定", func(t *testing.T) {
		st := newTestStorage()
		c := NewConsumer(st, testBuilders())
		res := c.Consume(ChangeEvent{EventID: "", ActionExecutionID: "act-1",
			Kind: KindActionSucceeded, ActionType: "credit", Payload: map[string]any{"amount": 1}})
		if got := errOf(t, res).Class; got != ErrIdentityUndecidable {
			t.Fatalf("错误类别应为 IdentityUndecidable，得到 %v", got)
		}
		if got := field(t, st, "a", "balance"); got != 0 {
			t.Fatalf("不得施加任何副作用，得到 %d", got)
		}
	})

	t.Run("目标对象不存在", func(t *testing.T) {
		st := newTestStorage()
		builders := testBuilders()
		builders["ghost"] = func(evt ChangeEvent) ([]SideEffect, error) {
			return []SideEffect{{ObjectID: "ghost", Op: OpAdd, Field: "x", Value: 1}}, nil
		}
		c := NewConsumer(st, builders)
		res := c.Consume(ChangeEvent{EventID: "evt-1", ActionExecutionID: "act-1",
			Kind: KindActionSucceeded, ActionType: "ghost"})
		if got := errOf(t, res).Class; got != ErrTargetMissing {
			t.Fatalf("错误类别应为 TargetMissing，得到 %v", got)
		}
		if res.Record.State != StateFailed || res.Record.CommittedCount() != 0 {
			t.Fatalf("记录应为 Failed 且零提交，得到 %+v", res.Record)
		}
	})

	t.Run("历史记录缺失", func(t *testing.T) {
		st := newTestStorage()
		evt := creditEvent("evt-1", "act-1", 10)
		crashing := NewConsumer(st, testBuilders(), WithCrashHook(
			func(id string, committed int) {
				if committed == 1 {
					panic("模拟进程崩溃")
				}
			}))
		tryConsume(crashing, evt)

		// 注入故障：补偿历史整体丢失。
		st.Journal.delete("act-1")
		restarted := NewConsumer(st, testBuilders())
		res := restarted.Consume(evt)
		if got := errOf(t, res).Class; got != ErrHistoryMissing {
			t.Fatalf("错误类别应为 HistoryMissing，得到 %v", got)
		}
		// 已生效部分保持在确定边界上，不得扩大。
		if got := field(t, st, "a", "balance"); got != 10 {
			t.Fatalf("已生效部分应保持 a.balance=10，得到 %d", got)
		}
		if got := field(t, st, "a", "count"); got != 0 {
			t.Fatalf("未生效部分不得扩大，a.count 应为 0，得到 %d", got)
		}
	})

	t.Run("历史明细损坏", func(t *testing.T) {
		st := newTestStorage()
		evt := creditEvent("evt-1", "act-1", 10)
		crashing := NewConsumer(st, testBuilders(), WithCrashHook(
			func(id string, committed int) {
				if committed == 1 {
					panic("模拟进程崩溃")
				}
			}))
		tryConsume(crashing, evt)

		st.Journal.corrupt("act-1")
		restarted := NewConsumer(st, testBuilders())
		res := restarted.Consume(evt)
		if got := errOf(t, res).Class; got != ErrHistoryMissing {
			t.Fatalf("错误类别应为 HistoryMissing，得到 %v", got)
		}
		if got := field(t, st, "a", "balance"); got != 10 {
			t.Fatalf("已生效部分应保持 a.balance=10，得到 %d", got)
		}
	})

	t.Run("原子性校验失败", func(t *testing.T) {
		st := newTestStorage()
		c := NewConsumer(st, testBuilders())
		res := c.Consume(ChangeEvent{EventID: "evt-1", ActionExecutionID: "act-1",
			Kind: KindActionSucceeded, ActionType: "badop"})
		if got := errOf(t, res).Class; got != ErrAtomicityViolation {
			t.Fatalf("错误类别应为 AtomicityViolation，得到 %v", got)
		}
		if got := field(t, st, "a", "balance"); got != 0 {
			t.Fatalf("校验失败不得施加任何副作用，得到 %d", got)
		}

		res = c.Consume(ChangeEvent{EventID: "evt-2", ActionExecutionID: "act-2",
			Kind: KindActionSucceeded, ActionType: "broken"})
		if got := errOf(t, res).Class; got != ErrAtomicityViolation {
			t.Fatalf("计划构造失败应为 AtomicityViolation，得到 %v", got)
		}
	})
}

// TestErrorPriority 同一条事件同时满足多类错误条件时，按固定优先级
// 只报告一类：Identity > History > Target > Atomicity。
func TestErrorPriority(t *testing.T) {
	t.Run("标识错误优先于一切", func(t *testing.T) {
		st := newTestStorage()
		builders := testBuilders()
		builders["ghost-badop"] = func(evt ChangeEvent) ([]SideEffect, error) {
			return []SideEffect{{ObjectID: "ghost", Op: "explode", Field: "x", Value: 1}}, nil
		}
		c := NewConsumer(st, builders)
		// 同时满足：标识缺失 + 目标缺失 + 原子性非法。
		res := c.Consume(ChangeEvent{EventID: "", ActionExecutionID: "",
			Kind: KindActionSucceeded, ActionType: "ghost-badop"})
		if got := errOf(t, res).Class; got != ErrIdentityUndecidable {
			t.Fatalf("应优先报告 IdentityUndecidable，得到 %v", got)
		}
	})

	t.Run("历史缺失优先于目标缺失", func(t *testing.T) {
		st := newTestStorage()
		evt := creditEvent("evt-1", "act-1", 10)
		crashing := NewConsumer(st, testBuilders(), WithCrashHook(
			func(id string, committed int) {
				if committed == 1 {
					panic("模拟进程崩溃")
				}
			}))
		tryConsume(crashing, evt)

		// 同时制造：历史损坏 + 剩余副作用目标缺失。
		st.Journal.corrupt("act-1")
		st.Store.apply(SideEffect{ObjectID: "b", Op: OpDelete})

		restarted := NewConsumer(st, testBuilders())
		res := restarted.Consume(evt)
		if got := errOf(t, res).Class; got != ErrHistoryMissing {
			t.Fatalf("应优先报告 HistoryMissing，得到 %v", got)
		}
	})

	t.Run("目标缺失优先于原子性非法", func(t *testing.T) {
		st := newTestStorage()
		builders := testBuilders()
		builders["ghost-badop"] = func(evt ChangeEvent) ([]SideEffect, error) {
			return []SideEffect{{ObjectID: "ghost", Op: "explode", Field: "x", Value: 1}}, nil
		}
		c := NewConsumer(st, builders)
		res := c.Consume(ChangeEvent{EventID: "evt-1", ActionExecutionID: "act-1",
			Kind: KindActionSucceeded, ActionType: "ghost-badop"})
		if got := errOf(t, res).Class; got != ErrTargetMissing {
			t.Fatalf("应优先报告 TargetMissing，得到 %v", got)
		}
	})
}
