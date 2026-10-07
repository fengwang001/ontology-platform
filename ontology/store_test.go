package ontology

import "testing"

func newTestStore() *Store {
	s := NewStore()
	s.AddTenant("tA")
	s.AddTenant("tB")
	s.DefineObjectType(ObjectTypeDef{
		Name:  "Doc",
		Attrs: []string{"title", "body", "secret"},
		Predicates: []Predicate{
			{ID: "pEng", Field: "dept", Op: "eq", Value: "eng"},
			{ID: "pHigh", Field: "level", Op: "ge", Value: 5},
		},
	})
	return s
}

func decideReq(subjectTenant, instanceTenant string) AccessRequest {
	return AccessRequest{
		SubjectTenant:  subjectTenant,
		SubjectClass:   "reader",
		Type:           "Doc",
		InstanceTenant: instanceTenant,
		Instance:       Instance{ID: "doc-1", Attrs: map[string]any{"dept": "eng", "level": 7}},
	}
}

func errCode(err error) ErrorCode {
	if e, ok := err.(*Error); ok {
		return e.Code
	}
	return -1
}

func attr(name string) Scope { return Scope{Kind: ScopeAttr, Name: name} }

func attrSet(names ...string) Scope { return Scope{Kind: ScopeAttrSet, Members: names} }

func pred(name string) Scope { return Scope{Kind: ScopePredicate, Name: name} }

// 收紧与放宽交叉：同一覆盖集合内同时包含收紧与放宽声明，二者互不影响。
func TestTightenAndLoosenCross(t *testing.T) {
	s := newTestStore()
	if err := s.SetLooseningBasisRequired("Doc", true); err != nil {
		t.Fatal(err)
	}
	if err := s.PutDefault("Doc", "reader", []Statement{
		{ID: "d1", Scope: attrSet("title", "body"), Effect: Allow},
		{ID: "d2", Scope: attr("secret"), Effect: Deny},
		{ID: "d3", Scope: pred("pEng"), Effect: Allow},
	}); err != nil {
		t.Fatal(err)
	}
	// 放宽缺少授权依据：整体拒绝，且不影响任何状态与审计。
	auditBefore := len(s.Audit())
	err := s.PutOverride("tA", "Doc", "reader", []Statement{
		{ID: "o1", Scope: attr("body"), Effect: Deny},
		{ID: "o2", Scope: attr("secret"), Effect: Allow},
	})
	if errCode(err) != ErrMissingBasis {
		t.Fatalf("期望 missing-basis，实际 %v", err)
	}
	if got := len(s.Audit()); got != auditBefore {
		t.Fatalf("被拒绝的声明影响了审计记录：%d -> %d", auditBefore, got)
	}
	dec, err := s.Decide(decideReq("tA", "tA"))
	if err != nil {
		t.Fatal(err)
	}
	if dec.Attrs["secret"] || !dec.Attrs["body"] {
		t.Fatalf("被拒绝的声明影响了规则状态：%+v", dec.Attrs)
	}
	// 携带授权依据后声明成功：body 被收紧为拒绝，secret 被放宽为允许。
	if err := s.PutOverride("tA", "Doc", "reader", []Statement{
		{ID: "o1", Scope: attr("body"), Effect: Deny},
		{ID: "o2", Scope: attr("secret"), Effect: Allow, Basis: "grant-42"},
	}); err != nil {
		t.Fatal(err)
	}
	dec, err = s.Decide(decideReq("tA", "tA"))
	if err != nil {
		t.Fatal(err)
	}
	if !dec.Attrs["title"] || dec.Attrs["body"] || !dec.Attrs["secret"] {
		t.Fatalf("收紧放宽交叉结果错误：%+v", dec.Attrs)
	}
	if dec.Trace.Basis["attr:title"] != "default:d1" ||
		dec.Trace.Basis["attr:body"] != "override:o1" ||
		dec.Trace.Basis["attr:secret"] != "override:o2" {
		t.Fatalf("层级依据错误：%+v", dec.Trace.Basis)
	}
	if !dec.RowAllowed {
		t.Fatal("行级应命中默认允许的 pEng")
	}
}

