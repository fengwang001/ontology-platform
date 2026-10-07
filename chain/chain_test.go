package chain

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

// parentOf 注册一个父动作：先 set(n=start) 建对象，再调用 inner 动作，
// 最后写 tail=val。inner 的关键性由 critical 控制，失败动作由 failKind 选择。
func parentOf(r *Registry, name, failKind string, critical bool) {
	mustReg(r, &Action{
		Name: name,
		Pre:  func(ExecContext) (bool, string) { return true, "always" },
		Effect: func(c ExecContext) ([]WriteOp, map[string]any, string, error) {
			p := c.Input()
			return []WriteOp{{ObjectID: p["obj"].(string),
				Upsert: map[string]any{"tail": p["tail"]}}}, nil, "parent tail", nil
		},
		Post: func(ExecContext, map[string]any) (bool, string) { return true, "always" },
		Children: []ChildSpec{
			{Name: "set", Critical: true,
				Bind: func(p Params, _ func(string) (any, bool)) Params {
					return Params{"obj": p["obj"], "k": "n", "v": num(p["start"])}
				}},
			{Name: failKind, Critical: critical,
				Bind: func(p Params, _ func(string) (any, bool)) Params {
					return Params{"obj": p["obj"]}
				}},
		},
	})
}

func statuses(rec *ChainRecord) []Status {
	out := make([]Status, len(rec.Frames))
	for i, f := range rec.Frames {
		out[i] = f.Status
	}
	return out
}

func runOnce(t *testing.T, r *Registry, entry string, in Params) (*ChainResult, *ObjectStore) {
	t.Helper()
	store := NewObjectStore()
	eng := NewEngine(r, store, nil)
	res, err := eng.Run(context.Background(), "c1", entry, in)
	if err != nil && !errors.As(err, new(*InvariantError)) {
		// 声明错误以外的失败都已记录在 res 中；不致命。
		t.Logf("Run returned err: %v", err)
	}
	return res, store
}

// 关键内层调用失败（后置失败）：外层此前写入计划必须一并放弃，无任何提交。
func TestCriticalFailureAbortsEverything(t *testing.T) {
	r := buildTestRegistry()
	parentOf(r, "parentCP", "failPost", true)

	res, store := runOnce(t, r, "parentCP", Params{"obj": "o1", "start": 10, "tail": "T"})
	rec := res.Record

	if rec.Committed {
		t.Fatalf("chain must not commit, got committed")
	}
	if rec.Status != StatusAborted {
		t.Fatalf("top frame = %v, want Aborted", rec.Status)
	}
	// 四类暴露之一：关键后置失败 + 外层连带放弃。
	wantStatuses := map[Status]bool{StatusPostFailed: false, StatusAborted: false}
	for _, f := range rec.Frames {
		if f.Action == "failPost" && f.Status != StatusPostFailed {
			t.Fatalf("failPost frame = %v, want PostFailed", f.Status)
		}
		if f.Action == "parentCP" && f.Status != StatusAborted {
			t.Fatalf("parent frame = %v, want Aborted", f.Status)
		}
		if _, ok := wantStatuses[f.Status]; ok {
			wantStatuses[f.Status] = true
		}
	}
	if !wantStatuses[StatusPostFailed] || !wantStatuses[StatusAborted] {
		t.Fatalf("missing status classes: %+v in %v", wantStatuses, statuses(rec))
	}
	// 外层此前的 set(n=10) 与尾部 tail 写入也必须随整体放弃：存储为空。
	if s := store.Snapshot(); len(s) != 0 {
		t.Fatalf("persisted state must be empty, got %v", s)
	}
}

