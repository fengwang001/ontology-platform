package hypchecktest

import (
	"testing"

	"ontology/hypcheck"
)

// TestErrorClasses 四类历史输入错误各自可被独立触发。
func TestErrorClasses(t *testing.T) {
	// E1: 类型未定义。
	e := hypcheck.NewEngine(hypcheck.Config{})
	r := e.Precheck(hypcheck.PrecheckRequest{At: 1, TypeID: "A", Caller: "u"})
	if r.Verdict != hypcheck.VerdictError || r.ErrorClass != hypcheck.ErrTypeUndefined {
		t.Fatalf("want E1, got %s %s", r.Verdict, r.ErrorClass)
	}

	// E3: 调用者尚不存在（类型已定义）。
	e2 := hypcheck.NewEngine(hypcheck.Config{})
	must(t, e2.DefineType(0, "A", hypcheck.TypeVersion{}))
	r = e2.Precheck(hypcheck.PrecheckRequest{At: 1, TypeID: "A", Caller: "ghost"})
	if r.ErrorClass != hypcheck.ErrCallerUnknown {
		t.Fatalf("want E3, got %s %s", r.Verdict, r.ErrorClass)
	}

	// E4: 参数违反当时结构约束。
	e3 := hypcheck.NewEngine(hypcheck.Config{})
	must(t, e3.DefineType(0, "A", hypcheck.TypeVersion{Schema: hypcheck.Schema{Fields: []hypcheck.Field{
		{Name: "p", Type: hypcheck.FieldType(hypcheck.KindInt), Required: true}}}}))
	must(t, e3.UpsertPrincipal(0, "u", true))
	must(t, e3.ToggleGrant(0, "u", "A", true))
	r = e3.Precheck(hypcheck.PrecheckRequest{At: 1, TypeID: "A", Caller: "u",
		Params: hypcheck.Params{"p": hypcheck.StrValue("not-int")}})
	if r.ErrorClass != hypcheck.ErrBadParams {
		t.Fatalf("want E4, got %s %s", r.Verdict, r.ErrorClass)
	}

	// E2: 压实后早于切点且类型在清册中不存在。
	e4 := hypcheck.NewEngine(hypcheck.Config{})
	must(t, e4.DefineType(0, "A", hypcheck.TypeVersion{}))
	must(t, e4.UpsertPrincipal(0, "u", true))
	must(t, e4.Compact(100))
	r = e4.Precheck(hypcheck.PrecheckRequest{At: 10, TypeID: "OTHER", Caller: "u"})
	if r.ErrorClass != hypcheck.ErrHistoryGap {
		t.Fatalf("want E2 for unknown type across horizon, got %s %s", r.Verdict, r.ErrorClass)
	}
	// 同一情形下已知类型在切点之前仍未定义 -> E1 优先。
	r = e4.Precheck(hypcheck.PrecheckRequest{At: 10, TypeID: "A", Caller: "u"})
	if r.ErrorClass != hypcheck.ErrTypeUndefined {
		t.Fatalf("want E1 with known-born-after-at, got %s %s", r.Verdict, r.ErrorClass)
	}
}

// TestErrorPriority 同时满足多类错误时，严格只报最高优先级一类。
func TestErrorPriority(t *testing.T) {
	e := hypcheck.NewEngine(hypcheck.Config{})
	must(t, e.DefineType(5, "A", hypcheck.TypeVersion{Schema: hypcheck.Schema{Fields: []hypcheck.Field{
		{Name: "p", Type: hypcheck.FieldType(hypcheck.KindInt), Required: true}}}}))
	must(t, e.UpsertPrincipal(6, "u", true))
	// at=4：类型未定义(E1)、调用者不存在(E3)、参数类型错(E4) 同时成立 -> 必须只报 E1。
	r := e.Precheck(hypcheck.PrecheckRequest{At: 4, TypeID: "A", Caller: "u",
		Params: hypcheck.Params{"p": hypcheck.StrValue("bad")}})
	if r.ErrorClass != hypcheck.ErrTypeUndefined {
		t.Fatalf("E1 must win, got %s", r.ErrorClass)
	}
	// at=5.5 (类型已定义)：E3 + E4 同时成立 -> 必须只报 E3。
	r = e.Precheck(hypcheck.PrecheckRequest{At: 5, TypeID: "A", Caller: "u",
		Params: hypcheck.Params{"p": hypcheck.StrValue("bad")}})
	if r.ErrorClass != hypcheck.ErrCallerUnknown {
		t.Fatalf("E3 must win over E4, got %s", r.ErrorClass)
	}
}

// TestPrecheckIsReadOnly 预检绝不改变对象状态、版本记录、继承关系或日志序号。
func TestPrecheckIsReadOnly(t *testing.T) {
	audit := hypcheck.NewMemoryAuditor()
	e := hypcheck.NewEngine(hypcheck.Config{Auditor: audit})
	must(t, e.DefineType(0, "A", hypcheck.TypeVersion{Schema: hypcheck.Schema{Fields: []hypcheck.Field{
		{Name: "p", Type: hypcheck.FieldType(hypcheck.KindStr), Required: true}}},
		Effects: []hypcheck.EffectDecl{{Key: "k1", Param: "p"}}}))
	must(t, e.UpsertPrincipal(0, "u", true))
	must(t, e.ToggleGrant(0, "u", "A", true))
	must(t, e.WriteState(0, "s", hypcheck.StrValue("v0")))
	must(t, e.PublishHook(0, "h", hypcheck.PhasePre, hypcheck.HookVersion{Version: "1",
		Spec: hypcheck.HookSpec{Kind: hypcheck.PreDenyStateEqual, Key: "s", Value: hypcheck.StrValue("v0")}}))
	must(t, e.BindHook(0, "A", "h", hypcheck.PhasePre))

	before := e.LastSeq()
	for i := 0; i < 50; i++ {
		e.Precheck(hypcheck.PrecheckRequest{At: 1, TypeID: "A", Caller: "u",
			Params: hypcheck.Params{"p": hypcheck.StrValue("x")}, ObjectKeys: []string{"s", "k1"},
			CollectAll: i%2 == 0})
	}
	if e.LastSeq() != before {
		t.Fatalf("precheck appended events: seq %d -> %d", before, e.LastSeq())
	}
	// 真实状态未被意图改变：k1 仍无写入，s 仍为 v0（由下一次预检再次拒绝佐证）。
	r := e.Precheck(hypcheck.PrecheckRequest{At: 1, TypeID: "A", Caller: "u",
		Params: hypcheck.Params{"p": hypcheck.StrValue("x")}, ObjectKeys: []string{"s", "k1"}})
	if r.Verdict != hypcheck.VerdictDenied || len(r.PreFailures) != 1 {
		t.Fatalf("state should be unchanged: %+v", r)
	}
	if len(audit.Entries()) != 51 {
		t.Fatalf("each precheck must be audited, got %d", len(audit.Entries()))
	}
}
