package ontology

import "testing"

// 同一实例同时承载两个不同链接类型的基数约束时：
// 单次请求必须分别校验每一个约束，任一不满足即整体拒绝，且判定依据各自独立可辨。
func TestMultipleConstraintsAllChecked(t *testing.T) {
	st := NewStore()
	must0(t, st.RegisterLinkType(LinkType{ID: "owns", CardinalityA: &Cardinality{Max: 1}}))
	must0(t, st.RegisterLinkType(LinkType{ID: "likes", CardinalityA: &Cardinality{Max: 2}}))
	must0(t, st.CreateInstance("x"))
	for _, id := range []string{"a", "b", "c", "d"} {
		must0(t, st.CreateInstance(id))
	}
	// likes 已满（2），owns 空闲。
	ok, err := st.commitRaider("x", []Op{
		{TypeID: "likes", Side: SideA, Other: "a", Add: true},
		{TypeID: "likes", Side: SideA, Other: "b", Add: true},
	})
	must0(t, err)
	if !ok {
		t.Fatal("seed failed")
	}

	eng := NewEngine(st, 2, nil)
	req := Request{Instance: "x", Baseline: 1, Ops: []Op{
		{TypeID: "owns", Side: SideA, Other: "c", Add: true},  // owns: 0+1<=1 满足
		{TypeID: "likes", Side: SideA, Other: "d", Add: true}, // likes: 2+1>2 不满足
	}}
	res, err := eng.Update(req)
	if err != nil {
		t.Fatal(err)
	}
	if res.Committed || res.Reject.Code != CodeCardinality {
		t.Fatalf("want cardinality rejection, got %+v", res)
	}
	if res.Reject.Constraint == nil || res.Reject.Constraint.TypeID != "likes" {
		t.Fatalf("rejection must name the violating constraint precisely, got %+v", res.Reject.Constraint)
	}
	vs := res.Attempts[0].Verdicts
	if len(vs) != 2 {
		t.Fatalf("want two distinct verdicts, got %+v", vs)
	}
	byType := map[string]ConstraintVerdict{}
	for _, v := range vs {
		byType[v.Constraint.TypeID] = v
	}
	if !byType["owns"].Satisfied || byType["owns"].Current != 0 || byType["owns"].Delta != 1 {
		t.Fatalf("owns verdict wrong: %+v", byType["owns"])
	}
	if byType["likes"].Satisfied || byType["likes"].Current != 2 || byType["likes"].Delta != 1 {
		t.Fatalf("likes verdict wrong: %+v", byType["likes"])
	}
	// 整体拒绝：两个链接都不得建立。
	n, _ := st.LinkCount("x")
	if n != 2 {
		t.Fatalf("failed multi-constraint request must not partial-commit, links=%d", n)
	}
}

// 两个约束同时满足时才提交。
func TestMultipleConstraintsBothPass(t *testing.T) {
	st := NewStore()
	must0(t, st.RegisterLinkType(LinkType{ID: "owns", CardinalityA: &Cardinality{Max: 1}}))
	must0(t, st.RegisterLinkType(LinkType{ID: "likes", CardinalityA: &Cardinality{Max: 2}}))
	must0(t, st.CreateInstance("x"))
	must0(t, st.CreateInstance("a"))
	must0(t, st.CreateInstance("b"))

	eng := NewEngine(st, 1, nil)
	req := Request{Instance: "x", Baseline: 0, Ops: []Op{
		{TypeID: "owns", Side: SideA, Other: "a", Add: true},
		{TypeID: "likes", Side: SideA, Other: "b", Add: true},
	}}
	res, err := eng.Update(req)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Committed {
		t.Fatalf("want commit, got %+v", res.Reject)
	}
	n, _ := st.LinkCount("x")
	if n != 2 {
		t.Fatalf("want 2 links, got %d", n)
	}
}

func must0(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
