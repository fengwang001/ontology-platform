package engine_test

import (
	"errors"
	"testing"

	"ontology/engine"
	"ontology/errs"
	"ontology/hooks"
	"ontology/spec"
)

// newBaseEngine 构造一个带两个对象类型的对象的引擎。
func newBaseEngine(t *testing.T) *engine.Engine {
	t.Helper()
	e := engine.New()
	e.RegisterType("doc", spec.Schema{Fields: map[string]spec.FieldType{
		"title": spec.FieldString,
		"views": spec.FieldInt,
	}})
	e.RegisterType("tag", spec.Schema{Fields: map[string]spec.FieldType{
		"label": spec.FieldString,
	}})
	return e
}

func mustRegister(t *testing.T, e *engine.Engine, def spec.ActionDef) {
	t.Helper()
	if err := e.RegisterAction(def); err != nil {
		t.Fatalf("RegisterAction(%s): %v", def.Name, err)
	}
}

func wr(typ, id string, op spec.WriteOp, fields map[string]any) spec.Op {
	return spec.Op{Write: &spec.Write{Type: typ, ID: id, Op: op, Fields: fields}}
}

func call(name string) spec.Op { return spec.Op{Call: name} }

func mustExist(t *testing.T, e *engine.Engine, typ, id string) map[string]any {
	t.Helper()
	fields, ok := e.State().Get(typ, id)
	if !ok {
		t.Fatalf("expected %s/%s to exist", typ, id)
	}
	return fields
}

func mustNotExist(t *testing.T, e *engine.Engine, typ, id string) {
	t.Helper()
	if _, ok := e.State().Get(typ, id); ok {
		t.Fatalf("expected %s/%s to be absent", typ, id)
	}
}

// TestNestedPreHookSeesOuterUncommittedWrites 验证嵌套调用中，
// 内层动作写入触发的前置钩子能看到外层已应用但未提交的写入。
func TestNestedPreHookSeesOuterUncommittedWrites(t *testing.T) {
	e := newBaseEngine(t)

	var seenFields map[string]any
	var seenOK bool
	var seenDepth int
	e.RegisterPreHook("tag", "observe-outer-doc", func(ctx hooks.Context) error {
		seenFields, seenOK = ctx.State.Get("doc", "d-outer")
		seenDepth = ctx.Depth
		return nil
	})

	mustRegister(t, e, spec.ActionDef{Name: "inner", Body: []spec.Op{
		wr("tag", "t-1", spec.OpCreate, map[string]any{"label": "x"}),
	}})
	mustRegister(t, e, spec.ActionDef{Name: "outer", Body: []spec.Op{
		wr("doc", "d-outer", spec.OpCreate, map[string]any{"title": "draft", "views": 7}),
		call("inner"),
	}})

	if err := e.Execute("outer"); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	t.Logf("判定依据：内层 tag 前置钩子读取外层刚写入的 doc/d-outer；"+
		"实际读到=%v found=%v depth=%d", seenFields, seenOK, seenDepth)
	if !seenOK {
		t.Fatal("内层前置钩子未看到外层未提交的写入 doc/d-outer")
	}
	if seenFields["title"] != "draft" || seenFields["views"] != 7 {
		t.Fatalf("看到的外层写入内容不符: %v", seenFields)
	}
	if seenDepth != 1 {
		t.Fatalf("钩子触发深度应为 1，实际 %d", seenDepth)
	}
}

// TestPostHookFiresOnceAtOutermost 验证后置钩子不区分层级，
// 只在最外层动作的全部嵌套调用执行完毕后触发一次，
// 且看到的是包含全部嵌套写入的最终状态。
func TestPostHookFiresOnceAtOutermost(t *testing.T) {
	e := newBaseEngine(t)

	postCalls := 0
	var finalDocCount, finalTagCount int
	e.RegisterPostHook("doc", "post-doc", func(ctx hooks.Context) error {
		postCalls++
		finalDocCount = ctx.State.Count("doc")
		finalTagCount = ctx.State.Count("tag")
		return nil
	})

	mustRegister(t, e, spec.ActionDef{Name: "level3", Body: []spec.Op{
		wr("tag", "t-3", spec.OpCreate, map[string]any{"label": "deep"}),
		wr("doc", "d-3", spec.OpCreate, map[string]any{"title": "l3", "views": 1}),
	}})
	mustRegister(t, e, spec.ActionDef{Name: "level2", Body: []spec.Op{
		wr("doc", "d-2", spec.OpCreate, map[string]any{"title": "l2", "views": 1}),
		call("level3"),
	}})
	mustRegister(t, e, spec.ActionDef{Name: "level1", Body: []spec.Op{
		wr("doc", "d-1", spec.OpCreate, map[string]any{"title": "l1", "views": 1}),
		call("level2"),
	}})

	if err := e.Execute("level1"); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	t.Logf("判定依据：三层嵌套只触发一次 doc 后置钩子，且看到全部嵌套写入；"+
		"实际 postCalls=%d docCount=%d tagCount=%d", postCalls, finalDocCount, finalTagCount)
	if postCalls != 1 {
		t.Fatalf("后置钩子应只触发 1 次，实际 %d 次", postCalls)
	}
	if finalDocCount != 3 || finalTagCount != 1 {
		t.Fatalf("后置钩子应看到最终状态 doc=3 tag=1，实际 doc=%d tag=%d",
			finalDocCount, finalTagCount)
	}
	// 钩子记录中 post 记录必须恰好一条。
	postRecords := 0
	for _, r := range e.HookLog() {
		if r.Kind == hooks.Post {
			postRecords++
		}
	}
	if postRecords != 1 {
		t.Fatalf("钩子日志中 post 记录应为 1 条，实际 %d 条", postRecords)
	}
}

