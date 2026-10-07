package hypchecktest

import (
	"testing"

	"ontology/hypcheck"
)

func engineWithTwoFailingHooks(t *testing.T) *hypcheck.Engine {
	e := hypcheck.NewEngine(hypcheck.Config{})
	tv := hypcheck.TypeVersion{
		Schema: hypcheck.Schema{Fields: []hypcheck.Field{
			{Name: "p", Type: hypcheck.FieldType(hypcheck.KindStr), Required: true},
		}},
		Effects: []hypcheck.EffectDecl{{Key: "k1", Param: "p"}},
	}
	must(t, e.DefineType(0, "A", tv))
	must(t, e.UpsertPrincipal(0, "u", true))
	must(t, e.ToggleGrant(0, "u", "A", true))
	must(t, e.WriteState(0, "s", hypcheck.StrValue("bad")))
	// 前置钩子：状态 s=="bad" 即拒绝（总会触发）。
	must(t, e.PublishHook(0, "pre1", hypcheck.PhasePre, hypcheck.HookVersion{Version: "1",
		Spec: hypcheck.HookSpec{Kind: hypcheck.PreDenyStateEqual, Key: "s", Value: hypcheck.StrValue("bad")}}))
	must(t, e.BindHook(0, "A", "pre1", hypcheck.PhasePre))
	// 后置钩子：意图写 k1==x 时拒绝。
	must(t, e.PublishHook(0, "post1", hypcheck.PhasePost, hypcheck.HookVersion{Version: "1",
		Spec: hypcheck.HookSpec{Kind: hypcheck.PostDenyIntendedSet, Key: "k1", Value: hypcheck.StrValue("x")}}))
	must(t, e.BindHook(0, "A", "post1", hypcheck.PhasePost))
	return e
}

// TestPreFailureBlocksPost 规定：前置阶段失败必须阻止后置阶段演算，
// 且即使 collectAll=true，前置与后置失败也绝不聚合在一起。
func TestPreFailureBlocksPost(t *testing.T) {
	for _, collect := range []bool{false, true} {
		e := engineWithTwoFailingHooks(t)
		r := e.Precheck(hypcheck.PrecheckRequest{
			At: 1, TypeID: "A", Caller: "u", CollectAll: collect,
			Params:     hypcheck.Params{"p": hypcheck.StrValue("x")},
			ObjectKeys: []string{"s"},
		})
		if r.Verdict != hypcheck.VerdictDenied {
			t.Fatalf("collect=%v: want denied", collect)
		}
		if len(r.PreFailures) == 0 {
			t.Fatalf("collect=%v: want pre failures", collect)
		}
		if len(r.PostFailures) != 0 {
			t.Fatalf("collect=%v: post phase must never be evaluated when pre failed, got %+v",
				collect, r.PostFailures)
		}
		if r.Effects != nil {
			t.Fatalf("collect=%v: no intended effects on denial", collect)
		}
	}
}

// TestCollectAllAggregatesWithinPhases 首败模式只报 1 条；聚合模式收集同阶段全部失败。
func TestCollectAllAggregatesWithinPhases(t *testing.T) {
	e := hypcheck.NewEngine(hypcheck.Config{})
	must(t, e.DefineType(0, "A", hypcheck.TypeVersion{Schema: hypcheck.Schema{Fields: []hypcheck.Field{
		{Name: "p", Type: hypcheck.FieldType(hypcheck.KindStr), Required: true}}}}))
	must(t, e.UpsertPrincipal(0, "u", true))
	must(t, e.ToggleGrant(0, "u", "A", true))
	// 两个总会触发的前置钩子；h00 先于 h01（按 hookID 排序）。
	for _, id := range []string{"h00", "h01"} {
		must(t, e.PublishHook(0, id, hypcheck.PhasePre, hypcheck.HookVersion{Version: "1",
			Spec: hypcheck.HookSpec{Kind: hypcheck.PreDenyParamEqual, Param: "p", Value: hypcheck.StrValue("z")}}))
		must(t, e.BindHook(0, "A", id, hypcheck.PhasePre))
	}
	first := e.Precheck(hypcheck.PrecheckRequest{At: 1, TypeID: "A", Caller: "u",
		Params: hypcheck.Params{"p": hypcheck.StrValue("z")}})
	if len(first.PreFailures) != 1 || first.PreFailures[0].HookID != "h00" {
		t.Fatalf("first-failure mode: %+v", first.PreFailures)
	}
	all := e.Precheck(hypcheck.PrecheckRequest{At: 1, TypeID: "A", Caller: "u", CollectAll: true,
		Params: hypcheck.Params{"p": hypcheck.StrValue("z")}})
	if len(all.PreFailures) != 2 {
		t.Fatalf("collect-all mode want 2 pre failures, got %+v", all.PreFailures)
	}
	if len(all.PostFailures) != 0 {
		t.Fatalf("post phase must be absent")
	}
}

