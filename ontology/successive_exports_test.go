package ontology

import (
	"context"
	"testing"
)

// 同一属性在相隔的两次导出中，因继承链规则调整（覆盖新增、覆盖删除回落、关系重组）
// 而固化出不同依据；历史导出的依据保持不变。
func TestSuccessiveExportsFreezeDifferentBases(t *testing.T) {
	ctx := context.Background()
	p := New()
	mustOK(t, p.CreateType(ctx, "Base", nil))
	mustOK(t, p.CreateType(ctx, "Mid", []string{"Base"}))
	mustOK(t, p.CreateType(ctx, "Leaf", []string{"Mid"}))
	mustOK(t, p.CreateSubject(ctx, "bob"))

	// 阶段一：只有 Base 上 DENY。
	vBase, err := p.PutRule(ctx, "Base", "bob", "ssn", EffectDeny)
	mustOK(t, err)
	e1, err := p.Export(ctx, ExportRequest{ExportID: "s1", TypeID: "Leaf", SubjectID: "bob", Attributes: []string{"ssn"}})
	mustOK(t, err)
	if got := e1.Excluded[0].VersionID; got != vBase || e1.Excluded[0].DeclaringType != "Base" {
		t.Fatalf("e1 expected Base rule %s, got %+v", vBase, e1.Excluded[0])
	}

	// 阶段二：Mid 上新增更近的 DENY，覆盖新增改变命中点。
	vMid, err := p.PutRule(ctx, "Mid", "bob", "ssn", EffectDeny)
	mustOK(t, err)
	e2, err := p.Export(ctx, ExportRequest{ExportID: "s2", TypeID: "Leaf", SubjectID: "bob", Attributes: []string{"ssn"}})
	mustOK(t, err)
	if vMid == vBase {
		t.Fatalf("overriding rule must produce a distinct version id")
	}
	if got := e2.Excluded[0].VersionID; got != vMid || e2.Excluded[0].DeclaringType != "Mid" {
		t.Fatalf("e2 expected Mid rule %s, got %+v", vMid, e2.Excluded[0])
	}

	// 阶段三：Mid 改为 ALLOW（覆盖替换），属性被纳入导出。
	_, err = p.PutRule(ctx, "Mid", "bob", "ssn", EffectAllow)
	mustOK(t, err)
	e3, err := p.Export(ctx, ExportRequest{ExportID: "s3", TypeID: "Leaf", SubjectID: "bob", Attributes: []string{"ssn"}})
	mustOK(t, err)
	if len(e3.Excluded) != 0 || len(e3.Included) != 1 {
		t.Fatalf("e3 expected ALLOW override to include ssn, got %+v", e3)
	}

	// 阶段四：删除 Mid 的规则，回落到 Base 的 DENY。
	_, err = p.DeleteRule(ctx, "Mid", "bob", "ssn")
	mustOK(t, err)
	e4, err := p.Export(ctx, ExportRequest{ExportID: "s4", TypeID: "Leaf", SubjectID: "bob", Attributes: []string{"ssn"}})
	mustOK(t, err)
	if len(e4.Excluded) != 1 || e4.Excluded[0].VersionID != vBase {
		t.Fatalf("e4 expected fallback to Base rule, got %+v", e4.Excluded)
	}

	// 阶段五：继承关系重组，Leaf 不再经过 Base/Mid，默认 ALLOW。
	mustOK(t, p.CreateType(ctx, "Other", nil))
	mustOK(t, p.SetParents(ctx, "Leaf", []string{"Other"}))
	e5, err := p.Export(ctx, ExportRequest{ExportID: "s5", TypeID: "Leaf", SubjectID: "bob", Attributes: []string{"ssn"}})
	mustOK(t, err)
	if len(e5.Excluded) != 0 {
		t.Fatalf("e5 after reorg must include ssn, got %+v", e5.Excluded)
	}

	// 全部历史导出的固化依据不变且可审计。
	check := func(exportID, attr string, wantVersion string) {
		t.Helper()
		res, err := p.Trace(ctx, exportID, attr)
		mustOK(t, err)
		if res.VersionID != wantVersion {
			t.Fatalf("%s frozen basis changed: got %s want %s", exportID, res.VersionID, wantVersion)
		}
	}
	check("s1", "ssn", vBase)
	check("s2", "ssn", vMid)
	check("s4", "ssn", vBase)
}
