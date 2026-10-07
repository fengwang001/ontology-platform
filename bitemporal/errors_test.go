package bitemporal

import (
	"context"
	"errors"
	"testing"
)

func setupErrStore(t *testing.T, sym bool) *Store {
	t.Helper()
	st := NewStore()
	must(t, st.RegisterObjectType("T", 5))
	if !sym {
		must(t, st.RegisterObjectType("U", 5))
	}
	lt := LinkType{ID: "L", SourceType: "T", TargetType: "U"}
	if sym {
		lt = LinkType{ID: "L", SourceType: "T", TargetType: "T", Symmetric: true}
	}
	must(t, st.RegisterLinkType(lt,
		Cardinality{Forward: Card{Max: 1}, Reverse: Card{Max: 1}}, 5))
	return st
}

func TestErrorPriority(t *testing.T) {
	st := setupErrStore(t, true)
	log := NewDecisionLog()
	aud := NewAuditor(st, log)

	// 同时制造 E3（矛盾区间）、E2（对象类型不存在）：E3 优先。
	_, err := aud.Audit(context.Background(), AuditRequest{
		LinkType: "L", RecordStart: 0, RecordEnd: 0})
	if !errors.Is(err, ErrIntervalContradiction) {
		t.Fatalf("want E3, got %v", err)
	}

	// 区间合法但对象类型在记录时刻 0 尚不存在：E2。
	_, err = aud.Audit(context.Background(), AuditRequest{
		LinkType: "L", RecordStart: 0, RecordEnd: 10})
	if !errors.Is(err, ErrObjectTypeMissing) {
		t.Fatalf("want E2, got %v", err)
	}

	// 未注册链接类型：E2。
	_, err = aud.Audit(context.Background(), AuditRequest{
		LinkType: "NOPE", RecordStart: 10, RecordEnd: 20})
	if !errors.Is(err, ErrObjectTypeMissing) {
		t.Fatalf("want E2 for unknown type, got %v", err)
	}

	// 结构缺陷（E4）与早期记录时刻（对象已存在）。
	must(t, st.IngestLegacyHalf("L", "a", "b", 6, 6, true, halfAB))
	_, err = aud.Audit(context.Background(), AuditRequest{
		LinkType: "L", RecordStart: 10, RecordEnd: 20})
	if !errors.Is(err, ErrMirrorStructural) {
		t.Fatalf("want E4, got %v", err)
	}

	// E3 与 E4 同时成立时仍报 E3。
	_, err = aud.Audit(context.Background(), AuditRequest{
		LinkType: "L", RecordStart: 20, RecordEnd: 5})
	if !errors.Is(err, ErrIntervalContradiction) {
		t.Fatalf("E3 must dominate E4, got %v", err)
	}

	// 所有失败判定都必须留痕。
	recs := log.Records()
	if len(recs) < 4 {
		t.Fatalf("expected decision records for failed audits, got %d", len(recs))
	}
	for _, r := range recs {
		if r.Err == "" {
			t.Fatalf("failed audit record missing error: %+v", r)
		}
	}

	// 失败审计不得改变任何历史：再次回放仍能看到结构缺陷。
	r := st.CurrentSnapshot().Replay("L", 10, 10)
	if len(r.Defects) != 1 || len(r.Edges) != 0 {
		t.Fatalf("history mutated by failed audit: %+v", r)
	}
}

func TestRuleSupersededDuringAudit(t *testing.T) {
	st := setupErrStore(t, false)
	must(t, st.RecordCreate("L", "a", "b", 6, 6))
	log := NewDecisionLog()
	aud := NewAuditor(st, log)

	aud.BasisHook = func(snapGen int64) {
		// 在审计进行到“最终基版校验”之前并发发布新规则版本。
		if err := st.PutRule("L",
			Cardinality{Forward: Card{Max: 7}, Reverse: Card{Max: 7}}, 12); err != nil {
			t.Errorf("concurrent rule put failed: %v", err)
		}
	}
	_, err := aud.Audit(context.Background(), AuditRequest{
		LinkType: "L", RecordStart: 6, RecordEnd: 20})
	if !errors.Is(err, ErrRuleVersionSuperseded) {
		t.Fatalf("want E1, got %v", err)
	}
	aud.BasisHook = nil

	// 无并发作废时审计正常成功。
	segs, err := aud.Audit(context.Background(), AuditRequest{
		LinkType: "L", RecordStart: 6, RecordEnd: 20})
	if err != nil {
		t.Fatalf("audit after basis stabilized: %v", err)
	}
	if len(segs) == 0 {
		t.Fatalf("expected segments")
	}
}

func TestErrorsDoNotMutateHistory(t *testing.T) {
	st := setupErrStore(t, false)
	must(t, st.RecordCreate("L", "a", "b", 6, 6))
	before := st.CurrentSnapshot().gen
	aud := NewAuditor(st, NewDecisionLog())
	for _, req := range []AuditRequest{
		{LinkType: "L", RecordStart: 10, RecordEnd: 5}, // E3
		{LinkType: "L", RecordStart: 0, RecordEnd: 3},  // E2
		{LinkType: "X", RecordStart: 10, RecordEnd: 20},
	} {
		if _, err := aud.Audit(context.Background(), req); err == nil {
			t.Fatalf("expected error for %+v", req)
		}
	}
	if st.CurrentSnapshot().gen != before {
		t.Fatalf("failed audit produced a new snapshot generation")
	}
}
