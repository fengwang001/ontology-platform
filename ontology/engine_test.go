package ontology

import (
	"reflect"
	"testing"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("变更操作失败: %v", err)
	}
}

func carriedTags(e *Engine, objectType string) map[string]bool {
	out := map[string]bool{}
	for tag := range e.snap.carried[objectType] {
		out[tag] = true
	}
	return out
}

// 多路径继承收敛：同一标签经多条路径到达同一对象类型，结论唯一。
func TestMultiPathConvergence(t *testing.T) {
	e := New(nil)
	for _, ot := range []string{"A", "B", "C", "D"} {
		must(t, e.AddObjectType(ot))
	}
	must(t, e.AddLinkType("L"))
	must(t, e.AddTag("T"))
	must(t, e.AddLinkEdge(LinkEdge{LinkType: "L", From: "A", To: "B"}))
	must(t, e.AddLinkEdge(LinkEdge{LinkType: "L", From: "A", To: "C"}))
	must(t, e.AddLinkEdge(LinkEdge{LinkType: "L", From: "B", To: "D"}))
	must(t, e.AddLinkEdge(LinkEdge{LinkType: "L", From: "C", To: "D"}))
	must(t, e.AddPropagation(Propagation{Tag: "T", LinkType: "L", Direction: Downstream}))
	must(t, e.AttachTag(Attachment{ObjectType: "A", Tag: "T"}))

	for _, ot := range []string{"A", "B", "C", "D"} {
		if !carriedTags(e, ot)["T"] {
			t.Fatalf("对象类型 %s 应携带标签 T", ot)
		}
	}
	// D 经两条路径继承同一标签，来源收敛为唯一挂载点。
	srcs := e.snap.carried["D"]["T"]
	if len(srcs) != 1 || srcs[0].Attachment.ObjectType != "A" {
		t.Fatalf("多路径继承未收敛: %+v", srcs)
	}
}

// 声明顺序不影响传播结论。
func TestDeclarationOrderIrrelevant(t *testing.T) {
	build := func(order []string) *Engine {
		e := New(nil)
		must(t, e.AddObjectType("X"))
		must(t, e.AddObjectType("Y"))
		must(t, e.AddLinkType("L"))
		must(t, e.AddTag("T"))
		for _, op := range order {
			switch op {
			case "edge":
				must(t, e.AddLinkEdge(LinkEdge{LinkType: "L", From: "X", To: "Y"}))
			case "prop":
				must(t, e.AddPropagation(Propagation{Tag: "T", LinkType: "L", Direction: Downstream}))
			case "attach":
				must(t, e.AttachTag(Attachment{ObjectType: "X", Tag: "T"}))
			}
		}
		return e
	}
	a := build([]string{"edge", "prop", "attach"})
	b := build([]string{"attach", "prop", "edge"})
	c := build([]string{"prop", "attach", "edge"})
	if !reflect.DeepEqual(a.snap.carried, b.snap.carried) ||
		!reflect.DeepEqual(a.snap.carried, c.snap.carried) {
		t.Fatal("声明顺序不应影响标签继承结论")
	}
}

