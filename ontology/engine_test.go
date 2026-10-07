package ontology

import "testing"

// 测试通用夹具：
//   - 租户 ta / tb / tc
//   - 类型 doc（放宽需要授权依据）与 open（不需要）
//   - 实例 doc-a1（ta, high）、doc-a2（ta, low）、doc-b1（tb, high）
func newFixture(t *testing.T) *Engine {
	t.Helper()
	e := NewEngine()
	for _, tenant := range []string{"ta", "tb", "tc"} {
		if err := e.RegisterTenant(tenant); err != nil {
			t.Fatalf("register tenant: %v", err)
		}
	}
	preds := map[string]PredicateFunc{
		"highValue":  func(inst Instance, _ Subject) bool { return inst.Attributes["level"] == "high" },
		"sameTenant": func(inst Instance, subj Subject) bool { return inst.OwnerTenant == subj.Tenant },
	}
	for _, def := range []ObjectTypeDef{
		{Name: "doc", Attributes: []string{"title", "secret"}, Predicates: preds, RequireRelaxationBasis: true},
		{Name: "open", Attributes: []string{"title", "secret"}, Predicates: preds, RequireRelaxationBasis: false},
	} {
		if err := e.RegisterObjectType(def); err != nil {
			t.Fatalf("register type: %v", err)
		}
	}
	for _, inst := range []Instance{
		{ID: "doc-a1", Type: "doc", OwnerTenant: "ta", Attributes: map[string]string{"level": "high"}},
		{ID: "doc-a2", Type: "doc", OwnerTenant: "ta", Attributes: map[string]string{"level": "low"}},
		{ID: "doc-b1", Type: "doc", OwnerTenant: "tb", Attributes: map[string]string{"level": "high"}},
	} {
		if err := e.RegisterInstance(inst); err != nil {
			t.Fatalf("register instance: %v", err)
		}
	}
	return e
}

var (
	subjA = Subject{ID: "u1", Tenant: "ta", Roles: []string{"analyst"}}
	subjB = Subject{ID: "u2", Tenant: "tb", Roles: []string{"analyst"}}
)

func mustSetDefault(t *testing.T, e *Engine, typeName string, entries ...RuleEntry) {
	t.Helper()
	if err := e.SetGlobalDefault(typeName, entries); err != nil {
		t.Fatalf("set default: %v", err)
	}
}

func mustSetOverride(t *testing.T, e *Engine, tenant, typeName string, entries ...RuleEntry) {
	t.Helper()
	if err := e.SetTenantOverride(tenant, typeName, entries); err != nil {
		t.Fatalf("set override: %v", err)
	}
}

func mustDecide(t *testing.T, e *Engine, subj Subject, instanceID, attribute string) Decision {
	t.Helper()
	d, err := e.Decide(subj, "read", instanceID, attribute)
	if err != nil {
		t.Fatalf("decide(%s,%s): %v", instanceID, attribute, err)
	}
	return d
}

func requireClass(t *testing.T, err error, class ErrorClass) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error class %s, got nil", class)
	}
	if ClassOf(err) != class {
		t.Fatalf("expected error class %s, got %v", class, err)
	}
}