// 非关键内层调用失败：记录为 NonCriticalFailed，外层继续且自己的写入保留并提交。
func TestNonCriticalFailureContinues(t *testing.T) {
	for _, failKind := range []string{"failPre", "failPost", "boomEffect"} {
		t.Run(failKind, func(t *testing.T) {
			r := buildTestRegistry()
			parentOf(r, "parentNC", failKind, false)

			res, store := runOnce(t, r, "parentNC", Params{"obj": "o1", "start": 10, "tail": "T"})
			rec := res.Record
			if !rec.Committed {
				t.Fatalf("chain should commit despite non-critical failure")
			}
			var bad *FrameRecord
			for _, f := range rec.Frames {
				if f.Action == failKind {
					bad = f
				}
			}
			if bad == nil || bad.Status != StatusNonCriticalFailed {
				t.Fatalf("%s frame = %+v, want NonCriticalFailed", failKind, bad)
			}
			if rec.Status != StatusCompleted {
				t.Fatalf("top = %v, want Completed", rec.Status)
			}
			st := store.Snapshot()
			attrs := st["o1"]
			if attrs["n"] != 10 || attrs["tail"] != "T" {
				t.Fatalf("outer writes should survive, got %v", attrs)
			}
			if _, leaked := attrs["postfail"]; leaked {
				t.Fatalf("non-critical child writes must be discarded: %v", attrs)
			}
			if _, leaked := attrs["boom"]; leaked {
				t.Fatalf("non-critical child writes must be discarded: %v", attrs)
			}
		})
	}
}

// 内层前置条件独立评估，但能看到外层触发前的中间写入计划。
func TestInnerPreSeesOuterWritePlan(t *testing.T) {
	r := buildTestRegistry()
	mustReg(r, &Action{
		Name: "parentMid",
		Pre:  func(ExecContext) (bool, string) { return true, "always" },
		Effect: func(c ExecContext) ([]WriteOp, map[string]any, string, error) {
			return nil, nil, "no own writes", nil
		},
		Post: func(c ExecContext, _ map[string]any) (bool, string) {
			attrs, _ := c.State().Get("o1")
			if num(attrs["n"]) != 6 {
				return false, fmt.Sprintf("want n=6 got %v", attrs["n"])
			}
			return true, "n=6"
		},
		Children: []ChildSpec{
			{Name: "set", Critical: true,
				Bind: func(Params, func(string) (any, bool)) Params {
					return Params{"obj": "o1", "k": "n", "v": 5}
				}},
			{Name: "increment", Critical: true,
				Bind: func(Params, func(string) (any, bool)) Params {
					return Params{"obj": "o1", "by": 1}
				}},
		},
	})

	res, store := runOnce(t, r, "parentMid", nil)
	if res.Record.Status != StatusCompleted || !res.Record.Committed {
		t.Fatalf("want committed success, got %v frames=%v", res.Record.Status, statuses(res.Record))
	}
	if got := store.Snapshot()["o1"]["n"]; got != 6 {
		t.Fatalf("n = %v, want 6 (inner pre must see uncommitted outer plan)", got)
	}
}

// 同动作+同输入的自我触发必须在触发前、前置评估之前被检测到，
// 且拒绝不改变此前任何已计算写入计划。
func TestSelfTriggerRejectedAtMultipleDepths(t *testing.T) {
	r := buildTestRegistry()

	res, _ := runOnce(t, r, "loopSame", Params{"x": 42})
	rec := res.Record
	if rec.Status != StatusAborted {
		t.Fatalf("top = %v, want Aborted", rec.Status)
	}
	// 两帧：第 0 帧进入；第 1 帧是同键重复触发，被拒绝。
	if len(rec.Frames) != 2 {
		t.Fatalf("frames = %d, want 2", len(rec.Frames))
	}
	rej := rec.Frames[1]
	if rej.Status != StatusSelfTriggerRejected {
		t.Fatalf("frame1 = %v, want SelfTriggerRejected", rej.Status)
	}
	if rej.PreBasis != "" {
		t.Fatalf("pre must not be evaluated before self-trigger check, basis=%q", rej.PreBasis)
	}

	// descend：不同输入，深度 0..N 全部放行，链条正常提交。
	for _, depth := range []int{0, 1, 2, 3, 7} {
		res, store := runOnce(t, r, "descend", Params{"depth": depth})
		if res.Record.Status != StatusCompleted || !res.Record.Committed {
			t.Fatalf("depth=%d frames=%v", depth, statuses(res.Record))
		}
		if len(res.Record.Frames) != depth+1 {
			t.Fatalf("depth=%d frames=%d want %d", depth, len(res.Record.Frames), depth+1)
		}
		// 写计划按执行顺序累积：最外层帧最后写 depth，故终值等于入口深度。
		if got := store.Snapshot()["chain"]["depth"]; got != depth {
			t.Fatalf("final depth = %v, want %d", got, depth)
		}
	}

	// 同动作但输入不同不构成自我触发；同动作同输入在深度 3 处再次出现才拒绝。
	mustReg(r, &Action{
		Name: "zig",
		Pre:  func(ExecContext) (bool, string) { return true, "always" },
		Effect: func(c ExecContext) ([]WriteOp, map[string]any, string, error) {
			return []WriteOp{{ObjectID: "z", Upsert: map[string]any{"d": num(c.Input()["d"])}}}, nil, "", nil
		},
		Post: func(ExecContext, map[string]any) (bool, string) { return true, "always" },
		Children: []ChildSpec{{
			Name: "zig", Critical: true,
			When: func(p Params, _ func(string) (any, bool)) bool {
				d := num(p["d"])
				return d < 3 // d=3 时再以 d=1 触发，与栈底同输入 -> 必须拒绝
			},
			Bind: func(p Params, _ func(string) (any, bool)) Params {
				d := num(p["d"])
				if d == 2 {
					return Params{"d": 1}
				}
				return Params{"d": d + 1}
			},
		}},
	})
	res, _ = runOnce(t, r, "zig", Params{"d": 1})
	if res.Record.Status != StatusAborted {
		t.Fatalf("zig should be aborted by repeated same input, frames=%v", statuses(res.Record))
	}
	last := res.Record.Frames[len(res.Record.Frames)-1]
	if last.Status != StatusSelfTriggerRejected || num(last.Input["d"]) != 1 {
		t.Fatalf("rejected frame = %+v", last)
	}
}