// 传播阻断：经过阻断点的路径不得继续传播，未经过的路径不受影响。
func TestPropagationBlock(t *testing.T) {
	e := New(nil)
	for _, ot := range []string{"A", "B", "C", "D", "E"} {
		must(t, e.AddObjectType(ot))
	}
	must(t, e.AddLinkType("L"))
	must(t, e.AddTag("T"))
	// A -> B -> C 与 A -> D -> E 两条路径。
	must(t, e.AddLinkEdge(LinkEdge{LinkType: "L", From: "A", To: "B"}))
	must(t, e.AddLinkEdge(LinkEdge{LinkType: "L", From: "B", To: "C"}))
	must(t, e.AddLinkEdge(LinkEdge{LinkType: "L", From: "A", To: "D"}))
	must(t, e.AddLinkEdge(LinkEdge{LinkType: "L", From: "D", To: "E"}))
	must(t, e.AddPropagation(Propagation{Tag: "T", LinkType: "L", Direction: Downstream}))
	must(t, e.AttachTag(Attachment{ObjectType: "A", Tag: "T"}))
	must(t, e.AddBlock(Block{ObjectType: "B", Tag: "T"}))

	if !carriedTags(e, "B")["T"] {
		t.Fatal("阻断点自身仍应携带标签")
	}
	if carriedTags(e, "C")["T"] {
		t.Fatal("经过阻断点的路径不得继续传播")
	}
	if !carriedTags(e, "E")["T"] {
		t.Fatal("未经过阻断点的路径应正常传播")
	}

	// 全部路径被阻断时的判定错误分类。
	must(t, e.AddBlock(Block{ObjectType: "D", Tag: "T"}))
	if carriedTags(e, "C")["T"] || carriedTags(e, "E")["T"] {
		t.Fatal("全部路径阻断后下游不应携带标签")
	}
	must(t, e.AddRole("r"))
	must(t, e.AddSubject("s", "r"))
	must(t, e.AddGrant(Grant{Role: "r", Tag: "T", Effect: Allow}))
	must(t, e.AddInstance("i1", "C"))
	d := e.DecideTag("s", "i1", "T")
	if d.Allowed || d.Reason != ReasonAllPathsBlocked {
		t.Fatalf("期望 all_paths_blocked, 得到 %+v", d)
	}
}

// 关系图中的环不得导致传播不终止或结果不稳定。
func TestCycleTermination(t *testing.T) {
	e := New(nil)
	for _, ot := range []string{"A", "B", "C"} {
		must(t, e.AddObjectType(ot))
	}
	must(t, e.AddLinkType("L"))
	must(t, e.AddTag("T"))
	must(t, e.AddLinkEdge(LinkEdge{LinkType: "L", From: "A", To: "B"}))
	must(t, e.AddLinkEdge(LinkEdge{LinkType: "L", From: "B", To: "C"}))
	must(t, e.AddPropagation(Propagation{Tag: "T", LinkType: "L", Direction: Downstream}))
	must(t, e.AttachTag(Attachment{ObjectType: "A", Tag: "T"}))
	// 运行期引入环 C -> A。
	must(t, e.AddLinkEdge(LinkEdge{LinkType: "L", From: "C", To: "A"}))
	for _, ot := range []string{"A", "B", "C"} {
		if !carriedTags(e, ot)["T"] {
			t.Fatalf("环上对象类型 %s 应携带标签", ot)
		}
	}
	snap1 := e.snap.carried
	// 再次触发等价变更（加边再删边），结果必须稳定。
	must(t, e.AddObjectType("Z"))
	must(t, e.AddLinkEdge(LinkEdge{LinkType: "L", From: "C", To: "Z"}))
	must(t, e.RemoveLinkEdge(LinkEdge{LinkType: "L", From: "C", To: "Z"}))
	if !reflect.DeepEqual(snap1, e.snap.carried) {
		t.Fatal("含环图的传播结果不稳定")
	}
}

// 上游传播：标签沿链接边反方向传播。
func TestUpstreamPropagation(t *testing.T) {
	e := New(nil)
	for _, ot := range []string{"P", "Q"} {
		must(t, e.AddObjectType(ot))
	}
	must(t, e.AddLinkType("L"))
	must(t, e.AddTag("T"))
	must(t, e.AddLinkEdge(LinkEdge{LinkType: "L", From: "P", To: "Q"}))
	must(t, e.AddPropagation(Propagation{Tag: "T", LinkType: "L", Direction: Upstream}))
	must(t, e.AttachTag(Attachment{ObjectType: "Q", Tag: "T"}))
	if !carriedTags(e, "P")["T"] {
		t.Fatal("上游传播应使 P 携带标签")
	}
	if carriedTags(e, "Q")["T"] != true {
		t.Fatal("挂载点自身应携带标签")
	}
}