// 收紧与放宽交叉：同一对象类型上，租户 A 收紧、租户 B 放宽、租户 C 沿用默认。
func TestTightenAndRelaxCross(t *testing.T) {
	e := newFixture(t)
	mustSetDefault(t, e, "doc",
		RuleEntry{Action: "read", Effect: Allow},
		RuleEntry{Action: "read", Attribute: "secret", Effect: Deny},
	)
	// 租户 A 收紧：默认允许的 title 改为拒绝，无需授权依据。
	mustSetOverride(t, e, "ta", "doc",
		RuleEntry{Action: "read", Attribute: "title", Effect: Deny},
	)
	// 租户 B 放宽：默认拒绝的 secret 改为允许，必须带授权依据。
	mustSetOverride(t, e, "tb", "doc",
		RuleEntry{Action: "read", Attribute: "secret", Effect: Allow, Basis: "dpo-approval-7"},
	)

	if d := mustDecide(t, e, subjA, "doc-a1", "title"); d.Effect != Deny {
		t.Fatalf("ta title should be tightened to deny, got %v", d.Effect)
	}
	d := mustDecide(t, e, subjB, "doc-b1", "secret")
	if d.Effect != Allow {
		t.Fatalf("tb secret should be relaxed to allow, got %v", d.Effect)
	}
	if len(d.Basis.WinningEntries) != 1 || d.Basis.WinningEntries[0].Layer != LayerOverride {
		t.Fatalf("tb secret should be decided by override layer, got %+v", d.Basis.WinningEntries)
	}
	// doc-b1 归属 tb，tb 的覆盖只触及 secret；title 仍沿用默认允许。
	if d := mustDecide(t, e, Subject{Tenant: "tc"}, "doc-b1", "title"); d.Effect != Allow {
		t.Fatalf("default title should allow, got %v", d.Effect)
	}
	// doc-b1 归属 tb，tb 的放宽只覆盖 secret；用 tc 主体访问 doc-a1（归属 ta）的 secret 应被拒。
	if d := mustDecide(t, e, Subject{Tenant: "tc"}, "doc-a1", "secret"); d.Effect != Deny {
		t.Fatalf("ta secret should stay denied, got %v", d.Effect)
	}
}

// 放宽缺少授权依据：类型声明需要依据时被拒；声明不需要时放行。
func TestRelaxBasisRequirement(t *testing.T) {
	e := newFixture(t)
	mustSetDefault(t, e, "doc", RuleEntry{Action: "read", Attribute: "secret", Effect: Deny})
	mustSetDefault(t, e, "open", RuleEntry{Action: "read", Attribute: "secret", Effect: Deny})

	// doc 要求依据：无依据的放宽必须被拒绝。
	err := e.SetTenantOverride("ta", "doc", []RuleEntry{{Action: "read", Attribute: "secret", Effect: Allow}})
	requireClass(t, err, ErrMissingBasis)
	// 带依据则接受。
	mustSetOverride(t, e, "ta", "doc",
		RuleEntry{Action: "read", Attribute: "secret", Effect: Allow, Basis: "legal-hold-42"})
	// open 不要求依据：无依据的放宽直接接受。
	mustSetOverride(t, e, "ta", "open",
		RuleEntry{Action: "read", Attribute: "secret", Effect: Allow})
}

// 放宽判定的保守性：默认规则内部有更细的拒绝条目时，覆盖允许仍视为放宽；
// 收紧永远不需要依据；默认已允许且不触及任何拒绝时不视为放宽。
func TestRelaxDetectionConservative(t *testing.T) {
	e := newFixture(t)
	mustSetDefault(t, e, "doc",
		RuleEntry{Action: "read", Attribute: "secret", Effect: Allow},
		RuleEntry{Action: "read", Attribute: "secret", Predicate: "highValue", Effect: Deny},
	)
	// 覆盖 (read, secret, allow) 与默认的 (read, secret, highValue, deny) 相交 => 视为放宽。
	err := e.SetTenantOverride("ta", "doc", []RuleEntry{{Action: "read", Attribute: "secret", Effect: Allow}})
	requireClass(t, err, ErrMissingBasis)
	// 收紧不需要依据。
	mustSetOverride(t, e, "ta", "doc", RuleEntry{Action: "read", Attribute: "secret", Effect: Deny})

	e2 := newFixture(t)
	mustSetDefault(t, e2, "doc", RuleEntry{Action: "read", Effect: Allow})
	// 默认已整体允许且无拒绝相交：不是放宽，不需要依据。
	mustSetOverride(t, e2, "ta", "doc", RuleEntry{Action: "read", Attribute: "secret", Effect: Allow})
}

