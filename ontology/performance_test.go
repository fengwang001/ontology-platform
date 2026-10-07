package ontology

import (
	"context"
	"fmt"
	"testing"
)

// TestSealedBasisResolutionIsConstantTime 以可验证方式证明：
// 解析某条已固化依据所需检查的规则变更记录数不随历史规则变更总次数增长。
// 做法：在同一规则位置制造 N 次内容变更（每次都产生新的历史版本），
// 然后解析最早被固化的版本标识，直接读取档案内部访问计数。
func TestSealedBasisResolutionIsConstantTime(t *testing.T) {
	ctx := context.Background()
	p := New()
	mustOK(t, p.CreateType(ctx, "T", nil))
	mustOK(t, p.CreateSubject(ctx, "s"))

	first, err := p.PutRule(ctx, "T", "s", "a", EffectDeny)
	mustOK(t, err)

	const changes = 500
	for i := 0; i < changes; i++ {
		effect := EffectAllow
		if i%2 == 0 {
			effect = EffectDeny
		}
		_, err := p.PutRule(ctx, "T", "s", "a", effect)
		mustOK(t, err)
	}

	before := p.archive.archiveReads()
	v, ok := p.archive.resolve(first)
	probe := p.archive.archiveReads() - before
	if !ok {
		t.Fatalf("oldest sealed version must still resolve after %d changes", changes)
	}
	if v.Effect != EffectDeny {
		t.Fatalf("resolved content drifted: %+v", v)
	}
	if probe != 1 {
		t.Fatalf("resolution inspected %d archive records, want exactly 1 map probe (O(1), independent of %d historical changes)",
			probe, changes)
	}

	// 再放大 10 倍变更量，访问计数必须仍然是 1。
	for i := 0; i < changes*10; i++ {
		effect := EffectAllow
		if i%2 == 1 {
			effect = EffectDeny
		}
		_, err := p.PutRule(ctx, "T", "s", "a", effect)
		mustOK(t, err)
	}
	before = p.archive.archiveReads()
	_, ok = p.archive.resolve(first)
	probe = p.archive.archiveReads() - before
	if !ok || probe != 1 {
		t.Fatalf("at 10x history, resolution probes=%d ok=%v, want 1", probe, ok)
	}

	// 端到端：早期导出在 5500 次变更后 Trace 依然恒定 1 次访问。
	rec, err := p.Export(ctx, ExportRequest{ExportID: "big", TypeID: "T", SubjectID: "s", Attributes: []string{"a"}})
	mustOK(t, err)
	if len(rec.Excluded) != 1 {
		t.Fatalf("latest rule state expected to exclude a")
	}
	before = p.archive.archiveReads()
	_, err = p.Trace(ctx, "big", "a")
	mustOK(t, err)
	if p.archive.archiveReads()-before != 1 {
		t.Fatalf("Trace did not resolve via a constant-time lookup")
	}
	t.Log(fmt.Sprintf("resolved sealed basis with exactly 1 archive probe after %d rule changes", changes*11+1))
}