// 授权裁决：允许/拒绝 × 直接/继承四种组合，显式拒绝一律优先。
func TestGrantAdjudicationMatrix(t *testing.T) {
	setup := func(t *testing.T) (*Engine, string) {
		e := New(nil)
		must(t, e.AddObjectType("O"))
		must(t, e.AddTag("T"))
		must(t, e.AttachTag(Attachment{ObjectType: "O", Tag: "T"}))
		must(t, e.AddRole("parent"))
		must(t, e.AddRole("child", "parent"))
		must(t, e.AddSubject("s", "child"))
		must(t, e.AddInstance("i", "O"))
		return e, "i"
	}
	cases := []struct {
		name   string
		grants []Grant
		want   bool
		reason ReasonCode
	}{
		{"允许直接+拒绝直接", []Grant{
			{Role: "child", Tag: "T", Effect: Allow},
			{Role: "child", Tag: "T", Effect: Deny},
		}, false, ReasonDenyOverrides},
		{"允许直接+拒绝继承", []Grant{
			{Role: "child", Tag: "T", Effect: Allow},
			{Role: "parent", Tag: "T", Effect: Deny},
		}, false, ReasonDenyOverrides},
		{"允许继承+拒绝直接", []Grant{
			{Role: "parent", Tag: "T", Effect: Allow},
			{Role: "child", Tag: "T", Effect: Deny},
		}, false, ReasonDenyOverrides},
		{"允许继承+拒绝继承", []Grant{
			{Role: "parent", Tag: "T", Effect: Allow},
			{Role: "parent", Tag: "T", Effect: Deny},
		}, false, ReasonDenyOverrides},
		{"仅允许", []Grant{
			{Role: "parent", Tag: "T", Effect: Allow},
		}, true, ReasonAllowed},
		{"仅拒绝", []Grant{
			{Role: "child", Tag: "T", Effect: Deny},
		}, false, ReasonExplicitDeny},
		{"无授权", nil, false, ReasonNoGrant},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e, inst := setup(t)
			for _, g := range tc.grants {
				must(t, e.AddGrant(g))
			}
			d := e.Decide("s", inst)
			if d.Allowed != tc.want || d.Reason != tc.reason {
				t.Fatalf("期望 (%v, %s), 得到 (%v, %s)", tc.want, tc.reason, d.Allowed, d.Reason)
			}
		})
	}
}

// 综合规则：实例携带多个标签时全允许才允许，且与标签登记顺序无关。
func TestCombinationRule(t *testing.T) {
	build := func(tagOrder []string) *Engine {
		e := New(nil)
		must(t, e.AddObjectType("O"))
		for _, tag := range tagOrder {
			must(t, e.AddTag(tag))
			must(t, e.AttachTag(Attachment{ObjectType: "O", Tag: tag}))
		}
		must(t, e.AddRole("r"))
		must(t, e.AddSubject("s", "r"))
		must(t, e.AddInstance("i", "O"))
		must(t, e.AddGrant(Grant{Role: "r", Tag: "T1", Effect: Allow}))
		must(t, e.AddGrant(Grant{Role: "r", Tag: "T2", Effect: Deny}))
		return e
	}
	a := build([]string{"T1", "T2"})
	b := build([]string{"T2", "T1"})
	da, db := a.Decide("s", "i"), b.Decide("s", "i")
	if da.Allowed || db.Allowed {
		t.Fatal("任一标签被拒绝则整体必须拒绝")
	}
	if da.Reason != db.Reason || da.Reason != ReasonExplicitDeny {
		t.Fatalf("综合结果不得依赖标签登记顺序: %s vs %s", da.Reason, db.Reason)
	}
}