// 覆盖粒度不一致（属性级覆盖 vs 谓词级默认）：覆盖范围完全包含默认条目时替代之。
func TestGranularitySubsumptionReplace(t *testing.T) {
	e := newFixture(t)
	mustSetDefault(t, e, "doc",
		RuleEntry{Action: "read", Attribute: "secret", Predicate: "highValue", Effect: Deny},
	)
	mustSetOverride(t, e, "ta", "doc",
		RuleEntry{Action: "read", Attribute: "secret", Effect: Allow, Basis: "dpo-9"},
	)
	d := mustDecide(t, e, subjA, "doc-a1", "secret")
	if d.Effect != Allow {
		t.Fatalf("attr-level override should replace finer default, got %v", d.Effect)
	}
	if d.Basis.DefaultsSubsumed != 1 {
		t.Fatalf("expected 1 subsumed default, got %d", d.Basis.DefaultsSubsumed)
	}
}

// 覆盖粒度不一致且效果冲突：冲突全部来自覆盖层 => ErrOverrideConflict。
func TestGranularityConflictOverrideInternal(t *testing.T) {
	e := newFixture(t)
	mustSetDefault(t, e, "doc", RuleEntry{Action: "read", Effect: Allow})
	// 一条只覆盖属性，另一条只覆盖谓词条件，二者在 (secret, highValue) 单元上冲突。
	mustSetOverride(t, e, "ta", "doc",
		RuleEntry{Action: "read", Attribute: "secret", Effect: Deny},
		RuleEntry{Action: "read", Predicate: "highValue", Effect: Allow, Basis: "x"},
	)
	_, err := e.Decide(subjA, "read", "doc-a1", "secret")
	requireClass(t, err, ErrOverrideConflict)
	// 非高价值实例上谓词不成立，属性级覆盖唯一裁决，不报错。
	if d := mustDecide(t, e, subjA, "doc-a2", "secret"); d.Effect != Deny {
		t.Fatalf("doc-a2 secret should be denied by attr override, got %v", d.Effect)
	}
}

// 覆盖粒度不一致且效果冲突：冲突跨层级 => ErrMergeNonUnique。
func TestGranularityConflictMixedLayers(t *testing.T) {
	e := newFixture(t)
	if err := e.RegisterInstance(Instance{ID: "open-a1", Type: "open", OwnerTenant: "ta",
		Attributes: map[string]string{"level": "high"}}); err != nil {
		t.Fatalf("register instance: %v", err)
	}
	mustSetDefault(t, e, "open",
		RuleEntry{Action: "read", Predicate: "highValue", Effect: Deny},
	)
	mustSetOverride(t, e, "ta", "open",
		RuleEntry{Action: "read", Attribute: "secret", Effect: Allow},
	)
	_, err := e.Decide(subjA, "read", "open-a1", "secret")
	requireClass(t, err, ErrMergeNonUnique)
}

// 覆盖粒度不一致但效果一致：合并结果唯一且确定。
func TestGranularityAgreementUnique(t *testing.T) {
	e := newFixture(t)
	mustSetDefault(t, e, "doc",
		RuleEntry{Action: "read", Predicate: "highValue", Effect: Allow},
	)
	mustSetOverride(t, e, "ta", "doc",
		RuleEntry{Action: "read", Attribute: "secret", Effect: Allow, Basis: "b1"},
	)
	d := mustDecide(t, e, subjA, "doc-a1", "secret")
	if d.Effect != Allow {
		t.Fatalf("agreeing entries should yield allow, got %v", d.Effect)
	}
	if len(d.Basis.WinningEntries) != 2 {
		t.Fatalf("expected 2 winning entries (attr + predicate), got %+v", d.Basis.WinningEntries)
	}
}

