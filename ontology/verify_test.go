package ontology

import (
	"fmt"
	"strings"
	"testing"
)

func summarize(r *VerifyReport) string {
	parts := []string{fmt.Sprintf("consistent=%v digest=%v checked=%d",
		r.Consistent, r.DigestIntact, r.EntriesChecked)}
	for _, m := range r.Mismatches {
		parts = append(parts, m.String())
	}
	return strings.Join(parts, "|")
}

// 重建期间到达的增量必须按写入生效顺序应用；最终索引等价于
// “基线 + 全部增量按序回放”后的对象状态。
func TestDeltaAppliedInWriteOrder(t *testing.T) {
	_, mgr, ver, _ := newHarness(t)
	mgr.Declare("T", "idx", "p")
	mgr.Write("T", "o1", map[string]PropertyValue{"p": "A"})
	if _, err := mgr.Rebuild("T", "idx"); err != nil {
		t.Fatal(err)
	}

	// 同一对象连续多次改值（含改走再改回），最终值必须等于最后一次写入。
	for _, v := range []string{"B", "C", "B", "D"} {
		mgr.Write("T", "o1", map[string]PropertyValue{"p": v})
	}
	mgr.Write("T", "o2", map[string]PropertyValue{"p": "D"})

	res, err := mgr.Query("T", "idx", "D")
	if err != nil {
		t.Fatal(err)
	}
	if got := res.ObjectIDs; len(got) != 2 || got[0] != "o1" || got[1] != "o2" {
		t.Fatalf("query D = %v, want [o1 o2]", got)
	}
	resB, _ := mgr.Query("T", "idx", "B")
	if len(resB.ObjectIDs) != 0 {
		t.Fatalf("stale value B must not return o1, got %v", resB.ObjectIDs)
	}

	audit, _, err := mgr.Audit("T", "idx")
	if err != nil {
		t.Fatal(err)
	}
	rep := ver.VerifyAudit(audit, mgr.Store())
	if !rep.Consistent {
		var ms []string
		for _, m := range rep.Mismatches {
			ms = append(ms, m.String())
		}
		t.Fatalf("post-delta index inconsistent:\n%s", strings.Join(ms, "\n"))
	}
}

// 复核独立于重建执行日志：拿到审计后人为注入条目级不一致，
// 复核必须定位到“哪个条目 / 哪个对象 / 期望值与实际值”。
func TestIndependentVerifyDetectsTamper(t *testing.T) {
	_, mgr, ver, logBuf := newHarness(t)
	mgr.Declare("T", "idx", "p")
	mgr.Write("T", "o1", map[string]PropertyValue{"p": "A"})
	mgr.Write("T", "o2", map[string]PropertyValue{"p": "A"})
	if _, err := mgr.Rebuild("T", "idx"); err != nil {
		t.Fatal(err)
	}

	old, ok := mgr.TamperEntryForTest("T", "idx", "o2", "FORGED")
	if !ok || old != "A" {
		t.Fatalf("tamper setup failed ok=%v old=%q", ok, old)
	}

	rep, err := ver.VerifyManager(mgr, "T", "idx", true)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Consistent || len(rep.Mismatches) != 1 {
		t.Fatalf("report consistent=%v mismatches=%d", rep.Consistent, len(rep.Mismatches))
	}
	mm := rep.Mismatches[0]
	if mm.ObjectID != "o2" || mm.Expected != "A" || mm.Actual != "FORGED" ||
		mm.Kind != MismatchValue || mm.IndexKey != "FORGED" {
		t.Fatalf("mismatch not located precisely: %+v", mm)
	}

	// 查询仍可进行，但必须在结果中声明存在未修复的不一致。
	res, err := mgr.Query("T", "idx", "FORGED")
	if err != nil {
		t.Fatalf("query must still be served: %v", err)
	}
	if !res.InconsistencyDeclared || !strings.Contains(res.InconsistencyReason, "mismatch") {
		t.Fatalf("inconsistency not declared: %+v", res)
	}
	if len(res.ObjectIDs) != 1 || res.ObjectIDs[0] != "o2" {
		t.Fatalf("query ids=%v", res.ObjectIDs)
	}

	var sawVerify bool
	for _, line := range strings.Split(strings.TrimSpace(logBuf.String()), "\n") {
		if strings.Contains(line, `"kind":"verify_decision"`) &&
			strings.Contains(line, "rebuild execution logs are not read") {
			sawVerify = true
		}
	}
	if !sawVerify {
		t.Fatalf("verify decision missing input/output/basis log")
	}
}

// 复核也能发现“对象当前有值但审计缺条目”（反向完整性）。
func TestVerifyDetectsMissingEntry(t *testing.T) {
	store, mgr, ver, _ := newHarness(t)
	mgr.Declare("T", "idx", "p")
	mgr.Write("T", "o1", map[string]PropertyValue{"p": "A"})
	if _, err := mgr.Rebuild("T", "idx"); err != nil {
		t.Fatal(err)
	}
	// 绕过管理器直接写存储，制造“对象状态已变、索引未跟进”的缺失。
	store.Put("T", "ghost", map[string]PropertyValue{"p": "A"})
	audit, _, err := mgr.Audit("T", "idx")
	if err != nil {
		t.Fatal(err)
	}
	rep := ver.VerifyAudit(audit, store)
	if rep.Consistent {
		t.Fatal("expected inconsistency from missing entry")
	}
	var found bool
	for _, mm := range rep.Mismatches {
		if mm.Kind == MismatchAbsent && mm.ObjectID == "ghost" && mm.Expected == "A" {
			found = true
		}
	}
	if !found {
		t.Fatalf("entry_missing not reported precisely: %+v", rep.Mismatches)
	}
}