// 撤销级联：移除挂载点只影响仅因该来源继承的下游；
// 移除传播资格同理；其他独立来源不受影响。
func TestRevocationCascade(t *testing.T) {
	e := New(nil)
	for _, ot := range []string{"A1", "A2", "M", "D"} {
		must(t, e.AddObjectType(ot))
	}
	must(t, e.AddLinkType("L"))
	must(t, e.AddTag("T"))
	// 两个独立源头 A1、A2 均经 M 传播到 D。
	must(t, e.AddLinkEdge(LinkEdge{LinkType: "L", From: "A1", To: "M"}))
	must(t, e.AddLinkEdge(LinkEdge{LinkType: "L", From: "A2", To: "M"}))
	must(t, e.AddLinkEdge(LinkEdge{LinkType: "L", From: "M", To: "D"}))
	must(t, e.AddPropagation(Propagation{Tag: "T", LinkType: "L", Direction: Downstream}))
	must(t, e.AttachTag(Attachment{ObjectType: "A1", Tag: "T"}))
	must(t, e.AttachTag(Attachment{ObjectType: "A2", Tag: "T"}))

	if len(e.snap.carried["D"]["T"]) != 2 {
		t.Fatal("D 应有两个独立来源")
	}
	// 撤销一个挂载：D 仍携带（另一来源独立成立）。
	must(t, e.DetachTag(Attachment{ObjectType: "A1", Tag: "T"}))
	if !carriedTags(e, "D")["T"] || carriedTags(e, "A1")["T"] {
		t.Fatal("撤销单个挂载不得影响其他独立来源")
	}
	// 撤销另一个挂载：D 级联失去标签。
	must(t, e.DetachTag(Attachment{ObjectType: "A2", Tag: "T"}))
	if carriedTags(e, "D")["T"] || carriedTags(e, "M")["T"] {
		t.Fatal("全部来源撤销后下游应级联失去标签")
	}
	// 恢复挂载后撤销传播资格：下游级联失去，挂载点保留。
	must(t, e.AttachTag(Attachment{ObjectType: "A1", Tag: "T"}))
	if !carriedTags(e, "D")["T"] {
		t.Fatal("恢复挂载后 D 应重新携带标签")
	}
	must(t, e.RemovePropagation(Propagation{Tag: "T", LinkType: "L", Direction: Downstream}))
	if carriedTags(e, "D")["T"] || carriedTags(e, "M")["T"] {
		t.Fatal("移除传播资格后下游应级联失去标签")
	}
	if !carriedTags(e, "A1")["T"] {
		t.Fatal("移除传播资格不得影响直接挂载点")
	}
}

// 增量维护与从头重算一致：任意变更序列后的物化视图，
// 与把最终声明集一次性灌入新引擎的结果完全相等。
func TestIncrementalEqualsRebuild(t *testing.T) {
	e := New(nil)
	for _, ot := range []string{"A", "B", "C", "D"} {
		must(t, e.AddObjectType(ot))
	}
	must(t, e.AddLinkType("L"))
	must(t, e.AddTag("T"))
	must(t, e.AddLinkEdge(LinkEdge{LinkType: "L", From: "A", To: "B"}))
	must(t, e.AddLinkEdge(LinkEdge{LinkType: "L", From: "B", To: "C"}))
	must(t, e.AddLinkEdge(LinkEdge{LinkType: "L", From: "C", To: "D"}))
	must(t, e.AddLinkEdge(LinkEdge{LinkType: "L", From: "A", To: "D"}))
	must(t, e.AddPropagation(Propagation{Tag: "T", LinkType: "L", Direction: Downstream}))
	must(t, e.AttachTag(Attachment{ObjectType: "A", Tag: "T"}))
	must(t, e.AddBlock(Block{ObjectType: "B", Tag: "T"}))
	// 运行期变更：删边、删阻断、删传播、再恢复。
	must(t, e.RemoveLinkEdge(LinkEdge{LinkType: "L", From: "A", To: "D"}))
	must(t, e.RemoveBlock(Block{ObjectType: "B", Tag: "T"}))
	must(t, e.RemovePropagation(Propagation{Tag: "T", LinkType: "L", Direction: Downstream}))
	must(t, e.AddPropagation(Propagation{Tag: "T", LinkType: "L", Direction: Downstream}))

	fresh := New(nil)
	for _, ot := range []string{"A", "B", "C", "D"} {
		must(t, fresh.AddObjectType(ot))
	}
	must(t, fresh.AddLinkType("L"))
	must(t, fresh.AddTag("T"))
	must(t, fresh.AddLinkEdge(LinkEdge{LinkType: "L", From: "A", To: "B"}))
	must(t, fresh.AddLinkEdge(LinkEdge{LinkType: "L", From: "B", To: "C"}))
	must(t, fresh.AddLinkEdge(LinkEdge{LinkType: "L", From: "C", To: "D"}))
	must(t, fresh.AddPropagation(Propagation{Tag: "T", LinkType: "L", Direction: Downstream}))
	must(t, fresh.AttachTag(Attachment{ObjectType: "A", Tag: "T"}))

	if !reflect.DeepEqual(e.snap.carried, fresh.snap.carried) ||
		!reflect.DeepEqual(e.snap.shadow, fresh.snap.shadow) {
		t.Fatal("增量维护结果与从头重算不一致")
	}
}