// 更细的覆盖在交叠单元上胜过更粗的默认；未交叠部分仍沿用默认。
func TestFinerOverrideBeatsCoarserDefault(t *testing.T) {
	e := newFixture(t)
	mustSetDefault(t, e, "doc",
		RuleEntry{Action: "read", Attribute: "secret", Effect: Allow},
	)
	mustSetOverride(t, e, "ta", "doc",
		RuleEntry{Action: "read", Attribute: "secret", Predicate: "highValue", Effect: Deny},
	)
	if d := mustDecide(t, e, subjA, "doc-a1", "secret"); d.Effect != Deny {
		t.Fatalf("high-value secret should be denied by finer override, got %v", d.Effect)
	}
	d := mustDecide(t, e, subjA, "doc-a2", "secret")
	if d.Effect != Allow {
		t.Fatalf("low-value secret should keep default allow, got %v", d.Effect)
	}
	if len(d.Basis.WinningEntries) != 1 || d.Basis.WinningEntries[0].Layer != LayerDefault {
		t.Fatalf("low-value secret should be decided by default layer, got %+v", d.Basis.WinningEntries)
	}
}

// 跨租户归属方向：判定依据实例归属租户的覆盖规则，而非主体归属租户。
func TestCrossTenantOwnerDirection(t *testing.T) {
	e := newFixture(t)
	mustSetDefault(t, e, "open", RuleEntry{Action: "read", Effect: Allow})
	if err := e.RegisterInstance(Instance{ID: "open-a1", Type: "open", OwnerTenant: "ta",
		Attributes: map[string]string{"level": "low"}}); err != nil {
		t.Fatalf("register instance: %v", err)
	}
	if err := e.RegisterInstance(Instance{ID: "open-b1", Type: "open", OwnerTenant: "tb",
		Attributes: map[string]string{"level": "low"}}); err != nil {
		t.Fatalf("register instance: %v", err)
	}
	// ta 拒绝 secret，tb 拒绝 title。
	mustSetOverride(t, e, "ta", "open", RuleEntry{Action: "read", Attribute: "secret", Effect: Deny})
	mustSetOverride(t, e, "tb", "open", RuleEntry{Action: "read", Attribute: "title", Effect: Deny})

	// tb 的主体访问 ta 的实例：适用 ta 的覆盖（secret 拒、title 允许）。
	if d := mustDecide(t, e, subjB, "open-a1", "secret"); d.Effect != Deny {
		t.Fatalf("tb subject on ta instance secret should be denied by ta override, got %v", d.Effect)
	}
	if d := mustDecide(t, e, subjB, "open-a1", "title"); d.Effect != Allow {
		t.Fatalf("tb subject on ta instance title should follow ta rules (allow), got %v", d.Effect)
	}
	// ta 主体访问 tb 的实例：适用 tb 的覆盖（title 拒、secret 允许）。
	if d := mustDecide(t, e, subjA, "open-b1", "title"); d.Effect != Deny {
		t.Fatalf("ta subject on tb instance title should be denied by tb override, got %v", d.Effect)
	}
	if d := mustDecide(t, e, subjA, "open-b1", "secret"); d.Effect != Allow {
		t.Fatalf("ta subject on tb instance secret should follow tb rules (allow), got %v", d.Effect)
	}
	// 判定依据中记录的归属租户必须是实例归属租户。
	d := mustDecide(t, e, subjB, "open-a1", "secret")
	if d.Basis.OwnerTenant != "ta" {
		t.Fatalf("basis owner tenant should be ta, got %q", d.Basis.OwnerTenant)
	}
}