// 放宽无需依据的场景：系统未声明要求时，租户可直接放宽。
func TestLoosenWithoutBasisWhenNotRequired(t *testing.T) {
	s := newTestStore()
	if err := s.PutDefault("Doc", "reader", []Statement{
		{ID: "d1", Scope: attr("secret"), Effect: Deny},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.PutOverride("tA", "Doc", "reader", []Statement{
		{ID: "o1", Scope: attr("secret"), Effect: Allow},
	}); err != nil {
		t.Fatal(err)
	}
	dec, err := s.Decide(decideReq("tA", "tA"))
	if err != nil {
		t.Fatal(err)
	}
	if !dec.Attrs["secret"] {
		t.Fatal("未声明要求时放宽应直接生效")
	}
}

// 覆盖粒度不一致但范围嵌套：合并结果唯一且确定（窄范围优先）。
func TestGranularityNestedMerge(t *testing.T) {
	s := newTestStore()
	if err := s.PutDefault("Doc", "reader", []Statement{
		{ID: "d1", Scope: Scope{Kind: ScopeAttrWildcard}, Effect: Deny},
		{ID: "d2", Scope: Scope{Kind: ScopePredicateWildcard}, Effect: Deny},
	}); err != nil {
		t.Fatal(err)
	}
	// 一条覆盖只覆盖属性集合，另一条只覆盖单个谓词条件：维度正交，合并确定。
	if err := s.PutOverride("tA", "Doc", "reader", []Statement{
		{ID: "o1", Scope: attrSet("title", "body"), Effect: Allow},
		{ID: "o2", Scope: pred("pEng"), Effect: Allow},
	}); err != nil {
		t.Fatal(err)
	}
	dec, err := s.Decide(decideReq("tB", "tA"))
	if err != nil {
		t.Fatal(err)
	}
	if !dec.Attrs["title"] || !dec.Attrs["body"] || dec.Attrs["secret"] {
		t.Fatalf("嵌套粒度合并错误：%+v", dec.Attrs)
	}
	if !dec.RowAllowed {
		t.Fatal("谓词级覆盖应放行命中 pEng 的实例")
	}
	// 属性覆盖不影响谓词维度，谓词覆盖不影响属性维度。
	if dec.Trace.Basis["attr:secret"] != "default:d1" {
		t.Fatalf("未被覆盖的部分应沿用默认规则：%+v", dec.Trace.Basis)
	}
}

// 覆盖粒度不一致且范围非嵌套部分重叠、效果矛盾：合并结果不唯一，必须报错。
func TestGranularityAmbiguousMerge(t *testing.T) {
	s := newTestStore()
	if err := s.PutOverride("tA", "Doc", "reader", []Statement{
		{ID: "o1", Scope: attrSet("title", "body"), Effect: Allow},
		{ID: "o2", Scope: attrSet("body", "secret"), Effect: Deny},
	}); err != nil {
		t.Fatal(err)
	}
	_, err := s.Decide(decideReq("tA", "tA"))
	if errCode(err) != ErrMergeAmbiguous {
		t.Fatalf("期望 merge-ambiguous，实际 %v", err)
	}
}

// 范围完全相同的矛盾声明：无法调和的内部冲突。
func TestOverrideConflict(t *testing.T) {
	s := newTestStore()
	if err := s.PutOverride("tA", "Doc", "reader", []Statement{
		{ID: "o1", Scope: attr("title"), Effect: Allow},
		{ID: "o2", Scope: attr("title"), Effect: Deny},
	}); err != nil {
		t.Fatal(err)
	}
	_, err := s.Decide(decideReq("tA", "tA"))
	if errCode(err) != ErrOverrideConflict {
		t.Fatalf("期望 override-conflict，实际 %v", err)
	}
}

// 错误优先级固定：冲突（3）优先于歧义（4）；不存在（1）优先于缺依据（2）。
func TestErrorPriority(t *testing.T) {
	s := newTestStore()
	// 同一覆盖集合内同时存在冲突与歧义，判定必须报告冲突。
	if err := s.PutOverride("tA", "Doc", "reader", []Statement{
		{ID: "c1", Scope: attr("title"), Effect: Allow},
		{ID: "c2", Scope: attr("title"), Effect: Deny},
		{ID: "a1", Scope: attrSet("body", "secret"), Effect: Allow},
		{ID: "a2", Scope: attrSet("secret", "body"), Effect: Deny},
	}); err != nil {
		t.Fatal(err)
	}
	_, err := s.Decide(decideReq("tA", "tA"))
	if errCode(err) != ErrOverrideConflict {
		t.Fatalf("冲突应优先于歧义，实际 %v", err)
	}
	// 租户不存在与缺依据同时成立时，报告不存在。
	s2 := newTestStore()
	if err := s2.SetLooseningBasisRequired("Doc", true); err != nil {
		t.Fatal(err)
	}
	err = s2.PutOverride("ghost", "Doc", "reader", []Statement{
		{ID: "o1", Scope: attr("title"), Effect: Allow},
	})
	if errCode(err) != ErrNotFound {
		t.Fatalf("不存在应优先于缺依据，实际 %v", err)
	}
	// 对象类型不存在同理。
	err = s2.PutOverride("tA", "Ghost", "reader", nil)
	if errCode(err) != ErrNotFound {
		t.Fatalf("类型不存在应报 not-found，实际 %v", err)
	}
}

// 跨租户访问的归属方向：判定依据实例归属租户的覆盖规则，
// 两个租户的覆盖规则互不污染。
func TestCrossTenantOwnershipDirection(t *testing.T) {
	s := newTestStore()
	if err := s.PutDefault("Doc", "reader", []Statement{
		{ID: "d1", Scope: attr("title"), Effect: Allow},
	}); err != nil {
		t.Fatal(err)
	}
	// tA 放宽 secret，tB 收紧 title。
	if err := s.PutOverride("tA", "Doc", "reader", []Statement{
		{ID: "oA", Scope: attr("secret"), Effect: Allow},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.PutOverride("tB", "Doc", "reader", []Statement{
		{ID: "oB", Scope: attr("title"), Effect: Deny},
	}); err != nil {
		t.Fatal(err)
	}
	// tB 的主体访问 tA 的实例：适用 tA 的覆盖，title 允许、secret 允许。
	dec, err := s.Decide(decideReq("tB", "tA"))
	if err != nil {
		t.Fatal(err)
	}
	if !dec.Attrs["title"] || !dec.Attrs["secret"] {
		t.Fatalf("应适用实例归属租户 tA 的覆盖：%+v", dec.Attrs)
	}
	// tA 的主体访问 tB 的实例：适用 tB 的覆盖，title 拒绝、secret 隐式拒绝。
	dec, err = s.Decide(decideReq("tA", "tB"))
	if err != nil {
		t.Fatal(err)
	}
	if dec.Attrs["title"] || dec.Attrs["secret"] {
		t.Fatalf("应适用实例归属租户 tB 的覆盖：%+v", dec.Attrs)
	}
	// 双方各自访问自己的实例，结果不受对方覆盖影响。
	decA, _ := s.Decide(decideReq("tA", "tA"))
	decB, _ := s.Decide(decideReq("tB", "tB"))
	if !decA.Attrs["secret"] || decB.Attrs["title"] {
		t.Fatalf("覆盖规则互相污染：tA=%+v tB=%+v", decA.Attrs, decB.Attrs)
	}
}

// 全局默认规则原子切换：未覆盖变更部分的租户立即感知，
// 已覆盖的租户继续沿用其覆盖。
func TestDefaultAtomicSwitch(t *testing.T) {
	s := newTestStore()
	if err := s.PutDefault("Doc", "reader", []Statement{
		{ID: "v1", Scope: attr("title"), Effect: Deny},
	}); err != nil {
		t.Fatal(err)
	}
	// tA 对 title 声明了覆盖（收紧前的允许），tB 未覆盖。
	if err := s.PutOverride("tA", "Doc", "reader", []Statement{
		{ID: "oA", Scope: attr("title"), Effect: Allow},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.PutDefault("Doc", "reader", []Statement{
		{ID: "v2", Scope: attr("title"), Effect: Allow},
	}); err != nil {
		t.Fatal(err)
	}
	decB, err := s.Decide(decideReq("tB", "tB"))
	if err != nil {
		t.Fatal(err)
	}
	if !decB.Attrs["title"] || decB.Trace.Basis["attr:title"] != "default:v2" {
		t.Fatalf("未覆盖租户应立即感知新默认：%+v", decB.Trace.Basis)
	}
	decA, err := s.Decide(decideReq("tA", "tA"))
	if err != nil {
		t.Fatal(err)
	}
	if decA.Trace.Basis["attr:title"] != "override:oA" {
		t.Fatalf("已覆盖租户应继续沿用覆盖：%+v", decA.Trace.Basis)
	}
	// 覆盖为收紧的租户在默认放宽后仍保持拒绝。
	if err := s.PutOverride("tB", "Doc", "reader", []Statement{
		{ID: "oB", Scope: attr("title"), Effect: Deny},
	}); err != nil {
		t.Fatal(err)
	}
	decB, _ = s.Decide(decideReq("tB", "tB"))
	if decB.Attrs["title"] {
		t.Fatal("收紧覆盖不应被默认放宽影响")
	}
}

// 撤销覆盖是原子的：撤销后全部属性恢复默认；历史判定审计记录保持不变。
func TestRevokeAtomicity(t *testing.T) {
	s := newTestStore()
	if err := s.PutDefault("Doc", "reader", []Statement{
		{ID: "d1", Scope: attrSet("title", "body"), Effect: Allow},
		{ID: "d2", Scope: attr("secret"), Effect: Deny},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.PutOverride("tA", "Doc", "reader", []Statement{
		{ID: "o1", Scope: attr("title"), Effect: Deny},
		{ID: "o2", Scope: attr("secret"), Effect: Allow},
	}); err != nil {
		t.Fatal(err)
	}
	before, err := s.Decide(decideReq("tA", "tA"))
	if err != nil {
		t.Fatal(err)
	}
	if before.Attrs["title"] || !before.Attrs["secret"] {
		t.Fatalf("撤销前判定错误：%+v", before.Attrs)
	}
	auditBefore := s.Audit()
	if err := s.RevokeOverride("tA", "Doc", "reader"); err != nil {
		t.Fatal(err)
	}
	after, err := s.Decide(decideReq("tA", "tA"))
	if err != nil {
		t.Fatal(err)
	}
	// 同一次判定内所有属性一致地恢复默认，不存在部分沿用覆盖的中间状态。
	if !after.Attrs["title"] || !after.Attrs["body"] || after.Attrs["secret"] {
		t.Fatalf("撤销后应整体恢复默认：%+v", after.Attrs)
	}
	for _, key := range []string{"attr:title", "attr:body", "attr:secret"} {
		if after.Trace.Basis[key] == "override:o1" || after.Trace.Basis[key] == "override:o2" {
			t.Fatalf("撤销后仍引用已撤销的覆盖：%s=%s", key, after.Trace.Basis[key])
		}
	}
	// 历史审计记录（含撤销前的判定依据）保持不变。
	auditAfter := s.Audit()
	if len(auditAfter) != len(auditBefore)+2 { // revoke + decide
		t.Fatalf("审计长度异常：%d -> %d", len(auditBefore), len(auditAfter))
	}
	for i, rec := range auditBefore {
		got := auditAfter[i]
		if got.Seq != rec.Seq || got.Op != rec.Op || got.Input != rec.Input || got.Output != rec.Output {
			t.Fatalf("历史审计记录被篡改：seq=%d", rec.Seq)
		}
		if len(got.RuleBasis) != len(rec.RuleBasis) {
			t.Fatalf("历史审计依据被篡改：seq=%d", rec.Seq)
		}
		for k, v := range rec.RuleBasis {
			if got.RuleBasis[k] != v {
				t.Fatalf("历史审计依据被篡改：seq=%d key=%s", rec.Seq, k)
			}
		}
	}
}

// 无任何规则时隐式拒绝，且判定依据可观测。
func TestImplicitDeny(t *testing.T) {
	s := newTestStore()
	dec, err := s.Decide(decideReq("tA", "tA"))
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range []string{"title", "body", "secret"} {
		if dec.Attrs[a] || dec.Trace.Basis["attr:"+a] != "implicit-deny" {
			t.Fatalf("无规则应隐式拒绝：%s", a)
		}
	}
	if dec.RowAllowed {
		t.Fatal("无规则时行级应拒绝")
	}
}

// 被拒绝的判定不产生审计记录。
func TestRejectedDecideNoAudit(t *testing.T) {
	s := newTestStore()
	before := len(s.Audit())
	if _, err := s.Decide(decideReq("ghost", "tA")); errCode(err) != ErrNotFound {
		t.Fatalf("期望 not-found，实际 %v", err)
	}
	if _, err := s.Decide(decideReq("tA", "ghost")); errCode(err) != ErrNotFound {
		t.Fatalf("期望 not-found，实际 %v", err)
	}
	if got := len(s.Audit()); got != before {
		t.Fatalf("被拒绝的判定影响了审计记录：%d -> %d", before, got)
	}
}