// 错误分类与固定优先级：不存在 > 全部阻断 > 拒绝优先 > 角色环。
func TestErrorPriority(t *testing.T) {
	e := New(nil)
	must(t, e.AddObjectType("O"))
	must(t, e.AddTag("T"))
	must(t, e.AttachTag(Attachment{ObjectType: "O", Tag: "T"}))
	must(t, e.AddRole("r1"))
	must(t, e.AddRole("r2", "r1"))
	must(t, e.AddRole("r3", "r3")) // 自环角色
	must(t, e.AddSubject("s", "r2"))
	must(t, e.AddSubject("sc", "r3"))
	must(t, e.AddInstance("i", "O"))

	// 优先级 1：主体或标签不存在。
	if d := e.Decide("ghost", "i"); d.Reason != ReasonNotFound {
		t.Fatalf("未知主体应报 not_found, 得到 %s", d.Reason)
	}
	if d := e.DecideTag("s", "i", "ghost"); d.Reason != ReasonNotFound {
		t.Fatalf("未知标签应报 not_found, 得到 %s", d.Reason)
	}
	if d := e.Decide("s", "ghost"); d.Reason != ReasonNotFound {
		t.Fatalf("未知实例应报 not_found, 得到 %s", d.Reason)
	}

	// 优先级 3：显式拒绝优先于隐式允许（即使角色层级同时存在其他问题）。
	must(t, e.AddGrant(Grant{Role: "r1", Tag: "T", Effect: Allow}))
	must(t, e.AddGrant(Grant{Role: "r2", Tag: "T", Effect: Deny}))
	if d := e.DecideTag("s", "i", "T"); d.Allowed || d.Reason != ReasonDenyOverrides {
		t.Fatalf("期望 deny_overrides_allow, 得到 %+v", d)
	}

	// 优先级 4：角色层级循环继承。
	if d := e.DecideTag("sc", "i", "T"); d.Allowed || d.Reason != ReasonRoleCycle {
		t.Fatalf("期望 role_hierarchy_cycle, 得到 %+v", d)
	}

	// 多标签实例：同时存在 not_found 之外的多类错误时，
	// 按固定优先级汇报（拒绝优先于角色环）。
	must(t, e.AddTag("T2"))
	must(t, e.AttachTag(Attachment{ObjectType: "O", Tag: "T2"}))
	must(t, e.AddSubject("mix", "r2", "r3"))
	d := e.Decide("mix", "i")
	if d.Allowed || d.Reason != ReasonDenyOverrides {
		t.Fatalf("多错误场景应按优先级汇报 deny_overrides_allow, 得到 %+v", d)
	}
}

// 被拒绝的判定不得对标签继承状态、角色层级或时钟产生任何影响。
func TestDeniedDecisionHasNoSideEffect(t *testing.T) {
	e := New(nil)
	must(t, e.AddObjectType("O"))
	must(t, e.AddTag("T"))
	must(t, e.AttachTag(Attachment{ObjectType: "O", Tag: "T"}))
	must(t, e.AddRole("r"))
	must(t, e.AddSubject("s", "r"))
	must(t, e.AddInstance("i", "O"))
	must(t, e.AddGrant(Grant{Role: "r", Tag: "T", Effect: Deny}))

	beforeCarried := e.snap.carried
	beforeShadow := e.snap.shadow
	beforeDecl := *e.decl
	d := e.Decide("s", "i")
	if d.Allowed {
		t.Fatal("应被拒绝")
	}
	if !reflect.DeepEqual(beforeCarried, e.snap.carried) ||
		!reflect.DeepEqual(beforeShadow, e.snap.shadow) {
		t.Fatal("被拒绝的判定改变了标签继承状态")
	}
	if !reflect.DeepEqual(beforeDecl, *e.decl) {
		t.Fatal("被拒绝的判定改变了声明状态")
	}
}