// 声明期拦截：后继流程依赖非关键子调用输出。
func TestDeclarationErrorConsumingNonCriticalOutput(t *testing.T) {
	r := buildTestRegistry()
	a := &Action{
		Name: "badDep",
		Pre:  func(ExecContext) (bool, string) { return true, "always" },
		Effect: func(c ExecContext) ([]WriteOp, map[string]any, string, error) {
			v, _ := c.Output("flaky") // 依赖非关键调用输出
			return []WriteOp{{ObjectID: "o", Upsert: map[string]any{"v": v}}}, nil, "", nil
		},
		Post: func(ExecContext, map[string]any) (bool, string) { return true, "always" },
		Children: []ChildSpec{
			{Name: "failPre", Critical: false,
				Bind: func(Params, func(string) (any, bool)) Params { return nil }},
			{Name: "set", Critical: true,
				Bind: func(Params, func(string) (any, bool)) Params {
					return Params{"obj": "o", "k": "v2", "v": 1}
				},
				Consumes: []string{"failPre"}},
		},
	}
	if err := r.Register(a); err != nil {
		t.Fatalf("Register itself accepts siblings; got %v", err)
	}
	err := r.ValidateChain("badDep")
	var de *declarationError
	if !errors.As(err, &de) {
		t.Fatalf("want declarationError, got %v", err)
	}

	// 引擎入口同样拦截，不执行任何帧。
	store := NewObjectStore()
	eng := NewEngine(r, store, nil)
	res, err := eng.Run(context.Background(), "x", "badDep", nil)
	if err == nil || res.Record.Status != StatusDeclarationError || len(res.Record.Frames) != 0 {
		t.Fatalf("want declaration rejection with no frames, got %+v err=%v", res.Record, err)
	}
}

// 审计记录须包含链条路径、各层前后置依据与最终结论。
func TestAuditTrailContents(t *testing.T) {
	r := buildTestRegistry()
	parentOf(r, "parentAudit", "failPost", true)
	res, _ := runOnce(t, r, "parentAudit", Params{"obj": "o", "start": 1, "tail": "T"})
	rec := res.Record

	wantPath := []string{"parentAudit", "set", "failPost"}
	gotPath := rec.Path()
	if len(gotPath) != len(wantPath) {
		t.Fatalf("path=%v want %v", gotPath, wantPath)
	}
	for i := range wantPath {
		if gotPath[i] != wantPath[i] {
			t.Fatalf("path=%v want %v", gotPath, wantPath)
		}
	}
	setFrame := rec.Frames[1]
	if setFrame.PreBasis == "" || setFrame.PostBasis == "" || setFrame.EffectBasis == "" {
		t.Fatalf("bases must be recorded: %+v", setFrame)
	}
	if setFrame.Status != StatusCompleted || len(setFrame.WritePlan) != 1 {
		t.Fatalf("set frame = %+v", setFrame)
	}
	if rec.Frames[2].Err == "" {
		t.Fatalf("failed frame must carry reason")
	}
}
