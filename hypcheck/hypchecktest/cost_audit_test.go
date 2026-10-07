package hypchecktest

import (
	"testing"

	"ontology/hypcheck"
)

// TestProbeCostSublinear 独立验证：重建快照的版本探测次数有界为
// (参与的存储数)·(一次二分 + 路径深度)，不随系统累计版本演进总次数线性增长。
//
// 做法：保持单一类型/调用者/钩子不变，持续制造与该动作无关的版本演进与状态写入，
// 记录总事件数 100 / 1000 / 10000 时的探测次数，断言其增长为零（有界常数）。
func TestProbeCostSublinear(t *testing.T) {
	measure := func(totalWrites int) (events, probes int) {
		e := hypcheck.NewEngine(hypcheck.Config{})
		must(t, e.DefineType(0, "A", hypcheck.TypeVersion{Schema: hypcheck.Schema{Fields: []hypcheck.Field{
			{Name: "p", Type: hypcheck.FieldType(hypcheck.KindInt), Required: true}}}}))
		must(t, e.UpsertPrincipal(0, "u", true))
		must(t, e.ToggleGrant(0, "u", "A", true))
		for i := 1; i <= totalWrites; i++ {
			// 与 A/u 无关的键持续演进（模拟全系统累计变更）。
			must(t, e.WriteState(hypcheck.Timestamp(i), "noise/"+itoa(i), hypcheck.IntValue(int64(i))))
		}
		r := e.Precheck(hypcheck.PrecheckRequest{At: hypcheck.Timestamp(totalWrites),
			TypeID: "A", Caller: "u", Params: hypcheck.Params{"p": hypcheck.IntValue(1)}})
		if r.Verdict != hypcheck.VerdictAllowed {
			t.Fatalf("unexpected %s: %s", r.Verdict, r.Message)
		}
		return int(r.LinearSeq), r.Probes
	}

	n1, p1 := measure(100)
	n2, p2 := measure(1000)
	n3, p3 := measure(10000)
	t.Logf("events=%d probes=%d ; events=%d probes=%d ; events=%d probes=%d", n1, p1, n2, p2, n3, p3)
	if p1 <= 0 {
		t.Fatal("probe accounting must be positive")
	}
	if p1 != p2 || p2 != p3 {
		t.Fatalf("probes grew with total history: %d %d %d", p1, p2, p3)
	}
	// 同时给出复杂度上界的可验证说明：单次 as-of 为一次二分（约 log 事件数级别的
	// 指针访问，但“版本记录探测”仅计 rootAt 一次），预检探测次数 ≤ 参与存储数。
	if p3 > 64 {
		t.Fatalf("probe count %d exceeds documented constant bound", p3)
	}
}

// TestAuditChainRecordsBasis 审计记录包含输入、依据钩子版本、权限快照与结论，且哈希链可验证。
func TestAuditChainRecordsBasis(t *testing.T) {
	audit := hypcheck.NewMemoryAuditor()
	e := hypcheck.NewEngine(hypcheck.Config{Auditor: audit})
	must(t, e.DefineType(0, "A", hypcheck.TypeVersion{Schema: hypcheck.Schema{Fields: []hypcheck.Field{
		{Name: "p", Type: hypcheck.FieldType(hypcheck.KindInt), Required: true}}}}))
	must(t, e.UpsertPrincipal(0, "u", true))
	must(t, e.UpsertPrincipal(0, "org", true))
	must(t, e.ToggleGrant(0, "org", "A", true))
	must(t, e.ToggleEdge(0, "org", "u", true))
	must(t, e.PublishHook(0, "h", hypcheck.PhasePre, hypcheck.HookVersion{Version: "v7",
		Spec: hypcheck.HookSpec{Kind: hypcheck.PreDenyParamEqual, Param: "p", Value: hypcheck.IntValue(9)}}))
	must(t, e.BindHook(0, "A", "h", hypcheck.PhasePre))

	r := e.Precheck(hypcheck.PrecheckRequest{At: 3, TypeID: "A", Caller: "u",
		Params: hypcheck.Params{"p": hypcheck.IntValue(9)}})
	if r.Verdict != hypcheck.VerdictDenied || r.AuditID == "" {
		t.Fatalf("want denied audited result: %+v", r)
	}
	entries := audit.Entries()
	if len(entries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(entries))
	}
	en := entries[0]
	if en.Request.TypeID != "A" || en.Request.Caller != "u" {
		t.Fatalf("audit must record request: %+v", en.Request)
	}
	if en.Result.TypeVersion == "" || len(en.Result.Hooks) != 1 || en.Result.Hooks[0].Version != "v7" {
		t.Fatalf("audit must record type & hook versions: %+v", en.Result)
	}
	if en.PermTrace == nil || !en.PermTrace.Allowed || en.PermTrace.GrantedBy != "org" {
		t.Fatalf("audit must record permission snapshot: %+v", en.PermTrace)
	}
	if err := audit.Verify(); err != nil {
		t.Fatalf("hash chain should verify: %v", err)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		b[pos] = '-'
	}
	return string(b[pos:])
}
