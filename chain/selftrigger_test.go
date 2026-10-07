package chain

import (
	"context"
	"testing"
)

// SelfTriggerAt 导出纯判定函数，供开销可验证测试直接使用：
// 它只在当前链条自身的调用栈上做线性比较，
// 比较次数恰为 len(stack)，即当前嵌套深度，不触碰任何历史链条。
func SelfTriggerAt(stack []FrameKey, next FrameKey) (found bool, comparisons int) {
	for i, k := range stack {
		comparisons++
		if k == next {
			return true, comparisons
		}
		_ = i
	}
	return false, comparisons
}

// 可验证开销：无论系统“历史上”积累过多少无关链条，
// 单次触发前检查的比较次数恒等于当前链条深度，且同键必检出、异键必放行。
func TestSelfTriggerCheckIsLinearInCurrentDepthOnly(t *testing.T) {
	// 模拟大量历史链条的帧键：它们不属于当前栈，检查时不应被接触。
	history := make([]FrameKey, 10000)
	for i := range history {
		history[i] = FrameKey{Action: "old", Input: CanonicalInput(Params{"i": i})}
	}

	for depth := 0; depth <= 100; depth++ {
		stack := make([]FrameKey, depth)
		for i := range stack {
			stack[i] = FrameKey{Action: "descend", Input: CanonicalInput(Params{"depth": i})}
		}
		// 放行边界：新输入。
		fresh := FrameKey{Action: "descend", Input: CanonicalInput(Params{"depth": depth})}
		found, n := SelfTriggerAt(stack, fresh)
		if found || n != depth {
			t.Fatalf("depth=%d: found=%v comparisons=%d, want false/%d", depth, found, n, depth)
		}
		// 历史帧键即使重复也不影响当前栈判定。
		for _, h := range history {
			if f, _ := SelfTriggerAt(stack, h); f {
				t.Fatalf("history key must never trigger self detection")
			}
		}
		// 触发边界：与栈中任意一帧同动作同输入，立刻检出。
		for _, existing := range stack {
			found, n := SelfTriggerAt(stack, existing)
			if !found || n > depth || n < 1 {
				t.Fatalf("depth=%d existing key: found=%v comparisons=%d", depth, found, n)
			}
		}
	}
}

// 端到端：深链条上同键重复出现在不同深度时都能检出，且被拒绝帧不产生写入计划。
func TestSelfTriggerEndToEndAtShallowAndDeep(t *testing.T) {
	r := buildTestRegistry()

	// depth=1：立即自我触发（loopSame）。
	store := NewObjectStore()
	eng := NewEngine(r, store, nil)
	res, _ := eng.Run(context.Background(), "c", "loopSame", Params{"x": 1})
	if got := len(res.Record.Frames); got != 2 {
		t.Fatalf("frames=%d want 2", got)
	}
	if res.Record.Frames[1].Status != StatusSelfTriggerRejected {
		t.Fatalf("want rejection, got %v", res.Record.Frames[1].Status)
	}
	if len(res.Record.Frames[1].WritePlan) != 0 || res.Record.Committed {
		t.Fatalf("rejection must leave no plan and no commit")
	}

	// 更深的链条：descend(depth=4) 全部不同输入放行，共 5 帧成功提交。
	res, _ = eng.Run(context.Background(), "d", "descend", Params{"depth": 4})
	if res.Record.Status != StatusCompleted || len(res.Record.Frames) != 5 {
		t.Fatalf("deep chain frames=%v status=%v", statuses(res.Record), res.Record.Status)
	}
	// 拒绝发生后不影响后续独立链条：状态按拒绝前之后的新链条正常累积。
	if got := store.Snapshot()["chain"]["depth"]; got != 4 {
		t.Fatalf("independent chain state = %v want 4", got)
	}
}

// 前置条件不被外层豁免：外层已通过的事实不能让内层跳过自身评估。
func TestInnerPreIndependentlyReevaluated(t *testing.T) {
	r := buildTestRegistry()
	// 父动作前置通过，但关键子调用 failPre 前置必失败 -> 内层 PreFailed，整体放弃。
	mustReg(r, &Action{
		Name: "wrapPreFail",
		Pre:  func(ExecContext) (bool, string) { return true, "outer pre ok" },
		Effect: func(c ExecContext) ([]WriteOp, map[string]any, string, error) {
			return []WriteOp{{ObjectID: "o", Upsert: map[string]any{"outer": true}}}, nil, "", nil
		},
		Post: func(ExecContext, map[string]any) (bool, string) { return true, "always" },
		Children: []ChildSpec{{
			Name:     "failPre",
			Critical: true,
			Bind:     func(Params, func(string) (any, bool)) Params { return Params{} },
		}},
	})
	store := NewObjectStore()
	eng := NewEngine(r, store, nil)
	res, _ := eng.Run(context.Background(), "c", "wrapPreFail", nil)
	var inner *FrameRecord
	for _, f := range res.Record.Frames {
		if f.Action == "failPre" {
			inner = f
		}
	}
	if inner == nil || inner.Status != StatusPreFailed || inner.PreBasis != "forced pre failure" {
		t.Fatalf("inner frame = %+v", inner)
	}
	if res.Record.Committed || len(store.Snapshot()) != 0 {
		t.Fatalf("outer plan must be discarded; state=%v", store.Snapshot())
	}
}