// TestPostHookFailureRollsBackEverything 验证后置钩子失败时
// （事务尚未提交），已应用的全部写入连同嵌套部分一起回退。
func TestPostHookFailureRollsBackEverything(t *testing.T) {
	e := newBaseEngine(t)

	e.RegisterPostHook("doc", "always-fail", func(ctx hooks.Context) error {
		return errors.New("doc invariant violated")
	})

	mustRegister(t, e, spec.ActionDef{Name: "inner", Body: []spec.Op{
		wr("tag", "t-1", spec.OpCreate, map[string]any{"label": "x"}),
	}})
	mustRegister(t, e, spec.ActionDef{Name: "outer", Body: []spec.Op{
		wr("doc", "d-1", spec.OpCreate, map[string]any{"title": "a", "views": 1}),
		call("inner"),
		wr("doc", "d-2", spec.OpCreate, map[string]any{"title": "b", "views": 2}),
	}})

	err := e.Execute("outer")
	t.Logf("判定依据：后置钩子失败应整体回退；实际错误=%v", err)
	if !errs.IsKind(err, errs.KindPostHook) {
		t.Fatalf("错误类别应为 post-hook-failed，实际 %v", err)
	}
	mustNotExist(t, e, "doc", "d-1")
	mustNotExist(t, e, "doc", "d-2")
	mustNotExist(t, e, "tag", "t-1")
}

// TestPreHookFailureAbortsImmediately 验证任一层级的前置钩子失败时：
// 该层尚未开始的后续写入与尚未发起的嵌套调用立即中止，
// 本事务已应用的全部写入（含更外层）一起回退，不遗留任何痕迹。
func TestPreHookFailureAbortsImmediately(t *testing.T) {
	e := newBaseEngine(t)

	// 内层 tag 的第二个写入触发前置钩子失败。
	e.RegisterPreHook("tag", "reject-t-bad", func(ctx hooks.Context) error {
		if ctx.Write.ID == "t-bad" {
			return errors.New("tag t-bad rejected by pre-hook")
		}
		return nil
	})
	// 后置钩子永远不应被触发（前置失败不进入后置聚合）。
	postFired := false
	e.RegisterPostHook("doc", "must-not-fire", func(ctx hooks.Context) error {
		postFired = true
		return nil
	})

	mustRegister(t, e, spec.ActionDef{Name: "inner2", Body: []spec.Op{
		wr("doc", "d-never", spec.OpCreate, map[string]any{"title": "never", "views": 0}),
	}})
	mustRegister(t, e, spec.ActionDef{Name: "inner", Body: []spec.Op{
		wr("tag", "t-ok", spec.OpCreate, map[string]any{"label": "ok"}),
		wr("tag", "t-bad", spec.OpCreate, map[string]any{"label": "bad"}), // 前置钩子在此失败
		wr("tag", "t-after", spec.OpCreate, map[string]any{"label": "after"}),
		call("inner2"),
	}})
	mustRegister(t, e, spec.ActionDef{Name: "outer", Body: []spec.Op{
		wr("doc", "d-outer", spec.OpCreate, map[string]any{"title": "outer", "views": 1}),
		call("inner"),
		wr("doc", "d-after", spec.OpCreate, map[string]any{"title": "after", "views": 2}),
	}})

	err := e.Execute("outer")
	t.Logf("判定依据：内层前置钩子失败应立即中止并整体回退；实际错误=%v", err)
	if !errs.IsKind(err, errs.KindPreHook) {
		t.Fatalf("错误类别应为 pre-hook-failed，实际 %v", err)
	}
	var nerr *errs.Error
	if !errors.As(err, &nerr) || nerr.HookName != "reject-t-bad" {
		t.Fatalf("归一化错误应携带钩子名 reject-t-bad，实际 %+v", err)
	}
	// 全部写入（含外层已应用、内层已应用）都不留痕迹。
	for _, id := range []string{"d-outer", "d-after", "d-never"} {
		mustNotExist(t, e, "doc", id)
	}
	for _, id := range []string{"t-ok", "t-bad", "t-after"} {
		mustNotExist(t, e, "tag", id)
	}
	if postFired {
		t.Fatal("前置钩子失败不应触发任何后置钩子")
	}
	// 钩子日志：t-bad 的前置记录存在且带错误；t-after 与 inner2 从未执行。
	var sawBad, sawAfter bool
	for _, r := range e.HookLog() {
		if r.WriteID == "t-bad" && r.Err != "" {
			sawBad = true
		}
		if r.WriteID == "t-after" {
			sawAfter = true
		}
		if r.TxOutcome != "rolled-back" {
			t.Fatalf("失败事务的钩子记录结局应为 rolled-back，实际 %s", r.TxOutcome)
		}
	}
	if !sawBad {
		t.Fatal("钩子日志缺少 t-bad 的失败前置记录")
	}
	if sawAfter {
		t.Fatal("t-bad 之后的写入不应再触发钩子")
	}
}