// 租户间互不污染：修改 tb 的覆盖不影响 ta 实例的判定结果。
func TestTenantIsolationNoPollution(t *testing.T) {
	e := newFixture(t)
	mustSetDefault(t, e, "open", RuleEntry{Action: "read", Effect: Allow})
	if err := e.RegisterInstance(Instance{ID: "open-a1", Type: "open", OwnerTenant: "ta",
		Attributes: map[string]string{"level": "low"}}); err != nil {
		t.Fatalf("register instance: %v", err)
	}
	mustSetOverride(t, e, "ta", "open", RuleEntry{Action: "read", Attribute: "secret", Effect: Deny})
	before := mustDecide(t, e, subjB, "open-a1", "secret")
	// tb 反复变更自己的覆盖规则。
	mustSetOverride(t, e, "tb", "open", RuleEntry{Action: "read", Attribute: "secret", Effect: Deny})
	mustSetOverride(t, e, "tb", "open", RuleEntry{Action: "read", Effect: Deny})
	if err := e.RevokeTenantOverride("tb", "open"); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	after := mustDecide(t, e, subjB, "open-a1", "secret")
	if before.Effect != after.Effect || before.Basis.OverrideVersion != after.Basis.OverrideVersion {
		t.Fatalf("ta instance decision polluted by tb override changes: before=%+v after=%+v", before, after)
	}
}

// 撤销覆盖是原子的，且历史判定记录保持不变。
func TestRevokeAtomicAndHistoryPreserved(t *testing.T) {
	e := newFixture(t)
	mustSetDefault(t, e, "open", RuleEntry{Action: "read", Effect: Allow})
	if err := e.RegisterInstance(Instance{ID: "open-a1", Type: "open", OwnerTenant: "ta",
		Attributes: map[string]string{"level": "low"}}); err != nil {
		t.Fatalf("register instance: %v", err)
	}
	// 覆盖同时拒绝两个属性。
	mustSetOverride(t, e, "ta", "open",
		RuleEntry{Action: "read", Attribute: "title", Effect: Deny},
		RuleEntry{Action: "read", Attribute: "secret", Effect: Deny},
	)
	if d := mustDecide(t, e, subjA, "open-a1", "title"); d.Effect != Deny {
		t.Fatalf("title should be denied before revoke, got %v", d.Effect)
	}
	auditBefore := len(e.Audit())

	if err := e.RevokeTenantOverride("ta", "open"); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	// 撤销后两个属性都立即恢复默认允许，且 OverrideVersion 归零（不存在中间状态）。
	for _, attr := range []string{"title", "secret"} {
		d := mustDecide(t, e, subjA, "open-a1", attr)
		if d.Effect != Allow || d.Basis.OverrideVersion != 0 {
			t.Fatalf("after revoke %s should follow default allow with no override, got %+v", attr, d)
		}
	}
	// 历史判定记录保持不变：撤销前的 deny 判定仍在审计日志中，且依据不变。
	var historical *AuditRecord
	for i, rec := range e.Audit() {
		if rec.Op != OpDecide {
			continue
		}
		in := rec.Input.(DecideInput)
		if in.InstanceID == "open-a1" && in.Attribute == "title" {
			historical = &e.Audit()[i]
			break
		}
	}
	if historical == nil {
		t.Fatal("historical decision record missing")
	}
	out := historical.Output.(Decision)
	if out.Effect != Deny || out.Basis.OverrideVersion == 0 {
		t.Fatalf("historical decision must stay deny with override basis, got %+v", out)
	}
	if len(e.Audit()) != auditBefore+3 { // revoke + 2 decides
		t.Fatalf("audit should only grow by accepted ops, before=%d after=%d", auditBefore, len(e.Audit()))
	}
	// 撤销未登记的覆盖是幂等空操作。
	if err := e.RevokeTenantOverride("tc", "open"); err != nil {
		t.Fatalf("revoke non-existent override should be a no-op, got %v", err)
	}
}

