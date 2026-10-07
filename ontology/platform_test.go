package ontology_test

import (
	"fmt"
	"math/rand"
	"sort"
	"sync"
	"testing"

	ontology "ontology/ontology"
)

// mustSetup 构建基础类型体系：Base <- Mid <- Leaf，主体 alice。
func mustSetup(t *testing.T) *ontology.Platform {
	t.Helper()
	p := ontology.NewPlatform()
	p.AddPrincipal("alice")
	if err := p.AddType("Base", "", []string{"id", "name", "secret"}); err != nil {
		t.Fatalf("add Base: %v", err)
	}
	if err := p.AddType("Mid", "Base", []string{"level"}); err != nil {
		t.Fatalf("add Mid: %v", err)
	}
	if err := p.AddType("Leaf", "Mid", []string{"token"}); err != nil {
		t.Fatalf("add Leaf: %v", err)
	}
	return p
}

func catOf(err error) ontology.ErrorCategory {
	e, ok := err.(*ontology.Error)
	if !ok {
		return ""
	}
	return e.Category
}

// 1. 命中规则之后被删除 / 被覆盖替换 / 继承关系重组后，历史依据仍可正确解析。
func TestHistoricalBasisSurvivesRuleDeletionAndRestructure(t *testing.T) {
	p := mustSetup(t)

	ruleID, err := p.UpsertRule("Mid", "alice", "secret", ontology.EffectDeny, "v1: deny secret")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	exp, err := p.Export("Leaf", "alice")
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if got := exp.Exclusions["secret"]; got != ruleID {
		t.Fatalf("exclusion basis = %q, want %q", got, ruleID)
	}
	t.Logf("判定: export=%s prop=secret 输入=(Leaf,alice) 输出=excluded 依据=%s", exp.ID, ruleID)

	// 之后：命中规则被删除。
	if err := p.DeleteRule("Mid", "alice", "secret"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	// 之后：继承关系重组（Leaf 改挂到 Base 下，Mid 退出链）。
	if err := p.SetParent("Leaf", "Base"); err != nil {
		t.Fatalf("restructure: %v", err)
	}
	// 之后：同名槽位被更高层覆盖替换（新规则版本）。
	if _, err := p.UpsertRule("Base", "alice", "secret", ontology.EffectAllow, "v2: allow secret"); err != nil {
		t.Fatalf("upsert v2: %v", err)
	}

	rec, err := p.AuditTrace(exp.ID, "secret")
	if err != nil {
		t.Fatalf("audit after delete+restructure+override: %v", err)
	}
	if rec.RuleID != ruleID {
		t.Fatalf("audit rule id = %q, want %q", rec.RuleID, ruleID)
	}
	if rec.Rule.Content != "v1: deny secret" || rec.Rule.Effect != ontology.EffectDeny {
		t.Fatalf("audit snapshot content drifted: %+v", rec.Rule)
	}
	if rec.DeclaredOn != "Mid" {
		t.Fatalf("declared on = %q, want Mid", rec.DeclaredOn)
	}
	t.Logf("审计: export=%s prop=secret 输出=(rule=%s, content=%q, declaredOn=%s)",
		rec.ExportID, rec.RuleID, rec.Rule.Content, rec.DeclaredOn)

	// 幂等且可重复。
	rec2, err := p.AuditTrace(exp.ID, "secret")
	if err != nil || rec2 != rec {
		t.Fatalf("audit not idempotent: %v / %+v vs %+v", err, rec2, rec)
	}
}

// 2. 同一属性在相隔的两次导出中因规则调整固化出不同依据；历史导出保持不变。
func TestSamePropertyDifferentBasisAcrossExports(t *testing.T) {
	p := mustSetup(t)

	id1, _ := p.UpsertRule("Mid", "alice", "secret", ontology.EffectDeny, "v1")
	exp1, err := p.Export("Leaf", "alice")
	if err != nil {
		t.Fatalf("export1: %v", err)
	}
	t.Logf("判定: export=%s prop=secret 输出=excluded 依据=%s", exp1.ID, id1)

	// 相隔的规则调整：覆盖新增（同槽位新版本）。
	id2, _ := p.UpsertRule("Mid", "alice", "secret", ontology.EffectDeny, "v2-stricter")
	exp2, err := p.Export("Leaf", "alice")
	if err != nil {
		t.Fatalf("export2: %v", err)
	}
	t.Logf("判定: export=%s prop=secret 输出=excluded 依据=%s", exp2.ID, id2)

	if exp1.Exclusions["secret"] != id1 || exp2.Exclusions["secret"] != id2 || id1 == id2 {
		t.Fatalf("basis not distinct/frozen: exp1=%v exp2=%v", exp1.Exclusions, exp2.Exclusions)
	}

	// 覆盖删除回落：删掉 Mid 的规则，在 Base 上声明的更老规则成为命中点。
	id0, _ := p.UpsertRule("Base", "alice", "secret", ontology.EffectDeny, "v0-base")
	if err := p.DeleteRule("Mid", "alice", "secret"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	exp3, err := p.Export("Leaf", "alice")
	if err != nil {
		t.Fatalf("export3: %v", err)
	}
	if got := exp3.Exclusions["secret"]; got != id0 {
		t.Fatalf("fallback basis = %q, want %q", got, id0)
	}
	t.Logf("判定: export=%s prop=secret 输出=excluded 依据=%s (删除后回落)", exp3.ID, id0)

	// 历史导出与其固化依据保持不变。
	r1, err := p.AuditTrace(exp1.ID, "secret")
	if err != nil || r1.RuleID != id1 || r1.Rule.Content != "v1" {
		t.Fatalf("exp1 audit drifted: %+v err=%v", r1, err)
	}
	r2, err := p.AuditTrace(exp2.ID, "secret")
	if err != nil || r2.RuleID != id2 || r2.Rule.Content != "v2-stricter" {
		t.Fatalf("exp2 audit drifted: %+v err=%v", r2, err)
	}
	r3, err := p.AuditTrace(exp3.ID, "secret")
	if err != nil || r3.RuleID != id0 || r3.DeclaredOn != "Base" {
		t.Fatalf("exp3 audit wrong: %+v err=%v", r3, err)
	}
	t.Logf("审计: exp1->%s(%q) exp2->%s(%q) exp3->%s(declared=%s)",
		r1.RuleID, r1.Rule.Content, r2.RuleID, r2.Rule.Content, r3.RuleID, r3.DeclaredOn)
}

// 3. 审计查询对"未被排除"情形的正确区分（含错误类别与优先级）。
func TestAuditDistinguishesNotExcluded(t *testing.T) {
	p := mustSetup(t)
	if _, err := p.UpsertRule("Base", "alice", "secret", ontology.EffectDeny, "deny"); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	exp, err := p.Export("Leaf", "alice")
	if err != nil {
		t.Fatalf("export: %v", err)
	}

	// 未被排除的属性：明确报告 CategoryPropertyNotExcluded，而非记录缺失。
	_, err = p.AuditTrace(exp.ID, "name")
	if catOf(err) != ontology.CategoryPropertyNotExcluded {
		t.Fatalf("want property_not_excluded, got %v", err)
	}
	t.Logf("审计: export=%s prop=name 输出=未被排除 (category=%s)", exp.ID, catOf(err))

	// 不存在的导出记录优先于属性判定。
	_, err = p.AuditTrace("exp-9999", "name")
	if catOf(err) != ontology.CategoryExportNotFound {
		t.Fatalf("want export_not_found, got %v", err)
	}
	// 即使属性在该系统中从未出现，记录缺失仍优先。
	_, err = p.AuditTrace("exp-9999", "nonexistent-prop")
	if catOf(err) != ontology.CategoryExportNotFound {
		t.Fatalf("want export_not_found (precedence), got %v", err)
	}
}

// 4. 导出请求的拒绝优先级：对象类型不存在 > 执行主体不存在。
func TestExportErrorPrecedence(t *testing.T) {
	p := mustSetup(t)

	_, err := p.Export("Ghost", "alice")
	if catOf(err) != ontology.CategoryObjectTypeNotFound {
		t.Fatalf("want object_type_not_found, got %v", err)
	}
	_, err = p.Export("Leaf", "bob")
	if catOf(err) != ontology.CategoryPrincipalNotFound {
		t.Fatalf("want principal_not_found, got %v", err)
	}
	// 两者同时不成立时，类型不存在优先。
	_, err = p.Export("Ghost", "bob")
	if catOf(err) != ontology.CategoryObjectTypeNotFound {
		t.Fatalf("want object_type_not_found precedence, got %v", err)
	}
	t.Logf("拒绝优先级: (Ghost,bob) -> %s", catOf(err))
}

// 5. 被拒绝的请求不产生副作用；只读审计幂等。
func TestRejectedRequestsAreSideEffectFree(t *testing.T) {
	p := mustSetup(t)
	ruleID, _ := p.UpsertRule("Base", "alice", "secret", ontology.EffectDeny, "deny")
	exp, _ := p.Export("Leaf", "alice")
	before, _ := p.GetExport(exp.ID)
	logBefore := len(p.OpLog())

	// 各类被拒绝请求。
	_, _ = p.Export("Ghost", "alice")
	_, _ = p.Export("Leaf", "bob")
	_, _ = p.Export("Ghost", "bob")
	_, _ = p.AuditTrace("exp-9999", "secret")
	_, _ = p.AuditTrace(exp.ID, "name")
	_ = p.SetParent("Leaf", "Leaf") // 自继承成环，拒绝
	_ = p.AddType("Base", "", nil)  // 重复类型，拒绝

	after, _ := p.GetExport(exp.ID)
	if len(p.OpLog()) != logBefore {
		t.Fatalf("rejected requests mutated op log: %d -> %d", logBefore, len(p.OpLog()))
	}
	if fmt.Sprintf("%v", before.Exclusions) != fmt.Sprintf("%v", after.Exclusions) ||
		before.ID != after.ID || before.Seq != after.Seq {
		t.Fatalf("rejected requests mutated export: %+v -> %+v", before, after)
	}
	// 既有规则仍可解析。
	rec, err := p.AuditTrace(exp.ID, "secret")
	if err != nil || rec.RuleID != ruleID {
		t.Fatalf("rule resolution changed after rejected requests: %+v %v", rec, err)
	}
	// 审计幂等：连续多次结果一致。
	for i := 0; i < 5; i++ {
		rec2, err := p.AuditTrace(exp.ID, "secret")
		if err != nil || rec2 != rec {
			t.Fatalf("audit not repeatable: %+v vs %+v", rec2, rec)
		}
	}
}

// 6. 性能可验证性：解析已固化依据检查的记录数与历史变更总次数无关（恒为 1）。
func TestAuditResolutionConstantProbes(t *testing.T) {
	p := mustSetup(t)
	if _, err := p.UpsertRule("Base", "alice", "secret", ontology.EffectDeny, "deny"); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	exp, err := p.Export("Leaf", "alice")
	if err != nil {
		t.Fatalf("export: %v", err)
	}

	// 制造大量历史规则变更（覆盖新增、删除、重组交错）。
	const N = 5000
	for i := 0; i < N; i++ {
		if _, err := p.UpsertRule("Mid", "alice", "secret", ontology.EffectDeny, fmt.Sprintf("v%d", i)); err != nil {
			t.Fatalf("upsert %d: %v", i, err)
		}
		if i%3 == 0 {
			_ = p.DeleteRule("Mid", "alice", "secret")
		}
		if i%7 == 0 {
			_ = p.SetParent("Leaf", "Mid")
		}
	}

	before := p.ProbeCount()
	rec, err := p.AuditTrace(exp.ID, "secret")
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	got := p.ProbeCount() - before
	if got != 1 {
		t.Fatalf("resolution inspected %d records after %d historical changes, want exactly 1", got, N)
	}
	if rec.Rule.Content != "deny" {
		t.Fatalf("snapshot drifted: %q", rec.Rule.Content)
	}
	t.Logf("性能验证: 历史变更=%d 次, 单次解析检查记录数=%d (常数)", N, got)
}

// replayFromLog 按全局串行日志重放变更到朴素模型，验证每个导出对应当初的状态。
func replayFromLog(t *testing.T, log []ontology.OpRecord) map[string]naiveExportSnap {
	t.Helper()
	m := newNaiveModel()
	snaps := map[string]naiveExportSnap{}
	for _, rec := range log {
		switch rec.Kind {
		case ontology.OpAddType:
			m.addType(rec.TypeName, rec.Parent, rec.Props)
		case ontology.OpSetParent:
			m.setParent(rec.TypeName, rec.Parent)
		case ontology.OpUpsertRule:
			// 日志不含规则 ID，重放时只需 effect/content；ID 对照由差分测试覆盖。
			m.upsert(rec.TypeName, rec.Subject, rec.Property, rec.Effect, rec.Content, "")
		case ontology.OpDeleteRule:
			m.deleteRule(rec.TypeName, rec.Subject, rec.Property)
		case ontology.OpExport:
			snaps[rec.ExportID] = m.export(rec.TypeName, rec.Subject)
		}
	}
	return snaps
}

// 7. 并发导出与规则调整交错：每个导出固化的结果对应其串行化点那一刻的状态。
func TestConcurrentExportAndRuleAdjustments(t *testing.T) {
	p := ontology.NewPlatform()
	for _, u := range []string{"u0", "u1", "u2"} {
		p.AddPrincipal(u)
	}
	types := []struct {
		name, parent string
		props        []string
	}{
		{"T0", "", []string{"p0", "p1"}},
		{"T1", "T0", []string{"p2"}},
		{"T2", "T1", []string{"p3"}},
		{"T3", "T0", []string{"p4"}},
	}
	for _, td := range types {
		if err := p.AddType(td.name, td.parent, td.props); err != nil {
			t.Fatalf("add type: %v", err)
		}
	}

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for i := 0; i < 200; i++ {
				tn := types[rng.Intn(len(types))].name
				subj := fmt.Sprintf("u%d", rng.Intn(3))
				prop := fmt.Sprintf("p%d", rng.Intn(5))
				switch rng.Intn(4) {
				case 0:
					_, _ = p.UpsertRule(tn, subj, prop, ontology.EffectDeny, fmt.Sprintf("deny-%d", i))
				case 1:
					_ = p.DeleteRule(tn, subj, prop)
				case 2:
					_ = p.SetParent(types[rng.Intn(len(types))].name, types[rng.Intn(len(types))].name)
				default:
					if _, err := p.Export(tn, subj); err != nil {
						t.Errorf("export: %v", err)
					}
				}
			}
		}(int64(g) * 7919)
	}
	wg.Wait()

	// 按串行日志重放，逐导出比对固化结果。
	snaps := replayFromLog(t, p.OpLog())
	checked := 0
	for _, rec := range p.OpLog() {
		if rec.Kind != ontology.OpExport {
			continue
		}
		exp, ok := p.GetExport(rec.ExportID)
		if !ok {
			t.Fatalf("export %s missing", rec.ExportID)
		}
		want := snaps[rec.ExportID]
		if len(exp.Exclusions) != len(want.exclusions) {
			t.Fatalf("export %s exclusions=%v, replay want %v", rec.ExportID, exp.Exclusions, want.exclusions)
		}
		for prop := range want.exclusions {
			if _, ok := exp.Exclusions[prop]; !ok {
				t.Fatalf("export %s missing exclusion %s (have %v)", rec.ExportID, prop, exp.Exclusions)
			}
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("no exports were checked")
	}
	t.Logf("并发验证: 重放串行日志比对导出数=%d, 全部与各自串行化点状态一致", checked)
}

// 8. 与独立朴素快照模型对照：随机构造的规则变更与导出交错序列。
func TestDifferentialAgainstNaiveSnapshotModel(t *testing.T) {
	rng := rand.New(rand.NewSource(20261007))
	p := ontology.NewPlatform()
	m := newNaiveModel()

	subjects := []string{"s0", "s1"}
	for _, s := range subjects {
		p.AddPrincipal(s)
	}
	typeNames := []string{"A", "B", "C", "D"}
	props := []string{"x", "y", "z"}
	parentOf := map[string]string{"A": "", "B": "A", "C": "B", "D": "A"}
	for _, tn := range typeNames {
		if err := p.AddType(tn, parentOf[tn], props); err != nil {
			t.Fatalf("add type: %v", err)
		}
		m.addType(tn, parentOf[tn], props)
	}

	var exportIDs []string
	decisions := 0
	for step := 0; step < 400; step++ {
		tn := typeNames[rng.Intn(len(typeNames))]
		subj := subjects[rng.Intn(len(subjects))]
		prop := props[rng.Intn(len(props))]
		switch rng.Intn(5) {
		case 0, 1: // 覆盖新增 / 修改
			effect := ontology.EffectDeny
			if rng.Intn(3) == 0 {
				effect = ontology.EffectAllow
			}
			content := fmt.Sprintf("step-%d", step)
			id, err := p.UpsertRule(tn, subj, prop, effect, content)
			if err != nil {
				t.Fatalf("step %d upsert: %v", step, err)
			}
			m.upsert(tn, subj, prop, effect, content, id)
			t.Logf("step=%d 判定输入: upsert(%s,%s,%s,%s,%q) -> rule=%s", step, tn, subj, prop, effect, content, id)
		case 2: // 覆盖删除回落
			if err := p.DeleteRule(tn, subj, prop); err != nil {
				t.Fatalf("step %d delete: %v", step, err)
			}
			m.deleteRule(tn, subj, prop)
			t.Logf("step=%d 判定输入: delete(%s,%s,%s)", step, tn, subj, prop)
		case 3: // 继承关系重组
			newParent := typeNames[rng.Intn(len(typeNames))]
			if err := p.SetParent(tn, newParent); err == nil {
				m.setParent(tn, newParent)
				t.Logf("step=%d 判定输入: setParent(%s,%s)", step, tn, newParent)
			}
		default: // 导出并即时比对
			exp, err := p.Export(tn, subj)
			if err != nil {
				t.Fatalf("step %d export: %v", step, err)
			}
			m.recordExport(exp.ID, tn, subj)
			exportIDs = append(exportIDs, exp.ID)
			snap := m.exports[exp.ID]
			if len(exp.Exclusions) != len(snap.exclusions) {
				t.Fatalf("step %d export %s: exclusions=%v, naive=%v", step, exp.ID, exp.Exclusions, snap.exclusions)
			}
			for prop, ruleID := range exp.Exclusions {
				nr, ok := snap.exclusions[prop]
				if !ok || nr.id != ruleID {
					t.Fatalf("step %d export %s prop %s: rule=%q, naive=%+v", step, exp.ID, prop, ruleID, nr)
				}
				t.Logf("step=%d 判定: export=%s 输入=(%s,%s) prop=%s 输出=excluded 依据=%s",
					step, exp.ID, tn, subj, prop, ruleID)
				decisions++
			}
		}
	}
	if len(exportIDs) == 0 {
		t.Fatal("no exports generated")
	}

	// 全部序列结束后，再制造一轮变更，然后对历史导出做审计对照。
	for i := 0; i < 50; i++ {
		tn := typeNames[rng.Intn(len(typeNames))]
		subj := subjects[rng.Intn(len(subjects))]
		prop := props[rng.Intn(len(props))]
		id, err := p.UpsertRule(tn, subj, prop, ontology.EffectDeny, fmt.Sprintf("post-%d", i))
		if err != nil {
			t.Fatalf("post upsert: %v", err)
		}
		m.upsert(tn, subj, prop, ontology.EffectDeny, fmt.Sprintf("post-%d", i), id)
		_ = p.DeleteRule(tn, subj, prop)
		m.deleteRule(tn, subj, prop)
	}

	audited := 0
	for _, expID := range exportIDs {
		exp, ok := p.GetExport(expID)
		if !ok {
			t.Fatalf("export %s missing", expID)
		}
		props := append([]string(nil), exp.Included...)
		for prop := range exp.Exclusions {
			props = append(props, prop)
		}
		sort.Strings(props)
		for _, prop := range props {
			rec, err := p.AuditTrace(expID, prop)
			nr, excluded := m.audit(expID, prop)
			if !excluded {
				if catOf(err) != ontology.CategoryPropertyNotExcluded {
					t.Fatalf("export %s prop %s: want not_excluded, got %v", expID, prop, err)
				}
				continue
			}
			if err != nil {
				t.Fatalf("export %s prop %s: audit: %v", expID, prop, err)
			}
			if rec.RuleID != nr.id || rec.Rule.Content != nr.content ||
				rec.Rule.Effect != nr.effect || rec.DeclaredOn != nr.declared {
				t.Fatalf("export %s prop %s: audit=%+v, naive=%+v", expID, prop, rec, nr)
			}
			t.Logf("审计: export=%s prop=%s 输出=(rule=%s, content=%q, declaredOn=%s)",
				expID, prop, rec.RuleID, rec.Rule.Content, rec.DeclaredOn)
			audited++
		}
	}
	if audited == 0 {
		t.Fatal("no exclusions audited")
	}
	t.Logf("差分对照: 导出数=%d, 排除判定=%d, 历史审计比对=%d, 全部一致", len(exportIDs), decisions, audited)
}
