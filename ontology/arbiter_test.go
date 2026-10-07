package ontology

import "testing"

// setupWorld 构造一个最小世界：
// 对象类型 Person/Company；链接类型 employs: Person.employer -- Company.employee，
// 两端基数上限分别为 srcMax=1（一个人最多一条受雇链接）、tgtMax=2。
func setupWorld(t *testing.T) *Arbiter {
	t.Helper()
	a := NewArbiter()
	must(t, a, RegisterObjectType{Name: "Person", Attrs: []AttrSpec{
		{Name: "name", Default: Visible},
		{Name: "employer", Default: Visible},
	}})
	must(t, a, RegisterObjectType{Name: "Company", Attrs: []AttrSpec{
		{Name: "title", Default: Visible},
		{Name: "employee", Default: Visible},
	}})
	must(t, a, RegisterLinkType{Spec: LinkTypeSpec{
		Name: "employs", SrcType: "Person", SrcAttr: "employer",
		TgtType: "Company", TgtAttr: "employee", SrcMax: 1, TgtMax: 2,
	}})
	for _, id := range []string{"p1", "p2", "p3", "c1"} {
		tn := "Person"
		if id == "c1" {
			tn = "Company"
		}
		must(t, a, CreateInstance{ID: id, TypeName: tn})
	}
	return a
}

func must(t *testing.T, a *Arbiter, cmd Command) {
	t.Helper()
	if err := a.Apply(cmd); err != nil {
		t.Fatalf("setup command %#v failed: %v", cmd, err)
	}
}

// TestInvisibleBeatsSourceCardinality 验证“权限不可见”优先于“起点基数超限”：
// p1 的 employer 属性对 alice 收紧为不可见，且 p1 已占满起点名额，
// 此时创建必须报 ErrInvisible 而不是基数错误。
func TestInvisibleBeatsSourceCardinality(t *testing.T) {
	a := setupWorld(t)
	// bob 先建一条 p1->c1，占满 p1 的起点名额(srcMax=1)与 c1 的一个终点名额。
	first := a.CreateLink(CreateLink{ID: "L0", TypeName: "employs", SrcID: "p1", TgtID: "c1", Operator: "bob"})
	if first.Err != nil {
		t.Fatalf("seed link failed: %v", first.Err)
	}
	// 收紧：alice 对 p1.employer 不可见。
	if err := a.Apply(DeclareOverride{
		InstanceID: "p1", Attr: "employer", Kind: SubjectOperator, Subject: "alice", Vis: Invisible,
	}); err != nil {
		t.Fatalf("override failed: %v", err)
	}

	req := CreateLink{ID: "L1", TypeName: "employs", SrcID: "p1", TgtID: "c1", Operator: "alice"}
	res := a.CreateLink(req)
	echo(t, "INPUT  CreateLink{id=L1 p1->c1 op=alice}（p1 起点名额已满 + alice 对 p1.employer 不可见）")
	echo(t, "OUTPUT code=%s", errCode(res.Err))
	echo(t, "BASIS  src=%s tgt=%s srcUse=%d/%d tgtUse=%d/%d",
		basisString(res.Evidence.SrcBasis), basisString(res.Evidence.TgtBasis),
		res.Evidence.Card.SrcUse, res.Evidence.Card.SrcMax,
		res.Evidence.Card.TgtUse, res.Evidence.Card.TgtMax)
	if res.Err == nil || res.Err.Code != ErrInvisible {
		t.Fatalf("want ErrInvisible, got %v", res.Err)
	}
}

// TestCardinalityOrdering 验证基数类内部起点优先于终点。
func TestCardinalityOrdering(t *testing.T) {
	a := setupWorld(t)
	must(t, a, CreateLink{ID: "L0", TypeName: "employs", SrcID: "p1", TgtID: "c1", Operator: "x"})
	must(t, a, CreateLink{ID: "L1", TypeName: "employs", SrcID: "p2", TgtID: "c1", Operator: "x"})
	// 此时 p1 起点已满、c1 终点(tgtMax=2)也满。两端基数同时超限，必须报起点。
	res := a.CreateLink(CreateLink{ID: "L2", TypeName: "employs", SrcID: "p1", TgtID: "c1", Operator: "x"})
	echo(t, "INPUT  CreateLink{id=L2 p1->c1 op=x}（起点与终点名额同时占满）")
	echo(t, "OUTPUT code=%s", errCode(res.Err))
	if res.Err == nil || res.Err.Code != ErrSrcCardinality {
		t.Fatalf("want ErrSrcCardinality, got %v", res.Err)
	}
	// 换成起点有名额(p3)、终点满的情形，必须报终点。
	res2 := a.CreateLink(CreateLink{ID: "L3", TypeName: "employs", SrcID: "p3", TgtID: "c1", Operator: "x"})
	echo(t, "INPUT  CreateLink{id=L3 p3->c1 op=x}（仅终点名额占满）")
	echo(t, "OUTPUT code=%s", errCode(res2.Err))
	if res2.Err == nil || res2.Err.Code != ErrTgtCardinality {
		t.Fatalf("want ErrTgtCardinality, got %v", res2.Err)
	}
}

// TestInvalidParamFirst 验证参数非法优先于一切（即使实例对操作者不可见）。
func TestInvalidParamFirst(t *testing.T) {
	a := setupWorld(t)
	if err := a.Apply(DeclareOverride{
		InstanceID: "p1", Attr: "employer", Kind: SubjectOperator, Subject: "alice", Vis: Invisible,
	}); err != nil {
		t.Fatal(err)
	}
	res := a.CreateLink(CreateLink{ID: "LX", TypeName: "no-such-type", SrcID: "p1", TgtID: "c1", Operator: "alice"})
	echo(t, "INPUT  CreateLink{unknown link type, op=alice（alice 同时不可见）}")
	echo(t, "OUTPUT code=%s", errCode(res.Err))
	if res.Err == nil || res.Err.Code != ErrInvalidParam {
		t.Fatalf("want ErrInvalidParam, got %v", res.Err)
	}
}