// TestPostHookAggregation 验证多个后置钩子失败时按注册顺序
// 收集全部失败结果一并报告，报告之后同样整体回退。
func TestPostHookAggregation(t *testing.T) {
	e := newBaseEngine(t)

	e.RegisterPostHook("doc", "p1-ok", func(ctx hooks.Context) error { return nil })
	e.RegisterPostHook("doc", "p2-fail", func(ctx hooks.Context) error {
		return errors.New("p2 says no")
	})
	e.RegisterPostHook("tag", "p3-fail", func(ctx hooks.Context) error {
		return errors.New("p3 says no")
	})
	e.RegisterPostHook("doc", "p4-fail", func(ctx hooks.Context) error {
		return errors.New("p4 says no")
	})

	mustRegister(t, e, spec.ActionDef{Name: "act", Body: []spec.Op{
		wr("doc", "d-1", spec.OpCreate, map[string]any{"title": "a", "views": 1}),
		wr("tag", "t-1", spec.OpCreate, map[string]any{"label": "x"}),
	}})

	err := e.Execute("act")
	t.Logf("判定依据：三个失败后置钩子应按注册顺序聚合；实际错误=\n%v", err)
	agg := errs.AsPostHook(err)
	if agg == nil {
		t.Fatalf("错误应为聚合的 post-hook-failed，实际 %v", err)
	}
	wantOrder := []string{"p2-fail", "p3-fail", "p4-fail"}
	if len(agg.Failures) != len(wantOrder) {
		t.Fatalf("聚合失败数应为 %d，实际 %d", len(wantOrder), len(agg.Failures))
	}
	for i, name := range wantOrder {
		if agg.Failures[i].HookName != name {
			t.Fatalf("聚合第 %d 条应为 %s，实际 %s", i, name, agg.Failures[i].HookName)
		}
	}
	// 全部失败都报告之后整体回退。
	mustNotExist(t, e, "doc", "d-1")
	mustNotExist(t, e, "tag", "t-1")
	// 全部后置钩子（含失败的与之后的）都被触发过。
	postCount := 0
	for _, r := range e.HookLog() {
		if r.Kind == hooks.Post {
			postCount++
		}
	}
	if postCount != 4 {
		t.Fatalf("4 个后置钩子都应被触发（失败不短路），实际 %d", postCount)
	}
}