// 全局默认规则原子切换：未覆盖变更部分的租户立即感知新默认，
// 已覆盖的租户继续沿用其覆盖规则。
func TestDefaultSwitchAtomicAcrossTenants(t *testing.T) {
	e := newFixture(t)
	mustSetDefault(t, e, "doc",
		RuleEntry{Action: "read", Attribute: "title", Effect: Allow},
		RuleEntry{Action: "read", Attribute: "secret", Effect: Deny},
	)
	// ta 覆盖了 secret（放宽）；tb 没有任何覆盖。
	mustSetOverride(t, e, "ta", "doc",
		RuleEntry{Action: "read", Attribute: "secret", Effect: Allow, Basis: "dpo-1"},
	)
	// 原子切换默认规则：title 变拒、secret 变允许。
	mustSetDefault(t, e, "doc",
		RuleEntry{Action: "read", Attribute: "title", Effect: Deny},
		RuleEntry{Action: "read", Attribute: "secret", Effect: Allow},
	)
	// tb（无覆盖）立即感知新默认。
	if d := mustDecide(t, e, subjB, "doc-b1", "title"); d.Effect != Deny {
		t.Fatalf("tb title should follow new default deny, got %v", d.Effect)
	}
	d := mustDecide(t, e, subjB, "doc-b1", "secret")
	if d.Effect != Allow || d.Basis.WinningEntries[0].Layer != LayerDefault {
		t.Fatalf("tb secret should follow new default allow via default layer, got %+v", d)
	}
	// ta 的 secret 覆盖不受默认切换影响，仍由覆盖层裁决。
	d = mustDecide(t, e, subjA, "doc-a1", "secret")
	if d.Effect != Allow || d.Basis.WinningEntries[0].Layer != LayerOverride {
		t.Fatalf("ta secret should stay override-decided, got %+v", d)
	}
	// ta 未覆盖 title，立即感知新默认拒绝。
	if d := mustDecide(t, e, subjA, "doc-a1", "title"); d.Effect != Deny {
		t.Fatalf("ta title should follow new default deny, got %v", d.Effect)
	}
	if d.Basis.DefaultVersion != 2 {
		t.Fatalf("decision should cite default version 2, got %d", d.Basis.DefaultVersion)
	}
}

// 错误类别按固定且唯一的优先顺序汇报。
func TestErrorPriority(t *testing.T) {
	e := newFixture(t)
	mustSetDefault(t, e, "doc",
		RuleEntry{Action: "read", Attribute: "secret", Effect: Deny},
		RuleEntry{Action: "read", Attribute: "title", Effect: Allow},
	)

	// 同时缺少租户与授权依据：优先报告 ErrNotFound。
	err := e.SetTenantOverride("ghost", "doc", []RuleEntry{{Action: "read", Attribute: "secret", Effect: Allow}})
	requireClass(t, err, ErrNotFound)
	// 同时缺少依据且声明内部冲突：优先报告 ErrMissingBasis。
	err = e.SetTenantOverride("ta", "doc", []RuleEntry{
		{Action: "read", Attribute: "secret", Effect: Allow},
		{Action: "read", Attribute: "title", Effect: Allow},
		{Action: "read", Attribute: "title", Effect: Deny},
	})
	requireClass(t, err, ErrMissingBasis)
	// 仅声明内部冲突（条目不构成放宽）：报告 ErrOverrideConflict。
	err = e.SetTenantOverride("ta", "doc", []RuleEntry{
		{Action: "read", Attribute: "title", Effect: Allow},
		{Action: "read", Attribute: "title", Effect: Deny},
	})
	requireClass(t, err, ErrOverrideConflict)
	// 类别数值即优先级。
	if !(ErrNotFound < ErrMissingBasis && ErrMissingBasis < ErrOverrideConflict && ErrOverrideConflict < ErrMergeNonUnique) {
		t.Fatal("error class ordering must be not-found < missing-basis < override-conflict < merge-non-unique")
	}
}