// TestTightenHidesWithoutDeletion 验证收紧后链接对该操作者立即隐藏，
// 但物理存在、基数占用与其他操作者的可见性均不受影响，且不释放基数。
func TestTightenHidesWithoutDeletion(t *testing.T) {
	a := setupWorld(t)
	must(t, a, CreateLink{ID: "L1", TypeName: "employs", SrcID: "p1", TgtID: "c1", Operator: "alice"})

	before := a.VisibleLinks("alice")
	echo(t, "STATE  收紧前 alice 可见链接=%v", SortedIDs(before))
	if len(before) != 1 {
		t.Fatalf("alice should see L1 before tightening")
	}

	must(t, a, DeclareOverride{
		InstanceID: "p1", Attr: "employer", Kind: SubjectOperator, Subject: "alice", Vis: Invisible,
	})

	afterAlice := a.VisibleLinks("alice")
	afterBob := a.VisibleLinks("bob")
	physical := a.PhysicalLinks()
	echo(t, "STATE  收紧后 alice 可见=%v bob 可见=%v 物理链接=%v",
		SortedIDs(afterAlice), SortedIDs(afterBob), SortedIDs(physical))
	if len(afterAlice) != 0 {
		t.Fatalf("L1 must be hidden from alice after tightening, got %v", SortedIDs(afterAlice))
	}
	if len(afterBob) != 1 || afterBob[0].ID != "L1" {
		t.Fatalf("L1 must remain visible to bob, got %v", SortedIDs(afterBob))
	}
	if len(physical) != 1 {
		t.Fatalf("tightening must not physically delete links")
	}

	// 基数未释放：alice 之外的任何人尝试再建 p1->c1 仍应被起点基数拒绝。
	res := a.CreateLink(CreateLink{ID: "L2", TypeName: "employs", SrcID: "p1", TgtID: "c1", Operator: "bob"})
	echo(t, "INPUT  收紧后 bob 再建 p1->c1（验证基数未释放）")
	echo(t, "OUTPUT code=%s", errCode(res.Err))
	if res.Err == nil || res.Err.Code != ErrSrcCardinality {
		t.Fatalf("cardinality slot must stay occupied, got %v", res.Err)
	}

	// 放宽后立即恢复可见。
	must(t, a, DeclareOverride{
		InstanceID: "p1", Attr: "employer", Kind: SubjectOperator, Subject: "alice", Vis: Visible,
	})
	again := a.VisibleLinks("alice")
	echo(t, "STATE  重新放宽后 alice 可见=%v", SortedIDs(again))
	if len(again) != 1 {
		t.Fatalf("loosening must restore visibility immediately")
	}
}

// TestDeleteHiddenLooksLikeMissing 验证删除不可见链接与删除不存在链接返回完全相同类别，
// 且不可见删除不造成物理删除。
func TestDeleteHiddenLooksLikeMissing(t *testing.T) {
	a := setupWorld(t)
	must(t, a, CreateLink{ID: "L1", TypeName: "employs", SrcID: "p1", TgtID: "c1", Operator: "bob"})

	missing := a.DeleteLink(DeleteLink{ID: "ghost", Operator: "alice"})
	echo(t, "INPUT  DeleteLink{id=ghost op=alice}（确实不存在）")
	echo(t, "OUTPUT code=%s msg=%q", errCode(missing.Err), missing.Err.Msg)

	must(t, a, DeclareOverride{
		InstanceID: "p1", Attr: "employer", Kind: SubjectOperator, Subject: "alice", Vis: Invisible,
	})
	hidden := a.DeleteLink(DeleteLink{ID: "L1", Operator: "alice"})
	echo(t, "INPUT  DeleteLink{id=L1 op=alice}（链接存在但对 alice 不可见）")
	echo(t, "OUTPUT code=%s msg=%q", errCode(hidden.Err), hidden.Err.Msg)

	if missing.Err == nil || hidden.Err == nil {
		t.Fatalf("both deletes must fail")
	}
	if missing.Err.Code != ErrNotFound || hidden.Err.Code != ErrNotFound {
		t.Fatalf("both must be ErrNotFound: %v / %v", missing.Err, hidden.Err)
	}
	if missing.Err.Error() != hidden.Err.Error() {
		t.Fatalf("error text must be indistinguishable: %q vs %q", missing.Err.Error(), hidden.Err.Error())
	}
	if !hidden.HiddenButExists {
		t.Fatalf("evidence should note the link physically exists")
	}
	if len(a.PhysicalLinks()) != 1 {
		t.Fatalf("hidden delete must not remove the physical link")
	}

	// 可见操作者可以正常删除；删除后再删同一 ID 也归入 ErrNotFound。
	ok := a.DeleteLink(DeleteLink{ID: "L1", Operator: "bob"})
	echo(t, "INPUT  DeleteLink{id=L1 op=bob}（可见操作者）")
	echo(t, "OUTPUT code=%s", errCode(ok.Err))
	if ok.Err != nil {
		t.Fatalf("visible delete should succeed: %v", ok.Err)
	}
	gone := a.DeleteLink(DeleteLink{ID: "L1", Operator: "bob"})
	echo(t, "INPUT  DeleteLink{id=L1 op=bob}（删除后重复删除）")
	echo(t, "OUTPUT code=%s", errCode(gone.Err))
	if gone.Err == nil || gone.Err.Code != ErrNotFound {
		t.Fatalf("repeat delete must be ErrNotFound, got %v", gone.Err)
	}
}