// TestErrorPrecedence 验证拒绝原因按固定次序只报第一个命中者：
// 参数非法 > 前置钩子失败 > 后置钩子失败（聚合），
// 且参数非法与前置钩子失败都不进入后置钩子聚合报告。
func TestErrorPrecedence(t *testing.T) {
	// 场景 1：一次写入同时会触发参数非法与（若生效）后置失败——
	// 参数非法最先报告，后置钩子根本不触发。
	t.Run("invalid-argument-beats-post", func(t *testing.T) {
		e := newBaseEngine(t)
		postFired := false
		e.RegisterPostHook("doc", "would-fail", func(ctx hooks.Context) error {
			postFired = true
			return errors.New("post failure")
		})
		mustRegister(t, e, spec.ActionDef{Name: "bad", Body: []spec.Op{
			wr("doc", "ghost", spec.OpUpdate, map[string]any{"views": 1}), // 目标不存在
		}})
		err := e.Execute("bad")
		t.Logf("判定依据：目标实例不存在应报参数非法；实际错误=%v", err)
		if !errs.IsKind(err, errs.KindInvalidArgument) {
			t.Fatalf("应为 invalid-argument，实际 %v", err)
		}
		if postFired {
			t.Fatal("参数非法不应进入后置钩子阶段")
		}
	})

	// 场景 2：前置钩子失败先于（本会在提交阶段失败的）后置钩子报告。
	t.Run("pre-hook-beats-post", func(t *testing.T) {
		e := newBaseEngine(t)
		postFired := false
		e.RegisterPreHook("doc", "pre-fail", func(ctx hooks.Context) error {
			return errors.New("pre failure")
		})
		e.RegisterPostHook("doc", "post-would-fail", func(ctx hooks.Context) error {
			postFired = true
			return errors.New("post failure")
		})
		mustRegister(t, e, spec.ActionDef{Name: "act", Body: []spec.Op{
			wr("doc", "d-1", spec.OpCreate, map[string]any{"title": "a", "views": 1}),
		}})
		err := e.Execute("act")
		t.Logf("判定依据：前置失败优先于后置聚合；实际错误=%v", err)
		if !errs.IsKind(err, errs.KindPreHook) {
			t.Fatalf("应为 pre-hook-failed，实际 %v", err)
		}
		if postFired {
			t.Fatal("前置钩子失败不应进入后置钩子聚合")
		}
	})

	// 场景 3：同一动作内，参数非法的写入排在前置钩子会失败的写入之前——
	// 按执行顺序参数非法先命中。
	t.Run("invalid-argument-beats-pre-when-earlier", func(t *testing.T) {
		e := newBaseEngine(t)
		e.RegisterPreHook("tag", "pre-fail", func(ctx hooks.Context) error {
			return errors.New("pre failure")
		})
		mustRegister(t, e, spec.ActionDef{Name: "act", Body: []spec.Op{
			wr("doc", "ghost", spec.OpDelete, nil), // 参数非法先命中
			wr("tag", "t-1", spec.OpCreate, map[string]any{"label": "x"}),
		}})
		err := e.Execute("act")
		t.Logf("判定依据：执行顺序上参数非法先命中；实际错误=%v", err)
		if !errs.IsKind(err, errs.KindInvalidArgument) {
			t.Fatalf("应为 invalid-argument，实际 %v", err)
		}
	})

	// 场景 4：写入内容类型不符属于参数非法。
	t.Run("type-mismatch-is-invalid-argument", func(t *testing.T) {
		e := newBaseEngine(t)
		mustRegister(t, e, spec.ActionDef{Name: "act", Body: []spec.Op{
			wr("doc", "d-1", spec.OpCreate, map[string]any{"title": "a", "views": "not-an-int"}),
		}})
		err := e.Execute("act")
		t.Logf("判定依据：views 应为 int；实际错误=%v", err)
		if !errs.IsKind(err, errs.KindInvalidArgument) {
			t.Fatalf("应为 invalid-argument，实际 %v", err)
		}
	})

	// 场景 5：只有后置钩子失败时，报聚合的 post-hook-failed，
	// 三类错误可通过 errs.KindOf 相互区分。
	t.Run("post-only-when-nothing-else-fails", func(t *testing.T) {
		e := newBaseEngine(t)
		e.RegisterPostHook("doc", "post-fail", func(ctx hooks.Context) error {
			return errors.New("post failure")
		})
		mustRegister(t, e, spec.ActionDef{Name: "act", Body: []spec.Op{
			wr("doc", "d-1", spec.OpCreate, map[string]any{"title": "a", "views": 1}),
		}})
		err := e.Execute("act")
		t.Logf("判定依据：仅后置失败时报聚合错误；实际错误=%v", err)
		if !errs.IsKind(err, errs.KindPostHook) {
			t.Fatalf("应为 post-hook-failed，实际 %v", err)
		}
		if errs.IsKind(err, errs.KindInvalidArgument) || errs.IsKind(err, errs.KindPreHook) {
			t.Fatal("三类错误必须可相互区分")
		}
	})
}

// TestSelfCallDepthLimit 验证动作允许调用自身（同一动作定义），
// 但受嵌套深度保护限制，不会无限递归。
func TestSelfCallDepthLimit(t *testing.T) {
	e := newBaseEngine(t)
	mustRegister(t, e, spec.ActionDef{Name: "loop", Body: []spec.Op{
		call("loop"),
	}})
	err := e.Execute("loop")
	t.Logf("判定依据：自调用应被深度保护中止；实际错误=%v", err)
	if !errors.Is(err, engine.ErrDepthLimit) {
		t.Fatalf("应为 ErrDepthLimit，实际 %v", err)
	}
}