// 被拒绝的声明与判定不影响任何规则状态与审计记录。
func TestRejectedOpsHaveNoSideEffects(t *testing.T) {
	e := newFixture(t)
	mustSetDefault(t, e, "doc", RuleEntry{Action: "read", Attribute: "secret", Effect: Deny})
	mustSetOverride(t, e, "ta", "doc",
		RuleEntry{Action: "read", Attribute: "secret", Effect: Allow, Basis: "ok-1"},
	)
	auditLen := len(e.Audit())

	// 被拒绝的覆盖声明：状态与审计不变。
	err := e.SetTenantOverride("ta", "doc", []RuleEntry{{Action: "read", Attribute: "title", Effect: Allow}})
	requireClass(t, err, ErrMissingBasis)
	if len(e.Audit()) != auditLen {
		t.Fatal("rejected override declaration must not append audit records")
	}
	d := mustDecide(t, e, subjA, "doc-a1", "secret")
	if d.Effect != Allow || d.Basis.OverrideVersion != 1 {
		t.Fatalf("rejected declaration must not change rule state, got %+v", d)
	}
	// 被拒绝的判定（合并不唯一）：审计不变。
	mustSetOverride(t, e, "tb", "doc",
		RuleEntry{Action: "read", Attribute: "secret", Effect: Allow, Basis: "ok-2"},
		RuleEntry{Action: "read", Predicate: "highValue", Effect: Deny},
	)
	auditLen = len(e.Audit())
	if _, err := e.Decide(subjB, "read", "doc-b1", "secret"); ClassOf(err) != ErrOverrideConflict {
		t.Fatalf("expected override conflict, got %v", err)
	}
	if len(e.Audit()) != auditLen {
		t.Fatal("rejected decision must not append audit records")
	}
}

// 封闭世界：无任何适用条目时默认拒绝。
func TestClosedWorldDeny(t *testing.T) {
	e := newFixture(t)
	d := mustDecide(t, e, subjA, "doc-a1", "title")
	if d.Effect != Deny || len(d.Basis.WinningEntries) != 0 {
		t.Fatalf("no applicable rules should deny, got %+v", d)
	}
}

// 未登记的谓词按不成立处理（安全方向）。
func TestUnknownPredicateNeverApplies(t *testing.T) {
	e := newFixture(t)
	mustSetDefault(t, e, "open", RuleEntry{Action: "read", Effect: Allow})
	if err := e.RegisterInstance(Instance{ID: "open-a1", Type: "open", OwnerTenant: "ta",
		Attributes: map[string]string{"level": "low"}}); err != nil {
		t.Fatalf("register instance: %v", err)
	}
	mustSetOverride(t, e, "ta", "open",
		RuleEntry{Action: "read", Attribute: "secret", Predicate: "no-such-predicate", Effect: Deny},
	)
	if d := mustDecide(t, e, subjA, "open-a1", "secret"); d.Effect != Allow {
		t.Fatalf("unknown predicate entry must never apply, got %v", d.Effect)
	}
}

// 审计日志完整记录每次被接受调用的输入、输出与规则层级依据。
func TestAuditLogCompleteness(t *testing.T) {
	e := newFixture(t)
	mustSetDefault(t, e, "doc", RuleEntry{Action: "read", Effect: Allow})
	mustSetOverride(t, e, "ta", "doc", RuleEntry{Action: "read", Attribute: "secret", Effect: Deny})
	mustDecide(t, e, subjA, "doc-a1", "secret")

	audit := e.Audit()
	var seqs uint64
	for _, rec := range audit {
		seqs++
		if rec.Seq != seqs {
			t.Fatalf("audit seq should be dense and ordered, got %d at position %d", rec.Seq, seqs)
		}
		if rec.Input == nil {
			t.Fatalf("audit record %d missing input", rec.Seq)
		}
	}
	last := audit[len(audit)-1]
	if last.Op != OpDecide {
		t.Fatalf("last record should be decide, got %s", last.Op)
	}
	in := last.Input.(DecideInput)
	if in.Subject.ID != subjA.ID || in.Subject.Tenant != subjA.Tenant ||
		in.InstanceID != "doc-a1" || in.Attribute != "secret" || in.Action != "read" {
		t.Fatalf("decide input not fully recorded: %+v", in)
	}
	out := last.Output.(Decision)
	if out.Basis.OwnerTenant != "ta" || out.Basis.OverrideVersion != 1 || len(out.Basis.WinningEntries) != 1 {
		t.Fatalf("decide basis not fully recorded: %+v", out.Basis)
	}
}
