package ontology

import (
	"context"
	"testing"
)

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertKind(t *testing.T, err error, kind ErrorKind) {
	t.Helper()
	if got := KindOf(err); got != kind {
		t.Fatalf("error kind = %q, want %q (err=%v)", got, kind, err)
	}
}

// 命中规则之后被删除或继承关系重组后，历史导出中的固化依据仍须解析出当时内容快照。
func TestHistoricalBasisSurvivesDeletionAndReorg(t *testing.T) {
	ctx := context.Background()
	p := New()
	mustOK(t, p.CreateType(ctx, "Animal", nil))
	mustOK(t, p.CreateType(ctx, "Dog", []string{"Animal"}))
	mustOK(t, p.CreateSubject(ctx, "alice"))

	_, err := p.PutRule(ctx, "Animal", "alice", "medical", EffectDeny)
	mustOK(t, err)

	first, err := p.Export(ctx, ExportRequest{
		ExportID:   "e1",
		TypeID:     "Dog",
		ObjectID:   "dog-1",
		SubjectID:  "alice",
		Attributes: []string{"name", "medical"},
		Values:     map[string]any{"name": "Rex", "medical": "secret"},
	})
	mustOK(t, err)
	if len(first.Excluded) != 1 || first.Excluded[0].Attribute != "medical" {
		t.Fatalf("expected medical excluded, got %+v", first.Excluded)
	}
	if first.Excluded[0].DeclaringType != "Animal" {
		t.Fatalf("expected hit on Animal, got %q", first.Excluded[0].DeclaringType)
	}
	frozenVersion := first.Excluded[0].VersionID
	if _, ok := first.Values["medical"]; ok {
		t.Fatalf("excluded value must not appear in export snapshot")
	}

	// 事后：删除命中规则（覆盖删除回落），并把继承关系完全重组。
	deleted, err := p.DeleteRule(ctx, "Animal", "alice", "medical")
	mustOK(t, err)
	if !deleted {
		t.Fatalf("rule should have been deleted")
	}
	mustOK(t, p.CreateType(ctx, "Robot", nil))
	mustOK(t, p.SetParents(ctx, "Dog", []string{"Robot"}))

	// 新导出反映调整后状态：medical 不再被排除。
	second, err := p.Export(ctx, ExportRequest{
		ExportID:   "e2",
		TypeID:     "Dog",
		SubjectID:  "alice",
		Attributes: []string{"name", "medical"},
	})
	mustOK(t, err)
	if len(second.Excluded) != 0 {
		t.Fatalf("post-adjustment export must not exclude medical, got %+v", second.Excluded)
	}

	// 历史导出 e1 不变，且固化依据仍可解析为命中时刻的内容快照。
	traced, err := p.Trace(ctx, "e1", "medical")
	mustOK(t, err)
	if traced.VersionID != frozenVersion {
		t.Fatalf("frozen version id changed: %q vs %q", traced.VersionID, frozenVersion)
	}
	if traced.Rule.Effect != EffectDeny || traced.Rule.DeclaringType != "Animal" ||
		traced.Rule.Subject != "alice" || traced.Rule.Attribute != "medical" {
		t.Fatalf("resolved rule snapshot differs from hit-time content: %+v", traced.Rule)
	}

	stored, err := p.GetExport(ctx, "e1")
	mustOK(t, err)
	if len(stored.Excluded) != 1 || stored.Excluded[0].VersionID != frozenVersion {
		t.Fatalf("historical export record mutated by later adjustments")
	}
}
