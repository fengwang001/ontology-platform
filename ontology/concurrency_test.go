package ontology

import (
	"context"
	"fmt"
	"sync"
	"testing"
)

// replayedExclusionAt 根据只增事件日志重建 seqAt 时刻的规则状态，
// 以朴素继承解析回答“属性当时是否被排除、命中的版本标识是什么”。
func replayedExclusionAt(events []ChangeEvent, rootType, subject, attr string, seqAt int64) (excluded bool, versionID string, declaring string) {
	parents := map[string][]string{}
	rules := map[struct{ t, s, a string }]string{}
	for _, e := range events {
		if e.Seq > seqAt {
			break
		}
		switch e.Kind {
		case "create_type":
			parents[e.TypeID] = append([]string(nil), e.ParentIDs...)
		case "set_parents":
			parents[e.TypeID] = append([]string(nil), e.ParentIDs...)
		case "put_rule":
			rules[struct{ t, s, a string }{e.DeclaringType, e.Subject, e.Attribute}] = e.VersionID
		case "delete_rule":
			delete(rules, struct{ t, s, a string }{e.DeclaringType, e.Subject, e.Attribute})
		}
	}
	// 朴素 DFS 线性化（派生在前），无环由平台变更校验保证。
	order := []string{}
	seen := map[string]bool{}
	var dfs func(string)
	dfs = func(cur string) {
		if seen[cur] {
			return
		}
		seen[cur] = true
		order = append(order, cur)
		for _, parent := range parents[cur] {
			dfs(parent)
		}
	}
	dfs(rootType)
	for _, typeID := range order {
		if vid, ok := rules[struct{ t, s, a string }{typeID, subject, attr}]; ok {
			// 从版本标识前缀无法直接知道 effect，故由调用方档案校验；这里返回命中点。
			return effectOfVersion(vid) == EffectDeny, vid, typeID
		}
	}
	return false, "", ""
}

// effectOfVersion 通过内容指纹反查 effect：测试用的小型倒排（仅测试包内）。
var effectByVersion = sync.Map{}

func effectOfVersion(vid string) Effect {
	if v, ok := effectByVersion.Load(vid); ok {
		return v.(Effect)
	}
	return EffectAllow
}

func TestConcurrentExportsVsRuleAdjustments(t *testing.T) {
	ctx := context.Background()
	p := New()
	mustOK(t, p.CreateType(ctx, "Base", nil))
	mustOK(t, p.CreateType(ctx, "Leaf", []string{"Base"}))
	for _, s := range []string{"u1", "u2", "u3", "u4"} {
		mustOK(t, p.CreateSubject(ctx, s))
	}

	attrs := []string{"a0", "a1", "a2", "a3"}
	subjects := []string{"u1", "u2", "u3", "u4"}

	// 调整线程：对 (subject, attr) 反复在 Base 上 DENY/ALLOW/删除。
	var adjustWG sync.WaitGroup
	for round := 0; round < 40; round++ {
		adjustWG.Add(1)
		go func(round int) {
			defer adjustWG.Done()
			s := subjects[round%len(subjects)]
			attr := attrs[(round/len(subjects))%len(attrs)]
			switch round % 3 {
			case 0:
				vid, err := p.PutRule(ctx, "Base", s, attr, EffectDeny)
				if err == nil {
					effectByVersion.Store(vid, EffectDeny)
				}
			case 1:
				vid, err := p.PutRule(ctx, "Base", s, attr, EffectAllow)
				if err == nil {
					effectByVersion.Store(vid, EffectAllow)
				}
			case 2:
				_, _ = p.DeleteRule(ctx, "Base", s, attr)
			}
		}(round)
	}

	// 导出线程：并发发起，使用唯一导出标识。
	var exportWG sync.WaitGroup
	var exportMu sync.Mutex
	exportIDs := []string{}
	for i := 0; i < 80; i++ {
		exportWG.Add(1)
		go func(i int) {
			defer exportWG.Done()
			s := subjects[i%len(subjects)]
			id := fmt.Sprintf("ce-%d", i)
			rec, err := p.Export(ctx, ExportRequest{
				ExportID:   id,
				TypeID:     "Leaf",
				SubjectID:  s,
				Attributes: append([]string(nil), attrs...),
			})
			if err != nil {
				t.Errorf("export %s failed: %v", id, err)
				return
			}
			exportMu.Lock()
			exportIDs = append(exportIDs, rec.ExportID)
			exportMu.Unlock()
		}(i)
	}

	adjustWG.Wait()
	exportWG.Wait()

	// 串行化断言：对每次导出，用事件日志重放其 Seq 时刻状态并逐一属性对照。
	events := p.Events()
	for _, id := range exportIDs {
		rec, err := p.GetExport(ctx, id)
		mustOK(t, err)
		actualExcluded := map[string]*ExclusionRecord{}
		for _, ex := range rec.Excluded {
			actualExcluded[ex.Attribute] = ex
		}
		for _, attr := range attrs {
			excluded, vid, declaring := replayedExclusionAt(events, "Leaf", rec.SubjectID, attr, rec.Seq)
			got, isExcluded := actualExcluded[attr]
			if excluded != isExcluded {
				t.Fatalf("export %s attr %s: exclusion mismatch frozen=%v replayed=%v at seq=%d",
					id, attr, isExcluded, excluded, rec.Seq)
			}
			if excluded {
				if got.VersionID != vid || got.DeclaringType != declaring {
					t.Fatalf("export %s attr %s: frozen basis %+v != init-time hit %s@%s",
						id, attr, got, vid, declaring)
				}
				// 固化依据必须仍能从档案解析出命中时刻内容。
				traced, err := p.Trace(ctx, id, attr)
				mustOK(t, err)
				if traced.VersionID != vid || traced.Rule.DeclaringType != declaring {
					t.Fatalf("trace mismatch for %s/%s", id, attr)
				}
			}
		}
	}
}