// TestPermissionFailureAggregatesWithPreHooks 权限失败与前置钩子失败同属前置阶段，
// collectAll 时一并聚合；首败模式下权限闸门排在最前。
func TestPermissionFailureAggregatesWithPreHooks(t *testing.T) {
	e := hypcheck.NewEngine(hypcheck.Config{})
	must(t, e.DefineType(0, "A", hypcheck.TypeVersion{Schema: hypcheck.Schema{Fields: []hypcheck.Field{
		{Name: "p", Type: hypcheck.FieldType(hypcheck.KindStr), Required: true}}}}))
	must(t, e.UpsertPrincipal(0, "u", true)) // 无授权
	must(t, e.PublishHook(0, "h", hypcheck.PhasePre, hypcheck.HookVersion{Version: "1",
		Spec: hypcheck.HookSpec{Kind: hypcheck.PreDenyParamEqual, Param: "p", Value: hypcheck.StrValue("z")}}))
	must(t, e.BindHook(0, "A", "h", hypcheck.PhasePre))

	first := e.Precheck(hypcheck.PrecheckRequest{At: 1, TypeID: "A", Caller: "u",
		Params: hypcheck.Params{"p": hypcheck.StrValue("z")}})
	if len(first.PreFailures) != 1 || first.PreFailures[0].Code != hypcheck.FailurePermissionDenied {
		t.Fatalf("permission gate must be the first failure: %+v", first.PreFailures)
	}
	all := e.Precheck(hypcheck.PrecheckRequest{At: 1, TypeID: "A", Caller: "u", CollectAll: true,
		Params: hypcheck.Params{"p": hypcheck.StrValue("z")}})
	if len(all.PreFailures) != 2 {
		t.Fatalf("collect-all should aggregate permission + hook: %+v", all.PreFailures)
	}
}

// TestPostOnlyAfterPrePass 前置通过后后置才能拒绝；且冻结快照不被意图修改。
func TestPostOnlyAfterPrePass(t *testing.T) {
	e := engineWithTwoFailingHooks(t)
	// s 状态改为 "ok" -> 前置通过；意图 k1=x -> 后置拒绝。
	must(t, e.WriteState(2, "s", hypcheck.StrValue("ok")))
	r := e.Precheck(hypcheck.PrecheckRequest{At: 3, TypeID: "A", Caller: "u",
		Params: hypcheck.Params{"p": hypcheck.StrValue("x")}, ObjectKeys: []string{"s"}})
	if r.Verdict != hypcheck.VerdictDenied || len(r.PostFailures) != 1 || len(r.PreFailures) != 0 {
		t.Fatalf("want post-only denial: pre=%+v post=%+v", r.PreFailures, r.PostFailures)
	}
	// s!="ok" 时前置通过，意图 k1=y 后置也通过 -> allowed 且仅报告意图。
	r2 := e.Precheck(hypcheck.PrecheckRequest{At: 3, TypeID: "A", Caller: "u",
		Params: hypcheck.Params{"p": hypcheck.StrValue("y")}, ObjectKeys: []string{"s"}})
	if r2.Verdict != hypcheck.VerdictAllowed || len(r2.Effects) != 1 || r2.Effects[0].Value.Str != "y" {
		t.Fatalf("want allowed with intended effect: %+v", r2)
	}
}
