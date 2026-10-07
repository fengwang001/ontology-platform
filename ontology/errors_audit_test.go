package ontology

import (
	"context"
	"testing"
)

func TestRejectionPrioritiesAndErrorKinds(t *testing.T) {
	ctx := context.Background()
	p := New()
	mustOK(t, p.CreateType(ctx, "T", nil))
	mustOK(t, p.CreateSubject(ctx, "s1"))

	// 导出：类型不存在 优先于 主体不存在。
	_, err := p.Export(ctx, ExportRequest{ExportID: "x", TypeID: "missing", SubjectID: "missing", Attributes: []string{"a"}})
	assertKind(t, err, KindObjectTypeNotFound)

	_, err = p.Export(ctx, ExportRequest{ExportID: "x", TypeID: "T", SubjectID: "missing", Attributes: []string{"a"}})
	assertKind(t, err, KindSubjectNotFound)

	// 被拒绝的导出不产生任何记录。
	_, err = p.GetExport(ctx, "x")
	assertKind(t, err, KindExportNotFound)

	// 审计：历史导出不存在 优先于 属性未被排除。
	_, err = p.Trace(ctx, "no-such-export", "a")
	assertKind(t, err, KindExportNotFound)

	// 一次正常导出：a 被排除、b 被纳入。
	_, err = p.PutRule(ctx, "T", "s1", "a", EffectDeny)
	mustOK(t, err)
	rec, err := p.Export(ctx, ExportRequest{ExportID: "e", TypeID: "T", SubjectID: "s1", Attributes: []string{"a", "b"}})
	mustOK(t, err)
	if len(rec.Excluded) != 1 || len(rec.Included) != 1 {
		t.Fatalf("unexpected export shape: %+v", rec)
	}

	// 属性在该次导出中未被排除：明确区分，而非记录缺失错误。
	_, err = p.Trace(ctx, "e", "b")
	assertKind(t, err, KindAttributeNotExcluded)

	// 请求一个完全不在导出范围内的属性，同样归类为“当时未被排除”。
	_, err = p.Trace(ctx, "e", "never-listed")
	assertKind(t, err, KindAttributeNotExcluded)

	// 被排除属性可正常溯源。
	res, err := p.Trace(ctx, "e", "a")
	mustOK(t, err)
	if res.Rule == nil || res.Rule.Attribute != "a" || res.Rule.DeclaringType != "T" {
		t.Fatalf("trace result mismatch: %+v", res)
	}

	// 溯源幂等可重复，只读：两次结果完全一致。
	res2, err := p.Trace(ctx, "e", "a")
	mustOK(t, err)
	if *res.Rule != *res2.Rule || res.VersionID != res2.VersionID {
		t.Fatalf("trace is not repeatable")
	}
	seqBefore := p.Seq()
	_, _ = p.Trace(ctx, "e", "a")
	_, _ = p.Trace(ctx, "e", "b")
	if p.Seq() != seqBefore {
		t.Fatalf("read-only trace must not advance the serial sequence")
	}
}

func TestRejectedMutationLeavesStateUntouched(t *testing.T) {
	ctx := context.Background()
	p := New()
	mustOK(t, p.CreateType(ctx, "T", nil))
	before := p.Seq()

	// 非法规则调整不得改变规则状态。
	_, err := p.PutRule(ctx, "missing", "nobody", "a", EffectDeny)
	assertKind(t, err, KindObjectTypeNotFound)
	_, err = p.PutRule(ctx, "T", "nobody", "a", EffectDeny)
	assertKind(t, err, KindSubjectNotFound)
	_, err = p.DeleteRule(ctx, "missing", "nobody", "a")
	assertKind(t, err, KindObjectTypeNotFound)
	err = p.SetParents(ctx, "missing", nil)
	assertKind(t, err, KindObjectTypeNotFound)

	if p.Seq() != before {
		t.Fatalf("rejected mutations must not advance sequence: before=%d after=%d", before, p.Seq())
	}

	// 环与悬空父引用必须被拒绝且不生效。
	mustOK(t, p.CreateType(ctx, "A", []string{"T"}))
	err = p.SetParents(ctx, "T", []string{"A"})
	if err == nil {
		t.Fatalf("inheritance cycle must be rejected")
	}
	err = p.SetParents(ctx, "T", []string{"Ghost"})
	if err == nil {
		t.Fatalf("dangling parent must be rejected")
	}
}
